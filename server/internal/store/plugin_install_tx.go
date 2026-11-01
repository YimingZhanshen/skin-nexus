package store

import "context"

type InstallTxRecord struct {
	ID          string
	Plugin      string
	Kind        string
	FromVersion string
	ToVersion   string
	Source      string
	State       string
	Error       string
	ActorUID    int64
}

func (m *Mysql) CreateInstallTx(ctx context.Context, tx InstallTxRecord) error {
	_, err := m.db.ExecContext(ctx,
		`INSERT INTO plugin_install_tx (id, plugin, kind, from_version, to_version, source, state, error, actor_uid)
		 VALUES (?,?,?,?,?,?,?,?,?)`,
		tx.ID, tx.Plugin, tx.Kind, nullableStr(tx.FromVersion), nullableStr(tx.ToVersion),
		tx.Source, tx.State, nullableStr(tx.Error), tx.ActorUID)
	return err
}

func (m *Mysql) UpdateInstallTx(ctx context.Context, id, state, errText string) error {
	_, err := m.db.ExecContext(ctx,
		`UPDATE plugin_install_tx SET state=?, error=?, updated_at=? WHERE id=?`,
		state, nullableStr(errText), m.now().UTC(), id)
	return err
}
