package plugin

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
)

// examplesDir points at the repo's shipped example plugins so a refactor can
// never silently break the documented samples. The full plugin set lives in
// the skin-nexus-plugins repository; the host repo keeps two minimal fixtures.
func examplesDir() string {
	return filepath.Join("..", "..", "..", "examples", "plugins")
}

func TestShippedExamplesLoadAndRun(t *testing.T) {
	// HostVersion is the host the examples ship against; the binary's own
	// version. A stale value here silently rejects any example that declares a
	// minServer, because the rejection looks like a bug in the example.
	m := NewHost(RuntimeConfig{Dir: examplesDir(), HostVersion: "0.5.0", Store: newMemPlatformStore()})
	t.Cleanup(func() { _ = m.Close() })
	if err := m.Load(examplesDir()); err != nil {
		t.Fatal(err)
	}
	list := m.List()
	if len(list) != 3 {
		t.Fatalf("examples = %d, want 3 (%+v)", len(list), list)
	}
	for _, st := range list {
		if st.State != "disabled" {
			t.Fatalf("%s state = %s, want disabled by default (%s)", st.Name, st.State, st.LastError)
		}
	}
	ctx := context.Background()
	for _, name := range []string{"hello-world", "hello-ui", "jobs-fixture"} {
		if err := m.Enable(ctx, name); err != nil {
			t.Fatalf("enable %s: %v", name, err)
		}
	}
	if ui := m.UI(); len(ui) != 1 || ui[0].Plugin != "hello-ui" {
		t.Fatalf("example UI = %+v", ui)
	}
	if !m.HasUIPath("ext/hello-ui/hello") || m.HasUIPath("ext/hello-ui/nope") {
		t.Fatal("HasUIPath mismatch")
	}
	if html := m.SlotHTML("app.topbar.after"); !strings.Contains(html, "/plugin-assets/hello-ui/hello.html") {
		t.Fatalf("slot html = %s", html)
	}
	for _, name := range []string{"hello-world", "hello-ui", "jobs-fixture"} {
		if err := m.Disable(ctx, name); err != nil {
			t.Fatalf("disable %s: %v", name, err)
		}
	}
}
