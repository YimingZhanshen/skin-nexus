package store

import (
	"context"
	"database/sql"
	"errors"
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
	var installed any
	if !p.InstalledAt.IsZero() {
		installed = p.InstalledAt
	}
	_, err := m.db.ExecContext(ctx,
		`INSERT INTO plugin_meta
		   (name, version, hook_api, runtime, manifest, config, schema_version,
		    source, trust, signer, checksum, state, last_error, installed_at, updated_at)
		 VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,COALESCE(?, CURRENT_TIMESTAMP(3)), CURRENT_TIMESTAMP(3))
		 ON DUPLICATE KEY UPDATE
		   version=VALUES(version), hook_api=VALUES(hook_api), runtime=VALUES(runtime),
		   manifest=VALUES(manifest), config=VALUES(config), schema_version=VALUES(schema_version),
		   source=VALUES(source), trust=VALUES(trust), signer=VALUES(signer),
		   checksum=VALUES(checksum), state=VALUES(state),
		   last_error=VALUES(last_error), updated_at=CURRENT_TIMESTAMP(3)`,
		p.Name, p.Version, p.HookAPI, p.Runtime, nullableBytes(p.Manifest), nullableBytes(p.Config),
		p.SchemaVersion, p.Source, p.Trust, p.Signer, p.Checksum, p.State, nullableStr(p.LastError), installed)
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
		`UPDATE plugin_meta SET state=?, last_error=?, updated_at=CURRENT_TIMESTAMP(3) WHERE name=?`,
		state, nullableStr(lastErr), name)
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
		`UPDATE plugin_meta SET config=?, updated_at=CURRENT_TIMESTAMP(3) WHERE name=?`,
		nullableBytes(config), name)
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
	_, err := m.db.ExecContext(ctx,
		`INSERT INTO plugin_secrets (plugin, k, ciphertext, nonce, key_version)
		 VALUES (?,?,?,?,?)
		 ON DUPLICATE KEY UPDATE ciphertext=VALUES(ciphertext), nonce=VALUES(nonce),
		   key_version=VALUES(key_version), updated_at=CURRENT_TIMESTAMP(3)`,
		plugin, key, s.Ciphertext, s.Nonce, s.KeyVersion)
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
		  WHERE plugin=? AND ns=? AND k=? AND (expires_at IS NULL OR expires_at > NOW(3))`,
		plugin, ns, key).Scan(&v, &expires)
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
	_, err := m.db.ExecContext(ctx,
		`INSERT INTO plugin_kv (plugin, ns, k, v, expires_at)
		 VALUES (?,?,?,?,?)
		 ON DUPLICATE KEY UPDATE v=VALUES(v), expires_at=VALUES(expires_at), updated_at=CURRENT_TIMESTAMP(3)`,
		plugin, ns, key, value, expiresAt)
	return err
}

func (m *Mysql) DeletePluginKV(ctx context.Context, plugin, ns, key string) error {
	_, err := m.db.ExecContext(ctx, `DELETE FROM plugin_kv WHERE plugin=? AND ns=? AND k=?`, plugin, ns, key)
	return err
}

func (m *Mysql) ListPluginKV(ctx context.Context, plugin, ns, prefix string, limit int) ([]PluginKV, error) {
	if limit <= 0 || limit > 1000 {
		limit = 100
	}
	rows, err := m.db.QueryContext(ctx,
		`SELECT ns, k, v, expires_at, updated_at FROM plugin_kv
		  WHERE plugin=? AND ns=? AND k LIKE CONCAT(?, '%')
		    AND (expires_at IS NULL OR expires_at > NOW(3))
		  ORDER BY k LIMIT ?`, plugin, ns, prefix, limit)
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
		  AND (expires_at IS NULL OR expires_at > NOW(3))`, plugin).Scan(&n)
	return n, err
}

func (m *Mysql) SweepPluginKV(ctx context.Context, limit int) (int64, error) {
	if limit <= 0 {
		limit = 500
	}
	res, err := m.db.ExecContext(ctx,
		`DELETE FROM plugin_kv WHERE expires_at IS NOT NULL AND expires_at <= NOW(3) LIMIT ?`, limit)
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
	_, err := m.db.ExecContext(ctx,
		`INSERT INTO plugin_docs (plugin, coll, id, doc) VALUES (?,?,?,?)
		 ON DUPLICATE KEY UPDATE doc=VALUES(doc), updated_at=CURRENT_TIMESTAMP(3)`,
		plugin, coll, id, doc)
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
	rows, err := m.db.QueryContext(ctx,
		`SELECT coll, id, doc, created_at, updated_at FROM plugin_docs
		  WHERE plugin=? AND coll=? AND id LIKE CONCAT(?, '%')
		  ORDER BY id LIMIT ?`, plugin, coll, prefix, limit)
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
	_, err := m.db.ExecContext(ctx,
		`INSERT IGNORE INTO plugin_migrations (plugin, version) VALUES (?,?)`, plugin, version)
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
