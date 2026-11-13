package jobs

// metrics_test.go — only TERMINAL transitions are counted. A job that goes
// back to queued for another attempt has not finished anything, and a WHERE
// clause that matched no row has not changed anything either; counting both
// would draw a completion rate that never happened.

import (
	"context"
	"errors"
	"testing"
	"time"

	"skin-nexus/server/internal/metrics"
)

func seedRunningJob(t *testing.T, s *Store, id string) {
	t.Helper()
	ctx := context.Background()
	if err := s.Create(ctx, Job{
		ID: id, Plugin: "demo", Handler: "run", State: "queued", Payload: []byte("{}"),
		MaxAttempts: 3, RunAfter: time.Now().UTC().Add(-time.Minute),
	}); err != nil {
		t.Fatalf("create: %v", err)
	}
	if _, err := s.Claim(ctx, "w1", time.Now().UTC(), time.Minute); err != nil {
		t.Fatalf("claim: %v", err)
	}
}

func TestTerminalJobTransitionsAreCounted(t *testing.T) {
	s := rig(t)
	ctx := context.Background()

	beforeDone := metrics.Jobs.Value("done")
	seedRunningJob(t, s, "job-done")
	if err := s.Finish(ctx, "job-done", "w1", nil); err != nil {
		t.Fatalf("finish: %v", err)
	}
	if got := metrics.Jobs.Value("done"); got != beforeDone+1 {
		t.Fatalf("done counter = %d, want %d", got, beforeDone+1)
	}

	beforeCancelled := metrics.Jobs.Value("cancelled")
	seedRunningJob(t, s, "job-cancel")
	if err := s.MarkCancelled(ctx, "job-cancel", "w1"); err != nil {
		t.Fatalf("cancel: %v", err)
	}
	if got := metrics.Jobs.Value("cancelled"); got != beforeCancelled+1 {
		t.Fatalf("cancelled counter = %d, want %d", got, beforeCancelled+1)
	}

	beforeFailed := metrics.Jobs.Value("failed")
	seedRunningJob(t, s, "job-fail")
	if err := s.Fail(ctx, "job-fail", "w1", errors.New("boom"), 3, 3); err != nil {
		t.Fatalf("fail: %v", err)
	}
	if got := metrics.Jobs.Value("failed"); got != beforeFailed+1 {
		t.Fatalf("failed counter = %d, want %d", got, beforeFailed+1)
	}
}

// The retry path is not a terminal state, and a transition that matched no
// row changed nothing: neither may move the counters.
func TestNonTerminalAndNoOpTransitionsAreNotCounted(t *testing.T) {
	s := rig(t)
	ctx := context.Background()

	beforeRetry := metrics.Jobs.Value("failed")
	seedRunningJob(t, s, "job-retry")
	if err := s.Fail(ctx, "job-retry", "w1", errors.New("flaky"), 1, 3); err != nil {
		t.Fatalf("fail(retry): %v", err)
	}
	if got := metrics.Jobs.Value("failed"); got != beforeRetry {
		t.Fatalf("a job sent back to the queue must not count as failed: %d != %d", got, beforeRetry)
	}

	beforeDone := metrics.Jobs.Value("done")
	// Finish against a row that is not running and not leased by w1: the UPDATE
	// matches nothing, so nothing finished.
	if err := s.Finish(ctx, "no-such-job", "w1", nil); err != nil {
		t.Fatalf("finish(no-op): %v", err)
	}
	if got := metrics.Jobs.Value("done"); got != beforeDone {
		t.Fatalf("a transition that matched no row must not count: %d != %d", got, beforeDone)
	}
}
