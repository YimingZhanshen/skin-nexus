package store

import (
	"context"
	"database/sql"
	"errors"
	"skin-nexus/server/internal/dialect"
	"time"
)

// PluginMeta mirrors one plugin_meta row. Manifest/Config are raw JSON so the
// plugin package owns schema validation and the store stays dumb.
type PluginMeta struct {
	Name          string
	Version       string
	HookAPI       int
	Runtime       string
	Manifest      []byte
	Config        []byte
	SchemaVersion int
	Source        string
	Trust         string
	Signer        string
	Checksum      string
	State         string
	LastError     string
	InstalledAt   time.Time
	UpdatedAt     time.Time
}

type PluginSecret struct {
	Ciphertext []byte
	Nonce      []byte
	KeyVersion int
}

type PluginKV struct {
	Namespace string
	Key       string
	Value     string
	ExpiresAt *time.Time
	UpdatedAt time.Time
}

type PluginDoc struct {
	Collection string
	ID         string
	Doc        []byte
	CreatedAt  time.Time
	UpdatedAt  time.Time
}

func nullableBytes(v []byte) any {
	if len(v) == 0 {
		return nil
	}
	return v
}

func scanPluginMeta(row interface{ Scan(...any) error }) (*PluginMeta, error) {
	var (
		m                PluginMeta
		manifest, config sql.NullString
		lastErr          sql.NullString
	)
	err := row.Scan(&m.Name, &m.Version, &m.HookAPI, &m.Runtime, &manifest, &config,
		&m.SchemaVersion, &m.Source, &m.Trust, &m.Signer, &m.Checksum, &m.State, &lastErr,
		&m.InstalledAt, &m.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	if manifest.Valid {
		m.Manifest = []byte(manifest.String)
	}
	if config.Valid {
		m.Config = []byte(config.String)
	}
	if lastErr.Valid {
		m.LastError = lastErr.String
	}
	return &m, nil
}

func (m *Mysql) GetPluginMeta(ctx context.Context, name string) (*PluginMeta, error) {
	return scanPluginMeta(m.db.QueryRowContext(ctx,
		`SELECT name, version, hook_api, runtime, manifest, config, schema_version,
		        source, trust, signer, checksum, state, last_error, installed_at, updated_at
		   FROM plugin_meta WHERE name = ?`, name))
}

func (m *Mysql) UpsertPluginMeta(ctx context.Context, p PluginMeta) error {
	if p.Runtime == "" {
		p.Runtime = "goja"
	}
	if p.Source == "" {
		p.Source = "manual"
	}
	if p.State == "" {
		p.State = "discovered"
	}
	now := m.now().UTC()
	installed := now
	if !p.InstalledAt.IsZero() {
		installed = p.InstalledAt.UTC()
	}
	// installed_at keeps its first value on conflict (not in the update list);
	// updated_at is carried as an insert column so VALUES()/excluded can stamp
	// the Go clock on both the insert and the update path. name is the PK.
	q, err := m.db.Dialect().Upsert("plugin_meta",
		[]string{"name", "version", "hook_api", "runtime", "manifest", "config", "schema_version",
			"source", "trust", "signer", "checksum", "state", "last_error", "installed_at", "updated_at"},
		[]string{"name"},
		[]string{"version", "hook_api", "runtime", "manifest", "config", "schema_version",
			"source", "trust", "signer", "checksum", "state", "last_error", "updated_at"})
	if err != nil {
		return err
	}
	_, err = m.db.ExecContext(ctx, q,
		p.Name, p.Version, p.HookAPI, p.Runtime, nullableBytes(p.Manifest), nullableBytes(p.Config),
		p.SchemaVersion, p.Source, p.Trust, p.Signer, p.Checksum, p.State, nullableStr(p.LastError), installed, now)
	return err
}

func (m *Mysql) ListPluginMeta(ctx context.Context) ([]PluginMeta, error) {
	rows, err := m.db.QueryContext(ctx,
		`SELECT name, version, hook_api, runtime, manifest, config, schema_version,
		        source, trust, signer, checksum, state, last_error, installed_at, updated_at
		   FROM plugin_meta ORDER BY name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []PluginMeta
	for rows.Next() {
		p, err := scanPluginMeta(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *p)
	}
	return out, rows.Err()
}

func (m *Mysql) UpdatePluginState(ctx context.Context, name, state, lastErr string) error {
	_, err := m.db.ExecContext(ctx,
		`UPDATE plugin_meta SET state=?, last_error=?, updated_at=? WHERE name=?`,
		state, nullableStr(lastErr), m.now().UTC(), name)
	return err
}

func (m *Mysql) GetPluginConfig(ctx context.Context, name string) ([]byte, error) {
	var cfg sql.NullString
	err := m.db.QueryRowContext(ctx, `SELECT config FROM plugin_meta WHERE name = ?`, name).Scan(&cfg)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	if !cfg.Valid {
		return nil, nil
	}
	return []byte(cfg.String), nil
}

func (m *Mysql) SetPluginConfig(ctx context.Context, name string, config []byte) error {
	_, err := m.db.ExecContext(ctx,
		`UPDATE plugin_meta SET config=?, updated_at=? WHERE name=?`,
		nullableBytes(config), m.now().UTC(), name)
	return err
}

func (m *Mysql) GetPluginSecret(ctx context.Context, plugin, key string) (*PluginSecret, error) {
	var s PluginSecret
	err := m.db.QueryRowContext(ctx,
		`SELECT ciphertext, nonce, key_version FROM plugin_secrets WHERE plugin=? AND k=?`,
		plugin, key).Scan(&s.Ciphertext, &s.Nonce, &s.KeyVersion)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return &s, nil
}

func (m *Mysql) SetPluginSecret(ctx context.Context, plugin, key string, s PluginSecret) error {
	if s.KeyVersion <= 0 {
		s.KeyVersion = 1
	}
	q, err := m.db.Dialect().Upsert("plugin_secrets",
		[]string{"plugin", "k", "ciphertext", "nonce", "key_version", "updated_at"},
		[]string{"plugin", "k"},
		[]string{"ciphertext", "nonce", "key_version", "updated_at"})
	if err != nil {
		return err
	}
	_, err = m.db.ExecContext(ctx, q, plugin, key, s.Ciphertext, s.Nonce, s.KeyVersion, m.now().UTC())
	return err
}

func (m *Mysql) DeletePluginSecret(ctx context.Context, plugin, key string) error {
	_, err := m.db.ExecContext(ctx, `DELETE FROM plugin_secrets WHERE plugin=? AND k=?`, plugin, key)
	return err
}

func (m *Mysql) GetPluginKV(ctx context.Context, plugin, ns, key string) (string, *time.Time, bool, error) {
	var (
		v       string
		expires sql.NullTime
	)
	err := m.db.QueryRowContext(ctx,
		`SELECT v, expires_at FROM plugin_kv
		  WHERE plugin=? AND ns=? AND k=? AND (expires_at IS NULL OR expires_at > ?)`,
		plugin, ns, key, m.now().UTC()).Scan(&v, &expires)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil, false, nil
	}
	if err != nil {
		return "", nil, false, err
	}
	if expires.Valid {
		t := expires.Time
		return v, &t, true, nil
	}
	return v, nil, true, nil
}

func (m *Mysql) SetPluginKV(ctx context.Context, plugin, ns, key, value string, expiresAt *time.Time) error {
	q, err := m.db.Dialect().Upsert("plugin_kv",
		[]string{"plugin", "ns", "k", "v", "expires_at", "updated_at"},
		[]string{"plugin", "ns", "k"},
		[]string{"v", "expires_at", "updated_at"})
	if err != nil {
		return err
	}
	_, err = m.db.ExecContext(ctx, q, plugin, ns, key, value, expiresAt, m.now().UTC())
	return err
}

func (m *Mysql) DeletePluginKV(ctx context.Context, plugin, ns, key string) error {
	_, err := m.db.ExecContext(ctx, `DELETE FROM plugin_kv WHERE plugin=? AND ns=? AND k=?`, plugin, ns, key)
	return err
}

// prefixMatch builds the "starts with" predicate for plugin key/doc listings.
// MySQL (utf8mb4_bin, migration 0032) and PostgreSQL compare case-sensitively
// in both `=` and LIKE, but SQLite's LIKE folds ASCII case while its PK does
// not — so a prefix listing would swallow the other case there. Flipping
// case_sensitive_like is off the table (every search box depends on the
// folding), so SQLite gets a length-bounded substring compare instead, which
// matches the case-sensitive equality and stays inside the (plugin, …) PK
// slice. An empty prefix lists everything on every engine.
func (m *Mysql) prefixMatch(col, prefix string) (string, []any) {
	if m.Engine() == dialect.SQLite && prefix != "" {
		return "substr(" + col + ", 1, length(?)) = ?", []any{prefix, prefix}
	}
	return col + " LIKE " + m.db.Dialect().Concat("?", "'%'") + dialect.LikeEscapeClause(),
		[]any{dialect.LikePrefix(prefix)}
}

func (m *Mysql) ListPluginKV(ctx context.Context, plugin, ns, prefix string, limit int) ([]PluginKV, error) {
	if limit <= 0 || limit > 1000 {
		limit = 100
	}
	pred, prefixArgs := m.prefixMatch("k", prefix)
	rows, err := m.db.QueryContext(ctx,
		`SELECT ns, k, v, expires_at, updated_at FROM plugin_kv
		  WHERE plugin=? AND ns=? AND `+pred+`
		    AND (expires_at IS NULL OR expires_at > ?)
		  ORDER BY k LIMIT ?`,
		append([]any{plugin, ns}, append(prefixArgs, m.now().UTC(), limit)...)...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []PluginKV
	for rows.Next() {
		var (
			kv      PluginKV
			expires sql.NullTime
		)
		if err := rows.Scan(&kv.Namespace, &kv.Key, &kv.Value, &expires, &kv.UpdatedAt); err != nil {
			return nil, err
		}
		if expires.Valid {
			t := expires.Time
			kv.ExpiresAt = &t
		}
		out = append(out, kv)
	}
	return out, rows.Err()
}

func (m *Mysql) CountPluginKV(ctx context.Context, plugin string) (int, error) {
	var n int
	err := m.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM plugin_kv WHERE plugin=?
		  AND (expires_at IS NULL OR expires_at > ?)`, plugin, m.now().UTC()).Scan(&n)
	return n, err
}

func (m *Mysql) SweepPluginKV(ctx context.Context, limit int) (int64, error) {
	if limit <= 0 {
		limit = 500
	}
	res, err := m.db.ExecContext(ctx,
		// DELETE ... LIMIT is a MySQL extension. The row keys come from a
		// derived table so MySQL does not see the delete target inside its own
		// subquery, and PostgreSQL gets the one spelling it accepts.
		`DELETE FROM plugin_kv WHERE (plugin, ns, k) IN (
			SELECT p.plugin, p.ns, p.k FROM (
				SELECT plugin, ns, k FROM plugin_kv
				 WHERE expires_at IS NOT NULL AND expires_at <= ? LIMIT ?) p)`,
		m.now().UTC(), limit)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

func (m *Mysql) GetPluginDoc(ctx context.Context, plugin, coll, id string) (*PluginDoc, error) {
	var (
		d   PluginDoc
		doc string
	)
	err := m.db.QueryRowContext(ctx,
		`SELECT coll, id, doc, created_at, updated_at FROM plugin_docs WHERE plugin=? AND coll=? AND id=?`,
		plugin, coll, id).Scan(&d.Collection, &d.ID, &doc, &d.CreatedAt, &d.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	d.Doc = []byte(doc)
	return &d, nil
}

func (m *Mysql) PutPluginDoc(ctx context.Context, plugin, coll, id string, doc []byte) error {
	q, err := m.db.Dialect().Upsert("plugin_docs",
		[]string{"plugin", "coll", "id", "doc", "updated_at"},
		[]string{"plugin", "coll", "id"},
		[]string{"doc", "updated_at"})
	if err != nil {
		return err
	}
	_, err = m.db.ExecContext(ctx, q, plugin, coll, id, doc, m.now().UTC())
	return err
}

func (m *Mysql) DeletePluginDoc(ctx context.Context, plugin, coll, id string) error {
	_, err := m.db.ExecContext(ctx, `DELETE FROM plugin_docs WHERE plugin=? AND coll=? AND id=?`, plugin, coll, id)
	return err
}

func (m *Mysql) ListPluginDocs(ctx context.Context, plugin, coll, prefix string, limit int) ([]PluginDoc, error) {
	if limit <= 0 || limit > 1000 {
		limit = 100
	}
	pred, prefixArgs := m.prefixMatch("id", prefix)
	rows, err := m.db.QueryContext(ctx,
		`SELECT coll, id, doc, created_at, updated_at FROM plugin_docs
		  WHERE plugin=? AND coll=? AND `+pred+`
		  ORDER BY id LIMIT ?`,
		append([]any{plugin, coll}, append(prefixArgs, limit)...)...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []PluginDoc
	for rows.Next() {
		var (
			d   PluginDoc
			doc string
		)
		if err := rows.Scan(&d.Collection, &d.ID, &doc, &d.CreatedAt, &d.UpdatedAt); err != nil {
			return nil, err
		}
		d.Doc = []byte(doc)
		out = append(out, d)
	}
	return out, rows.Err()
}

func (m *Mysql) CountPluginDocs(ctx context.Context, plugin, coll string) (int, error) {
	var n int
	err := m.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM plugin_docs WHERE plugin=? AND coll=?`, plugin, coll).Scan(&n)
	return n, err
}

func (m *Mysql) PluginMigrationApplied(ctx context.Context, plugin string, version int) (bool, error) {
	var n int
	err := m.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM plugin_migrations WHERE plugin=? AND version=?`, plugin, version).Scan(&n)
	if err != nil {
		return false, err
	}
	return n > 0, nil
}

func (m *Mysql) RecordPluginMigration(ctx context.Context, plugin string, version int) error {
	q, err := m.db.Dialect().InsertIgnore("plugin_migrations", []string{"plugin", "version"})
	if err != nil {
		return err
	}
	_, err = m.db.ExecContext(ctx, q, plugin, version)
	return err
}

func (m *Mysql) DeletePluginData(ctx context.Context, plugin string) error {
	tx, err := m.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, q := range []string{
		`DELETE FROM plugin_kv WHERE plugin=?`,
		`DELETE FROM plugin_docs WHERE plugin=?`,
		`DELETE FROM plugin_secrets WHERE plugin=?`,
		`DELETE FROM plugin_migrations WHERE plugin=?`,
		`DELETE FROM plugin_install_tx WHERE plugin=?`,
		`DELETE FROM plugin_meta WHERE name=?`,
	} {
		if _, err := tx.ExecContext(ctx, q, plugin); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (m *Mysql) PluginMigrationLatest(ctx context.Context, plugin string) (int, error) {
	var v sql.NullInt64
	err := m.db.QueryRowContext(ctx,
		`SELECT MAX(version) FROM plugin_migrations WHERE plugin=?`, plugin).Scan(&v)
	if err != nil {
		return 0, err
	}
	if !v.Valid {
		return 0, nil
	}
	return int(v.Int64), nil
}
