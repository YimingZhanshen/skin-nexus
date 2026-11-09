package main

import "net/http"

// mountCompatSurfaces registers on the assembled root mux the legacy surface
// that callers outside this site reach.
//
// The yggdrasil prefix needs both spellings, and the second one is not
// decoration. Go's ServeMux matches a "/x/" subtree only against paths that
// carry the trailing slash, and inside the prefix a pattern ending in "{$}"
// answers the bare "/x" with a 307 that the mux writes itself — no handler
// runs, so the CORS headers handleMetadata sets never reach that response. The
// MUA hub resolves each member's display name and capability list from the
// visitor's browser, at exactly the canonical no-slash URL, and a redirect with
// no Access-Control-Allow-Origin fails the whole fetch there. Live on
// 2026-11-09: the hub showed "✕ WZBC" with a disabled button and an empty name
// cell, while nine of the ten sibling stations probed answered 200 with a
// single ACAO at the same URL.
//
// The union subtree needs no second registration here: "/api/union/" does
// match /api/union/member and passes it down, where the route table answers it
// (internal/union/inbound.go). Both halves are pinned by tests at their own
// layer, because the defect lived in the gap between them.
func mountCompatSurfaces(root *http.ServeMux, ygg http.Handler) {
	root.Handle("/api/yggdrasil/", ygg)
	root.Handle("GET /api/yggdrasil", ygg)
}
