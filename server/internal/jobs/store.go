// Package jobs is the durable plugin job queue (host_jobs table). It owns
// only persistence and leasing; plugin execution lives in internal/plugin.
package jobs

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

var ErrNotFound = errors.New("jobs: not found")

type Job struct {
	ID          string
	Plugin      string
	Handler     string
	OwnerUID    int64
	State       string
	Payload     []byte
	Cursor      []byte
	Result      []byte
	Progress    int
	Message     string
	LastError   string
	Attempts    int
	MaxAttempts int
	Reclaims    int
	RunAfter    time.Time
	LeaseOwner  string
	LeaseUntil  time.Time
	CreatedAt   time.Time
	UpdatedAt   time.Time
	StartedAt   time.Time
	FinishedAt  time.Time
}

type Store struct{ db *sql.DB }

func New(db *sql.DB) *Store { return &Store{db: db} }

const cols = `id, plugin, handler, owner_uid, state, payload, job_cursor, result,
 progress, message, COALESCE(last_error,''), attempts, max_attempts, reclaims,
 run_after, lease_owner, lease_until, created_at, updated_at, started_at, finished_at`

func scan(s interface{ Scan(...any) error }) (Job, error) {
	var j Job
	var cursor, result []byte
	var leaseUntil, started, finished sql.NullTime
	err := s.Scan(&j.ID, &j.Plugin, &j.Handler, &j.OwnerUID, &j.State, &j.Payload, &cursor, &result,
		&j.Progress, &j.Message, &j.LastError, &j.Attempts, &j.MaxAttempts, &j.Reclaims,
		&j.RunAfter, &j.LeaseOwner, &leaseUntil, &j.CreatedAt, &j.UpdatedAt, &started, &finished)
	if cursor != nil {
		j.Cursor = cursor
	}
	if result != nil {
		j.Result = result
	}
	if leaseUntil.Valid {
		j.LeaseUntil = leaseUntil.Time
	}
	if started.Valid {
		j.StartedAt = started.Time
	}
	if finished.Valid {
		j.FinishedAt = finished.Time
	}
	return j, err
}

func jsonOrNil(b []byte) any {
	if len(b) == 0 {
		return nil
	}
	return b
}

func (s *Store) Create(ctx context.Context, j Job) error {
	now := time.Now()
	if j.RunAfter.IsZero() {
		j.RunAfter = now
	}
	// MySQL 5.7 DATETIME(3) rounds sub-millisecond values; truncate so a
	// job created with the same instant a worker polls is immediately due.
	j.RunAfter = j.RunAfter.Truncate(time.Millisecond)
	if j.CreatedAt.IsZero() {
		j.CreatedAt = now
	}
	if j.UpdatedAt.IsZero() {
		j.UpdatedAt = now
	}
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO host_jobs (id, plugin, handler, owner_uid, state, payload, job_cursor, progress,
		 message, attempts, max_attempts, run_after, created_at, updated_at)
		 VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		j.ID, j.Plugin, j.Handler, j.OwnerUID, j.State, j.Payload, jsonOrNil(j.Cursor),
		j.Progress, j.Message, j.Attempts, j.MaxAttempts, j.RunAfter, j.CreatedAt, j.UpdatedAt)
	return err
}

func (s *Store) Get(ctx context.Context, id string) (Job, error) {
	j, err := scan(s.db.QueryRowContext(ctx, `SELECT `+cols+` FROM host_jobs WHERE id=?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return Job{}, ErrNotFound
	}
	return j, err
}

func (s *Store) List(ctx context.Context, plugin, state string, page, perPage int) ([]Job, int, error) {
	if page < 1 {
		page = 1
	}
	if perPage < 1 || perPage > 100 {
		perPage = 20
	}
	where, args := "1=1", []any{}
	if plugin != "" {
		where += " AND plugin=?"
		args = append(args, plugin)
	}
	if state != "" {
		where += " AND state=?"
		args = append(args, state)
	}
	var total int
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM host_jobs WHERE `+where, args...).Scan(&total); err != nil {
		return nil, 0, err
	}
	args = append(args, perPage, (page-1)*perPage)
	rows, err := s.db.QueryContext(ctx,
		`SELECT `+cols+` FROM host_jobs WHERE `+where+` ORDER BY created_at DESC LIMIT ? OFFSET ?`, args...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	out := make([]Job, 0, perPage)
	for rows.Next() {
		j, err := scan(rows)
		if err != nil {
			return nil, 0, err
		}
		out = append(out, j)
	}
	return out, total, rows.Err()
}

// Claim leases one queued job, or reclaims a running job whose lease expired.
func (s *Store) Claim(ctx context.Context, worker string, now time.Time, lease time.Duration) (*Job, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	j, err := scan(tx.QueryRowContext(ctx,
		`SELECT `+cols+` FROM host_jobs
		  WHERE (state='queued' AND run_after<=?)
		     OR (state='running' AND (lease_until IS NULL OR lease_until<?))
		  ORDER BY run_after LIMIT 1 FOR UPDATE`, now, now))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	reclaims := j.Reclaims
	if j.State == "running" {
		reclaims++
	}
	until := now.Add(lease)
	if _, err := tx.ExecContext(ctx,
		`UPDATE host_jobs SET state='running', lease_owner=?, lease_until=?,
		 reclaims=?, started_at=COALESCE(started_at,?), updated_at=? WHERE id=?`,
		worker, until, reclaims, now, now, j.ID); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	j.State, j.LeaseOwner, j.LeaseUntil, j.Reclaims = "running", worker, until, reclaims
	return &j, nil
}

// SaveStep persists the cursor and releases the lease so the next tick can
// claim the job again. Keeping the lease would stall every step until it
// expires (30s), which also delays cancel/resume past the one-step budget.
// delay lets a plugin space upstream calls without blocking its VM.
func (s *Store) SaveStep(ctx context.Context, id, worker string, cursor []byte, progress int, message string, delay time.Duration) (bool, error) {
	if delay < 0 {
		delay = 0
	}
	if delay > time.Minute {
		delay = time.Minute
	}
	runAfter := time.Now().Add(delay).Truncate(time.Millisecond)
	res, err := s.db.ExecContext(ctx,
		`UPDATE host_jobs SET job_cursor=?, progress=?, message=?, state='queued',
		 run_after=?, lease_owner='', lease_until=NULL, updated_at=NOW(3)
		 WHERE id=? AND state='running' AND lease_owner=?`,
		jsonOrNil(cursor), progress, message, runAfter, id, worker)
	if err != nil {
		return false, err
	}
	n, _ := res.RowsAffected()
	return n > 0, nil
}

func (s *Store) Finish(ctx context.Context, id, worker string, result []byte) error {
	_, err := s.db.ExecContext(ctx,
		`UPDATE host_jobs SET state='done', result=?, progress=100, lease_owner='', lease_until=NULL,
		 finished_at=NOW(3), updated_at=NOW(3) WHERE id=? AND state='running' AND lease_owner=?`,
		jsonOrNil(result), id, worker)
	return err
}

func (s *Store) MarkCancelled(ctx context.Context, id, worker string) error {
	_, err := s.db.ExecContext(ctx,
		`UPDATE host_jobs SET state='cancelled', lease_owner='', lease_until=NULL,
		 finished_at=NOW(3), updated_at=NOW(3) WHERE id=? AND state IN ('running','cancelling') AND lease_owner=?`,
		id, worker)
	return err
}

// Fail retries with exponential backoff, then marks failed.
func (s *Store) Fail(ctx context.Context, id, worker string, cause error, attempts, maxAttempts int) error {
	msg := ""
	if cause != nil {
		msg = cause.Error()
	}
	if attempts < maxAttempts {
		backoff := time.Duration(1<<uint(attempts)) * 2 * time.Second
		_, err := s.db.ExecContext(ctx,
			`UPDATE host_jobs SET state='queued', attempts=?, last_error=?, run_after=?,
			 lease_owner='', lease_until=NULL, updated_at=NOW(3) WHERE id=? AND lease_owner=?`,
			attempts, msg, time.Now().Add(backoff), id, worker)
		return err
	}
	_, err := s.db.ExecContext(ctx,
		`UPDATE host_jobs SET state='failed', attempts=?, last_error=?, lease_owner='', lease_until=NULL,
		 finished_at=NOW(3), updated_at=NOW(3) WHERE id=? AND lease_owner=?`,
		attempts, msg, id, worker)
	return err
}

func (s *Store) RequestCancel(ctx context.Context, id string) error {
	_, err := s.db.ExecContext(ctx,
		`UPDATE host_jobs SET state=CASE WHEN state='queued' THEN 'cancelled' ELSE 'cancelling' END,
		 updated_at=NOW(3) WHERE id=? AND state IN ('queued','running','paused')`, id)
	return err
}

func (s *Store) Resume(ctx context.Context, id string) error {
	res, err := s.db.ExecContext(ctx,
		`UPDATE host_jobs SET state='queued', run_after=NOW(3), attempts=0, lease_owner='', lease_until=NULL,
		 updated_at=NOW(3) WHERE id=? AND state IN ('cancelled','failed','paused')`, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *Store) PausePlugin(ctx context.Context, plugin string) error {
	_, err := s.db.ExecContext(ctx,
		`UPDATE host_jobs SET state='paused', updated_at=NOW(3) WHERE plugin=? AND state='queued'`, plugin)
	return err
}

// Requeue releases a lease without consuming an attempt (plugin not loaded
// yet, host shutting down, etc.).
func (s *Store) Requeue(ctx context.Context, id, worker string) error {
	_, err := s.db.ExecContext(ctx,
		`UPDATE host_jobs SET state='queued', run_after=NOW(3), lease_owner='', lease_until=NULL,
		 updated_at=NOW(3) WHERE id=? AND state='running' AND lease_owner=?`, id, worker)
	return err
}
