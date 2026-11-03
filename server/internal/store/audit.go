// audit.go: audit trail writes + shared lookups used by the phase-2
// integration surfaces (union events, OIDC admin events, queryemail).
package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
)

// InsertAudit appends one audit row. actorUID 0 = system/union event (the
// column stays NULL, matching AdminAudit's COALESCE(actor_uid,0)).
// detail is marshalled to JSON; marshal failures degrade to a plain string
// so an audit write never breaks the calling flow.
func (m *Mysql) InsertAudit(ctx context.Context, actorUID int64, action string, detail any) {
	if action == "" {
		return
	}
	var payload any
	switch d := detail.(type) {
	case nil:
		payload = nil
	case string:
		payload = d
	default:
		if b, err := json.Marshal(d); err == nil {
			payload = string(b)
		} else {
			payload = "unmarshalable detail"
		}
	}
	var actor sql.NullInt64
	if actorUID > 0 {
		actor = sql.NullInt64{Int64: actorUID, Valid: true}
	}
	_, _ = m.db.ExecContext(ctx,
		`INSERT INTO audit_log (actor_uid, action, detail) VALUES (?,?,?)`,
		actor, action, payload)
}

// EmailByPlayerName resolves a player name to its owning account email
// (players JOIN users). Backs union queryemail (Hooks.QueryEmailFunc).
func (m *Mysql) EmailByPlayerName(ctx context.Context, name string) (string, bool, error) {
	var email sql.NullString
	err := m.db.QueryRowContext(ctx,
		`SELECT u.email FROM players p JOIN users u ON u.id = p.uid
		 WHERE `+m.db.Dialect().CIEquals("p.name")+` LIMIT 1`, name).
		Scan(&email)
	if errors.Is(err, sql.ErrNoRows) {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	return email.String, email.Valid && email.String != "", nil
}

// GetOption / SetOption persist small runtime state in the options k/v table
// (union member key rotation, cached server list, etc.).
func (m *Mysql) GetOption(ctx context.Context, k string) (string, bool, error) {
	var v sql.NullString
	err := m.db.QueryRowContext(ctx, `SELECT v FROM options WHERE k = ?`, k).Scan(&v)
	if errors.Is(err, sql.ErrNoRows) {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	return v.String, true, nil
}

func (m *Mysql) SetOption(ctx context.Context, k, v string) error {
	q, err := m.db.Dialect().Upsert("options", []string{"k", "v"}, []string{"k"}, []string{"v"})
	if err != nil {
		return err
	}
	_, err = m.db.ExecContext(ctx, q, k, v)
	return err
}

// GetUnionKV / SetUnionKV are the union_kv escape-hatch accessors (0004).
// Kept separate from options so union state never collides with site options.
func (m *Mysql) GetUnionKV(ctx context.Context, k string) (string, bool, error) {
	var v sql.NullString
	err := m.db.QueryRowContext(ctx, `SELECT v FROM union_kv WHERE k = ?`, k).Scan(&v)
	if errors.Is(err, sql.ErrNoRows) {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	return v.String, true, nil
}

func (m *Mysql) SetUnionKV(ctx context.Context, k, v string) error {
	q, err := m.db.Dialect().Upsert("union_kv", []string{"k", "v"}, []string{"k"}, []string{"v"})
	if err != nil {
		return err
	}
	_, err = m.db.ExecContext(ctx, q, k, v)
	return err
}
