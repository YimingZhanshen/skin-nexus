package plugin

// jobs_bridge_test.go covers the one read-only question a long job step can
// ask from inside itself: has the operator asked this job to stop? The job
// object handed to a step is a snapshot taken before it began, so the payload's
// own cancelRequested field cannot answer that — these tests pin what the
// polling call does answer, and that it cannot be pointed at another plugin's
// job.

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"
	"time"

	"skin-nexus/server/internal/dbx"
	"skin-nexus/server/internal/dialect"
	"skin-nexus/server/internal/jobs"
	"skin-nexus/server/internal/migrate"
)

const probeManifest = `{
  "name": "probe",
  "version": "1.0.0",
  "hookApi": 2,
  "entrypoint": "index.js",
  "permissions": ["schedule:jobs"],
  "jobs": [{ "name": "watch", "timeoutMS": 2000, "maxAttempts": 1 }]
}`

// The handler reports what it sees, in the result the host persists for the
// step, so the assertion reads the queue rather than a test-only side channel.
const probeScript = `
skin.jobs.register("watch", function (job) {
  return { done: true, result: { asked: skin.jobs.cancelRequested(job.jobId) } };
});
`

func jobStore(t *testing.T) *jobs.Store {
	t.Helper()
	db, err := dbx.Open(context.Background(), "sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := migrate.ApplySQLDirTracked(db.Underlying(), filepath.Join("..", "..", "migrations"), dialect.SQLite); err != nil {
		t.Fatalf("apply migrations: %v", err)
	}
	return jobs.New(db)
}

// runStep executes one step of a seeded job through the production path
// (runJobStep), so the identity and context the bridge reads are the ones a
// real worker sets.
func runStep(t *testing.T, m *Manager, store *jobs.Store, plugin, id, state string) map[string]any {
	t.Helper()
	mu := time.Now().UTC().Truncate(time.Millisecond)
	seed := jobs.Job{
		ID: id, Plugin: plugin, Handler: "watch", OwnerUID: 1, State: state,
		Payload: []byte(`{}`), MaxAttempts: 1, RunAfter: mu, CreatedAt: mu, UpdatedAt: mu,
	}
	if err := store.Create(context.Background(), seed); err != nil {
		t.Fatalf("seed %s job: %v", state, err)
	}
	m.mu.RLock()
	inst := m.instances[plugin]
	m.mu.RUnlock()
	if inst == nil || inst.jobRt == nil {
		t.Fatalf("plugin %q has no job VM", plugin)
	}
	res, err := inst.jobRt.runJobStep(context.Background(), &seed)
	if err != nil {
		t.Fatalf("run step for state %q: %v", state, err)
	}
	var out struct {
		Asked any `json:"asked"`
	}
	if len(res.Result) > 0 {
		if err := json.Unmarshal(res.Result, &out); err != nil {
			t.Fatalf("decode result %s: %v", res.Result, err)
		}
	}
	return map[string]any{"asked": out.Asked, "done": res.Done}
}

func newJobHost(t *testing.T, root string, store *jobs.Store) *Manager {
	t.Helper()
	m := NewHost(RuntimeConfig{
		Dir: root, HostVersion: "0.5.0", State: newMemState(), Jobs: store,
		HookTimeout: 80 * time.Millisecond, AuthTimeout: 40 * time.Millisecond,
		RouteTimeout: 200 * time.Millisecond,
	})
	t.Cleanup(func() { _ = m.Close() })
	if err := m.Load(root); err != nil {
		t.Fatal(err)
	}
	if err := m.Enable(context.Background(), "probe"); err != nil {
		t.Fatalf("enable probe: %v", err)
	}
	return m
}

func TestCancelRequestedTracksTheLiveRow(t *testing.T) {
	store := jobStore(t)
	root := putPlugin(t, "probe", probeManifest, probeScript)
	m := newJobHost(t, root, store)

	// The row is settled at 'cancelling' by RequestCancel the moment an admin
	// asks while a step is in flight — that is precisely when the step can still
	// act on it.
	got := runStep(t, m, store, "probe", "j-cancel", "cancelling")
	if asked, _ := got["asked"].(bool); !asked {
		t.Errorf("cancelRequested during cancelling = %#v, want true", got["asked"])
	}

	// A request that landed on a queued job settles at 'cancelled' outright, and
	// a step picked up before the worker notices must see that too rather than
	// running an extra step.
	got = runStep(t, m, store, "probe", "j-cancelled", "cancelled")
	if asked, _ := got["asked"].(bool); !asked {
		t.Errorf("cancelRequested after cancelled = %#v, want true", got["asked"])
	}

	for _, state := range []string{"queued", "running", "paused", "failed", "done"} {
		got := runStep(t, m, store, "probe", "j-"+state, state)
		if asked, ok := got["asked"].(bool); !ok || asked {
			t.Errorf("cancelRequested during %s = %#v, want false", state, got["asked"])
		}
	}
}

func TestCancelRequestedRejectsOtherPluginsJobs(t *testing.T) {
	store := jobStore(t)
	root := putPlugin(t, "probe", probeManifest, probeScript)
	m := newJobHost(t, root, store)

	// A job row owned by somebody else must not be readable, even though the
	// id is all the caller has to offer.
	mu := time.Now().UTC().Truncate(time.Millisecond)
	if err := store.Create(context.Background(), jobs.Job{
		ID: "foreign", Plugin: "other-plugin", Handler: "watch", OwnerUID: 1, State: "cancelling",
		Payload: []byte(`{}`), MaxAttempts: 1, RunAfter: mu, CreatedAt: mu, UpdatedAt: mu,
	}); err != nil {
		t.Fatalf("seed foreign job: %v", err)
	}
	m.mu.RLock()
	inst := m.instances["probe"]
	m.mu.RUnlock()
	probeJob := jobs.Job{ID: "foreign", Plugin: "other-plugin", Handler: "watch", State: "cancelling"}
	if _, err := inst.jobRt.runJobStep(context.Background(), &probeJob); err == nil {
		t.Fatal("reading another plugin's job must throw, got no error")
	}
}
