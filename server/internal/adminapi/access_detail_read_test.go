package adminapi

// access_detail_read_test.go — ticket 5d, the read half of diagnostic mode.
//
// Three promises, each of which has a way to fail quietly:
//
//  1. One page of ids costs ONE store call. The N+1 shape was removed from the
//     player list in G5 for the same reason: a log page an operator merely
//     looks at must not pay a query per row. This is asserted with a counting
//     stub, because Backend.Store is the concrete *store.Mysql and the query
//     count is invisible from outside the seam.
//  2. The reader never hands back a credential. store.AccessDetailRow does not
//     even carry the column — the reader answers `credential_present` — so this
//     test pins the wire shape, not just the intent. A panel that can read logs
//     must not become a token export.
//  3. The route is behind b.auth like every other admin read. Asserted against
//     the registration source (the shape sitemap_limit_test.go uses), since a
//     handler that tests green can still be mounted on the wrong mux entry.

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"regexp"
	"strings"
	"testing"
	"time"

	"skin-nexus/server/internal/store"
)

type detailReadStub struct {
	calls int
	ids   []int64
	rows  []store.AccessDetailRow
}

func (s *detailReadStub) install(t *testing.T) {
	t.Helper()
	orig := detailFetchRows
	detailFetchRows = func(_ context.Context, _ *Backend, ids []int64) ([]store.AccessDetailRow, error) {
		s.calls++
		s.ids = append([]int64(nil), ids...)
		return s.rows, nil
	}
	t.Cleanup(func() { detailFetchRows = orig })
}

func detailsBackend() *Backend { return &Backend{} }

func getDetails(t *testing.T, b *Backend, query string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/api/admin/v1/access-log/details"+query, nil)
	rec := httptest.NewRecorder()
	b.handleAccessLogDetails(rec, req)
	return rec
}

func TestAccessLogDetailsOneStoreCallPerPage(t *testing.T) {
	stub := &detailReadStub{rows: []store.AccessDetailRow{
		{ID: 30, StatusCode: 403, DurationMS: 4, CapturedAt: time.Unix(0, 0).UTC()},
		{ID: 29, StatusCode: 200, DurationMS: 17, CapturedAt: time.Unix(0, 0).UTC()},
	}}
	stub.install(t)
	b := detailsBackend()

	rec := getDetails(t, b, "?ids=30,29,28")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body.String())
	}
	if stub.calls != 1 {
		t.Fatalf("a page of 3 ids cost %d store calls, want exactly 1 (N+1 regrew)", stub.calls)
	}
	if len(stub.ids) != 3 {
		t.Fatalf("store was asked for %d ids (%v), want the 3 from the page", len(stub.ids), stub.ids)
	}
	if got := stub.ids[0]; got != 30 {
		t.Errorf("id order lost: first id = %d, want 30", got)
	}

	var out struct {
		Rows []struct {
			ID         int64  `json:"id"`
			StatusCode int    `json:"status_code"`
			UA         string `json:"upstream_code"`
		} `json:"rows"`
		Total int `json:"total"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode %s: %v", rec.Body.String(), err)
	}
	if len(out.Rows) != 2 || out.Rows[0].ID != 30 || out.Rows[0].StatusCode != 403 {
		t.Fatalf("rows wrong: %s", rec.Body.String())
	}
	if out.Total != 3 {
		t.Errorf("total = %d, want 3 (the page asked for three ids)", out.Total)
	}
}

func TestAccessLogDetailsNeverReturnsACredential(t *testing.T) {
	stub := &detailReadStub{rows: []store.AccessDetailRow{{
		ID: 7, StatusCode: 502, UpstreamCode: "hub.502",
		Headers: `{"User-Agent":"launcher/3.5"}`, Body: `{"error":"upstream blew up"}`,
		CredentialSet: true, CapturedAt: time.Unix(0, 0).UTC(),
	}}}
	stub.install(t)
	rec := getDetails(t, detailsBackend(), "?ids=7")
	body := rec.Body.String()

	// The flag is the only credential-shaped thing the reader may say.
	if !strings.Contains(body, `"credential_present":true`) {
		t.Errorf("response lost credential_present: %s", body)
	}
	for _, forbidden := range []string{"credential_ref", "access_token", "accessToken", "skin_session"} {
		if strings.Contains(body, forbidden) {
			t.Errorf("detail response mentions %q — the log reader must not become a credential export: %s", forbidden, body)
		}
	}
}

func TestAccessLogDetailsClampsAHugeIdList(t *testing.T) {
	var many []string
	for i := 1; i <= 400; i++ {
		many = append(many, fmt.Sprint(i))
	}
	stub := &detailReadStub{}
	stub.install(t)
	rec := getDetails(t, detailsBackend(), "?ids="+strings.Join(many, ","))
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body.String())
	}
	if stub.calls != 1 {
		t.Fatalf("400 ids cost %d calls, want 1", stub.calls)
	}
	if len(stub.ids) != maxDetailIDs {
		t.Fatalf("queried %d ids, want the cap %d — an unbounded IN list is a memory and planner problem", len(stub.ids), maxDetailIDs)
	}
	var out struct {
		Requested int  `json:"requested"`
		Limited   bool `json:"limited"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &out)
	if out.Requested != 400 || !out.Limited {
		t.Errorf("the truncation is invisible to the caller (requested=%d limited=%v) — saying so is the difference between a clamp and a silent cut", out.Requested, out.Limited)
	}
}

func TestAccessLogDetailsGarbageIdsAreNotAFiveHundred(t *testing.T) {
	stub := &detailReadStub{}
	stub.install(t)
	for _, q := range []string{"?ids=", "?ids=abc", "?ids=abc,,9x", "?ids=-3;DROP+TABLE"} {
		rec := getDetails(t, detailsBackend(), q)
		if rec.Code != http.StatusOK {
			t.Errorf("%s: status %d, want 200 with empty rows: %s", q, rec.Code, rec.Body.String())
		}
		if stub.calls > 1 {
			t.Errorf("%s: %d store calls", q, stub.calls)
		}
		for _, id := range stub.ids {
			if id <= 0 {
				t.Errorf("%s: let a non-positive id through to SQL: %d", q, id)
			}
		}
	}
}

func TestAccessLogDetailsRouteIsMountedBehindAuth(t *testing.T) {
	raw, err := os.ReadFile("adminapi.go")
	if err != nil {
		t.Fatalf("read adminapi.go: %v", err)
	}
	re := regexp.MustCompile(`(?m)^.*access-log/details.*$`)
	line := re.FindString(string(raw))
	if line == "" {
		t.Fatal("the detail route is not registered — the handler exists but nothing reaches it")
	}
	if !strings.Contains(line, "b.auth(") {
		t.Errorf("detail route mounted without the admin auth wrapper: %s", line)
	}
}
