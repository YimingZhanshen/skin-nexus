package serveraccess

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"skin-nexus/server/internal/metrics"
	"skin-nexus/server/internal/store"
)

type retentionStoreStub struct {
	policies   []store.SAServerPolicy
	purge      func(context.Context, int64, time.Time, int) (int64, error)
	purgeQuota func(context.Context, time.Time, int) (int64, error)
}

func (s *retentionStoreStub) SAEventRetentionPolicies(context.Context) ([]store.SAServerPolicy, error) {
	return s.policies, nil
}

func (s *retentionStoreStub) PurgeSAJoinEvents(ctx context.Context, serverID int64, cutoff time.Time, limit int) (int64, error) {
	return s.purge(ctx, serverID, cutoff, limit)
}

func (s *retentionStoreStub) PurgeSAQuotaUsage(ctx context.Context, cutoff time.Time, limit int) (int64, error) {
	if s.purgeQuota != nil {
		return s.purgeQuota(ctx, cutoff, limit)
	}
	return 0, nil
}

func TestRetentionRunOncePurgesExpiredQuotaWindows(t *testing.T) {
	now := time.Date(2026, 11, 24, 12, 0, 0, 0, time.UTC)
	calls := 0
	stub := &retentionStoreStub{purgeQuota: func(_ context.Context, cutoff time.Time, limit int) (int64, error) {
		calls++
		if !cutoff.Equal(now) || limit != 1000 {
			t.Fatalf("quota purge cutoff=%s limit=%d", cutoff, limit)
		}
		return 0, nil
	}}
	worker := NewRetentionWorker(stub, func() time.Time { return now }, time.Hour)
	if err := worker.RunOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	if calls != 1 {
		t.Fatalf("quota purge calls = %d, want 1", calls)
	}
}

func TestRetentionRunOnceUsesEachPolicyAndContinuesAfterFailure(t *testing.T) {
	now := time.Date(2026, 11, 24, 12, 0, 0, 0, time.UTC)
	var cutoffs []time.Time
	var servers []int64
	stub := &retentionStoreStub{policies: []store.SAServerPolicy{
		{ServerID: 1, EventRetentionDays: 7},
		{ServerID: 2, EventRetentionDays: 1},
	}}
	stub.purge = func(_ context.Context, serverID int64, cutoff time.Time, limit int) (int64, error) {
		if limit != 1000 {
			t.Fatalf("batch limit = %d, want 1000", limit)
		}
		servers = append(servers, serverID)
		cutoffs = append(cutoffs, cutoff)
		if serverID == 1 {
			return 0, errors.New("forced cleanup failure")
		}
		return 0, nil
	}
	before := metrics.ServerAccessRetentionCleanupFailures.Value()
	worker := NewRetentionWorker(stub, func() time.Time { return now }, time.Hour)
	if err := worker.RunOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(servers) != 2 || servers[0] != 1 || servers[1] != 2 {
		t.Fatalf("cleanup servers = %v", servers)
	}
	if !cutoffs[0].Equal(now.Add(-7*24*time.Hour)) || !cutoffs[1].Equal(now.Add(-24*time.Hour)) {
		t.Fatalf("cleanup cutoffs = %v", cutoffs)
	}
	if got := metrics.ServerAccessRetentionCleanupFailures.Value(); got != before+1 {
		t.Fatalf("cleanup failure metric = %d, want %d", got, before+1)
	}
}

func TestRetentionWorkerDrainsBatchesAndHonorsCancellation(t *testing.T) {
	calls := 0
	secondBatch := make(chan struct{})
	stub := &retentionStoreStub{policies: []store.SAServerPolicy{{ServerID: 1, EventRetentionDays: 30}}}
	stub.purge = func(ctx context.Context, _ int64, _ time.Time, _ int) (int64, error) {
		calls++
		if calls == 1 {
			return 1000, nil
		}
		close(secondBatch)
		<-ctx.Done()
		return 0, ctx.Err()
	}
	worker := NewRetentionWorker(stub, time.Now, time.Hour)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- worker.RunOnce(ctx) }()
	select {
	case <-secondBatch:
	case <-time.After(5 * time.Second):
		t.Fatal("retention did not request the second batch")
	}
	cancel()
	if err := <-done; err != nil {
		t.Fatalf("cancellation = %v", err)
	}
	if calls != 2 {
		t.Fatalf("purge calls = %d, want 2", calls)
	}
}

func TestRetentionLifecycleIsExplicitAndDisabledStackStartsNoWorker(t *testing.T) {
	started := make(chan struct{}, 1)
	release := make(chan struct{})
	stub := &retentionStoreStub{policies: []store.SAServerPolicy{{ServerID: 1, EventRetentionDays: 7}}}
	stub.purge = func(ctx context.Context, _ int64, _ time.Time, _ int) (int64, error) {
		select {
		case started <- struct{}{}:
		default:
		}
		select {
		case <-release:
			return 0, nil
		case <-ctx.Done():
			return 0, ctx.Err()
		}
	}
	s := NewStack(Config{})
	s.retention = NewRetentionWorker(stub, time.Now, time.Hour)
	if err := s.StartRetention(context.Background()); err != nil {
		t.Fatal(err)
	}
	if s.RetentionRunning() {
		t.Fatal("disabled stack started retention")
	}
	select {
	case <-started:
		t.Fatal("disabled stack reached retention store")
	default:
	}
	s.SetEnabled(true)
	if err := s.StartRetention(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := s.StartRetention(context.Background()); err != nil {
		t.Fatalf("repeat start must be idempotent: %v", err)
	}
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("enabled stack did not start retention")
	}
	stopped := make(chan struct{})
	go func() { s.StopRetention(); close(stopped) }()
	select {
	case <-stopped:
		t.Fatal("stop returned before in-flight cleanup exited")
	default:
	}
	close(release)
	select {
	case <-stopped:
	case <-time.After(5 * time.Second):
		t.Fatal("retention did not stop")
	}
	if s.RetentionRunning() {
		t.Fatal("retention still marked running after stop")
	}
}

func TestRetentionStartWaitsForConcurrentStopAndLeavesWorkerRunning(t *testing.T) {
	started := make(chan int, 2)
	cancelObserved := make(chan struct{})
	release := make(chan struct{})
	var releaseOnce sync.Once
	releaseFirstPass := func() { releaseOnce.Do(func() { close(release) }) }
	defer releaseFirstPass()

	calls := 0
	stub := &retentionStoreStub{policies: []store.SAServerPolicy{{ServerID: 1, EventRetentionDays: 7}}}
	stub.purge = func(ctx context.Context, _ int64, _ time.Time, _ int) (int64, error) {
		calls++
		started <- calls
		if calls == 1 {
			<-ctx.Done()
			close(cancelObserved)
			<-release
		}
		return 0, nil
	}
	worker := NewRetentionWorker(stub, time.Now, time.Hour)
	if err := worker.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(worker.Stop)
	select {
	case call := <-started:
		if call != 1 {
			t.Fatalf("first cleanup call = %d, want 1", call)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("retention worker did not start")
	}

	stopDone := make(chan struct{})
	go func() {
		worker.Stop()
		close(stopDone)
	}()
	select {
	case <-cancelObserved:
	case <-time.After(5 * time.Second):
		t.Fatal("stop did not cancel the in-flight cleanup")
	}

	startCalled := make(chan struct{})
	startDone := make(chan error, 1)
	go func() {
		close(startCalled)
		startDone <- worker.Start(context.Background())
	}()
	<-startCalled
	var earlyErr error
	returnedEarly := false
	select {
	case earlyErr = <-startDone:
		returnedEarly = true
	case <-time.After(100 * time.Millisecond):
	}

	releaseFirstPass()
	select {
	case <-stopDone:
	case <-time.After(5 * time.Second):
		t.Fatal("retention stop did not finish")
	}
	if returnedEarly {
		t.Fatalf("Start returned while the previous worker was still stopping: %v", earlyErr)
	}
	select {
	case err := <-startDone:
		if err != nil {
			t.Fatalf("Start after Stop: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Start did not finish after Stop")
	}
	select {
	case call := <-started:
		if call != 2 {
			t.Fatalf("replacement cleanup call = %d, want 2", call)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("replacement retention worker did not run")
	}
	if !worker.Running() {
		t.Fatal("enabled retention has no running worker after concurrent Stop and Start")
	}
}
