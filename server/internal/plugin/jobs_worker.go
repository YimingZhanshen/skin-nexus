package plugin

import (
	"context"
	"errors"
	"time"
)

// jobWorker claims durable jobs and runs one step at a time. The step runs in
// the plugin's dedicated job VM (instance.jobRt), never the request VM.
func (m *Manager) jobWorker(name string) {
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-m.done:
			return
		case <-ticker.C:
			m.runOneJob(name + "-" + randomJobID())
		}
	}
}

func (m *Manager) runOneJob(worker string) {
	if m.cfg.Jobs == nil {
		return
	}
	ctx := context.Background()
	// The lease must outlive the largest step budget (30s) plus DB/PNG work;
	// a 30s lease let another worker reclaim and double-run a live step.
	j, err := m.cfg.Jobs.Claim(ctx, worker, time.Now(), 90*time.Second)
	if err != nil || j == nil {
		return
	}
	if j.Reclaims > 10 {
		_ = m.cfg.Jobs.Fail(ctx, j.ID, worker, errors.New("job lease repeatedly lost"), j.MaxAttempts, j.MaxAttempts)
		return
	}
	m.mu.RLock()
	inst := m.instances[j.Plugin]
	m.mu.RUnlock()
	if inst == nil || !inst.enabled || inst.jobRt == nil {
		_ = m.cfg.Jobs.Requeue(ctx, j.ID, worker)
		return
	}
	// Two workers may claim two due jobs of the same plugin; the job VM is
	// shared, so serialize steps per instance instead of racing rt.jobUser.
	select {
	case inst.jobSem <- struct{}{}:
		defer func() { <-inst.jobSem }()
	default:
		_ = m.cfg.Jobs.Requeue(ctx, j.ID, worker)
		return
	}
	// Cancel is checked between steps; a running step gets one more chance to
	// persist its cursor before the next tick sees cancelling.
	if state, err := m.cfg.Jobs.Get(ctx, j.ID); err == nil && state.State == "cancelling" {
		_ = m.cfg.Jobs.MarkCancelled(ctx, j.ID, worker)
		return
	}
	res, err := inst.jobRt.runJobStep(ctx, j)
	if err != nil {
		// Cancel may have arrived mid-step. Fail is guarded by
		// state='running', so without this the job would be stranded in
		// 'cancelling' with no claim path.
		if cur, gerr := m.cfg.Jobs.Get(ctx, j.ID); gerr == nil && cur.State == "cancelling" {
			_ = m.cfg.Jobs.MarkCancelled(ctx, j.ID, worker)
			return
		}
		_ = m.cfg.Jobs.Fail(ctx, j.ID, worker, err, j.Attempts+1, j.MaxAttempts)
		return
	}
	if res.Done {
		if err := m.cfg.Jobs.Finish(ctx, j.ID, worker, res.Result); err == nil {
			if cur, getErr := m.cfg.Jobs.Get(ctx, j.ID); getErr == nil && cur.State == "cancelling" {
				_ = m.cfg.Jobs.MarkCancelled(ctx, j.ID, worker)
			}
		}
		return
	}
	ok, err := m.cfg.Jobs.SaveStep(ctx, j.ID, worker, res.Cursor, res.Progress, res.Message,
		time.Duration(res.DelayMS)*time.Millisecond)
	if err != nil || !ok {
		// Lost lease or cancelled: do not overwrite a terminal state.
		_ = m.cfg.Jobs.MarkCancelled(ctx, j.ID, worker)
	}
}

func randomJobID() string {
	const chars = "0123456789abcdef"
	b := make([]byte, 8)
	now := time.Now().UnixNano()
	for i := range b {
		b[i] = chars[(now>>uint(i*4))&0xf]
	}
	return string(b)
}
