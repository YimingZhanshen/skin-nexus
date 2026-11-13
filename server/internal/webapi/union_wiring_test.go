package webapi

// union_wiring_test.go — the panel's server list must be folded before it
// reaches the browser.
//
// The Vue row key on that page is code + '|' + bs_root, so two spellings of
// one station (host case, trailing slash, credentials left in) are two rows of
// the same station and duplicate keys once they render. The fold lives in
// union.CanonicalizeServers; nothing but this handler serves that endpoint, so
// a source-level assertion is the cheapest honest guard against the call being
// dropped during a refactor — there is no Backend harness in this package to
// drive the handler end to end.

import (
	"os"
	"strings"
	"testing"
)

func TestUnionServersHandlerCanonicalisesRows(t *testing.T) {
	src, err := os.ReadFile("union_user.go")
	if err != nil {
		t.Fatal(err)
	}
	body := string(src)
	start := strings.Index(body, "func (b *Backend) handleUnionServers")
	if start < 0 {
		t.Fatal("handleUnionServers not found in union_user.go")
	}
	rest := body[start:]
	end := strings.Index(rest[1:], "\nfunc ")
	if end >= 0 {
		rest = rest[:end+1]
	}
	if !strings.Contains(rest, "union.CanonicalizeServers(b.memberNames.Fill(") {
		t.Fatalf("handleUnionServers must fold the hub rows through union.CanonicalizeServers before writing them, got:\n%s", rest)
	}
}
