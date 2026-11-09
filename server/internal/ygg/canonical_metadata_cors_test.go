package ygg

// The canonical authlib-injector URL is the one we advertise in
// X-Authlib-Injector-API-Location: /api/yggdrasil, with no trailing slash.
// The MUA hub resolves each member station's display name by fetching exactly
// that URL from the visitor's browser (authorize page: fetch(bs_root +
// "/api/yggdrasil") -> meta.serverName), so the surface has to answer a
// cross-origin GET there.
//
// It did not. Only the "/api/yggdrasil/{$}" form was registered, and Go's
// ServeMux answers a bare 307 for the no-slash form itself — a redirect it
// writes before any handler runs, so the CORS headers handleMetadata sets never
// reach it, and the browser fails the whole fetch on the redirect response:
//
//	Access to fetch at 'https://…/api/yggdrasil' from origin
//	'https://skin.mualliance.ltd' has been blocked by CORS policy:
//	No 'Access-Control-Allow-Origin' header is present on the requested resource.
//
// The hub then falls into its catch branch and renders "✕ <CODE>" with the
// button disabled, and the member list shows an empty name cell. Ten sibling
// stations were probed for the reference behaviour: nine answer 200 with a
// single ACAO at the no-slash path; the tenth (ECNU) answers 200 but with ACAO
// twice and fails in a different way. Both halves of that are pinned below.

import (
	"bytes"
	"net/http"
	"testing"
)

func TestCanonicalNoSlashMetadataServesCORS(t *testing.T) {
	api, _ := newTestAPI(t)
	h := api.Handler()

	slashed := do(t, h, http.MethodGet, "/api/yggdrasil/", "", nil)
	bare := do(t, h, http.MethodGet, "/api/yggdrasil", "", nil)

	if bare.Code != http.StatusOK {
		t.Fatalf("GET /api/yggdrasil = %d (Location %q); the canonical URL must serve the "+
			"metadata itself — a 307 carries no CORS headers and the hub's cross-origin "+
			"name lookup dies on it", bare.Code, bare.Header().Get("Location"))
	}

	// Exactly one ACAO: a duplicated header is as fatal as a missing one
	// ("multiple values '*, *'"), which is how ECNU fails today.
	if got := bare.Header().Values("Access-Control-Allow-Origin"); len(got) != 1 || got[0] != "*" {
		t.Fatalf("ACAO values = %q, want exactly [\"*\"]", got)
	}

	// Same bytes, same ALI header: the two forms are one surface, not two.
	if !bytes.Equal(bare.Body.Bytes(), slashed.Body.Bytes()) {
		t.Fatalf("no-slash body drifted from the trailing-slash form:\n bare: %s\nslash: %s",
			bare.Body.String(), slashed.Body.String())
	}
	if a, b := bare.Header().Get("X-Authlib-Injector-API-Location"),
		slashed.Header().Get("X-Authlib-Injector-API-Location"); a != b {
		t.Fatalf("ALI header = %q, want the same as the slashed form %q", a, b)
	}
}

// The fix is one GET route, not a rewrite of the prefix: nothing else may start
// answering on this path. The invariant is "the metadata handler never serves a
// non-GET, and never serves a deeper path" — not a particular status, because
// the mux's own answer for a method mismatch (405 here) is not ours to pin.
func TestCanonicalNoSlashDoesNotWidenSurface(t *testing.T) {
	api, _ := newTestAPI(t)
	h := api.Handler()

	for _, method := range []string{http.MethodPost, http.MethodPut, http.MethodDelete} {
		rec := do(t, h, method, "/api/yggdrasil", `{"x":1}`, nil)
		if rec.Code == http.StatusOK {
			t.Fatalf("%s /api/yggdrasil = 200: the canonical route widened beyond GET", method)
		}
		if bytes.Contains(rec.Body.Bytes(), []byte("signaturePublickey")) {
			t.Fatalf("%s /api/yggdrasil served metadata anyway (body %s)", method, rec.Body.String())
		}
	}
	if rec := do(t, h, http.MethodGet, "/api/yggdrasil/nope", "", nil); rec.Code == http.StatusOK {
		t.Fatalf("GET /api/yggdrasil/nope = 200, swallowed by the canonical route: %s", rec.Body.String())
	}
	// HEAD is a GET without a body, and the CORS header list we advertise says
	// "GET, HEAD" — so it must keep working on the canonical URL too.
	if rec := do(t, h, http.MethodHead, "/api/yggdrasil", "", nil); rec.Code != http.StatusOK {
		t.Fatalf("HEAD /api/yggdrasil = %d, want 200 (advertised in Allow-Methods)", rec.Code)
	}
}
