package store

// Credentials at rest.
//
// Passwords, e-mail verification tokens and password-reset tokens have always
// been stored as digests. The session and launcher tokens were the exception:
// they sat in `web_sessions`, `ygg_tokens` and `ygg_join_sessions` as the exact
// strings a client presents, so one read of those tables (a backup, an SQL
// error echoing a row, a compromised dump) handed over live logins and live
// launcher sessions. This file closes that gap: the tables hold
// hex(sha256(value)) and are keyed by it, which is all any lookup ever needed.
//
// What stays verbatim, and why: `ygg_tokens.client_token`. It is the launcher's
// per-install pairing id — every endpoint that consumes it also requires a valid
// access token, so it cannot authenticate by itself — and Yggdrasil expects the
// server to hand the same value back, which a one-way form cannot do. It is also
// CHAR(32), where a SHA-256 hex digest needs 64: storing its digest would cost
// a table rebuild on SQLite for a value that is not a usable credential.

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
)

// tokenDigestMarker records that the existing rows have been rewritten. It is
// committed in the same transaction as the rewrite, so the state is binary:
// either the marker exists and every credential column holds a digest, or
// nothing was rewritten. There is no in-between to read, so no dual-format
// lookup — and no extra discriminator column, which the three-tree schema gate
// would have had to carry.
const tokenDigestMarker = "token_digest_v1"

// credentialDigest is the at-rest form of an opaque token. An empty value
// digests to empty, preserving the "no client token recorded" state that the
// token checks deliberately skip.
func credentialDigest(s string) string {
	if s == "" {
		return ""
	}
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}

// digestRewrite is one credential column and the key its rows are updated by.
// The list is a package-level constant and the only source of the identifiers
// interpolated below — nothing here reaches a query from a request.
type digestRewrite struct {
	table    string
	column   string
	key      string
	byColumn bool // web_sessions is keyed by the token itself (it is the PK)
}

var digestRewrites = []digestRewrite{
	{table: "ygg_tokens", column: "access_token", key: "id"},
	{table: "ygg_join_sessions", column: "access_token", key: "id"},
	{table: "web_sessions", column: "token", key: "token", byColumn: true},
}

// EnsureTokenDigests rewrites pre-digest rows once, at boot, in a single
// transaction. It runs next to EnsureNoRowIDReuse, which fixes another
// historical row shape the same way.
//
// The work is irreversible — a digest cannot be turned back into the token — so
// it is guarded twice, deliberately: the marker read above answers every ordinary
// boot without opening a transaction, and the do-nothing claim inside the
// transaction is what makes a *concurrent* boot walk away instead of digesting
// values that are already digests. The pair is the safety net, not either half:
// one test asserts the behaviour they both produce ("a second pass rewrites
// nothing"), and deleting one still leaves that green. That redundancy is the
// point, because re-running the rewrite would silently log out every account on
// the site and only the race can produce it.
//
// It returns the number of rows rewritten (0 when the work was already done).
func (m *Mysql) EnsureTokenDigests(ctx context.Context) (int, error) {
	var already int
	if err := m.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM options WHERE k = ?`, tokenDigestMarker).Scan(&already); err != nil {
		return 0, fmt.Errorf("digest marker read: %w", err)
	}
	if already > 0 {
		return 0, nil
	}

	tx, err := m.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()

	claim, err := m.db.Dialect().InsertIgnore("options", []string{"k", "v"})
	if err != nil {
		return 0, fmt.Errorf("digest claim sql: %w", err)
	}
	res, err := tx.ExecContext(ctx, claim, tokenDigestMarker, "1")
	if err != nil {
		return 0, fmt.Errorf("digest marker claim: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		// Another boot claimed it first and is (or already was) the one rewriting.
		return 0, nil
	}

	done := 0
	for _, rw := range digestRewrites {
		rows, err := tx.QueryContext(ctx,
			fmt.Sprintf(`SELECT %s, %s FROM %s WHERE %s <> ''`, rw.key, rw.column, rw.table, rw.column))
		if err != nil {
			return done, fmt.Errorf("digest scan %s: %w", rw.table, err)
		}
		type pair struct{ key, value string }
		var batch []pair
		for rows.Next() {
			var k, v string
			if err := rows.Scan(&k, &v); err != nil {
				rows.Close()
				return done, fmt.Errorf("digest scan %s row: %w", rw.table, err)
			}
			batch = append(batch, pair{k, credentialDigest(v)})
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			return done, fmt.Errorf("digest scan %s: %w", rw.table, err)
		}
		rows.Close()

		update := fmt.Sprintf(`UPDATE %s SET %s = ? WHERE %s = ?`, rw.table, rw.column, rw.key)
		for _, p := range batch {
			if _, err := tx.ExecContext(ctx, update, p.value, p.key); err != nil {
				return done, fmt.Errorf("digest rewrite %s: %w", rw.table, err)
			}
		}
		done += len(batch)
	}
	if err := tx.Commit(); err != nil {
		return 0, fmt.Errorf("digest commit: %w", err)
	}
	return done, nil
}
