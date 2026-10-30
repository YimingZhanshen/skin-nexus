package plugin

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"time"

	"github.com/dop251/goja"

	"skin-nexus/server/internal/jobs"
)

type jobReg struct {
	spec JobSpec
	fn   goja.Callable
}

type jobStepResult struct {
	Done     bool
	Cursor   []byte
	Progress int
	Message  string
	Result   []byte
	DelayMS  int
}

func (rt *runtime) regJob(call goja.FunctionCall) goja.Value {
	if !rt.info.hasPermission(PermScheduleJobs) {
		rt.loadErr = errors.New("missing permission schedule:jobs")
		return goja.Undefined()
	}
	name := call.Argument(0).String()
	var spec *JobSpec
	for i := range rt.info.Jobs {
		if rt.info.Jobs[i].Name == name {
			spec = &rt.info.Jobs[i]
			break
		}
	}
	if spec == nil {
		rt.loadErr = errors.New("job " + name + " is not declared in plugin.json")
		return goja.Undefined()
	}
	cb, ok := goja.AssertFunction(call.Argument(1))
	if !ok {
		rt.loadErr = errors.New("job handler must be a function")
		return goja.Undefined()
	}
	rt.jobHandlers[name] = jobReg{spec: *spec, fn: cb}
	return goja.Undefined()
}

func (rt *runtime) jsJobSubmit(call goja.FunctionCall) goja.Value {
	if !rt.info.hasPermission(PermScheduleJobs) {
		rt.throw(errors.New("missing permission schedule:jobs"))
	}
	if rt.jobStore == nil {
		rt.throw(errors.New("jobs.unavailable: queue is not configured"))
	}
	if rt.routeUser == nil || rt.routeUser.ID == 0 || rt.routeUser.Permission <= 0 {
		rt.throw(errors.New("jobs.forbidden: admin identity required"))
	}
	name := call.Argument(0).String()
	var spec *JobSpec
	for i := range rt.info.Jobs {
		if rt.info.Jobs[i].Name == name {
			spec = &rt.info.Jobs[i]
			break
		}
	}
	if spec == nil {
		rt.throw(errors.New("jobs.unknown: handler is not declared"))
	}
	var raw any
	if v := call.Argument(1); v != nil && !goja.IsUndefined(v) && !goja.IsNull(v) {
		raw = v.Export()
	}
	payload, err := json.Marshal(raw)
	if err != nil {
		rt.throw(errors.New("jobs.invalid: payload is not JSON"))
	}
	buf := make([]byte, 13)
	if _, err := rand.Read(buf); err != nil {
		rt.throw(err)
	}
	now := time.Now()
	j := jobs.Job{
		ID: hex.EncodeToString(buf), Plugin: rt.info.Name, Handler: name,
		OwnerUID: rt.routeUser.ID, State: "queued", Payload: payload,
		MaxAttempts: spec.MaxAttempts, RunAfter: now, CreatedAt: now, UpdatedAt: now,
	}
	if j.MaxAttempts <= 0 {
		j.MaxAttempts = 3
	}
	if err := rt.jobStore.Create(rt.jobCtx(), j); err != nil {
		rt.throw(err)
	}
	return rt.vm.ToValue(j.ID)
}

func (rt *runtime) jobCtx() context.Context {
	if rt.routeCtx != nil {
		return rt.routeCtx
	}
	return context.Background()
}

func (rt *runtime) adminJobs() *jobs.Store {
	if !rt.info.hasPermission(PermScheduleJobs) || rt.jobStore == nil {
		rt.throw(errors.New("jobs.unavailable: queue is not configured"))
	}
	if rt.routeUser == nil || rt.routeUser.Permission <= 0 {
		rt.throw(errors.New("jobs.forbidden: admin identity required"))
	}
	return rt.jobStore
}

func jobMap(j jobs.Job) map[string]any {
	out := map[string]any{
		"id": j.ID, "plugin": j.Plugin, "handler": j.Handler,
		"state": j.State, "progress": j.Progress, "message": j.Message,
		"lastError": j.LastError, "attempts": j.Attempts, "maxAttempts": j.MaxAttempts,
		"createdAt": j.CreatedAt.UTC().Format("2006-01-02T15:04:05Z07:00"),
		"updatedAt": j.UpdatedAt.UTC().Format("2006-01-02T15:04:05Z07:00"),
	}
	if len(j.Payload) > 0 {
		var v any
		if json.Unmarshal(j.Payload, &v) == nil {
			out["payload"] = v
		}
	}
	if len(j.Cursor) > 0 {
		var v any
		if json.Unmarshal(j.Cursor, &v) == nil {
			out["cursor"] = v
		}
	}
	if len(j.Result) > 0 {
		var v any
		if json.Unmarshal(j.Result, &v) == nil {
			out["result"] = v
		}
	}
	return out
}

func (rt *runtime) jsJobList(call goja.FunctionCall) goja.Value {
	store := rt.adminJobs()
	page := int64(1)
	if v := call.Argument(0); v != nil && !goja.IsUndefined(v) {
		page = v.ToInteger()
	}
	list, total, err := store.List(rt.jobCtx(), rt.info.Name, "", int(page), 50)
	if err != nil {
		rt.throw(err)
	}
	jobs := make([]any, 0, len(list))
	for _, j := range list {
		jobs = append(jobs, jobMap(j))
	}
	return rt.vm.ToValue(map[string]any{"jobs": jobs, "total": total})
}

func (rt *runtime) jsJobStatus(call goja.FunctionCall) goja.Value {
	store := rt.adminJobs()
	id := call.Argument(0).String()
	j, err := store.Get(rt.jobCtx(), id)
	if err != nil || j.Plugin != rt.info.Name {
		rt.throw(errors.New("jobs.not_found"))
	}
	return rt.vm.ToValue(jobMap(j))
}

func (rt *runtime) jsJobCancel(call goja.FunctionCall) goja.Value {
	store := rt.adminJobs()
	id := call.Argument(0).String()
	j, err := store.Get(rt.jobCtx(), id)
	if err != nil || j.Plugin != rt.info.Name {
		rt.throw(errors.New("jobs.not_found"))
	}
	if err := store.RequestCancel(rt.jobCtx(), id); err != nil {
		rt.throw(err)
	}
	return rt.vm.ToValue(true)
}

func (rt *runtime) jsJobResume(call goja.FunctionCall) goja.Value {
	store := rt.adminJobs()
	id := call.Argument(0).String()
	j, err := store.Get(rt.jobCtx(), id)
	if err != nil || j.Plugin != rt.info.Name {
		rt.throw(errors.New("jobs.not_found"))
	}
	if err := store.Resume(rt.jobCtx(), id); err != nil {
		rt.throw(err)
	}
	return rt.vm.ToValue(true)
}

// runJobStep executes one manifest-declared job handler in this runtime. The
// caller must use a dedicated job VM; the route VM's mutex is never touched.
func (rt *runtime) runJobStep(ctx context.Context, j *jobs.Job) (jobStepResult, error) {
	reg, ok := rt.jobHandlers[j.Handler]
	if !ok {
		return jobStepResult{}, errors.New("jobs.unknown: handler is not registered")
	}
	var payload, cursor any
	_ = json.Unmarshal(j.Payload, &payload)
	if len(j.Cursor) > 0 {
		_ = json.Unmarshal(j.Cursor, &cursor)
	}
	timeout := time.Duration(reg.spec.TimeoutMS) * time.Millisecond
	if timeout <= 0 {
		timeout = 10 * time.Second
	}
	val, err := rt.withTimeout(timeout, func() (goja.Value, error) {
		// Identity/context are swapped under rt.mu so a second worker cannot
		// clobber them while this step's VM is executing.
		rt.jobID = j.ID
		rt.jobUser = &SessionIdentity{ID: j.OwnerUID, Permission: 1}
		rt.routeCtx = ctx
		return reg.fn(goja.Undefined(), rt.vm.ToValue(map[string]any{
			"jobId": j.ID, "attempt": j.Attempts + 1, "payload": payload,
			"cursor": cursor, "cancelRequested": false,
		}))
	})
	if err != nil {
		return jobStepResult{}, err
	}
	out := map[string]any{}
	if val != nil && !goja.IsUndefined(val) && !goja.IsNull(val) {
		if m, ok := val.Export().(map[string]any); ok {
			out = m
		}
	}
	res := jobStepResult{Progress: j.Progress}
	if v, ok := out["done"].(bool); ok {
		res.Done = v
	}
	switch v := out["progress"].(type) {
	case int64:
		res.Progress = int(v)
	case float64:
		res.Progress = int(v)
	}
	if v, ok := out["message"].(string); ok {
		res.Message = v
	}
	switch v := out["delayMS"].(type) {
	case int64:
		res.DelayMS = int(v)
	case float64:
		res.DelayMS = int(v)
	}
	if res.DelayMS < 0 {
		res.DelayMS = 0
	}
	if res.DelayMS > 60000 {
		res.DelayMS = 60000
	}
	if v, exists := out["cursor"]; exists {
		if b, err := json.Marshal(v); err == nil {
			res.Cursor = b
		}
	}
	if v, exists := out["result"]; exists {
		if b, err := json.Marshal(v); err == nil {
			res.Result = b
		}
	}
	return res, nil
}
