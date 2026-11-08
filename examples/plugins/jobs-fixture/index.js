// jobs-fixture exists because nothing else in this repository can produce a
// running durable job on a machine with no network. Every real job handler
// talks to an upstream, and the examples must not — so this one burns its steps
// inside the job VM instead. It is a test fixture: no data is read, no data is
// written, and the only state is the step counter in the job cursor.
//
// What it is for: the panel's 任务队列 page offers cancel and resume, and the
// only honest way to check those buttons is a row that is still running when the
// click lands. The host ticks a job once per second (jobs_worker.go), so twelve
// steps is roughly twelve seconds of observable running state.
//
// The second thing it proves is mid-step cancellation. The host cannot interrupt
// a VM call already in flight — cancel takes effect between steps — so a long
// step has to ask for itself, and the only way to ask is
// skin.jobs.cancelRequested(jobId). The `job.cancelRequested` field this handler
// receives is a snapshot from before the step began and never changes under it.

var DEFAULT_STEPS = 12;
var DEFAULT_STEP_MS = 350;
var POLL_MS = 100;

// Waiting on the clock, not on a timer: the sandbox has no setTimeout, and the
// point of the step is that it is still executing when the cancel arrives.
function burnOrAbort(jobId, ms) {
  var started = Date.now();
  var until = started + ms;
  var poll = typeof skin.jobs.cancelRequested === 'function';
  var next = 0;
  while (Date.now() < until) {
    if (!poll || Date.now() < next) continue;
    next = Date.now() + POLL_MS;
    if (skin.jobs.cancelRequested(jobId)) {
      // The elapsed figure is the point of this line: it is what a reader checks
      // against the step budget to see that the handler stopped early instead of
      // burning the full timeout. The job's own message column cannot carry it —
      // the host settles the row at 'cancelled' and does not save the aborted step.
      skin.log('jobs-fixture: cancel seen mid-step ' + jobId + ' after ' + (Date.now() - started) + 'ms of ' + ms + 'ms budget');
      return { aborted: true, took: Date.now() - started };
    }
  }
  return { aborted: false, took: Date.now() - started };
}

function clampInt(v, min, max, dflt) {
  var n = Number(v);
  if (!(n >= min)) return dflt;
  if (n > max) return max;
  return Math.floor(n);
}

skin.jobs.register("tick", function (job) {
  var p = job.payload || {};
  var total = clampInt(p.steps, 1, 1000, DEFAULT_STEPS);
  var stepMS = clampInt(p.stepMS, 100, 20000, DEFAULT_STEP_MS);
  var cursor = job.cursor || { step: 0 };
  var run = burnOrAbort(job.jobId, stepMS);
  if (run.aborted) {
    // Keep the cursor where it is and report the early exit: the host settles
    // the row at 'cancelled' on the next tick, and what the panel shows is the
    // evidence that this step stopped itself rather than running out its budget.
    return {
      done: false, cursor: cursor, progress: Math.floor((cursor.step / total) * 100),
      message: "cancel seen mid-step " + cursor.step + "/" + total + ", stopped after " + run.took + "ms",
    };
  }
  cursor.step += 1;
  return {
    done: cursor.step >= total,
    cursor: cursor,
    progress: Math.floor((cursor.step / total) * 100),
    message: "step " + cursor.step + "/" + total + " (" + run.took + "ms)",
  };
});

skin.route("POST", "/admin/start", function (r) {
  // Clamped rather than rejected: this route exists to hand a test a running
  // job, and a caller that passes steps: 0 or 99999 still gets a usable fixture.
  var body = r.body || {};
  var steps = clampInt(body.steps, 1, 1000, DEFAULT_STEPS);
  var stepMS = clampInt(body.stepMS, 100, 20000, DEFAULT_STEP_MS);
  return {
    status: 200,
    body: { jobId: skin.jobs.submit("tick", { steps: steps, stepMS: stepMS }) },
  };
});

skin.route("GET", "/admin/jobs", function () {
  return { status: 200, body: skin.jobs.list(1) };
});
