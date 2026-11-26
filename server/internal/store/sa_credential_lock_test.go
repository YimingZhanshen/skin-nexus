package store

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"modernc.org/sqlite"

	"skin-nexus/server/internal/dbx"
	"skin-nexus/server/internal/dialect"
	"skin-nexus/server/internal/migrate"
)

type saRecordingDriver struct {
	inner driver.Driver
	mu    sync.Mutex
	sql   []string
}

func (d *saRecordingDriver) Open(name string) (driver.Conn, error) {
	conn, err := d.inner.Open(name)
	if err != nil {
		return nil, err
	}
	return &saRecordingConn{Conn: conn, recorder: d}, nil
}

func (d *saRecordingDriver) reset() {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.sql = nil
}

func (d *saRecordingDriver) statements() []string {
	d.mu.Lock()
	defer d.mu.Unlock()
	return append([]string(nil), d.sql...)
}

func (d *saRecordingDriver) record(query string) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.sql = append(d.sql, strings.Join(strings.Fields(query), " "))
}

type saRecordingConn struct {
	driver.Conn
	recorder *saRecordingDriver
}

func (c *saRecordingConn) Prepare(query string) (driver.Stmt, error) {
	return c.Conn.Prepare(query)
}

func (c *saRecordingConn) PrepareContext(ctx context.Context, query string) (driver.Stmt, error) {
	return c.Conn.(driver.ConnPrepareContext).PrepareContext(ctx, query)
}

func (c *saRecordingConn) Close() error { return c.Conn.Close() }

func (c *saRecordingConn) Begin() (driver.Tx, error) {
	tx, err := c.Conn.(driver.ConnBeginTx).BeginTx(context.Background(), driver.TxOptions{})
	if err != nil {
		return nil, err
	}
	return &saRecordingTx{Tx: tx, recorder: c.recorder}, nil
}

func (c *saRecordingConn) BeginTx(ctx context.Context, opts driver.TxOptions) (driver.Tx, error) {
	tx, err := c.Conn.(driver.ConnBeginTx).BeginTx(ctx, opts)
	if err != nil {
		return nil, err
	}
	return &saRecordingTx{Tx: tx, recorder: c.recorder}, nil
}

func (c *saRecordingConn) ExecContext(ctx context.Context, query string, args []driver.NamedValue) (driver.Result, error) {
	c.recorder.record(query)
	return c.Conn.(driver.ExecerContext).ExecContext(ctx, query, args)
}

func (c *saRecordingConn) QueryContext(ctx context.Context, query string, args []driver.NamedValue) (driver.Rows, error) {
	c.recorder.record(query)
	return c.Conn.(driver.QueryerContext).QueryContext(ctx, query, args)
}

type saRecordingTx struct {
	driver.Tx
	recorder *saRecordingDriver
}

func (t *saRecordingTx) Commit() error   { return t.Tx.Commit() }
func (t *saRecordingTx) Rollback() error { return t.Tx.Rollback() }

func (t *saRecordingTx) ExecContext(ctx context.Context, query string, args []driver.NamedValue) (driver.Result, error) {
	t.recorder.record(query)
	return t.Tx.(driver.ExecerContext).ExecContext(ctx, query, args)
}

func (t *saRecordingTx) QueryContext(ctx context.Context, query string, args []driver.NamedValue) (driver.Rows, error) {
	t.recorder.record(query)
	return t.Tx.(driver.QueryerContext).QueryContext(ctx, query, args)
}

func openSARecordingSQLiteStore(t *testing.T) (*Mysql, *saRecordingDriver) {
	t.Helper()
	dsn := filepath.Join(t.TempDir(), "recording.db")
	recorder := &saRecordingDriver{inner: &sqlite.Driver{}}
	name := fmt.Sprintf("sa-recording-%d", saSeedSeq.Add(1))
	sql.Register(name, recorder)
	raw, err := sql.Open(name, dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = raw.Close() })
	st := &Mysql{db: dbx.Wrap(raw, dialect.SQLite), now: time.Now}
	if err := migrate.ApplySQLDirTracked(st.DB(), filepath.Join("..", "..", "migrations"), dialect.SQLite); err != nil {
		t.Fatal(err)
	}
	return st, recorder
}

func TestServerAccessMembershipCheckUsesUserLockWithoutJoinedRowLock(t *testing.T) {
	st, recorder := openSARecordingSQLiteStore(t)
	ctx := context.Background()
	uid := saSeedUser(t, st)
	if _, _, err := st.CreateSATenantWithOwner(ctx, "membership-lock-existing", 2, uid); err != nil {
		t.Fatal(err)
	}
	recorder.reset()
	if _, _, err := st.CreateSATenantWithOwner(ctx, "membership-lock-second", 2, uid); !errors.Is(err, ErrForbidden) {
		t.Fatalf("second active owner err = %v, want ErrForbidden", err)
	}
	statements := recorder.statements()
	userLock := saStatementIndex(statements, "UPDATE users SET id=id")
	membershipRead := saStatementIndex(statements, "SELECT tm.tenant_id FROM sa_tenant_members")
	if userLock < 0 || membershipRead < 0 || userLock >= membershipRead {
		t.Fatalf("membership lock order = %v, want user lock before membership read", statements)
	}
	if strings.Contains(strings.ToUpper(statements[membershipRead]), "FOR UPDATE") {
		t.Fatalf("membership read must not lock joined tenant rows: %s", statements[membershipRead])
	}
}

func TestServerAccessCredentialCreateLocksTenantBeforeServerAndInsert(t *testing.T) {
	st, recorder := openSARecordingSQLiteStore(t)
	ctx := context.Background()
	tenant, err := st.CreateSATenant(ctx, "lock-order", 2)
	if err != nil {
		t.Fatal(err)
	}
	server, err := st.CreateSAServer(ctx, tenant, "srv_lock_order", "lock-order")
	if err != nil {
		t.Fatal(err)
	}
	recorder.reset()
	if _, err := st.CreateSAServerCredential(ctx, tenant, server.ID, SACredential{
		Mode: "header", SecretHash: saTestHash("lock-order-secret"), Scopes: `["verify:join"]`, Status: "active",
	}); err != nil {
		t.Fatal(err)
	}
	statements := recorder.statements()
	tenantLock := saStatementIndex(statements, "UPDATE sa_tenants SET name=name")
	serverLock := saStatementIndex(statements, "UPDATE sa_servers SET name=name")
	insert := saStatementIndex(statements, "INSERT INTO sa_server_credentials")
	if tenantLock < 0 || serverLock < 0 || insert < 0 || !(tenantLock < serverLock && serverLock < insert) {
		t.Fatalf("credential create lock order = %v, want tenant then server then insert", statements)
	}
}

func TestServerAccessCredentialRotateLocksTenantServerCredentialBeforeWrite(t *testing.T) {
	st, recorder := openSARecordingSQLiteStore(t)
	ctx := context.Background()
	tenant, err := st.CreateSATenant(ctx, "rotate-lock-order", 2)
	if err != nil {
		t.Fatal(err)
	}
	server, err := st.CreateSAServer(ctx, tenant, "srv_rotate_lock_order", "rotate-lock-order")
	if err != nil {
		t.Fatal(err)
	}
	oldID, err := st.CreateSAServerCredential(ctx, tenant, server.ID, SACredential{
		Mode: "header", SecretHash: saTestHash("rotate-lock-old"), Scopes: `["verify:join"]`, Status: "active",
	})
	if err != nil {
		t.Fatal(err)
	}
	recorder.reset()
	if _, err := st.RotateSAServerCredential(ctx, tenant, server.ID, oldID, SACredential{
		Mode: "header", SecretHash: saTestHash("rotate-lock-new"), Scopes: `["verify:join"]`, Status: "active",
	}, time.Hour, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	statements := recorder.statements()
	tenantLock := saStatementIndex(statements, "UPDATE sa_tenants SET name=name")
	serverLock := saStatementIndex(statements, "UPDATE sa_servers SET name=name")
	credentialLock := saStatementIndex(statements, "UPDATE sa_server_credentials SET status=status")
	update := saStatementIndex(statements, "UPDATE sa_server_credentials SET expires_at=CASE")
	insert := saStatementIndex(statements, "INSERT INTO sa_server_credentials")
	if tenantLock < 0 || serverLock < 0 || credentialLock < 0 || update < 0 || insert < 0 ||
		!(tenantLock < serverLock && serverLock < credentialLock && credentialLock < update && update < insert) {
		t.Fatalf("credential rotate lock order = %v, want tenant then server then credential then update/insert", statements)
	}
}

func TestServerAccessCredentialWritesRequireActiveLockedRows(t *testing.T) {
	for _, tc := range []struct {
		name   string
		tenant string
		server string
		old    string
	}{
		{"suspended tenant", "suspended", "active", "active"},
		{"deleting tenant", "deleting", "active", "active"},
		{"suspended server", "active", "suspended", "active"},
		{"revoked credential", "active", "active", "revoked"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			st := openSQLiteStore(t)
			ctx := context.Background()
			tenant, _ := st.CreateSATenant(ctx, "inactive-credential", 2)
			server, err := st.CreateSAServer(ctx, tenant, "srv_inactive_credential", "inactive")
			if err != nil {
				t.Fatal(err)
			}
			oldID, err := st.CreateSAServerCredential(ctx, tenant, server.ID, SACredential{
				Mode: "header", SecretHash: saTestHash("inactive-old"), Scopes: `["verify:join"]`, Status: "active",
			})
			if err != nil {
				t.Fatal(err)
			}
			if tc.tenant == "deleting" {
				_, err = st.RequestSATenantDeletion(ctx, tenant, DefaultSATenantDeletionGrace, time.Now().UTC())
			} else if tc.tenant != "active" {
				err = st.UpdateSATenantStatus(ctx, tenant, tc.tenant)
			} else if tc.server != "active" {
				err = st.UpdateSAServer(ctx, tenant, server.ID, server.Name, tc.server)
			} else {
				err = st.SetSAServerCredentialStatus(ctx, tenant, server.ID, oldID, tc.old)
			}
			if err != nil {
				t.Fatal(err)
			}
			if tc.old == "active" {
				if _, err := st.CreateSAServerCredential(ctx, tenant, server.ID, SACredential{
					Mode: "header", SecretHash: saTestHash("inactive-new"), Scopes: `["verify:join"]`, Status: "active",
				}); !errors.Is(err, ErrNotFound) {
					t.Fatalf("create with %s = %v, want not found", tc.name, err)
				}
			}
			if tc.old != "active" {
				if _, err := st.RotateSAServerCredential(ctx, tenant, server.ID, oldID, SACredential{
					Mode: "header", SecretHash: saTestHash("inactive-rotated"), Scopes: `["verify:join"]`, Status: "active",
				}, time.Hour, time.Now().UTC()); !errors.Is(err, ErrNotFound) {
					t.Fatalf("rotate with %s = %v, want not found", tc.name, err)
				}
			}
		})
	}
}

func TestServerAccessDeletionSerializesCredentialCreateAndRotate(t *testing.T) {
	saExerciseDeletionCredentialRace(t, openSQLiteStore(t))
}

func saExerciseDeletionCredentialRace(t *testing.T, st *Mysql) {
	t.Helper()
	ctx := context.Background()
	for i := 0; i < 24; i++ {
		tenant, err := st.CreateSATenant(ctx, fmt.Sprintf("delete-credential-race-%d", i), 2)
		if err != nil {
			t.Fatal(err)
		}
		server, err := st.CreateSAServer(ctx, tenant, fmt.Sprintf("srv_delete_credential_race_%d", i), "race")
		if err != nil {
			t.Fatal(err)
		}
		oldID, err := st.CreateSAServerCredential(ctx, tenant, server.ID, SACredential{
			Mode: "header", SecretHash: saTestHash(fmt.Sprintf("race-old-%d", i)), Scopes: `["verify:join"]`, Status: "active",
		})
		if err != nil {
			t.Fatal(err)
		}
		start := make(chan struct{})
		errs := make([]error, 3)
		var wg sync.WaitGroup
		wg.Add(3)
		go func() {
			defer wg.Done()
			<-start
			_, errs[0] = st.RequestSATenantDeletion(ctx, tenant, DefaultSATenantDeletionGrace, time.Now().UTC())
		}()
		go func() {
			defer wg.Done()
			<-start
			_, errs[1] = st.CreateSAServerCredential(ctx, tenant, server.ID, SACredential{
				Mode: "header", SecretHash: saTestHash(fmt.Sprintf("race-create-%d", i)), Scopes: `["verify:join"]`, Status: "active",
			})
		}()
		go func() {
			defer wg.Done()
			<-start
			_, errs[2] = st.RotateSAServerCredential(ctx, tenant, server.ID, oldID, SACredential{
				Mode: "header", SecretHash: saTestHash(fmt.Sprintf("race-rotate-%d", i)), Scopes: `["verify:join"]`, Status: "active",
			}, time.Hour, time.Now().UTC())
		}()
		close(start)
		wg.Wait()
		if errs[0] != nil {
			t.Fatalf("iteration %d deletion: %v", i, errs[0])
		}
		for j, err := range errs[1:] {
			if err != nil && !errors.Is(err, ErrNotFound) {
				t.Fatalf("iteration %d credential writer %d: %v", i, j, err)
			}
		}
		rows, err := st.ListSAServerCredentials(ctx, tenant, server.ID)
		if err != nil {
			t.Fatal(err)
		}
		for _, row := range rows {
			if row.Status != "revoked" {
				t.Fatalf("iteration %d credential %d survived deletion as %s", i, row.ID, row.Status)
			}
		}
	}
}

func TestServerAccessDeletionReplayRejectsCredentialWritesWithoutChangingRows(t *testing.T) {
	saExerciseDeletionCredentialReplay(t, openSQLiteStore(t))
}

func saExerciseDeletionCredentialReplay(t *testing.T, st *Mysql) {
	t.Helper()
	ctx := context.Background()
	tenant, _ := st.CreateSATenant(ctx, "delete-credential-replay", 2)
	server, err := st.CreateSAServer(ctx, tenant, "srv_delete_credential_replay", "replay")
	if err != nil {
		t.Fatal(err)
	}
	oldID, err := st.CreateSAServerCredential(ctx, tenant, server.ID, SACredential{
		Mode: "header", SecretHash: saTestHash("replay-old"), Scopes: `["verify:join"]`, Status: "active",
	})
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Truncate(time.Second)
	first, err := st.RequestSATenantDeletion(ctx, tenant, 48*time.Hour, now)
	if err != nil {
		t.Fatal(err)
	}
	const replays = 8
	errs := make([]error, replays)
	deadlines := make([]time.Time, replays)
	start := make(chan struct{})
	var wg sync.WaitGroup
	for i := 0; i < replays; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			replay, err := st.RequestSATenantDeletion(ctx, tenant, time.Hour, now.Add(time.Hour))
			errs[i] = err
			if replay.DeletionDueAt != nil {
				deadlines[i] = *replay.DeletionDueAt
			}
		}(i)
	}
	close(start)
	wg.Wait()
	for i, err := range errs {
		if err != nil || !deadlines[i].Equal(now.Add(48*time.Hour)) {
			t.Fatalf("delete replay %d = deadline %s err %v, want original deadline", i, deadlines[i], err)
		}
	}
	if _, err := st.CreateSAServerCredential(ctx, tenant, server.ID, SACredential{
		Mode: "header", SecretHash: saTestHash("replay-create"), Scopes: `["verify:join"]`, Status: "active",
	}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("replayed delete then create = %v, want not found", err)
	}
	if _, err := st.RotateSAServerCredential(ctx, tenant, server.ID, oldID, SACredential{
		Mode: "header", SecretHash: saTestHash("replay-rotate"), Scopes: `["verify:join"]`, Status: "active",
	}, time.Hour, now); !errors.Is(err, ErrNotFound) {
		t.Fatalf("replayed delete then rotate = %v, want not found", err)
	}
	rows, err := st.ListSAServerCredentials(ctx, tenant, server.ID)
	if err != nil || len(rows) != 1 || rows[0].ID != oldID || rows[0].Status != "revoked" {
		t.Fatalf("credential rows after replayed writes = %+v %v, want only revoked original", rows, err)
	}
	replay, err := st.RequestSATenantDeletion(ctx, tenant, time.Hour, now.Add(2*time.Hour))
	if err != nil || replay.DeletionDueAt == nil || !replay.DeletionDueAt.Equal(*first.DeletionDueAt) {
		t.Fatalf("final delete replay = %+v %v, want original deadline", replay, err)
	}
}

func saStatementIndex(statements []string, fragment string) int {
	for i, statement := range statements {
		if strings.Contains(statement, fragment) {
			return i
		}
	}
	return -1
}
