package store

// ---- OIDC provider persistence (oidc.Backend) ----
//
// Contract: internal/oidc/backend.go (frozen). String-slice record fields
// (redirect_uris / grant_types / scopes) live in the schema as JSON TEXT
// arrays; zero times are stored as NULL so strict sql_mode never rejects a
// zero date. Getters report a miss as oidc.ErrNotFound.

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"skin-nexus/server/internal/oidc"
)

// ---- JSON list columns ----

// encodeStringList marshals a string slice to the JSON array column value
// (nil and empty both become "[]").
func encodeStringList(v []string) (string, error) {
	if v == nil {
		v = []string{}
	}
	b, err := json.Marshal(v)
	if err != nil {
		return "", fmt.Errorf("store: encode string list: %w", err)
	}
	return string(b), nil
}

// decodeStringList unmarshals a JSON array column back into a slice; a NULL
// or blank column reads as nil.
func decodeStringList(raw string) ([]string, error) {
	if strings.TrimSpace(raw) == "" {
		return nil, nil
	}
	var out []string
	if err := json.Unmarshal([]byte(raw), &out); err != nil {
		return nil, fmt.Errorf("store: decode string list: %w", err)
	}
	return out, nil
}

// nullableTime maps the zero time to NULL (NULL round-trips to the zero time).
func nullableTime(t time.Time) interface{} {
	if t.IsZero() {
		return nil
	}
	return t
}

// ---- clients ----

// GetClient returns a registered OAuth client or oidc.ErrNotFound. The
// schema-only is_public column is not part of the frozen record.
func (m *Mysql) GetClient(ctx context.Context, id string) (oidc.ClientRecord, error) {
	var rec oidc.ClientRecord
	var redirectURIs, grantTypes, scopes string
	err := m.db.QueryRowContext(ctx,
		`SELECT client_id, secret_hash, name, redirect_uris, grant_types, scopes
		 FROM oidc_clients WHERE client_id = ?`, id).
		Scan(&rec.ID, &rec.SecretHash, &rec.Name, &redirectURIs, &grantTypes, &scopes)
	if errors.Is(err, sql.ErrNoRows) {
		return oidc.ClientRecord{}, oidc.ErrNotFound
	}
	if err != nil {
		return oidc.ClientRecord{}, err
	}
	return decodeClientRecord(rec, redirectURIs, grantTypes, scopes)
}

// ListClients returns every registered client (admin surface), ordered by id.
func (m *Mysql) ListClients(ctx context.Context) ([]oidc.ClientRecord, error) {
	rows, err := m.db.QueryContext(ctx,
		`SELECT client_id, secret_hash, name, redirect_uris, grant_types, scopes
		 FROM oidc_clients ORDER BY client_id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]oidc.ClientRecord, 0)
	for rows.Next() {
		var rec oidc.ClientRecord
		var redirectURIs, grantTypes, scopes string
		if err := rows.Scan(&rec.ID, &rec.SecretHash, &rec.Name, &redirectURIs, &grantTypes, &scopes); err != nil {
			return nil, err
		}
		dec, err := decodeClientRecord(rec, redirectURIs, grantTypes, scopes)
		if err != nil {
			return nil, err
		}
		out = append(out, dec)
	}
	return out, rows.Err()
}

// DeleteClient removes a client row (idempotent: missing rows are a no-op).
func (m *Mysql) DeleteClient(ctx context.Context, id string) error {
	_, err := m.db.ExecContext(ctx, `DELETE FROM oidc_clients WHERE client_id = ?`, id)
	return err
}

func decodeClientRecord(rec oidc.ClientRecord, redirectURIs, grantTypes, scopes string) (oidc.ClientRecord, error) {
	var err error
	if rec.RedirectURIs, err = decodeStringList(redirectURIs); err != nil {
		return oidc.ClientRecord{}, err
	}
	if rec.GrantTypes, err = decodeStringList(grantTypes); err != nil {
		return oidc.ClientRecord{}, err
	}
	if rec.Scopes, err = decodeStringList(scopes); err != nil {
		return oidc.ClientRecord{}, err
	}
	return rec, nil
}

// EnsureOidcClient upserts a client row by client_id (startup hook for
// seeding the shared public client of discovery `shared_client_id`).
// ClientRecord carries no IsPublic flag, so the schema column is derived:
// a client with an empty SecretHash is public.
func (m *Mysql) EnsureOidcClient(ctx context.Context, rec oidc.ClientRecord) error {
	redirectURIs, err := encodeStringList(rec.RedirectURIs)
	if err != nil {
		return err
	}
	grantTypes, err := encodeStringList(rec.GrantTypes)
	if err != nil {
		return err
	}
	scopes, err := encodeStringList(rec.Scopes)
	if err != nil {
		return err
	}
	isPublic := 0
	if rec.SecretHash == "" {
		isPublic = 1
	}
	// The ON DUPLICATE update assignments all reuse the inserted values, so
	// dialect's VALUES()/excluded form covers it; client_id is the primary key.
	q, err := m.db.Dialect().Upsert("oidc_clients",
		[]string{"client_id", "secret_hash", "name", "redirect_uris", "grant_types", "scopes", "is_public"},
		[]string{"client_id"},
		[]string{"secret_hash", "name", "redirect_uris", "grant_types", "scopes", "is_public"})
	if err != nil {
		return err
	}
	_, err = m.db.ExecContext(ctx, q,
		rec.ID, rec.SecretHash, rec.Name, redirectURIs, grantTypes, scopes, isPublic)
	return err
}

// ---- authorization-code flow ----

const authRequestCols = `id, client_id, subject, scopes, redirect_uri, state, nonce,
  code_challenge, code_challenge_method, selected_pid, auth_time, done, code,
  code_expires, created_at`

func scanAuthRequest(s rowScanner) (oidc.AuthRequestRecord, error) {
	var rec oidc.AuthRequestRecord
	var scopes string
	var sel sql.NullInt64
	var authTime, codeExpires sql.NullTime
	var code sql.NullString
	err := s.Scan(&rec.ID, &rec.ClientID, &rec.Subject, &scopes, &rec.RedirectURI,
		&rec.State, &rec.Nonce, &rec.CodeChallenge, &rec.CodeChallengeMethod,
		&sel, &authTime, &rec.Done, &code, &codeExpires, &rec.CreatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return oidc.AuthRequestRecord{}, oidc.ErrNotFound
	}
	if err != nil {
		return oidc.AuthRequestRecord{}, err
	}
	if rec.Scopes, err = decodeStringList(scopes); err != nil {
		return oidc.AuthRequestRecord{}, err
	}
	rec.SelectedPID = sel.Int64
	rec.AuthTime = authTime.Time
	rec.Code = code.String
	rec.CodeExpires = codeExpires.Time
	return rec, nil
}

// GetAuthRequest returns an authorization request by id or oidc.ErrNotFound.
func (m *Mysql) GetAuthRequest(ctx context.Context, id string) (oidc.AuthRequestRecord, error) {
	return scanAuthRequest(m.db.QueryRowContext(ctx,
		"SELECT "+authRequestCols+" FROM oidc_auth_requests WHERE id = ?", id))
}

// GetAuthRequestByCode returns the request owning a minted code.
func (m *Mysql) GetAuthRequestByCode(ctx context.Context, code string) (oidc.AuthRequestRecord, error) {
	return scanAuthRequest(m.db.QueryRowContext(ctx,
		"SELECT "+authRequestCols+" FROM oidc_auth_requests WHERE code = ?", code))
}

// SaveAuthRequest persists an authorization request. The frozen contract has
// no UpdateAuthRequest, so this doubles as the update path of later flow steps
// (subject / auth_time / code / done) keyed by id: insert first, fall back to
// an in-place update. created_at keeps its first stored value on update.
func (m *Mysql) SaveAuthRequest(ctx context.Context, rec oidc.AuthRequestRecord) error {
	scopes, err := encodeStringList(rec.Scopes)
	if err != nil {
		return err
	}
	createdAt := rec.CreatedAt
	if createdAt.IsZero() {
		createdAt = m.now()
	}
	_, err = m.db.ExecContext(ctx,
		`INSERT INTO oidc_auth_requests (`+authRequestCols+`)
		 VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		rec.ID, rec.ClientID, rec.Subject, scopes, rec.RedirectURI, rec.State, rec.Nonce,
		rec.CodeChallenge, rec.CodeChallengeMethod, nullableInt(rec.SelectedPID),
		nullableTime(rec.AuthTime), rec.Done, nullableStr(rec.Code),
		nullableTime(rec.CodeExpires), createdAt)
	if err == nil {
		return nil
	}
	if !isDupKey(err) {
		return err
	}
	// id (or code) already stored: apply as an in-place update on this id.
	res, uerr := m.db.ExecContext(ctx,
		`UPDATE oidc_auth_requests SET client_id = ?, subject = ?, scopes = ?,
		   redirect_uri = ?, state = ?, nonce = ?, code_challenge = ?,
		   code_challenge_method = ?, selected_pid = ?, auth_time = ?, done = ?,
		   code = ?, code_expires = ?
		 WHERE id = ?`,
		rec.ClientID, rec.Subject, scopes, rec.RedirectURI, rec.State, rec.Nonce,
		rec.CodeChallenge, rec.CodeChallengeMethod, nullableInt(rec.SelectedPID),
		nullableTime(rec.AuthTime), rec.Done, nullableStr(rec.Code),
		nullableTime(rec.CodeExpires), rec.ID)
	if uerr != nil {
		return uerr
	}
	if n, nerr := res.RowsAffected(); nerr == nil && n > 0 {
		return nil
	}
	// MySQL reports changed rows, not matched rows: 0 rows updated is either
	// an identical re-save or a code collision with another request. Probe by
	// id to tell them apart.
	var one int
	switch perr := m.db.QueryRowContext(ctx,
		`SELECT 1 FROM oidc_auth_requests WHERE id = ?`, rec.ID).Scan(&one); {
	case perr == nil:
		return nil
	case errors.Is(perr, sql.ErrNoRows):
		return fmt.Errorf("store: save auth request: %w", err)
	default:
		return perr
	}
}

// DeleteAuthRequest drops a request once its code was exchanged (idempotent).
func (m *Mysql) DeleteAuthRequest(ctx context.Context, id string) error {
	_, err := m.db.ExecContext(ctx, `DELETE FROM oidc_auth_requests WHERE id = ?`, id)
	return err
}

// ---- access tokens (jti ledger) ----

// SaveAccessToken records a JWT access-token ledger entry.
func (m *Mysql) SaveAccessToken(ctx context.Context, rec oidc.AccessTokenRecord) error {
	scopes, err := encodeStringList(rec.Scopes)
	if err != nil {
		return err
	}
	createdAt := rec.CreatedAt
	if createdAt.IsZero() {
		createdAt = m.now()
	}
	_, err = m.db.ExecContext(ctx,
		`INSERT INTO oidc_access_tokens (jti, uid, client_id, scopes, selected_pid, expires_at, created_at, revoked_at)
		 VALUES (?,?,?,?,?,?,?,?)`,
		rec.JTI, rec.UserID, rec.ClientID, scopes, nullableInt(rec.SelectedPID),
		rec.ExpiresAt, createdAt, nullableTime(rec.RevokedAt))
	return err
}

// GetAccessToken returns a ledger entry by jti. Revoked entries are returned
// as well (RevokedAt non-zero); liveness is the caller's call.
func (m *Mysql) GetAccessToken(ctx context.Context, jti string) (oidc.AccessTokenRecord, error) {
	var rec oidc.AccessTokenRecord
	var scopes string
	var sel sql.NullInt64
	var revokedAt sql.NullTime
	err := m.db.QueryRowContext(ctx,
		`SELECT jti, uid, client_id, scopes, selected_pid, expires_at, created_at, revoked_at
		 FROM oidc_access_tokens WHERE jti = ?`, jti).
		Scan(&rec.JTI, &rec.UserID, &rec.ClientID, &scopes, &sel,
			&rec.ExpiresAt, &rec.CreatedAt, &revokedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return oidc.AccessTokenRecord{}, oidc.ErrNotFound
	}
	if err != nil {
		return oidc.AccessTokenRecord{}, err
	}
	if rec.Scopes, err = decodeStringList(scopes); err != nil {
		return oidc.AccessTokenRecord{}, err
	}
	rec.SelectedPID = sel.Int64
	rec.RevokedAt = revokedAt.Time
	return rec, nil
}

// RevokeAccessToken marks one jti revoked (idempotent; live rows only).
func (m *Mysql) RevokeAccessToken(ctx context.Context, jti string) error {
	_, err := m.db.ExecContext(ctx,
		`UPDATE oidc_access_tokens SET revoked_at = ? WHERE jti = ? AND revoked_at IS NULL`, m.now().UTC(), jti)
	return err
}

// RevokeUserAccessTokens revokes every live token of one user (password
// change / signout semantics). Refresh tokens are deliberately out of scope.
func (m *Mysql) RevokeUserAccessTokens(ctx context.Context, uid int64) error {
	_, err := m.db.ExecContext(ctx,
		`UPDATE oidc_access_tokens SET revoked_at = ? WHERE uid = ? AND revoked_at IS NULL`, m.now().UTC(), uid)
	return err
}

// ListUserAccessTokens returns the user's active GRANTS, newest first: one
// row per client (its latest non-revoked access entry), shown when the access
// token is still live OR the same client still holds a live refresh token.
// The authorized-apps UI lists authorizations, and refresh grants outlive the
// 1-hour access tokens — live-only filtering showed 0 while two launchers
// still refreshed fine (F12 report, 2026-10-23).
func (m *Mysql) ListUserAccessTokens(ctx context.Context, uid int64) ([]oidc.AccessTokenRecord, error) {
	now := m.now().UTC()
	rows, err := m.db.QueryContext(ctx,
		`SELECT a.jti, a.uid, a.client_id, a.scopes, a.selected_pid, a.expires_at, a.created_at
		 FROM oidc_access_tokens a
		 JOIN (SELECT client_id, MAX(created_at) AS mc
		       FROM oidc_access_tokens
		       WHERE uid = ? AND revoked_at IS NULL
		       GROUP BY client_id) latest
		   ON latest.client_id = a.client_id AND latest.mc = a.created_at
		 WHERE a.uid = ? AND a.revoked_at IS NULL
		   AND (a.expires_at > ?
		        OR EXISTS (SELECT 1 FROM oidc_refresh_tokens r
		                   WHERE r.uid = a.uid AND r.client_id = a.client_id
		                     AND r.revoked = 0 AND r.expires_at > ?))
		 ORDER BY a.created_at DESC, a.jti DESC LIMIT 100`, uid, uid, now, now)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]oidc.AccessTokenRecord, 0)
	seen := map[string]bool{} // one row per client: same-millisecond ties from
	for rows.Next() {         // the MAX(created_at) join collapse here (review B P2)
		var rec oidc.AccessTokenRecord
		var scopes string
		var sel sql.NullInt64
		if err := rows.Scan(&rec.JTI, &rec.UserID, &rec.ClientID, &scopes, &sel,
			&rec.ExpiresAt, &rec.CreatedAt); err != nil {
			return nil, err
		}
		if seen[rec.ClientID] {
			continue
		}
		seen[rec.ClientID] = true
		if rec.Scopes, err = decodeStringList(scopes); err != nil {
			return nil, err
		}
		rec.SelectedPID = sel.Int64
		out = append(out, rec)
	}
	return out, rows.Err()
}

// ---- refresh tokens ----

// SaveRefreshToken stores an opaque refresh token.
func (m *Mysql) SaveRefreshToken(ctx context.Context, rec oidc.RefreshTokenRecord) error {
	scopes, err := encodeStringList(rec.Scopes)
	if err != nil {
		return err
	}
	createdAt := rec.CreatedAt
	if createdAt.IsZero() {
		createdAt = m.now()
	}
	_, err = m.db.ExecContext(ctx,
		`INSERT INTO oidc_refresh_tokens (token, uid, client_id, scopes, selected_pid, auth_time, expires_at, created_at, revoked)
		 VALUES (?,?,?,?,?,?,?,?,?)`,
		rec.Token, rec.UserID, rec.ClientID, scopes, nullableInt(rec.SelectedPID),
		nullableTime(rec.AuthTime), rec.ExpiresAt, createdAt, rec.Revoked)
	return err
}

// GetRefreshToken returns a refresh token by value. Revoked rows are returned
// as well (Revoked true); liveness is the caller's call.
func (m *Mysql) GetRefreshToken(ctx context.Context, token string) (oidc.RefreshTokenRecord, error) {
	var rec oidc.RefreshTokenRecord
	var scopes string
	var sel sql.NullInt64
	var authTime sql.NullTime
	err := m.db.QueryRowContext(ctx,
		`SELECT token, uid, client_id, scopes, selected_pid, auth_time, expires_at, created_at, revoked
		 FROM oidc_refresh_tokens WHERE token = ?`, token).
		Scan(&rec.Token, &rec.UserID, &rec.ClientID, &scopes, &sel,
			&authTime, &rec.ExpiresAt, &rec.CreatedAt, &rec.Revoked)
	if errors.Is(err, sql.ErrNoRows) {
		return oidc.RefreshTokenRecord{}, oidc.ErrNotFound
	}
	if err != nil {
		return oidc.RefreshTokenRecord{}, err
	}
	if rec.Scopes, err = decodeStringList(scopes); err != nil {
		return oidc.RefreshTokenRecord{}, err
	}
	rec.SelectedPID = sel.Int64
	rec.AuthTime = authTime.Time
	return rec, nil
}

// RotateRefreshToken revokes old and stores new atomically (single tx).
// The revoke is guarded (`AND revoked = 0`): two concurrent refreshes of the
// same token race the guard and exactly one wins — RFC 6749 single-use
// (review P2: the unguarded UPDATE let both reuse attempts rotate).
func (m *Mysql) RotateRefreshToken(ctx context.Context, old, new oidc.RefreshTokenRecord) error {
	tx, err := m.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	res, err := tx.ExecContext(ctx,
		`UPDATE oidc_refresh_tokens SET revoked = 1 WHERE token = ? AND revoked = 0`, old.Token)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return oidc.ErrNotFound // already consumed or revoked
	}
	scopes, err := encodeStringList(new.Scopes)
	if err != nil {
		return err
	}
	createdAt := new.CreatedAt
	if createdAt.IsZero() {
		createdAt = m.now()
	}
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO oidc_refresh_tokens (token, uid, client_id, scopes, selected_pid, auth_time, expires_at, created_at, revoked)
		 VALUES (?,?,?,?,?,?,?,?,?)`,
		new.Token, new.UserID, new.ClientID, scopes, nullableInt(new.SelectedPID),
		nullableTime(new.AuthTime), new.ExpiresAt, createdAt, new.Revoked); err != nil {
		return err
	}
	return tx.Commit()
}

// RevokeRefreshToken marks one refresh token revoked (idempotent).
func (m *Mysql) RevokeRefreshToken(ctx context.Context, token string) error {
	_, err := m.db.ExecContext(ctx,
		`UPDATE oidc_refresh_tokens SET revoked = 1 WHERE token = ?`, token)
	return err
}

// RevokeUserRefreshTokens revokes every live refresh token of one user.
func (m *Mysql) RevokeUserRefreshTokens(ctx context.Context, uid int64) error {
	_, err := m.db.ExecContext(ctx,
		`UPDATE oidc_refresh_tokens SET revoked = 1 WHERE uid = ? AND revoked = 0`, uid)
	return err
}

// RevokeClientRefreshTokens revokes live refresh tokens of one user for one
// client. The refresh table carries no jti column, so a single access-token
// revocation extends to the uid+client refresh scope (documented in the API
// response); this is the narrowest defensible granularity.
func (m *Mysql) RevokeClientRefreshTokens(ctx context.Context, uid int64, clientID string) error {
	_, err := m.db.ExecContext(ctx,
		`UPDATE oidc_refresh_tokens SET revoked = 1 WHERE uid = ? AND client_id = ? AND revoked = 0`,
		uid, clientID)
	return err
}

// ---- device flow (RFC 8628) ----

const deviceAuthCols = `device_code, user_code, client_id, scopes, expires_at,
  done, denied, uid, selected_pid, auth_time, created_at`

func scanDeviceAuth(s rowScanner) (oidc.DeviceAuthRecord, error) {
	var rec oidc.DeviceAuthRecord
	var scopes string
	var sel sql.NullInt64
	var authTime sql.NullTime
	err := s.Scan(&rec.DeviceCode, &rec.UserCode, &rec.ClientID, &scopes,
		&rec.ExpiresAt, &rec.Done, &rec.Denied, &rec.UserID, &sel, &authTime, &rec.CreatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return oidc.DeviceAuthRecord{}, oidc.ErrNotFound
	}
	if err != nil {
		return oidc.DeviceAuthRecord{}, err
	}
	if rec.Scopes, err = decodeStringList(scopes); err != nil {
		return oidc.DeviceAuthRecord{}, err
	}
	rec.SelectedPID = sel.Int64
	rec.AuthTime = authTime.Time
	return rec, nil
}

// SaveDeviceAuth stores a new device authorization. A colliding user_code
// must surface as oidc.ErrDuplicateUserCode so callers regenerate the code;
// the UNIQUE violation is mapped even when two inserts race (device_code is
// caller-minted and effectively collision-free, so any ER_DUP_ENTRY folds
// into that signal).
func (m *Mysql) SaveDeviceAuth(ctx context.Context, rec oidc.DeviceAuthRecord) error {
	scopes, err := encodeStringList(rec.Scopes)
	if err != nil {
		return err
	}
	createdAt := rec.CreatedAt
	if createdAt.IsZero() {
		createdAt = m.now()
	}
	_, err = m.db.ExecContext(ctx,
		`INSERT INTO oidc_device_auth (`+deviceAuthCols+`) VALUES (?,?,?,?,?,?,?,?,?,?,?)`,
		rec.DeviceCode, rec.UserCode, rec.ClientID, scopes, rec.ExpiresAt,
		rec.Done, rec.Denied, rec.UserID, nullableInt(rec.SelectedPID),
		nullableTime(rec.AuthTime), createdAt)
	if isDupKey(err) {
		return oidc.ErrDuplicateUserCode
	}
	return err
}

// GetDeviceAuthByDeviceCode returns a device authorization by device_code or
// oidc.ErrNotFound.
func (m *Mysql) GetDeviceAuthByDeviceCode(ctx context.Context, code string) (oidc.DeviceAuthRecord, error) {
	return scanDeviceAuth(m.db.QueryRowContext(ctx,
		"SELECT "+deviceAuthCols+" FROM oidc_device_auth WHERE device_code = ?", code))
}

// GetDeviceAuthByUserCode returns a device authorization by user_code or
// oidc.ErrNotFound.
func (m *Mysql) GetDeviceAuthByUserCode(ctx context.Context, code string) (oidc.DeviceAuthRecord, error) {
	return scanDeviceAuth(m.db.QueryRowContext(ctx,
		"SELECT "+deviceAuthCols+" FROM oidc_device_auth WHERE user_code = ?", code))
}

// UpdateDeviceAuth applies the verification-page outcome (done / denied / uid
// / selected_pid / auth_time) to the row named by device_code, as a guarded
// pending->decided transition: a second decision or a stale code reports
// oidc.ErrNotFound (review P2: the unguarded UPDATE let concurrent approve/
// deny writes overwrite each other, last writer silently winning).
func (m *Mysql) UpdateDeviceAuth(ctx context.Context, rec oidc.DeviceAuthRecord) error {
	res, err := m.db.ExecContext(ctx,
		`UPDATE oidc_device_auth SET done = ?, denied = ?, uid = ?, selected_pid = ?, auth_time = ?
		 WHERE device_code = ? AND done = 0 AND denied = 0`,
		rec.Done, rec.Denied, rec.UserID, nullableInt(rec.SelectedPID),
		nullableTime(rec.AuthTime), rec.DeviceCode)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return oidc.ErrNotFound // already decided, consumed, or unknown
	}
	return nil
}

// ConsumeDeviceAuth burns an approved device authorization on first token
// redemption (single-use device codes — review P2: replays within the code
// lifetime minted a fresh pair per poll). Rows not in the approved state
// report oidc.ErrNotFound.
func (m *Mysql) ConsumeDeviceAuth(ctx context.Context, deviceCode string) error {
	res, err := m.db.ExecContext(ctx,
		`DELETE FROM oidc_device_auth WHERE device_code = ? AND done = 1 AND denied = 0`, deviceCode)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return oidc.ErrNotFound
	}
	return nil
}

// ---- health probe ----

// Health pings the pool.
func (m *Mysql) Health(ctx context.Context) error {
	return m.db.PingContext(ctx)
}

// Compile-time proof that *Mysql satisfies the frozen OIDC contract.
var _ oidc.Backend = (*Mysql)(nil)
