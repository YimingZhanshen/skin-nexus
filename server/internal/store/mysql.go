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
	"strings"
	"time"

	"skin-nexus/server/internal/auth"
	"skin-nexus/server/internal/dbx"
	"skin-nexus/server/internal/dialect"
	"skin-nexus/server/internal/siteurl"
	"skin-nexus/server/internal/ygg"
)

// ErrNotFound is the canonical "no such row" signal.
var ErrNotFound = errors.New("store: not found")

// ErrDuplicatePublic means another public texture already owns this
// (type, hash) content. Private copies are intentionally not covered.
var ErrDuplicatePublic = errors.New("store: duplicate public texture")

// ErrModerationBan: the texture was struck by a moderation verdict and may
// never re-enter the public library, only stay private or be deleted.
var ErrModerationBan = errors.New("store: texture banned from republication")

// ErrForbidden: the row exists but the caller may not touch it (e.g.
// editing another user's texture). Kept distinct from ErrNotFound so the
// API layer can answer 403 vs 404 correctly.
var ErrForbidden = errors.New("store: forbidden")

type Mysql struct {
	db     *dbx.DB
	legacy *auth.LegacyVerifier // nil = legacy logins rejected
	now    func() time.Time
}

// Open connects and pings the database, then hands the pool to the store.
// legacy may be nil (no legacy corpus yet). engine is "" (infer from the DSN
// scheme, else MySQL — what every deployment ran on before this existed),
// "mysql", "sqlite" or "postgres".
//
// It takes no base URL on purpose: the address is resolved from the database a
// few steps later in boot (the `site_url` option overrides env), so a store
// that captured it at Open time would serve the pre-override value forever.
// Absolute URLs come from internal/siteurl, read when they are built.
func Open(engine, dsn string, legacy *auth.LegacyVerifier) (*Mysql, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	db, err := dbx.Open(ctx, engine, dsn)
	if err != nil {
		return nil, err
	}
	return &Mysql{db: db, legacy: legacy, now: time.Now}, nil
}

// Engine reports which SQL dialect this store speaks (tests and the migrate
// CLI use it to pick the schema tree).
func (m *Mysql) Engine() dialect.Engine { return m.db.Engine() }

func (m *Mysql) Close() error { return m.db.Close() }

func (m *Mysql) TextureURL(hash string) string { return siteurl.Texture(hash) }

// ---- users ----

type rowScanner interface{ Scan(...interface{}) error }

func scanUser(s rowScanner) (ygg.User, error) {
	var u ygg.User
	err := s.Scan(&u.ID, &u.Nickname, &u.EmailVerified, &u.CanJoinServer, &u.CreatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return ygg.User{}, ErrNotFound
	}
	return u, err
}

func (m *Mysql) UserByID(ctx context.Context, id int64) (ygg.User, error) {
	return scanUser(m.db.QueryRowContext(ctx,
		"SELECT id, nickname, email_verified, can_join_server, created_at FROM users WHERE id = ?", id))
}

func (m *Mysql) UserByIdentifier(ctx context.Context, ident string) (ygg.User, error) {
	// Legacy semantics: identify by email OR nickname, both case-insensitive
	// (the utf8mb4_general_ci column collation folded both on MySQL). LOWER()
	// keeps that folding on PostgreSQL, whose default collation is case-
	// sensitive; MySQL/SQLite already fold, so the predicate is a no-op there.
	eng := m.db.Dialect()
	return scanUser(m.db.QueryRowContext(ctx,
		"SELECT id, nickname, email_verified, can_join_server, created_at FROM users WHERE "+
			eng.CIEquals("email")+" OR "+eng.CIEquals("nickname")+" LIMIT 1",
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
	// Case-insensitive legacy name match: on MySQL the general_ci column
	// collation folded it, so LOWER() preserves that on PostgreSQL (case-
	// sensitive default) and is a no-op on MySQL/SQLite.
	return m.scanPlayer(ctx, m.db.Dialect().CIEquals("p.name"), name)
}

func (m *Mysql) PlayersByUID(ctx context.Context, uid int64) ([]ygg.Player, error) {
	rows, err := m.db.QueryContext(ctx, playerSelect+" WHERE p.uid = ? ORDER BY p.id", uid)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return collectPlayers(rows)
}

const playerSelect = `SELECT p.id, p.uid, p.name, COALESCE(u.uuid,''),
  COALESCE(ts.hash,''), COALESCE(ts.model,'default'), COALESCE(tc.hash,''),
  COALESCE(p.tid_skin,0), COALESCE(p.tid_cape,0)
  FROM players p
  LEFT JOIN uuid_map u ON u.player_id = p.id
  LEFT JOIN textures ts ON ts.id = p.tid_skin
  LEFT JOIN textures tc ON tc.id = p.tid_cape`

func (m *Mysql) scanPlayer(ctx context.Context, where string, arg interface{}) (ygg.Player, error) {
	row := m.db.QueryRowContext(ctx, playerSelect+" WHERE "+where+" LIMIT 1", arg)
	return scanPlayerRow(row)
}

func scanPlayerRow(s rowScanner) (ygg.Player, error) {
	var p ygg.Player
	err := s.Scan(&p.ID, &p.UID, &p.Name, &p.UUID, &p.SkinURL, &p.SkinModel, &p.CapeURL,
		&p.SkinTID, &p.CapeTID)
	if errors.Is(err, sql.ErrNoRows) {
		return ygg.Player{}, ErrNotFound
	}
	if err != nil {
		return ygg.Player{}, err
	}
	// hash columns -> absolute texture URLs (hashes kept for plugin filters)
	if p.SkinURL != "" && !strings.HasPrefix(p.SkinURL, "http") {
		p.SkinHash = p.SkinURL
		p.SkinURL = siteurl.Texture(p.SkinURL)
	}
	if p.CapeURL != "" && !strings.HasPrefix(p.CapeURL, "http") {
		p.CapeHash = p.CapeURL
		p.CapeURL = siteurl.Texture(p.CapeURL)
	}
	return p, nil
}

func collectPlayers(rows *sql.Rows) ([]ygg.Player, error) {
	var out []ygg.Player
	for rows.Next() {
		p, err := scanPlayerRow(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// ---- ygg tokens ----
//
// access_token is stored and matched as hex(sha256(value)) — see digest.go.
// client_token stays verbatim: it is the pairing id, useless without an access
// token, and Yggdrasil requires the server to echo the same value back.

func (m *Mysql) SaveToken(ctx context.Context, t ygg.Token) error {
	_, err := m.db.ExecContext(ctx,
		`INSERT INTO ygg_tokens (user_id, client_token, access_token, selected_pid, created_at, expires_at, invalidated)
		 VALUES (?,?,?,?,?,?,0)`,
		t.UserID, t.ClientToken, credentialDigest(t.AccessToken), nullableInt(t.SelectedPID), t.CreatedAt, t.ExpiresAt)
	return err
}

func (m *Mysql) TokenByAccess(ctx context.Context, access string) (ygg.Token, error) {
	var t ygg.Token
	var sel sql.NullInt64
	err := m.db.QueryRowContext(ctx,
		`SELECT user_id, client_token, selected_pid, created_at, expires_at, invalidated
		 FROM ygg_tokens WHERE access_token = ?`, credentialDigest(access)).
		Scan(&t.UserID, &t.ClientToken, &sel, &t.CreatedAt, &t.ExpiresAt, &t.Invalidated)
	if errors.Is(err, sql.ErrNoRows) {
		return ygg.Token{}, ErrNotFound
	}
	t.SelectedPID = sel.Int64
	// The stored column is a digest and cannot be turned back into a token; the
	// caller already holds the value it presented, and that is the only thing
	// downstream code (Join → SaveJoin) needs it for.
	t.AccessToken = access
	return t, err
}

func (m *Mysql) InvalidateToken(ctx context.Context, access string) error {
	_, err := m.db.ExecContext(ctx, "UPDATE ygg_tokens SET invalidated = 1 WHERE access_token = ?", credentialDigest(access))
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
		// UPDATE ... ORDER BY ... LIMIT is MySQL-only; the derived table keeps
		// "evict the oldest, keep the newest `keep`" on all three engines.
		`UPDATE ygg_tokens SET invalidated = 1 WHERE id IN (
			SELECT t.id FROM (
				SELECT id FROM ygg_tokens WHERE user_id = ? AND invalidated = 0
				 ORDER BY id ASC LIMIT ?) t)`, uid, n-keep)
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
		credentialDigest(j.AccessToken), j.UUID, j.ServerID, j.ServerIP, j.CreatedAt, j.ExpiresAt)
	return err
}

func (m *Mysql) JoinByTokenServer(ctx context.Context, access, serverID string) (ygg.JoinSession, error) {
	var j ygg.JoinSession
	// access_token is not selected: the column now holds a digest, and no caller
	// may treat it as the token.
	err := m.db.QueryRowContext(ctx,
		`SELECT uuid, server_id, server_ip, created_at, expires_at
		 FROM ygg_join_sessions WHERE access_token = ? AND server_id = ? ORDER BY id DESC LIMIT 1`,
		credentialDigest(access), serverID).
		Scan(&j.UUID, &j.ServerID, &j.ServerIP, &j.CreatedAt, &j.ExpiresAt)
	if errors.Is(err, sql.ErrNoRows) {
		return ygg.JoinSession{}, ErrNotFound
	}
	j.AccessToken = access
	return j, err
}

// FindLiveJoins powers hasJoined (service uses this optional scanner).
func (m *Mysql) FindLiveJoins(ctx context.Context, uuid string, now time.Time) ([]ygg.JoinSession, error) {
	rows, err := m.db.QueryContext(ctx,
		`SELECT uuid, server_id, server_ip, created_at, expires_at
		 FROM ygg_join_sessions WHERE uuid = ? AND expires_at > ? ORDER BY id DESC`, uuid, now)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ygg.JoinSession
	for rows.Next() {
		var j ygg.JoinSession
		if err := rows.Scan(&j.UUID, &j.ServerID, &j.ServerIP, &j.CreatedAt, &j.ExpiresAt); err != nil {
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
