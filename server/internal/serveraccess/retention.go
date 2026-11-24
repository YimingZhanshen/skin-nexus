package serveraccess

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"time"

	"skin-nexus/server/internal/metrics"
	"skin-nexus/server/internal/store"
)

const (
	retentionBatchSize       = 1000
	defaultRetentionInterval = time.Hour
)

type retentionStore interface {
	SAEventRetentionPolicies(context.Context) ([]store.SAServerPolicy, error)
	PurgeSAJoinEvents(context.Context, int64, time.Time, int) (int64, error)
}

// RetentionWorker owns one explicitly started goroutine. Construction is inert;
// Start is idempotent and Stop waits for the in-flight database pass to exit.
type RetentionWorker struct {
	store    retentionStore
	now      func() time.Time
	interval time.Duration

	lifecycle sync.Mutex
	mu        sync.Mutex
	cancel    context.CancelFunc
	done      chan struct{}
}

func NewRetentionWorker(st retentionStore, now func() time.Time, interval time.Duration) *RetentionWorker {
	if now == nil {
		now = time.Now
	}
	if interval <= 0 {
		interval = defaultRetentionInterval
	}
	return &RetentionWorker{store: st, now: now, interval: interval}
}

func (w *RetentionWorker) Running() bool {
	if w == nil {
		return false
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.cancel != nil
}

func (w *RetentionWorker) Start(ctx context.Context) error {
	if w == nil || w.store == nil {
		return errors.New("serveraccess: retention store is required")
	}
	w.lifecycle.Lock()
	defer w.lifecycle.Unlock()
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.cancel != nil {
		return nil
	}
	workerCtx, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	w.cancel, w.done = cancel, done
	go func() {
		defer close(done)
		ticker := time.NewTicker(w.interval)
		defer ticker.Stop()
		for {
			w.RunOnce(workerCtx)
			select {
			case <-workerCtx.Done():
				return
			case <-ticker.C:
			}
		}
	}()
	return nil
}

func (w *RetentionWorker) Stop() {
	if w == nil {
		return
	}
	w.lifecycle.Lock()
	defer w.lifecycle.Unlock()
	w.mu.Lock()
	cancel, done := w.cancel, w.done
	w.mu.Unlock()
	if cancel == nil {
		return
	}
	cancel()
	<-done
	w.mu.Lock()
	if w.done == done {
		w.cancel, w.done = nil, nil
	}
	w.mu.Unlock()
}

// RunOnce applies a single UTC cutoff per server. A failed server increments the
// alert counter and does not prevent other servers from being cleaned.
func (w *RetentionWorker) RunOnce(ctx context.Context) error {
	if w == nil || w.store == nil {
		return errors.New("serveraccess: retention store is required")
	}
	policies, err := w.store.SAEventRetentionPolicies(ctx)
	if err != nil {
		metrics.ServerAccessRetentionCleanupFailures.Inc()
		slog.Error("serveraccess.retention policy_read_failed")
		return err
	}
	if len(policies) == 0 || ctx.Err() != nil {
		return nil
	}
	now := w.now().UTC()
	for _, policy := range policies {
		if ctx.Err() != nil {
			return nil
		}
		if policy.ServerID <= 0 || policy.EventRetentionDays <= 0 {
			metrics.ServerAccessRetentionCleanupFailures.Inc()
			slog.Error("serveraccess.retention invalid_policy", "server_id", policy.ServerID)
			continue
		}
		cutoff := now.Add(-time.Duration(policy.EventRetentionDays) * 24 * time.Hour)
		if err := w.purgeServer(ctx, policy.ServerID, cutoff); err != nil {
			if ctx.Err() == nil {
				metrics.ServerAccessRetentionCleanupFailures.Inc()
				slog.Error("serveraccess.retention cleanup_failed", "server_id", policy.ServerID)
			}
		}
	}
	return nil
}

func (w *RetentionWorker) purgeServer(ctx context.Context, serverID int64, cutoff time.Time) error {
	for {
		n, err := w.store.PurgeSAJoinEvents(ctx, serverID, cutoff, retentionBatchSize)
		if err != nil {
			return err
		}
		if n < retentionBatchSize || ctx.Err() != nil {
			return nil
		}
	}
}
