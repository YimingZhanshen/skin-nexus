package metrics

// metrics_test.go — the exposition is the product here: a counter nobody can
// scrape correctly is worse than no counter, because it looks like monitoring.
//
// The format is Prometheus text exposition, written by hand (this repository
// takes no new dependencies): a HELP line and a TYPE line per family, then
// one sample per label set, values sorted so two scrapes of an idle process
// are byte-identical.

import (
	"strings"
	"testing"
	"time"
)

func TestCounterExpositionShape(t *testing.T) {
	r := New()
	c := r.NewCounter("demo_total", "Demo counter.", "stage")
	c.Inc("a")
	c.Inc("a")
	c.Inc("b")

	var b strings.Builder
	if err := r.WriteText(&b); err != nil {
		t.Fatal(err)
	}
	out := b.String()

	for _, want := range []string{
		"# HELP demo_total Demo counter.\n",
		"# TYPE demo_total counter\n",
		"demo_total{stage=\"a\"} 2\n",
		"demo_total{stage=\"b\"} 1\n",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("exposition missing %q:\n%s", want, out)
		}
	}
}

// Two scrapes of an untouched process must be byte-identical, so a diffing
// scraper never reports motion where there is none.
func TestExpositionIsDeterministic(t *testing.T) {
	r := New()
	c := r.NewCounter("demo_total", "Demo.", "stage")
	for _, s := range []string{"z", "a", "m", "a"} {
		c.Inc(s)
	}
	render := func() string {
		var b strings.Builder
		if err := r.WriteText(&b); err != nil {
			t.Fatal(err)
		}
		return b.String()
	}
	if render() != render() {
		t.Fatalf("two scrapes of the same registry differ:\n%s\n---\n%s", render(), render())
	}
	// label sets in sorted order, not insertion order
	if strings.Index(render(), `stage="a"`) > strings.Index(render(), `stage="z"`) {
		t.Fatalf("label sets must be sorted for a stable diff:\n%s", render())
	}
}

func TestUptimeIsAGaugeThatOnlyGrows(t *testing.T) {
	r := New()
	first := r.Uptime()
	time.Sleep(2 * time.Millisecond)
	if second := r.Uptime(); second < first {
		t.Fatalf("uptime went backwards: %v then %v", first, second)
	}
	var b strings.Builder
	if err := r.WriteText(&b); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(b.String(), "# TYPE process_uptime_seconds gauge\n") {
		t.Fatalf("uptime must be exported as a gauge:\n%s", b.String())
	}
}

// Label values travel through the parser on the other side; a value holding a
// quote or a newline must be escaped rather than truncate the line.
func TestLabelValuesAreEscaped(t *testing.T) {
	r := New()
	c := r.NewCounter("demo_total", "Demo.", "note")
	c.Inc("say \"hi\"\nnext")
	var b strings.Builder
	if err := r.WriteText(&b); err != nil {
		t.Fatal(err)
	}
	line := ""
	for _, l := range strings.Split(b.String(), "\n") {
		if strings.HasPrefix(l, "demo_total{") {
			line = l
		}
	}
	if line == "" {
		t.Fatalf("no sample line emitted:\n%s", b.String())
	}
	if strings.Count(line, "\n") != 0 || !strings.Contains(line, `note="say \"hi\"\nnext"`) {
		t.Fatalf("label value not escaped, line is %q", line)
	}
}

// Registering the same family twice must return the same counter, not fork a
// second one: call sites in different packages declare the same metric.
func TestNewCounterIsIdempotentByName(t *testing.T) {
	r := New()
	a := r.NewCounter("demo_total", "Demo.", "stage")
	b := r.NewCounter("demo_total", "Different help.", "stage")
	if a != b {
		t.Fatal("the same name must resolve to the same counter")
	}
	a.Inc("x")
	if got := b.Value("x"); got != 1 {
		t.Fatalf("value = %d, want 1 — the two handles must share storage", got)
	}
}

// A label arity mismatch is a programming error, not a runtime condition: it
// must be caught where it is written rather than render a broken line.
func TestWrongLabelCountPanics(t *testing.T) {
	r := New()
	c := r.NewCounter("demo_total", "Demo.", "stage")
	defer func() {
		if recover() == nil {
			t.Fatal("Inc with the wrong number of label values must panic")
		}
	}()
	c.Inc("a", "b")
}
