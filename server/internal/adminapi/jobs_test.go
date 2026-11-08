package adminapi

// jobs_test.go pins the two queue controls that change what runs next. The
// panel's 任务队列 page had no test at all, and the view had drifted from the
// store: Resume accepts a cancelled job (jobs/store.go), while the button was
// offered only for paused/failed — so a cancelled task could be watched but not
// restarted. These tests keep the handler honest about the states it answers
// for, and about leaving an audit row behind for each decision.

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"skin-nexus/server/internal/jobs"
)

func jobsBackend(t *testing.T) (*Backend, *jobs.Store) {
	t.Helper()
	m := diagStore(t)
	js := jobs.New(m.DBX())
	b := New(m, nil)
	b.Jobs = js
	b.SettingsDefaults = map[string]string{}
	b.SiteName = "Jobs"
	return b, js
}

func seedJob(t *testing.T, js *jobs.Store, id, state string) {
	t.Helper()
	err := js.Create(context.Background(), jobs.Job{
		ID: id, Plugin: "jobs-fixture", Handler: "tick", OwnerUID: 1, State: state,
		Payload: []byte(`{"steps":12}`), Attempts: 1, MaxAttempts: 3,
		RunAfter: time.Now().UTC().Truncate(time.Millisecond),
	})
	if err != nil {
		t.Fatalf("seed %s job %q: %v", state, id, err)
	}
}

// jobRequest stands in for the mux: the handlers read r.PathValue("id"), which
// only a routed request would carry.
func jobRequest(method, path, id string, uid int64) *http.Request {
	r := httptest.NewRequest(method, path, nil)
	r.SetPathValue("id", id)
	return r.WithContext(context.WithValue(r.Context(), actorUIDKey{}, uid))
}

func TestJobCancelMovesQueuedToCancelled(t *testing.T) {
	b, js := jobsBackend(t)
	seedJob(t, js, "j-cancel", "queued")

	w := httptest.NewRecorder()
	b.handleJobCancel(w, jobRequest("POST", "/api/admin/v1/jobs/j-cancel/cancel", "j-cancel", 1))
	if w.Code != http.StatusOK {
		t.Fatalf("cancel queued job = %d %s, want 200", w.Code, w.Body.String())
	}
	got, err := js.Get(context.Background(), "j-cancel")
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	// A job that has not started needs no step boundary: the store settles it
	// straight at 'cancelled', not the intermediate 'cancelling'.
	if got.State != "cancelled" {
		t.Errorf("state after cancel = %q, want cancelled", got.State)
	}
	if total := auditCount(t, b, "plugin_job_cancel"); total != 1 {
		t.Errorf("plugin_job_cancel audit rows = %d, want 1 — cancelling from the panel must be attributable", total)
	}
}

func TestJobResumeAcceptsCancelled(t *testing.T) {
	b, js := jobsBackend(t)
	seedJob(t, js, "j-resume", "cancelled")

	w := httptest.NewRecorder()
	b.handleJobResume(w, jobRequest("POST", "/api/admin/v1/jobs/j-resume/resume", "j-resume", 1))
	if w.Code != http.StatusOK {
		t.Fatalf("resume cancelled job = %d %s, want 200", w.Code, w.Body.String())
	}
	got, err := js.Get(context.Background(), "j-resume")
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	if got.State != "queued" {
		t.Errorf("state after resume = %q, want queued", got.State)
	}
	// attempts is reset on resume, so a cancelled-then-resumed job gets its full
	// retry budget back rather than failing on the first stumble.
	if got.Attempts != 0 {
		t.Errorf("attempts after resume = %d, want 0", got.Attempts)
	}
	if total := auditCount(t, b, "plugin_job_resume"); total != 1 {
		t.Errorf("plugin_job_resume audit rows = %d, want 1", total)
	}
}

func TestJobResumeRejectsFinishedJob(t *testing.T) {
	b, js := jobsBackend(t)
	seedJob(t, js, "j-done", "done")

	w := httptest.NewRecorder()
	b.handleJobResume(w, jobRequest("POST", "/api/admin/v1/jobs/j-done/resume", "j-done", 1))
	if w.Code != http.StatusConflict {
		t.Fatalf("resume a finished job = %d %s, want 409", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "job.not_resumable") {
		t.Errorf("body %s should carry the code the dictionary translates", w.Body.String())
	}
	if total := auditCount(t, b, "plugin_job_resume"); total != 0 {
		t.Errorf("a refused resume wrote %d audit rows, want 0", total)
	}
}

func TestJobsListFiltersByPluginAndState(t *testing.T) {
	b, js := jobsBackend(t)
	seedJob(t, js, "j-a", "queued")
	if err := js.Create(context.Background(), jobs.Job{
		ID: "j-b", Plugin: "other-plugin", Handler: "tick", OwnerUID: 1, State: "failed",
		Payload: []byte(`{}`), MaxAttempts: 3, RunAfter: time.Now().UTC().Truncate(time.Millisecond),
	}); err != nil {
		t.Fatalf("seed second job: %v", err)
	}

	w := httptest.NewRecorder()
	b.handleJobsList(w, httptest.NewRequest("GET", "/api/admin/v1/jobs?plugin=jobs-fixture&state=queued", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("list = %d %s", w.Code, w.Body.String())
	}
	var out struct {
		Jobs  []jobView `json:"jobs"`
		Total int       `json:"total"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode: %v (%s)", err, w.Body.String())
	}
	if out.Total != 1 || len(out.Jobs) != 1 || out.Jobs[0].ID != "j-a" {
		t.Errorf("filtered list = total %d ids %v, want only j-a", out.Total, jobIDs(out.Jobs))
	}
	if out.Jobs[0].Payload == nil {
		t.Errorf("job %q lost its payload in the view — the panel reads it to explain what the job was asked to do", out.Jobs[0].ID)
	}
}

func jobIDs(list []jobView) []string {
	out := make([]string, 0, len(list))
	for _, j := range list {
		out = append(out, j.ID)
	}
	return out
}

func auditCount(t *testing.T, b *Backend, action string) int {
	t.Helper()
	_, total, err := b.Store.AdminAudit(context.Background(), "", action, 1, 10)
	if err != nil {
		t.Fatalf("read audit rows for %q: %v", action, err)
	}
	return total
}
