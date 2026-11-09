package union

// Same layering defect as /api/yggdrasil, second half of one hub request.
//
// The MUA hub's authorize page reads a member's display name and then its
// capability list in a single cross-origin chain (authorize:82-89):
//
//	fetch(bs_root + "/api/yggdrasil")  -> meta.serverName
//	fetch(bs_root + "/api/union/member") -> enabledFeatures.includes("unionOAuth2")
//
// Fixing only the first would still leave the button disabled, because the
// second fetch dies the same way: the route table registered
// "GET /api/union/member/{$}" alone, so Go's ServeMux answered the no-slash
// form with a bare 307 that carries no Access-Control-Allow-Origin, and the
// browser rejected the whole request. The hub's console said exactly that:
//
//	Access to fetch at 'https://…/api/union/member' ... blocked by CORS policy:
//	No 'Access-Control-Allow-Origin' header is present on the requested resource.
//
// The reference behaviour comes from sibling stations in the same list: nine
// of the ten we probed answer 200 with a single ACAO at the no-slash URL, and
// the tenth answers 200 with ACAO twice and fails for that reason instead.

import (
	"bytes"
	"net/http"
	"testing"
)

func TestCanonicalNoSlashSelfDescription(t *testing.T) {
	f := newInboundFixture(t, func(cfg *Config, hooks *Hooks) {
		cfg.YggdrasilApiVersion = "1.2.3"
		cfg.UnionOAuth2 = true
	}, nil)

	slashed := f.call(t, http.MethodGet, "/api/union/member/", "")
	bare := f.call(t, http.MethodGet, "/api/union/member", "")

	if bare.Code != http.StatusOK {
		t.Fatalf("GET /api/union/member = %d (Location %q): the no-slash form must serve the "+
			"capability document, or the hub cannot see unionOAuth2 and keeps the button disabled",
			bare.Code, bare.Header().Get("Location"))
	}
	if got := bare.Header().Values("Access-Control-Allow-Origin"); len(got) != 1 || got[0] != "*" {
		t.Fatalf("ACAO values = %q, want exactly [\"*\"] — a duplicated header fails CORS too", got)
	}
	// One document, two spellings of its URL: the hub must not be able to tell
	// which form it reached.
	if !bytes.Equal(bare.Body.Bytes(), slashed.Body.Bytes()) {
		t.Fatalf("no-slash body drifted from the trailing-slash form:\n bare: %s\nslash: %s",
			bare.Body.String(), slashed.Body.String())
	}
}

// The canonical route is one exact path, not a prefix: the OAuth2 sub-surfaces
// keep their own registration and the capability document must not leak onto
// paths that were never theirs.
func TestCanonicalNoSlashSelfDescriptionDoesNotWiden(t *testing.T) {
	f := newInboundFixture(t, nil, nil)

	rec := f.call(t, http.MethodGet, "/api/union/member/oauth2", "")
	if rec.Code == http.StatusOK && bytes.Contains(rec.Body.Bytes(), []byte("enabledFeatures")) {
		t.Fatalf("GET /api/union/member/oauth2 served the self-description (%d): the exact "+
			"route widened into a prefix", rec.Code)
	}
	if rec := f.call(t, http.MethodGet, "/api/union/member/nope", ""); rec.Code == http.StatusOK {
		t.Fatal("GET /api/union/member/nope = 200, swallowed by the canonical route")
	}
}
