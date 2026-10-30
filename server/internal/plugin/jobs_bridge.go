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

// runJobStep executes one manifest-declared job handler in this runtime. The
// caller must use a dedicated job VM; the route VM's mutex is never touched.
func (rt *runtime) runJobStep(ctx context.Context, j *jobs.Job) (jobStepResult, error) {
	reg, ok := rt.jobHandlers[j.Handler]
	if !ok {
		return jobStepResult{}, errors.New("jobs.unknown: handler is not registered")
	}
	rt.jobID = j.ID
	rt.jobUser = &SessionIdentity{ID: j.OwnerUID, Permission: 1}
	rt.routeCtx = ctx
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
