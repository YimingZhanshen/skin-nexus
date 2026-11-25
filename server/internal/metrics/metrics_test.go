package metrics

// metrics_test.go — the exposition is the product here: a counter nobody can
// scrape correctly is worse than no counter, because it looks like monitoring.
//
// The format is Prometheus text exposition, written by hand (this repository
// takes no new dependencies): a HELP line and a TYPE line per family, then
// one sample per label set, values sorted so counter samples from two scrapes
// of an idle process are byte-identical. The uptime gauge intentionally grows.

import (
	"fmt"
	"io"
	"strings"
	"sync"
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

// Counter samples from two scrapes of an untouched process must be
// byte-identical. Uptime is excluded because it is a deliberately live gauge.
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
		lines := strings.Split(b.String(), "\n")
		out := lines[:0]
		for _, line := range lines {
			if !strings.HasPrefix(line, "process_uptime_seconds ") {
				out = append(out, line)
			}
		}
		return strings.Join(out, "\n")
	}
	first, second := render(), render()
	if first != second {
		t.Fatalf("two scrapes of the same registry differ:\n%s\n---\n%s", first, second)
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

// A live metrics endpoint must tolerate registration, increments and scrapes at
// the same time. Reverting WriteText to rendering live counter pointers makes
// the race detector report concurrent map/slice access here.
func TestConcurrentRegistrationIncrementAndScrape(t *testing.T) {
	const (
		workers    = 8
		iterations = 250
	)
	r := New()
	shared := r.NewCounter("shared_total", "Shared counter.", "stage")
	start := make(chan struct{})
	errs := make(chan error, workers)
	var wg sync.WaitGroup

	for worker := 0; worker < workers; worker++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			own := r.NewCounter(fmt.Sprintf("worker_%d_total", worker), "Worker counter.", "stage")
			for i := 0; i < iterations; i++ {
				own.Inc("own")
				shared.Inc("shared")
				if err := r.WriteText(io.Discard); err != nil {
					errs <- err
					return
				}
			}
		}()
	}
	close(start)
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Errorf("concurrent scrape failed: %v", err)
	}

	if got, want := shared.Value("shared"), uint64(workers*iterations); got != want {
		t.Fatalf("shared count = %d, want %d", got, want)
	}
	var b strings.Builder
	if err := r.WriteText(&b); err != nil {
		t.Fatal(err)
	}
	out := b.String()
	for worker := 0; worker < workers; worker++ {
		name := fmt.Sprintf("worker_%d_total", worker)
		if got := r.NewCounter(name, "ignored", "stage").Value("own"); got != iterations {
			t.Errorf("%s count = %d, want %d", name, got, iterations)
		}
		want := fmt.Sprintf("%s{stage=\"own\"} %d\n", name, iterations)
		if !strings.Contains(out, want) {
			t.Errorf("final exposition missing %q", want)
		}
	}
}
