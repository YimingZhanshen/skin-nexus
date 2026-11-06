package adminapi

// access_detail_read.go — ticket 5d, reading the diagnostic rows back.
//
// The panel already lists access_logs (summary lines). When a super admin opens
// the window, the useful next question is "what did this one request actually
// carry" — status, latency, the two whitelisted headers, the body excerpt, and
// whether a credential was captured. All of that comes from access_log_details,
// keyed by the summary ids the page already has.
//
// Two rules this file owns:
//
//  1. One page, one query. `ids` arrives as the visible page's primary keys and
//     is answered with a single store call (see store.AccessDetailRows). The
//     per-row variant would reproduce the N+1 G5 removed from the player list,
//     and it would be paid by every operator who opens the page and expands
//     nothing.
//  2. A credential is reported, never returned. store.AccessDetailRow carries
//     `credential_present` and no credential column at all, so this endpoint
//     cannot be turned into a token export. A station where "read the logs"
//     implies "hold every live session" has merged two different permissions —
//     the same mistake the diagnostic-window design refuses twice elsewhere
//     (the summary path never stores a credential, the detail path wipes one on
//     a fixed 24h clock whatever the retention knob says).

import (
	"context"
	"net/http"
	"strconv"
	"strings"

	"skin-nexus/server/internal/store"
)

// maxDetailIDs bounds one request's IN list. The panel pages at 50, so 200 is
// already four pages of slack; past that the query stops being "fill this page"
// and becomes a free way to make the database build an enormous key list.
const maxDetailIDs = 200

// detailFetchRows is the store seam (same shape as detailReadOption in
// access_detail_mode.go): Backend.Store is the concrete *store.Mysql, so
// without this indirection the "one call per page" promise could only be
// tested by instrumenting SQL.
var detailFetchRows = func(ctx context.Context, b *Backend, ids []int64) ([]store.AccessDetailRow, error) {
	return b.Store.AccessDetailRows(ctx, ids)
}

// parseDetailIDs keeps positive integers and drops everything else. A malformed
// id is the caller's bug, not a server error: answering 200 with the ids that
// could be used is honest, while a 500 out of a malformed IN list would be a
// new way to take the logs page down with a query string.
func parseDetailIDs(raw string) (ids []int64, asked int) {
	for _, part := range strings.Split(raw, ",") {
		n, err := strconv.ParseInt(strings.TrimSpace(part), 10, 64)
		if err != nil || n <= 0 {
			continue
		}
		ids = append(ids, n)
		asked++
	}
	return ids, asked
}

// GET /api/admin/v1/access-log/details?ids=1,2,3 — the page's detail rows.
func (b *Backend) handleAccessLogDetails(w http.ResponseWriter, r *http.Request) {
	ids, asked := parseDetailIDs(r.URL.Query().Get("ids"))
	limited := len(ids) > maxDetailIDs
	if limited {
		ids = ids[:maxDetailIDs]
	}
	// Rows is [] not nil: an empty JSON array means "nothing expanded", while
	// null is a frontend crash waiting for the first quiet night.
	out := map[string]any{"rows": []store.AccessDetailRow{}, "total": len(ids), "requested": asked, "limited": limited}
	if len(ids) > 0 {
		// Exactly one call for the whole page. access_detail_read_test.go
		// replaces detailFetchRows with a counting stub and fails the moment
		// this line turns into a loop over ids.
		rows, err := detailFetchRows(r.Context(), b, ids)
		if err != nil {
			writeInternal(r, w, err)
			return
		}
		if rows == nil {
			rows = []store.AccessDetailRow{}
		}
		out["rows"] = rows
	}
	// `limited` is what separates a clamp from a silent cut: the panel can then
	// say "showing the first 200 of N" instead of an operator concluding the
	// remaining requests simply had no detail rows.
	writeJSON(w, http.StatusOK, out)
}
