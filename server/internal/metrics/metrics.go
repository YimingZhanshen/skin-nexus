// Package metrics is the server's own instrumentation, scraped in Prometheus
// text exposition format and written by hand: this repository takes no new
// dependencies, and the format is small enough that a hand-rolled writer is
// easier to audit than a vendored client.
//
// Three rules the tests hold this package to:
//
//   - a scrape of an idle process is byte-identical to the previous one
//     (sorted label sets), so a diffing scraper never reports motion that is
//     not there;
//   - label values are escaped, because they travel through the parser on the
//     other side and a quote in a value must not truncate the line;
//   - a label arity mismatch panics where it is written rather than rendering
//     a broken line at scrape time.
//
// Counters are declared once, here, so every call site in every package
// records into the same family with the same help text and label names.
package metrics

import (
	"fmt"
	"io"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// nameRe is the Prometheus metric/label name grammar.
var nameRe = regexp.MustCompile(`^[a-zA-Z_:][a-zA-Z0-9_:]*$`)

// counter is one metric family: a name, its help, its label names, and the
// samples keyed by the label values (joined with NUL, which no label value
// can contain as a practical matter — and if one does, it is still just a
// different series key).
type counter struct {
	name       string
	help       string
	labelNames []string
	series     map[string]uint64
	order      []string // first-seen order of series keys, for a stable diff
	handle     *Counter // one handle per family, so a repeated declaration is the same one
}

func (c *counter) key(values []string) string {
	return strings.Join(values, "\x00")
}

// Registry holds every family this process exposes plus the process start
// time (the uptime gauge is derived from it, not stored as a value that could
// drift from the clock).
type Registry struct {
	mu       sync.Mutex
	counters map[string]*counter
	order    []string
	started  time.Time
}

// New returns an empty registry with its own clock — tests use one so two
// suites never read each other's samples.
func New() *Registry {
	return &Registry{counters: map[string]*counter{}, started: time.Now()}
}

// Counter declares a metric family and returns a handle for recording into
// it. Declaring the same name twice returns the same handle: call sites in
// different packages describe one metric, and forking it would silently split
// the series.
func (r *Registry) NewCounter(name, help string, labelNames ...string) *Counter {
	if !nameRe.MatchString(name) {
		panic(fmt.Sprintf("metrics: invalid metric name %q", name))
	}
	for _, l := range labelNames {
		if !nameRe.MatchString(l) {
			panic(fmt.Sprintf("metrics: invalid label name %q", l))
		}
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if c, ok := r.counters[name]; ok {
		if len(c.labelNames) != len(labelNames) {
			panic(fmt.Sprintf("metrics: %q already declared with %d labels, not %d", name, len(c.labelNames), len(labelNames)))
		}
		return c.handle
	}
	c := &counter{
		name:       name,
		help:       help,
		labelNames: labelNames,
		series:     map[string]uint64{},
	}
	c.handle = &Counter{r: r, name: name}
	r.counters[name] = c
	r.order = append(r.order, name)
	return c.handle
}

// Counter is a handle to one family.
type Counter struct {
	r    *Registry
	name string
}

// Inc adds one. The label values must match the declared label names exactly —
// a mismatch is a programming error, caught here rather than as a malformed
// line in a scraper's parse log.
func (c *Counter) Inc(labelValues ...string) {
	c.Add(1, labelValues...)
}

// Add adds delta to the series identified by labelValues.
func (c *Counter) Add(delta uint64, labelValues ...string) {
	c.r.mu.Lock()
	defer c.r.mu.Unlock()
	def, ok := c.r.counters[c.name]
	if !ok {
		return // unreachable: the handle could only have come from a declaration
	}
	if len(labelValues) != len(def.labelNames) {
		panic(fmt.Sprintf("metrics: %s takes %d label values (%s), got %d", c.name, len(def.labelNames), strings.Join(def.labelNames, ", "), len(labelValues)))
	}
	k := def.key(labelValues)
	if _, seen := def.series[k]; !seen {
		def.order = append(def.order, k)
	}
	def.series[k] += delta
}

// Value reports the current count for one series — the assertion seam the
// wiring tests read, and nothing in production needs it.
func (c *Counter) Value(labelValues ...string) uint64 {
	c.r.mu.Lock()
	defer c.r.mu.Unlock()
	def, ok := c.r.counters[c.name]
	if !ok {
		return 0
	}
	if len(labelValues) != len(def.labelNames) {
		return 0
	}
	return def.series[def.key(labelValues)]
}

// Uptime is how long this process has been running.
func (r *Registry) Uptime() time.Duration {
	r.mu.Lock()
	defer r.mu.Unlock()
	return time.Since(r.started)
}

// WriteText renders the whole registry in Prometheus text exposition format:
// HELP and TYPE per family, then one sample per series, then the uptime
// gauge. Output is deterministic for a given state.
func (r *Registry) WriteText(w io.Writer) error {
	r.mu.Lock()
	names := append([]string(nil), r.order...)
	families := make(map[string]*counter, len(names))
	for _, n := range names {
		families[n] = r.counters[n]
	}
	uptime := time.Since(r.started)
	r.mu.Unlock()

	var b strings.Builder
	for _, name := range names {
		c := families[name]
		fmt.Fprintf(&b, "# HELP %s %s\n", name, escapeHelp(c.help))
		fmt.Fprintf(&b, "# TYPE %s counter\n", name)
		keys := append([]string(nil), c.order...)
		sort.Strings(keys)
		for _, k := range keys {
			b.WriteString(name)
			if len(c.labelNames) > 0 {
				values := strings.Split(k, "\x00")
				b.WriteByte('{')
				for i, ln := range c.labelNames {
					if i > 0 {
						b.WriteByte(',')
					}
					fmt.Fprintf(&b, "%s=%s", ln, quote(values[i]))
				}
				b.WriteByte('}')
			}
			b.WriteByte(' ')
			b.WriteString(strconv.FormatUint(c.series[k], 10))
			b.WriteByte('\n')
		}
	}
	fmt.Fprintf(&b, "# HELP process_uptime_seconds Seconds since this process started.\n")
	fmt.Fprintf(&b, "# TYPE process_uptime_seconds gauge\n")
	fmt.Fprintf(&b, "process_uptime_seconds %s\n", strconv.FormatFloat(uptime.Seconds(), 'f', 3, 64))
	_, err := io.WriteString(w, b.String())
	return err
}

// quote renders a label value in the format the exposition grammar expects.
func quote(v string) string {
	r := strings.NewReplacer(`\`, `\\`, `"`, `\"`, "\n", `\n`)
	return `"` + r.Replace(v) + `"`
}

// escapeHelp keeps the HELP line to one line and out of the way of a backslash.
func escapeHelp(h string) string {
	r := strings.NewReplacer(`\`, `\\`, "\n", `\n`)
	return r.Replace(h)
}

// Default is the process-wide registry the server records into.
var Default = New()

// The counters every package writes into. Declared once so the help text and
// label set cannot drift between the access log, the job queue and the panel.
var (
	// HTTPRequests counts every request the access log middleware sees — the
	// same middleware, so the counter and the log can never disagree about
	// what was served.
	HTTPRequests = Default.NewCounter("http_requests_total", "HTTP requests served, by method and status code.", "method", "status")
	// Jobs counts terminal transitions only: a job sent back to the queue for
	// another attempt has not finished anything.
	Jobs = Default.NewCounter("jobs_total", "Background jobs reaching a terminal state, by outcome.", "outcome")
	// Uploads counts every texture upload attempt, accepted or refused.
	Uploads = Default.NewCounter("uploads_total", "Texture uploads, by outcome.", "outcome")
	// SignatureFailures counts refused union requests by the same reason
	// tokens the audit log records. A module that is off is not failing.
	SignatureFailures = Default.NewCounter("union_signature_failures_total", "Union request signature checks refused, by reason.", "reason")
)
