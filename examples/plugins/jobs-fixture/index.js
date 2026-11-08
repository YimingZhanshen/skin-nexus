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

var DEFAULT_STEPS = 12;
var STEP_MS = 350;

function burn(ms) {
  // A busy wait, not a timer: the sandbox has no setTimeout, and the point is
  // that the step is still executing when the cancel request arrives.
  var until = Date.now() + ms;
  while (Date.now() < until) { }
}

skin.jobs.register("tick", function (job) {
  var total = Number((job.payload && job.payload.steps) || DEFAULT_STEPS);
  if (total < 1 || total > 1000) total = DEFAULT_STEPS;
  var cursor = job.cursor || { step: 0 };
  burn(STEP_MS);
  cursor.step += 1;
  return {
    done: cursor.step >= total,
    cursor: cursor,
    progress: Math.floor((cursor.step / total) * 100),
    message: "step " + cursor.step + "/" + total,
  };
});

skin.route("POST", "/admin/start", function (r) {
  // Clamped rather than rejected: this route exists to hand a test a running
  // job, and a caller that passes steps: 0 or 99999 still gets a usable fixture.
  var steps = Number((r.body || {}).steps || DEFAULT_STEPS);
  if (!(steps >= 1)) steps = DEFAULT_STEPS;
  if (steps > 1000) steps = 1000;
  return { status: 200, body: { jobId: skin.jobs.submit("tick", { steps: Math.floor(steps) }) } };
});

skin.route("GET", "/admin/jobs", function () {
  return { status: 200, body: skin.jobs.list(1) };
});
