package main

// "Site-wide security headers" is only true of the handler that actually sits
// inside the wrapper. CustomSkinAPI answers `GET /{player}.json` at the root
// mux, because ServeMux cannot express that pattern beside the "/" SPA catch-all
// — so whichever way the two wrappers nest decides whether launchers' JSON
// carries the baseline headers at all. This reads the wiring instead of a
// synthetic handler, which is the only way to catch the order being flipped.

import (
	"os"
	"regexp"
	"strings"
	"testing"
)

func TestSecurityHeadersWrapTheWholeRootHandler(t *testing.T) {
	src, err := os.ReadFile("main.go")
	if err != nil {
		t.Fatalf("read main.go: %v", err)
	}
	line := ""
	for _, l := range strings.Split(string(src), "\n") {
		if strings.Contains(l, "newHTTPServer(") && strings.Contains(l, "WrapRoot") {
			line = strings.TrimSpace(l)
			break
		}
	}
	if line == "" {
		t.Fatal(`no newHTTPServer(...) wrapping pub.WrapRoot found in main.go; update this gate to wherever the root handler is composed`)
	}
	args := line[strings.Index(line, "newHTTPServer(")+len("newHTTPServer("):]
	args = strings.TrimRight(args, ")")
	// Drop the addr argument: everything after the first comma is the handler.
	if i := strings.Index(args, ","); i >= 0 {
		args = strings.TrimSpace(args[i+1:])
	}
	if !strings.HasPrefix(args, "securityHeaders(") {
		t.Errorf("the outermost wrapper is %q; the CustomSkinAPI routes answered inside pub.WrapRoot then get no security headers at all",
			args)
	}
	if matched, _ := regexp.MatchString(`WrapRoot\(securityHeaders`, line); matched {
		t.Errorf("pub.WrapRoot still wraps the security-header handler, so its own routes skip the headers: %s", line)
	}
}
