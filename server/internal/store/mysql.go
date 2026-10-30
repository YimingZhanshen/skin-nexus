// Package store implements the ygg.Store port over MySQL plus the web-facing
// queries (registration, sessions, textures, players).
//
// password_algo conventions (docs/phase1-dev-plan.md §0):
//   - "argon2id" / "bcrypt"  -> modern PHC digest, verify directly
//   - "legacy"               -> try ALL configured legacy methods in order
//     (official strategy: Cipher.php:24-49)
//   - "legacy:<method>"      -> known method from legacy .env, verify just it
package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"skin-nexus/server/internal/auth"
	"skin-nexus/server/internal/ygg"

	mysql "github.com/go-sql-driver/mysql"
)

// ErrNotFound is the canonical "no such row" signal.
var ErrNotFound = errors.New("store: not found")

// ErrDuplicatePublic means another public texture already owns this
// (type, hash) content. Private copies are intentionally not covered.
var ErrDuplicatePublic = errors.New("store: duplicate public texture")

func isDuplicateKey(err error) bool {
	var me *mysql.MySQLError
	return errors.As(err, &me) && me.Number == 1062
}

// ErrForbidden: the row exists but the caller may not touch it (e.g.
// editing another user's texture). Kept distinct from ErrNotFound so the
// API layer can answer 403 vs 404 correctly.
var ErrForbidden = errors.New("store: forbidden")

type Mysql struct {
	db      *sql.DB
	baseURL string               // for texture URLs: <base>/textures/<hash>
	legacy  *auth.LegacyVerifier // nil = legacy logins rejected
	now     func() time.Time
}

// Open connects and pings MySQL. legacy may be nil (no legacy corpus yet).
func Open(dsn, baseURL string, legacy *auth.LegacyVerifier) (*Mysql, error) {
	// DSN foot-gun guard (review C P3): without parseTime=true every
	// time.Time scan fails at runtime while Ping still succeeds.
	if !strings.Contains(dsn, "parseTime=true") {
		return nil, fmt.Errorf("store: DSN must carry parseTime=true")
	}
	db, err := sql.Open("mysql", dsn)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(20)
	db.SetMaxIdleConns(10)
	db.SetConnMaxLifetime(5 * time.Minute)
	if err := db.Ping(); err != nil {
		return nil, fmt.Errorf("store: ping: %w", err)
	}
	return &Mysql{db: db, baseURL: baseURL, legacy: legacy, now: time.Now}, nil
}

func (m *Mysql) Close() error { return m.db.Close() }

func (m *Mysql) TextureURL(hash string) string {
	return m.baseURL + "/textures/" + hash
}

// ---- users ----

type rowScanner interface{ Scan(...interface{}) error }

func scanUser(s rowScanner) (ygg.User, error) {
	var u ygg.User
	err := s.Scan(&u.ID, &u.Nickname, &u.EmailVerified, &u.CanJoinServer)
	if errors.Is(err, sql.ErrNoRows) {
		return ygg.User{}, ErrNotFound
	}
	return u, err
}

func (m *Mysql) UserByID(ctx context.Context, id int64) (ygg.User, error) {
	return scanUser(m.db.QueryRowContext(ctx,
		"SELECT id, nickname, email_verified, can_join_server FROM users WHERE id = ?", id))
}

func (m *Mysql) UserByIdentifier(ctx context.Context, ident string) (ygg.User, error) {
	// Legacy semantics: identify by email OR nickname (case-insensitive email).
	// COLLATE must sit on the column expression: `? COLLATE ...` is a syntax
	// error on MySQL 5.7 (8.0 tolerates it) and made every login fail with
	// "account or password" while the register->session path stayed healthy.
	return scanUser(m.db.QueryRowContext(ctx,
		"SELECT id, nickname, email_verified, can_join_server FROM users WHERE email COLLATE utf8mb4_general_ci = ? OR nickname = ? LIMIT 1",
		ident, ident))
}

// credentials returns password + algo for verification.
func (m *Mysql) credentials(ctx context.Context, uid int64) (hash, algo string, err error) {
	err = m.db.QueryRowContext(ctx, "SELECT password, password_algo FROM users WHERE id = ?", uid).
		Scan(&hash, &algo)
	if errors.Is(err, sql.ErrNoRows) {
		err = ErrNotFound
	}
	return
}

// VerifyPassword implements ygg.Store. Modern digests verify directly; legacy
// rows walk the configured chain (try-all or pinned method). PHC
// self-describing digests (imported $2y$ bcrypt / argon2 rows) verify via the
// PHC path even when the legacy salt chain is not mounted (SKIN_LEGACY_ENV) —
// the chain only matters for positional-salt formats (migrate importer marks
// them `legacy:bcrypt` etc).
func (m *Mysql) VerifyPassword(ctx context.Context, uid int64, password string) (bool, error) {
	hash, algo, err := m.credentials(ctx, uid)
	if err != nil {
		return false, err
	}
	switch {
	case strings.HasPrefix(algo, "legacy"):
		if strings.HasPrefix(hash, "$2") || strings.HasPrefix(hash, "$argon2") {
			return auth.VerifyPHC(password, hash), nil // self-describing
		}
		if m.legacy == nil {
			return false, nil
		}
		if method, ok := strings.CutPrefix(algo, "legacy:"); ok {
			return m.legacy.VerifyOne(password, hash, method), nil // pinned method
		}
		_, ok := m.legacy.VerifyLegacy(password, hash)
		return ok, nil
	default:
		return auth.VerifyPHC(password, hash), nil
	}
}

// ---- players ----

func (m *Mysql) PlayerByID(ctx context.Context, id int64) (ygg.Player, error) {
	// p.id must be qualified: playerSelect joins textures (ts/tc also carry
	// id columns) — a bare `id` raised ER1052 ambiguity on first admin use
	// (CI integration suite, 2026-10-24).
	return m.scanPlayer(ctx, "p.id = ?", id)
}

func (m *Mysql) PlayerByUUID(ctx context.Context, uuid string) (ygg.Player, error) {
	return m.scanPlayer(ctx, "u.uuid = ?", uuid)
}

func (m *Mysql) PlayerByName(ctx context.Context, name string) (ygg.Player, error) {
	// COLLATE sits on the COLUMN side on purpose: go-sql-driver uses
	// server-side prepared statements (binary protocol), where the
	// parameter charset is not yet known — `? COLLATE utf8mb4_general_ci`
	// (collate-on-placeholder) made MySQL 5.7 fail every lookup with
	// 1267 Illegal mix of collations (observed on the panel 5.7.44 test
	// DB; literal-text queries still worked, masking it in ad-hoc SQL).
	// Collating the column (whose charset is fixed by DDL) keeps the
	// case-insensitive legacy name match in both protocols.
	return m.scanPlayer(ctx, "p.name COLLATE utf8mb4_general_ci = ?", name)
}

func (m *Mysql) PlayersByUID(ctx context.Context, uid int64) ([]ygg.Player, error) {
	rows, err := m.db.QueryContext(ctx, playerSelect+" WHERE p.uid = ? ORDER BY p.id", uid)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return collectPlayers(rows, m.baseURL)
}

const playerSelect = `SELECT p.id, p.uid, p.name, COALESCE(u.uuid,''),
  COALESCE(ts.hash,''), COALESCE(ts.model,'default'), COALESCE(tc.hash,'')
  FROM players p
  LEFT JOIN uuid_map u ON u.player_id = p.id
  LEFT JOIN textures ts ON ts.id = p.tid_skin
  LEFT JOIN textures tc ON tc.id = p.tid_cape`

func (m *Mysql) scanPlayer(ctx context.Context, where string, arg interface{}) (ygg.Player, error) {
	row := m.db.QueryRowContext(ctx, playerSelect+" WHERE "+where+" LIMIT 1", arg)
	return scanPlayerRow(row, m.baseURL)
}

func scanPlayerRow(s rowScanner, baseURL string) (ygg.Player, error) {
	var p ygg.Player
	err := s.Scan(&p.ID, &p.UID, &p.Name, &p.UUID, &p.SkinURL, &p.SkinModel, &p.CapeURL)
	if errors.Is(err, sql.ErrNoRows) {
		return ygg.Player{}, ErrNotFound
	}
	if err != nil {
		return ygg.Player{}, err
	}
	// hash columns -> absolute texture URLs (hashes kept for plugin filters)
	if p.SkinURL != "" && !strings.HasPrefix(p.SkinURL, "http") {
		p.SkinHash = p.SkinURL
		p.SkinURL = baseURL + "/textures/" + p.SkinURL
	}
	if p.CapeURL != "" && !strings.HasPrefix(p.CapeURL, "http") {
		p.CapeHash = p.CapeURL
		p.CapeURL = baseURL + "/textures/" + p.CapeURL
	}
	return p, nil
}

func collectPlayers(rows *sql.Rows, baseURL string) ([]ygg.Player, error) {
	var out []ygg.Player
	for rows.Next() {
		p, err := scanPlayerRow(rows, baseURL)
		if err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// ---- ygg tokens ----

func (m *Mysql) SaveToken(ctx context.Context, t ygg.Token) error {
	_, err := m.db.ExecContext(ctx,
		`INSERT INTO ygg_tokens (user_id, client_token, access_token, selected_pid, created_at, expires_at, invalidated)
		 VALUES (?,?,?,?,?,?,0)`,
		t.UserID, t.ClientToken, t.AccessToken, nullableInt(t.SelectedPID), t.CreatedAt, t.ExpiresAt)
	return err
}

func (m *Mysql) TokenByAccess(ctx context.Context, access string) (ygg.Token, error) {
	var t ygg.Token
	var sel sql.NullInt64
	err := m.db.QueryRowContext(ctx,
		`SELECT user_id, client_token, access_token, selected_pid, created_at, expires_at, invalidated
		 FROM ygg_tokens WHERE access_token = ?`, access).
		Scan(&t.UserID, &t.ClientToken, &t.AccessToken, &sel, &t.CreatedAt, &t.ExpiresAt, &t.Invalidated)
	if errors.Is(err, sql.ErrNoRows) {
		return ygg.Token{}, ErrNotFound
	}
	t.SelectedPID = sel.Int64
	return t, err
}

func (m *Mysql) InvalidateToken(ctx context.Context, access string) error {
	_, err := m.db.ExecContext(ctx, "UPDATE ygg_tokens SET invalidated = 1 WHERE access_token = ?", access)
	return err
}

func (m *Mysql) InvalidateUserTokens(ctx context.Context, uid int64) error {
	_, err := m.db.ExecContext(ctx, "UPDATE ygg_tokens SET invalidated = 1 WHERE user_id = ?", uid)
	return err
}

// PruneUserTokens implements ygg.TokenPruner (ygg_tokens_limit, 2026-10-24
// 拍板：超限作废最早签发的令牌 + 写审计，不拒新签发). Keeps at most `keep`
// live tokens per user; `id` is AUTO_INCREMENT so ASC = issuance order.
// MySQL single-table UPDATE ... ORDER BY ... LIMIT extension.
func (m *Mysql) PruneUserTokens(ctx context.Context, uid int64, keep int) (int64, error) {
	if keep <= 0 {
		return 0, nil
	}
	var n int
	if err := m.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM ygg_tokens WHERE user_id = ? AND invalidated = 0`, uid).Scan(&n); err != nil {
		return 0, err
	}
	if n <= keep {
		return 0, nil
	}
	res, err := m.db.ExecContext(ctx,
		`UPDATE ygg_tokens SET invalidated = 1
		 WHERE user_id = ? AND invalidated = 0
		 ORDER BY id ASC LIMIT ?`, uid, n-keep)
	if err != nil {
		return 0, err
	}
	evicted, _ := res.RowsAffected()
	if evicted > 0 {
		m.InsertAudit(ctx, uid, "ygg_tokens_pruned",
			map[string]any{"evicted": evicted, "kept": keep, "reason": "ygg_tokens_limit"})
	}
	return evicted, nil
}

// ---- join sessions ----

func (m *Mysql) SaveJoin(ctx context.Context, j ygg.JoinSession) error {
	_, err := m.db.ExecContext(ctx,
		`INSERT INTO ygg_join_sessions (access_token, uuid, server_id, server_ip, created_at, expires_at)
		 VALUES (?,?,?,?,?,?)`,
		j.AccessToken, j.UUID, j.ServerID, j.ServerIP, j.CreatedAt, j.ExpiresAt)
	return err
}

func (m *Mysql) JoinByTokenServer(ctx context.Context, access, serverID string) (ygg.JoinSession, error) {
	var j ygg.JoinSession
	err := m.db.QueryRowContext(ctx,
		`SELECT access_token, uuid, server_id, server_ip, created_at, expires_at
		 FROM ygg_join_sessions WHERE access_token = ? AND server_id = ? ORDER BY id DESC LIMIT 1`,
		access, serverID).
		Scan(&j.AccessToken, &j.UUID, &j.ServerID, &j.ServerIP, &j.CreatedAt, &j.ExpiresAt)
	if errors.Is(err, sql.ErrNoRows) {
		return ygg.JoinSession{}, ErrNotFound
	}
	return j, err
}

// FindLiveJoins powers hasJoined (service uses this optional scanner).
func (m *Mysql) FindLiveJoins(ctx context.Context, uuid string, now time.Time) ([]ygg.JoinSession, error) {
	rows, err := m.db.QueryContext(ctx,
		`SELECT access_token, uuid, server_id, server_ip, created_at, expires_at
		 FROM ygg_join_sessions WHERE uuid = ? AND expires_at > ? ORDER BY id DESC`, uuid, now)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ygg.JoinSession
	for rows.Next() {
		var j ygg.JoinSession
		if err := rows.Scan(&j.AccessToken, &j.UUID, &j.ServerID, &j.ServerIP, &j.CreatedAt, &j.ExpiresAt); err != nil {
			return nil, err
		}
		out = append(out, j)
	}
	return out, rows.Err()
}

func nullableInt(v int64) interface{} {
	if v == 0 {
		return nil
	}
	return v
}
