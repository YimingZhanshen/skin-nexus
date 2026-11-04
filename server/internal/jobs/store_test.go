package jobs

// The queue's contract lives in SQL predicates, so it is tested against a real
// engine rather than a mock: which row Claim may pick, which state transitions
// the cancel/resume/pause commands are allowed to make, and how the lease and
// backoff clocks round-trip. A mock would keep passing if any of those WHERE
// clauses lost its guard.

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"skin-nexus/server/internal/dbx"
	"skin-nexus/server/internal/dialect"
	"skin-nexus/server/internal/migrate"
)

func rig(t *testing.T) *Store {
	t.Helper()
	db, err := dbx.Open(context.Background(), "sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := migrate.ApplySQLDirTracked(db.Underlying(), filepath.Join("..", "..", "migrations"), dialect.SQLite); err != nil {
		t.Fatalf("apply migration tree: %v", err)
	}
	return New(db)
}

// seed writes a row in an arbitrary state, which Create cannot do: Create only
// ever inserts a fresh job and never touches lease_owner/lease_until.
func seed(t *testing.T, st *Store, j Job) {
	t.Helper()
	var lease any
	if !j.LeaseUntil.IsZero() {
		lease = j.LeaseUntil.UTC()
	}
	var started any
	if !j.StartedAt.IsZero() {
		started = j.StartedAt.UTC()
	}
	var finished any
	if !j.FinishedAt.IsZero() {
		finished = j.FinishedAt.UTC()
	}
	ctx := context.Background()
	_, err := st.db.ExecContext(ctx,
		`INSERT INTO host_jobs (id, plugin, handler, owner_uid, state, payload, job_cursor, result,
		   progress, message, last_error, attempts, max_attempts, reclaims, run_after,
		   lease_owner, lease_until, created_at, updated_at, started_at, finished_at)
		 VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		j.ID, j.Plugin, orStr(j.Handler, "import"), j.OwnerUID, j.State, []byte(`{"n":1}`),
		jsonOrNil(j.Cursor), jsonOrNil(j.Result), j.Progress, j.Message, j.LastError,
		j.Attempts, orInt(j.MaxAttempts, 3), j.Reclaims, j.RunAfter.UTC(), j.LeaseOwner, lease,
		j.CreatedAt.UTC(), j.UpdatedAt.UTC(), started, finished)
	if err != nil {
		t.Fatalf("seed %s: %v", j.ID, err)
	}
}

func orStr(s, def string) string {
	if s == "" {
		return def
	}
	return s
}

func orInt(v, def int) int {
	if v == 0 {
		return def
	}
	return v
}

func mustGet(t *testing.T, st *Store, id string) Job {
	t.Helper()
	j, err := st.Get(context.Background(), id)
	if err != nil {
		t.Fatalf("get %s: %v", id, err)
	}
	return j
}

func past(d time.Duration) time.Time { return time.Now().UTC().Add(-d) }

func TestCreateThenGetRoundTripsEveryColumn(t *testing.T) {
	st := rig(t)
	ctx := context.Background()
	want := Job{
		ID: "01JNEW", Plugin: "skin-importer", Handler: "batch", OwnerUID: 7,
		State: "queued", Payload: []byte(`{"scope":"hot"}`), Cursor: []byte(`{"page":2}`),
		Progress: 40, Message: "halfway", MaxAttempts: 5,
	}
	if err := st.Create(ctx, want); err != nil {
		t.Fatalf("create: %v", err)
	}
	got := mustGet(t, st, want.ID)

	if got.Plugin != want.Plugin || got.Handler != want.Handler || got.OwnerUID != want.OwnerUID ||
		got.State != want.State || got.Progress != want.Progress || got.Message != want.Message ||
		got.MaxAttempts != want.MaxAttempts {
		t.Errorf("round trip lost a field: %+v", got)
	}
	if string(got.Payload) != string(want.Payload) {
		t.Errorf("payload = %q, want %q", got.Payload, want.Payload)
	}
	if string(got.Cursor) != string(want.Cursor) {
		t.Errorf("cursor = %q, want %q", got.Cursor, want.Cursor)
	}
	if got.Result != nil {
		t.Errorf("result = %q, want nil for an unwritten column", got.Result)
	}
	if got.LeaseUntil.IsZero() == false || got.StartedAt.IsZero() == false {
		t.Errorf("nullable instants came back set: lease=%v started=%v", got.LeaseUntil, got.StartedAt)
	}
	// DATETIME(3) truncation is deliberate: an untruncated run_after sorts and
	// compares as a different instant than the one a worker polls with.
	if got.RunAfter.Nanosecond()%int(time.Millisecond) != 0 {
		t.Errorf("run_after %v carries sub-millisecond precision", got.RunAfter)
	}
	if time.Since(got.RunAfter) > time.Minute || got.RunAfter.Before(past(time.Minute)) {
		t.Errorf("run_after = %v, want the default to be roughly now", got.RunAfter)
	}
}

func TestGetUnknownIDIsErrNotFound(t *testing.T) {
	st := rig(t)
	if _, err := st.Get(context.Background(), "nope"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("err = %v, want ErrNotFound (a bare sql.ErrNoRows leaks the driver)", err)
	}
}

func TestClaimPicksTheEarliestDueJobAndSkipsFutureOnes(t *testing.T) {
	st := rig(t)
	seed(t, st, Job{ID: "late", Plugin: "p", State: "queued", RunAfter: time.Now().UTC().Add(time.Hour),
		CreatedAt: past(time.Hour), UpdatedAt: past(time.Hour)})
	seed(t, st, Job{ID: "second", Plugin: "p", State: "queued", RunAfter: past(time.Minute),
		CreatedAt: past(2 * time.Hour), UpdatedAt: past(time.Minute)})
	seed(t, st, Job{ID: "first", Plugin: "p", State: "queued", RunAfter: past(5 * time.Minute),
		CreatedAt: past(3 * time.Hour), UpdatedAt: past(5 * time.Minute)})

	got, err := st.Claim(context.Background(), "w1", time.Now().UTC(), time.Minute)
	if err != nil {
		t.Fatalf("claim: %v", err)
	}
	if got == nil || got.ID != "first" {
		t.Fatalf("claim picked %+v, want the earliest due row 'first' (not 'late', which is not due)", got)
	}
	if got.State != "running" || got.LeaseOwner != "w1" {
		t.Errorf("leased row = state %q owner %q", got.State, got.LeaseOwner)
	}
	// started_at is stamped on the row; the struct only echoes the fields Claim
	// re-assigns locally, so this is asserted against what was written.
	if s := mustGet(t, st, "first"); s.StartedAt.IsZero() {
		t.Errorf("row has no started_at after being claimed")
	}
}

func TestClaimIsExclusiveBetweenWorkers(t *testing.T) {
	st := rig(t)
	seed(t, st, Job{ID: "one", Plugin: "p", State: "queued", RunAfter: past(time.Second),
		CreatedAt: past(time.Minute), UpdatedAt: past(time.Minute)})
	ctx := context.Background()

	first, err := st.Claim(ctx, "w1", time.Now().UTC(), time.Minute)
	if err != nil || first == nil {
		t.Fatalf("first claim = %+v %v, want the job", first, err)
	}
	second, err := st.Claim(ctx, "w2", time.Now().UTC(), time.Minute)
	if err != nil {
		t.Fatalf("second claim: %v", err)
	}
	if second != nil {
		t.Errorf("worker w2 also got job %q — the lease is not exclusive, so two runners would import the same batch", second.ID)
	}
	if got := mustGet(t, st, "one"); got.LeaseOwner != "w1" {
		t.Errorf("stored lease_owner = %q, want w1", got.LeaseOwner)
	}
}

func TestClaimReclaimsAnExpiredLeaseAndCountsIt(t *testing.T) {
	st := rig(t)
	leaseEnds := past(time.Minute)
	started := past(10 * time.Minute)
	seed(t, st, Job{ID: "orphan", Plugin: "p", State: "running", RunAfter: past(20 * time.Minute),
		LeaseOwner: "dead-worker", LeaseUntil: leaseEnds, StartedAt: started,
		CreatedAt: past(20 * time.Minute), UpdatedAt: past(20 * time.Minute)})

	got, err := st.Claim(context.Background(), "w2", time.Now().UTC(), time.Minute)
	if err != nil || got == nil {
		t.Fatalf("claim of an expired lease = %+v %v, want the job", got, err)
	}
	if got.Reclaims != 1 {
		t.Errorf("reclaims = %d, want 1 — this counter is how an operator sees a crash loop", got.Reclaims)
	}
	stored := mustGet(t, st, "orphan")
	if stored.LeaseOwner != "w2" || stored.State != "running" {
		t.Errorf("row after reclaim = owner %q state %q, want w2/running", stored.LeaseOwner, stored.State)
	}
	if !stored.StartedAt.Equal(started) {
		t.Errorf("started_at = %v, want the original %v (COALESCE must not restart it)", stored.StartedAt, started)
	}
	// A lease that has not expired yet belongs to its worker.
	seed(t, st, Job{ID: "held", Plugin: "p", State: "running", RunAfter: past(time.Hour),
		LeaseOwner: "w3", LeaseUntil: time.Now().UTC().Add(5 * time.Minute),
		CreatedAt: past(time.Hour), UpdatedAt: past(time.Hour)})
	if other, err := st.Claim(context.Background(), "w4", time.Now().UTC(), time.Minute); err != nil || other != nil {
		t.Errorf("claimed a row whose lease is still live: %+v %v", other, err)
	}
}

func TestSaveStepReleasesTheLeaseAndResetsAttempts(t *testing.T) {
	st := rig(t)
	ctx := context.Background()
	seed(t, st, Job{ID: "step", Plugin: "p", State: "running", RunAfter: past(time.Minute),
		LeaseOwner: "w1", LeaseUntil: time.Now().UTC().Add(time.Minute), Attempts: 2, LastError: "boom",
		CreatedAt: past(time.Hour), UpdatedAt: past(time.Hour)})

	ok, err := st.SaveStep(ctx, "step", "w1", []byte(`{"page":3}`), 66, "3 pages in", 0)
	if err != nil {
		t.Fatalf("save step: %v", err)
	}
	if !ok {
		t.Fatal("SaveStep reported no row changed for the holding worker")
	}
	got := mustGet(t, st, "step")
	if got.State != "queued" || got.LeaseOwner != "" || !got.LeaseUntil.IsZero() {
		t.Errorf("lease not released: state %q owner %q until %v", got.State, got.LeaseOwner, got.LeaseUntil)
	}
	if got.Attempts != 0 || got.LastError != "" {
		t.Errorf("attempt budget not reset: attempts %d last_error %q", got.Attempts, got.LastError)
	}
	if string(got.Cursor) != `{"page":3}` || got.Progress != 66 || got.Message != "3 pages in" {
		t.Errorf("progress not persisted: cursor %q progress %d message %q", got.Cursor, got.Progress, got.Message)
	}
	// A step from a worker that no longer holds the lease must not land.
	if ok, err := st.SaveStep(ctx, "step", "w1", []byte(`x`), 99, "stale", 0); err != nil || ok {
		t.Errorf("SaveStep after release = %v %v, want (false, nil)", ok, err)
	}
}

func TestSaveStepKeepsTheDelayInsideItsWindow(t *testing.T) {
	st := rig(t)
	ctx := context.Background()
	claim := func(id string) {
		seed(t, st, Job{ID: id, Plugin: "p", State: "running", RunAfter: past(time.Minute),
			LeaseOwner: "w1", LeaseUntil: time.Now().UTC().Add(time.Minute),
			CreatedAt: past(time.Hour), UpdatedAt: past(time.Hour)})
	}
	claim("long")
	if _, err := st.SaveStep(ctx, "long", "w1", nil, 10, "", 10*time.Minute); err != nil {
		t.Fatalf("save step: %v", err)
	}
	// The cap is the point: an uncapped delay would park the job out of the
	// operator's reach, and cancel/resume are specified to land within a step.
	if gap := mustGet(t, st, "long").RunAfter.Sub(time.Now().UTC()); gap < 50*time.Second || gap > 70*time.Second {
		t.Errorf("run_after is %v from now, want clamped to the 1m ceiling", gap)
	}
	claim("neg")
	if _, err := st.SaveStep(ctx, "neg", "w1", nil, 10, "", -time.Hour); err != nil {
		t.Fatalf("save step: %v", err)
	}
	if gap := mustGet(t, st, "neg").RunAfter.Sub(time.Now().UTC()); gap < -5*time.Second || gap > 5*time.Second {
		t.Errorf("run_after is %v from now, want a negative delay treated as immediate", gap)
	}
}

func TestFailRetriesWithBackoffThenMarksFailed(t *testing.T) {
	st := rig(t)
	ctx := context.Background()
	seed(t, st, Job{ID: "retry", Plugin: "p", State: "running", RunAfter: past(time.Minute),
		LeaseOwner: "w1", LeaseUntil: time.Now().UTC().Add(time.Minute),
		CreatedAt: past(time.Hour), UpdatedAt: past(time.Hour)})
	if err := st.Fail(ctx, "retry", "w1", errors.New("upstream 503"), 1, 3); err != nil {
		t.Fatalf("fail: %v", err)
	}
	got := mustGet(t, st, "retry")
	if got.State != "queued" || got.Attempts != 1 {
		t.Errorf("state %q attempts %d, want queued/1 for a retryable failure", got.State, got.Attempts)
	}
	if got.LastError != "upstream 503" {
		t.Errorf("last_error = %q, want the cause so the panel can show it", got.LastError)
	}
	// attempts=1 → 2^1 × 2s = 4s of backoff.
	if gap := got.RunAfter.Sub(time.Now().UTC()); gap < 2*time.Second || gap > 6*time.Second {
		t.Errorf("backoff is %v, want ~4s", gap)
	}

	seed(t, st, Job{ID: "dead", Plugin: "p", State: "running", RunAfter: past(time.Minute),
		LeaseOwner: "w1", LeaseUntil: time.Now().UTC().Add(time.Minute),
		CreatedAt: past(time.Hour), UpdatedAt: past(time.Hour)})
	if err := st.Fail(ctx, "dead", "w1", nil, 3, 3); err != nil {
		t.Fatalf("fail: %v", err)
	}
	dead := mustGet(t, st, "dead")
	if dead.State != "failed" || dead.FinishedAt.IsZero() {
		t.Errorf("state %q finished %v, want failed with a finish stamp", dead.State, dead.FinishedAt)
	}
	if dead.LastError != "" {
		t.Errorf("last_error = %q, want empty when the cause is nil", dead.LastError)
	}
}

func TestFinishDoneAndMarkCancelledPaths(t *testing.T) {
	st := rig(t)
	ctx := context.Background()
	seed(t, st, Job{ID: "fin", Plugin: "p", State: "running", RunAfter: past(time.Minute),
		LeaseOwner: "w1", LeaseUntil: time.Now().UTC().Add(time.Minute),
		CreatedAt: past(time.Hour), UpdatedAt: past(time.Hour)})
	if err := st.Finish(ctx, "fin", "w1", []byte(`{"imported":12}`)); err != nil {
		t.Fatalf("finish: %v", err)
	}
	got := mustGet(t, st, "fin")
	if got.State != "done" || got.Progress != 100 || string(got.Result) != `{"imported":12}` ||
		got.FinishedAt.IsZero() || got.LeaseOwner != "" {
		t.Errorf("finished row = %+v", got)
	}

	seed(t, st, Job{ID: "cnl", Plugin: "p", State: "cancelling", RunAfter: past(time.Minute),
		LeaseOwner: "w1", LeaseUntil: time.Now().UTC().Add(time.Minute),
		CreatedAt: past(time.Hour), UpdatedAt: past(time.Hour)})
	// The wrong worker must not be able to finalise someone else's cancellation.
	if err := st.MarkCancelled(ctx, "cnl", "w2"); err != nil {
		t.Fatalf("mark cancelled: %v", err)
	}
	if s := mustGet(t, st, "cnl").State; s != "cancelling" {
		t.Errorf("state after a non-holder marked it = %q, want still cancelling", s)
	}
	if err := st.MarkCancelled(ctx, "cnl", "w1"); err != nil {
		t.Fatalf("mark cancelled: %v", err)
	}
	done := mustGet(t, st, "cnl")
	if done.State != "cancelled" || done.FinishedAt.IsZero() {
		t.Errorf("holder's cancellation = %+v", done)
	}
}

func TestRequestCancelStateMachine(t *testing.T) {
	cases := []struct {
		from, want string
		mutable    bool
	}{
		{"queued", "cancelled", true},   // never started: nothing to stop
		{"paused", "cancelled", true},   // parked by a disable: same
		{"running", "cancelling", true}, // must let the worker finish its step
		{"done", "done", false},         // finished work is not rewound
		{"failed", "failed", false},
		{"cancelled", "cancelled", false},
	}
	for _, c := range cases {
		t.Run(c.from, func(t *testing.T) {
			st := rig(t)
			seed(t, st, Job{ID: "j", Plugin: "p", State: c.from, RunAfter: past(time.Minute),
				CreatedAt: past(time.Hour), UpdatedAt: past(time.Hour)})
			if err := st.RequestCancel(context.Background(), "j"); err != nil {
				t.Fatalf("request cancel: %v", err)
			}
			if got := mustGet(t, st, "j").State; got != c.want {
				t.Errorf("cancel from %q → %q, want %q", c.from, got, c.want)
			}
		})
	}
}

func TestResumeOnlyAcceptsDeadStates(t *testing.T) {
	ctx := context.Background()
	for _, from := range []string{"cancelled", "failed", "paused", "cancelling"} {
		st := rig(t)
		seed(t, st, Job{ID: "j", Plugin: "p", State: from, RunAfter: past(3 * time.Hour),
			Attempts: 3, LeaseOwner: "w1", CreatedAt: past(time.Hour), UpdatedAt: past(time.Hour)})
		if err := st.Resume(ctx, "j"); err != nil {
			t.Fatalf("resume from %s: %v", from, err)
		}
		got := mustGet(t, st, "j")
		// run_after comes from the Go clock, not a SQL now(): a +08 database
		// session would otherwise put the resumed job eight hours in the future.
		if got.State != "queued" || got.Attempts != 0 || got.LeaseOwner != "" {
			t.Errorf("resumed row = state %q attempts %d owner %q", got.State, got.Attempts, got.LeaseOwner)
		}
		if gap := got.RunAfter.Sub(time.Now().UTC()); gap < -time.Minute || gap > time.Minute {
			t.Errorf("run_after is %v from now, want the Go clock so the worker can take it this tick", gap)
		}
	}
	for _, live := range []string{"queued", "running", "done"} {
		st := rig(t)
		seed(t, st, Job{ID: "j", Plugin: "p", State: live, RunAfter: past(time.Hour),
			CreatedAt: past(time.Hour), UpdatedAt: past(time.Hour)})
		if err := st.Resume(ctx, "j"); !errors.Is(err, ErrNotFound) {
			t.Errorf("resume from %s = %v, want ErrNotFound (no silent requeue of live work)", live, err)
		}
	}
}

func TestPauseAndResumeByPlugin(t *testing.T) {
	st := rig(t)
	ctx := context.Background()
	seed(t, st, Job{ID: "q1", Plugin: "imp", State: "queued", RunAfter: past(time.Minute), CreatedAt: past(time.Hour), UpdatedAt: past(time.Hour)})
	seed(t, st, Job{ID: "q2", Plugin: "imp", State: "queued", RunAfter: past(time.Minute), CreatedAt: past(time.Hour), UpdatedAt: past(time.Hour)})
	seed(t, st, Job{ID: "run", Plugin: "imp", State: "running", RunAfter: past(time.Minute), LeaseOwner: "w1", CreatedAt: past(time.Hour), UpdatedAt: past(time.Hour)})
	seed(t, st, Job{ID: "other", Plugin: "notheme", State: "queued", RunAfter: past(time.Minute), CreatedAt: past(time.Hour), UpdatedAt: past(time.Hour)})

	if err := st.PausePlugin(ctx, "imp"); err != nil {
		t.Fatalf("pause: %v", err)
	}
	for id, want := range map[string]string{"q1": "paused", "q2": "paused", "run": "running", "other": "queued"} {
		if got := mustGet(t, st, id).State; got != want {
			t.Errorf("%s after PausePlugin(imp) = %q, want %q", id, got, want)
		}
	}
	// disable→enable used to leave every queued job paused forever.
	if err := st.ResumePausedByPlugin(ctx, "imp"); err != nil {
		t.Fatalf("resume paused: %v", err)
	}
	for _, id := range []string{"q1", "q2"} {
		got := mustGet(t, st, id)
		if got.State != "queued" || got.RunAfter.After(time.Now().UTC().Add(time.Minute)) {
			t.Errorf("%s after re-enable = state %q run_after %v, want queued and due now", id, got.State, got.RunAfter)
		}
	}
	if s := mustGet(t, st, "run").State; s != "running" {
		t.Errorf("a running job was disturbed by re-enable: %q", s)
	}
}

func TestRequeueReleasesTheLeaseWithoutConsumingAnAttempt(t *testing.T) {
	st := rig(t)
	ctx := context.Background()
	seed(t, st, Job{ID: "j", Plugin: "p", State: "running", RunAfter: past(time.Minute),
		LeaseOwner: "w1", LeaseUntil: time.Now().UTC().Add(time.Minute), Attempts: 1,
		CreatedAt: past(time.Hour), UpdatedAt: past(time.Hour)})
	if err := st.Requeue(ctx, "j", "w1"); err != nil {
		t.Fatalf("requeue: %v", err)
	}
	got := mustGet(t, st, "j")
	if got.State != "queued" || got.Attempts != 1 || got.LeaseOwner != "" || !got.LeaseUntil.IsZero() {
		t.Errorf("requeued row = state %q attempts %d owner %q until %v", got.State, got.Attempts, got.LeaseOwner, got.LeaseUntil)
	}
	// A non-holder must not be able to hand someone else's lease to the pool.
	seed(t, st, Job{ID: "h", Plugin: "p", State: "running", RunAfter: past(time.Minute),
		LeaseOwner: "w1", LeaseUntil: time.Now().UTC().Add(time.Minute),
		CreatedAt: past(time.Hour), UpdatedAt: past(time.Hour)})
	if err := st.Requeue(ctx, "h", "w2"); err != nil {
		t.Fatalf("requeue by stranger: %v", err)
	}
	if s := mustGet(t, st, "h").State; s != "running" {
		t.Errorf("stranger released a foreign lease: state %q", s)
	}
}

func TestListClampsPageAndPerPage(t *testing.T) {
	st := rig(t)
	ctx := context.Background()
	for i := 0; i < 25; i++ {
		id := string(rune('a'+i)) + string(rune('A'+i))
		seed(t, st, Job{ID: id, Plugin: "imp", State: "queued", RunAfter: past(time.Duration(i) * time.Minute),
			CreatedAt: past(time.Duration(i) * time.Hour), UpdatedAt: past(time.Minute)})
	}
	cases := []struct {
		name        string
		page, per   int
		wantRows    int
		wantPerPage int
	}{
		{"per above the ceiling falls back to 20", 1, 500, 20, 20},
		{"zero per falls back to 20", 1, 0, 20, 20},
		{"negative page is the first page", -3, 10, 10, 10},
		{"page past the end is empty, not the first page again", 99, 10, 0, 10},
		{"a page number that overflows int64", 9223372036854775807, 10, 0, 10},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			rows, total, err := st.List(ctx, "", "", c.page, c.per)
			if err != nil {
				t.Fatalf("list: %v", err)
			}
			if total != 25 {
				t.Errorf("total = %d, want 25 (the count must stay honest on any page)", total)
			}
			if len(rows) != c.wantRows {
				t.Errorf("page %d per %d returned %d rows, want %d", c.page, c.per, len(rows), c.wantRows)
			}
		})
	}
}

func TestListFiltersBothByPluginAndState(t *testing.T) {
	st := rig(t)
	ctx := context.Background()
	seed(t, st, Job{ID: "a", Plugin: "imp", State: "queued", RunAfter: past(time.Minute), CreatedAt: past(time.Hour), UpdatedAt: past(time.Hour)})
	seed(t, st, Job{ID: "b", Plugin: "imp", State: "done", RunAfter: past(time.Minute), CreatedAt: past(time.Hour), UpdatedAt: past(time.Hour)})
	seed(t, st, Job{ID: "c", Plugin: "notheme", State: "queued", RunAfter: past(time.Minute), CreatedAt: past(time.Hour), UpdatedAt: past(time.Hour)})

	rows, total, err := st.List(ctx, "imp", "queued", 1, 10)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if total != 1 || len(rows) != 1 || rows[0].ID != "a" {
		t.Errorf("filtered list = %d rows / total %d %+v, want only imp+queued", len(rows), total, rows)
	}
	if _, _, err := st.List(ctx, "imp", "queued", 1, -1); err != nil {
		t.Errorf("list with a junk page size: %v", err)
	}
}

// A queue row whose nullable columns are NULL must still scan — the panel reads
// these rows for every plugin, and a scan error there is an empty jobs view.
func TestScanToleratesNullOptionalColumns(t *testing.T) {
	st := rig(t)
	ctx := context.Background()
	if _, err := st.db.ExecContext(ctx,
		`INSERT INTO host_jobs (id, plugin, handler, owner_uid, state, payload, progress,
		   attempts, max_attempts, reclaims, run_after, lease_owner, created_at, updated_at)
		 VALUES ('bare','imp','import',1,'queued','{}',0,0,3,0,?,'',?,?)`,
		time.Now().UTC(), time.Now().UTC(), time.Now().UTC()); err != nil {
		t.Fatalf("insert bare row: %v", err)
	}
	got, err := st.Get(ctx, "bare")
	if err != nil {
		t.Fatalf("get bare row: %v", err)
	}
	if got.Cursor != nil || got.Result != nil || !got.LeaseUntil.IsZero() || got.LastError != "" {
		t.Errorf("NULL columns scanned as %+v", got)
	}
	var check sql.NullString
	if err := st.db.QueryRowContext(ctx, `SELECT last_error FROM host_jobs WHERE id='bare'`).Scan(&check); err != nil {
		t.Fatalf("read raw last_error: %v", err)
	}
	if check.Valid {
		t.Errorf("last_error was expected to be NULL in the row but it is %q", check.String)
	}
}
