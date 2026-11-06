package adminapi

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"skin-nexus/server/internal/accesslog"
	"skin-nexus/server/internal/i18n"
)

// detailState is the stubbed store + clock the handlers reach through the
// package seams; the real Backend.Store stays nil (nothing here touches SQL).
type detailState struct {
	opts   map[string]string
	now    time.Time
	audits []detailAudit
	writes int
	reads  int
}

type detailAudit struct {
	actor  int64
	action string
	detail map[string]any
}

// stubDetail installs the fake clock and in-memory option/audit seams for one
// test, restoring the package defaults on cleanup.
func stubDetail(t *testing.T, now time.Time, seed map[string]string) *detailState {
	t.Helper()
	st := &detailState{now: now, opts: map[string]string{}}
	for k, v := range seed {
		st.opts[k] = v
	}
	or, ow, oa, on := detailReadOption, detailWriteOption, detailInsertAudit, detailNow
	detailReadOption = func(_ context.Context, _ *Backend, key string) (string, bool, error) {
		st.reads++
		v, ok := st.opts[key]
		return v, ok, nil
	}
	detailWriteOption = func(_ context.Context, _ *Backend, key, val string) error {
		st.opts[key] = val
		st.writes++
		return nil
	}
	detailInsertAudit = func(_ context.Context, _ *Backend, actor int64, action string, detail map[string]any) {
		st.audits = append(st.audits, detailAudit{actor, action, detail})
	}
	detailNow = func() time.Time { return st.now }
	t.Cleanup(func() {
		detailReadOption, detailWriteOption, detailInsertAudit, detailNow = or, ow, oa, on
	})
	return st
}

func doDetail(t *testing.T, b *Backend, method, path string, body any, adminToken string) *httptest.ResponseRecorder {
	t.Helper()
	var rdr *bytes.Reader
	if body == nil {
		rdr = bytes.NewReader(nil)
	} else {
		raw, err := json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
		rdr = bytes.NewReader(raw)
	}
	r := httptest.NewRequest(method, path, rdr)
	if adminToken != "" {
		r.Header.Set("X-Admin-Token", adminToken)
	}
	rec := httptest.NewRecorder()
	b.Handler().ServeHTTP(rec, r)
	return rec
}

func bodyCode(t *testing.T, rec *httptest.ResponseRecorder) string {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &m); err != nil {
		t.Fatalf("decode body %q: %v", rec.Body.String(), err)
	}
	c, _ := m["code"].(string)
	return c
}

var detailFakeNow = time.Date(2026, 11, 5, 12, 0, 0, 0, time.UTC)

// TestDetailModePostValidation is the table-driven core: who may write, and
// which distinct code each bad `until` earns.
func TestDetailModePostValidation(t *testing.T) {
	cases := []struct {
		name       string
		actor      int64 // session admin uid (0 = token/machine path)
		perm       int
		machineTok bool // drive the X-Admin-Token channel instead of a session
		until      string
		wantStatus int
		wantCode   string
		wantStored bool // option must be written verbatim (accepted cases)
	}{
		{"ordinary admin refused", 5, 1, false, detailFakeNow.Add(3 * time.Hour).Format(time.RFC3339),
			http.StatusForbidden, i18n.MsgSuperAdminRequired, false},
		{"token channel refused", 0, 2, true, detailFakeNow.Add(3 * time.Hour).Format(time.RFC3339),
			http.StatusForbidden, i18n.MsgWebSessionRequired, false},
		{"super admin +3h accepted", 5, 2, false, detailFakeNow.Add(3 * time.Hour).Format(time.RFC3339),
			http.StatusOK, "", true},
		{"until in the past", 5, 2, false, detailFakeNow.Add(-time.Hour).Format(time.RFC3339),
			http.StatusBadRequest, i18n.MsgDetailUntilPast, false},
		{"until exactly now (not future)", 5, 2, false, detailFakeNow.Format(time.RFC3339),
			http.StatusBadRequest, i18n.MsgDetailUntilPast, false},
		{"until unparseable", 5, 2, false, "not-a-timestamp",
			http.StatusBadRequest, i18n.MsgDetailUntilPast, false},
		{"until wall-clock no timezone", 5, 2, false, "2026-11-05T15:00:00",
			http.StatusBadRequest, i18n.MsgDetailUntilPast, false},
		{"until +25h beyond cap", 5, 2, false, detailFakeNow.Add(25 * time.Hour).Format(time.RFC3339),
			http.StatusBadRequest, i18n.MsgDetailUntilTooFar, false},
		// 24h boundary is INCLUSIVE — see the code comment in access_detail_mode.go.
		{"until +24h exactly accepted", 5, 2, false, detailFakeNow.Add(24 * time.Hour).Format(time.RFC3339),
			http.StatusOK, "", true},
		{"until +24h+1s rejected", 5, 2, false, detailFakeNow.Add(24*time.Hour + time.Second).Format(time.RFC3339),
			http.StatusBadRequest, i18n.MsgDetailUntilTooFar, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			st := stubDetail(t, detailFakeNow, nil)
			stubPerm(t, tc.perm)
			stubSession(t, func(*http.Request) (int64, bool) {
				if tc.actor == 0 {
					return 0, false
				}
				return tc.actor, true
			})
			b := &Backend{AdminTok: "sekret"}
			tok := ""
			if tc.machineTok {
				tok = "sekret"
			}
			rec := doDetail(t, b, http.MethodPost, "/api/admin/v1/access-log/detail-mode",
				map[string]string{"until": tc.until}, tok)
			if rec.Code != tc.wantStatus {
				t.Fatalf("status = %d, want %d (body %s)", rec.Code, tc.wantStatus, rec.Body.String())
			}
			if tc.wantCode != "" {
				if got := bodyCode(t, rec); got != tc.wantCode {
					t.Fatalf("code = %q, want %q", got, tc.wantCode)
				}
			}
			if tc.wantStatus != http.StatusOK {
				if st.writes != 0 || len(st.audits) != 0 {
					t.Fatalf("rejected write must not persist: writes=%d audits=%d", st.writes, len(st.audits))
				}
				return
			}
			// accepted: option written verbatim, exactly one audit row.
			wantUntil := tc.until
			if got := st.opts[accesslog.DetailUntilOption]; got != wantUntil {
				t.Fatalf("stored option = %q, want verbatim %q", got, wantUntil)
			}
			if len(st.audits) != 1 {
				t.Fatalf("audits = %d, want exactly 1", len(st.audits))
			}
			if st.audits[0].action != "access_detail_mode_enable" || st.audits[0].actor != tc.actor {
				t.Fatalf("audit = %+v, want enable by actor %d", st.audits[0], tc.actor)
			}
		})
	}
}

func TestDetailModeDeleteClearsAndAudits(t *testing.T) {
	st := stubDetail(t, detailFakeNow, map[string]string{
		accesslog.DetailUntilOption: detailFakeNow.Add(3 * time.Hour).Format(time.RFC3339),
	})
	stubPerm(t, 2)
	stubSession(t, func(*http.Request) (int64, bool) { return 7, true })
	b := &Backend{AdminTok: ""}
	rec := doDetail(t, b, http.MethodDelete, "/api/admin/v1/access-log/detail-mode", nil, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body %s", rec.Code, rec.Body.String())
	}
	if st.opts[accesslog.DetailUntilOption] != "" {
		t.Fatalf("option not cleared: %q", st.opts[accesslog.DetailUntilOption])
	}
	if len(st.audits) != 1 || st.audits[0].action != "access_detail_mode_disable" || st.audits[0].actor != 7 {
		t.Fatalf("audit = %+v, want one disable by actor 7", st.audits)
	}
	// delete refuses the token channel
	bTok := &Backend{AdminTok: "sekret"}
	recTok := doDetail(t, bTok, http.MethodDelete, "/api/admin/v1/access-log/detail-mode", nil, "sekret")
	if bodyCode(t, recTok) != i18n.MsgWebSessionRequired {
		t.Fatalf("token DELETE code = %q, want web-session-required", bodyCode(t, recTok))
	}
}

// TestDetailModeGetSecondsRemaining proves seconds_remaining comes from the
// injected clock, not time.Now(): a fixed option value yields a fixed remainder.
func TestDetailModeGetSecondsRemaining(t *testing.T) {
	until := detailFakeNow.Add(90 * time.Minute)
	stubDetail(t, detailFakeNow, map[string]string{accesslog.DetailUntilOption: until.Format(time.RFC3339)})
	stubSession(t, func(*http.Request) (int64, bool) { return 5, true })
	b := &Backend{}
	rec := doDetail(t, b, http.MethodGet, "/api/admin/v1/access-log/detail-mode", nil, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	var m map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &m); err != nil {
		t.Fatal(err)
	}
	if m["enabled"] != true {
		t.Fatalf("enabled = %v, want true", m["enabled"])
	}
	if got := int64(m["seconds_remaining"].(float64)); got != 5400 {
		t.Fatalf("seconds_remaining = %d, want 5400 (injected clock, not wall clock)", got)
	}
	if m["until"] != until.Format(time.RFC3339) {
		t.Fatalf("until = %v", m["until"])
	}
}

// TestDetailActiveUsesGoClock: advancing the injected clock past `until` flips
// DiagnosticActive to false WITHOUT any SQL re-evaluation — the option row is
// read again as the SAME value; only the Go clock moved. A now()-in-SQL design
// could not flip without touching the DB.
func TestDetailActiveUsesGoClock(t *testing.T) {
	until := detailFakeNow.Add(time.Hour)
	raw := until.Format(time.RFC3339)
	st := stubDetail(t, detailFakeNow, map[string]string{accesslog.DetailUntilOption: raw})
	b := &Backend{}

	d := b.diagnostic()
	if !d.DiagnosticActive(context.Background()) {
		t.Fatal("expected active while the clock is before until")
	}

	// Advance the Go clock past until; the stored option is unchanged.
	st.now = until.Add(time.Second)
	if d.DiagnosticActive(context.Background()) {
		t.Fatal("expected inactive after the Go clock passes until")
	}
	// The verdict flipped purely on clock position: the option value the reader
	// returned never moved, and no time comparison reached SQL.
	if v := st.opts[accesslog.DetailUntilOption]; v != raw {
		t.Fatalf("stored value drifted to %q — the flip must come from the clock", v)
	}
}
