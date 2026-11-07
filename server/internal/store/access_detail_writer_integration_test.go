//go:build integration

// access_detail_writer_integration_test.go — tickets 5c and 5e against a real
// database (all three engines, one DSN at a time).
//
// These are the end-to-end legs of the diagnostic writer: the decision lives in
// internal/accesslog (detail_test.go), but the two promises only a database can
// keep are here —
//   - the detail row's id IS the summary row's id (this table has no
//     autoincrement, so a wrong carry shows up as a broken 1:1),
//   - and the two clocks are different clocks: captured_at follows the panel's
//     retention option, credentials_until follows its own fixed 24h.
//
// Every timestamp comes from the injected store clock (m.now) — never SQL
// now() — so "this row is 8 days old" means the application says so, whatever
// the DB session timezone is set to.

package store

import (
	"context"
	"database/sql"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"skin-nexus/server/internal/accesslog"
)

const detSecret = "b3d1f0a7-0000-4000-8000-aaaaaaaaaaaa"

// detBase is the frozen "now" every detail test writes from.
var detBase = time.Date(2026, 11, 5, 12, 0, 0, 0, time.UTC)

func detClock(at time.Time) func() time.Time { return func() time.Time { return at } }

// detClockAt re-points the store clock (it is what stamps captured_at).
func detClockAt(m *Mysql, at time.Time) { m.now = detClock(at) }

// detOptions is a mutable option map: changing a value between two calls is how
// the retention test proves the option is read live, not baked in at boot.
func detOptions(vals map[string]string) accesslog.OptionReader {
	return func(_ context.Context, key string) (string, bool, error) {
		v, ok := vals[key]
		return v, ok, nil
	}
}

// detRow is one access_log_details row as the writer and the sweepers see it.
type detRow struct {
	id       int64
	status   int
	duration int64
	upstream string
	headers  string
	body     string
	cred     string
	until    sql.NullTime
	captured time.Time
}

const detColumns = `SELECT id, status_code, duration_ms, upstream_code,
	COALESCE(headers_json,''), COALESCE(body_excerpt,''), COALESCE(credential_ref,''),
	credentials_until, captured_at FROM access_log_details WHERE `

func detSelect(ctx context.Context, m *Mysql, where string, args ...any) ([]detRow, error) {
	rows, err := m.db.QueryContext(ctx, detColumns+where, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []detRow
	for rows.Next() {
		var r detRow
		if err := rows.Scan(&r.id, &r.status, &r.duration, &r.upstream, &r.headers,
			&r.body, &r.cred, &r.until, &r.captured); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// detOneErr fetches one detail row by id, erroring when the 1:1 shape is broken.
func detOneErr(m *Mysql, id int64) (detRow, error) {
	out, err := detSelect(context.Background(), m, "id = ?", id)
	if err != nil {
		return detRow{}, err
	}
	if len(out) != 1 {
		return detRow{}, errors.New("want exactly 1 detail row for id " +
			strconv.FormatInt(id, 10) + ", got " + strconv.Itoa(len(out)))
	}
	return out[0], nil
}

func detOne(t *testing.T, m *Mysql, id int64) detRow {
	t.Helper()
	r, err := detOneErr(m, id)
	if err != nil {
		t.Fatalf("detail row %d: %v", id, err)
	}
	return r
}

func detCountTable(t *testing.T, m *Mysql, table string) int {
	t.Helper()
	var n int
	// `table` is a literal at every call site below, never request input.
	if err := m.db.QueryRowContext(context.Background(), `SELECT COUNT(*) FROM `+table).Scan(&n); err != nil {
		t.Fatalf("count %s: %v", table, err)
	}
	return n
}

func detSummaryID(t *testing.T, m *Mysql) int64 {
	t.Helper()
	var id int64
	if err := m.db.QueryRowContext(context.Background(), `SELECT COALESCE(MAX(id),0) FROM access_logs`).Scan(&id); err != nil {
		t.Fatalf("summary id: %v", err)
	}
	return id
}

// detCapture is a dirty capture on purpose: it carries the headers the whitelist
// must reject, plus a credential.
func detCapture() *accesslog.Capture {
	h := http.Header{}
	h.Set("User-Agent", "launcher/3.5")
	h.Set("X-Request-Id", "req-42")
	h.Set("Cookie", "skin_session="+detSecret)
	h.Set("Authorization", "Bearer "+detSecret)
	h.Set("X-Union-Member-Key", "hunter2")
	return &accesslog.Capture{
		StatusCode:   502,
		DurationMS:   17,
		UpstreamCode: "hub.timeout",
		Headers:      h,
		Body:         `{"error":"ForbiddenOperationException"}`,
		Credential:   detSecret,
	}
}

// detLogger wires the production shape: the summary sink on the store plus the
// diagnostic switch over the option map.
func detLogger(m *Mysql, vals map[string]string) *accesslog.Logger {
	l := accesslog.New(m.InsertAccessLog)
	l.SetDiagnostic(&accesslog.Diagnostic{Option: detOptions(vals), Now: detClock(detBase)})
	return l
}

// detWindow opens the diagnostic window (the option value is exactly what
// adminapi writes: a UTC RFC3339 expiry).
func detWindow(vals map[string]string) {
	vals[accesslog.DetailUntilOption] = detBase.Add(30 * time.Minute).Format(time.RFC3339)
}

// (a) window CLOSED — no detail row at all, and no credential stored anywhere.
func TestIntegrationDetailWindowClosedWritesNoRow(t *testing.T) {
	m := openTestStore(t)
	ctx := context.Background()
	vals := map[string]string{} // no access_detail_until = closed
	detClockAt(m, detBase)

	detLogger(m, vals).Log(ctx, accesslog.Entry{Kind: accesslog.WebLogin,
		Outcome: accesslog.OutcomeOK, UA: "rig/1", Capture: detCapture()})

	if n := detCountTable(t, m, "access_logs"); n != 1 {
		t.Fatalf("summary rows = %d, want 1 (the always-on surface is unchanged)", n)
	}
	if n := detCountTable(t, m, "access_log_details"); n != 0 {
		t.Errorf("detail rows = %d with the window closed, want 0", n)
	}
	rows, err := detSelect(ctx, m, "COALESCE(credential_ref,'') <> ''")
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 0 {
		t.Errorf("credential_ref is populated somewhere with the window closed: %+v", rows)
	}
}

// (b) window OPEN — exactly one detail row, and its id is the summary row's id.
func TestIntegrationDetailWindowOpenCarriesTheSummaryID(t *testing.T) {
	m := openTestStore(t)
	ctx := context.Background()
	vals := map[string]string{}
	detWindow(vals)
	detClockAt(m, detBase)

	detLogger(m, vals).Log(ctx, accesslog.Entry{Kind: accesslog.YggRefresh,
		Outcome: accesslog.OutcomeOK, UA: "rig/1", Capture: detCapture()})

	summary := detSummaryID(t, m)
	if summary == 0 {
		t.Fatal("no summary row written")
	}
	if n := detCountTable(t, m, "access_log_details"); n != 1 {
		t.Fatalf("detail rows = %d, want exactly 1", n)
	}
	// The lookup is keyed BY the summary id: that is the 1:1 assertion.
	r := detOne(t, m, summary)
	if !r.captured.UTC().Equal(detBase) {
		t.Errorf("captured_at = %v, want the injected Go clock %v", r.captured.UTC(), detBase)
	}
	if r.status != 502 || r.duration != 17 {
		t.Errorf("status/duration = %d/%d, want 502/17", r.status, r.duration)
	}
	if r.upstream != "hub.timeout" {
		t.Errorf("upstream_code = %q, want hub.timeout", r.upstream)
	}
}

// (c) non-whitelisted headers never reach headers_json — asserted at the store
// gate itself, so a caller that bypassed Logger.Log still cannot write a Cookie.
func TestIntegrationDetailHeaderWhitelistAtTheStore(t *testing.T) {
	m := openTestStore(t)
	ctx := context.Background()
	detClockAt(m, detBase)

	if err := m.InsertAccessDetail(ctx, 770001, detCapture()); err != nil {
		t.Fatalf("insert: %v", err)
	}
	r := detOne(t, m, 770001)
	for _, ban := range []string{"Cookie", "Authorization", "X-Union-Member-Key", "hunter2", detSecret} {
		if strings.Contains(r.headers, ban) {
			t.Errorf("headers_json carries %q: %s", ban, r.headers)
		}
	}
	if !strings.Contains(r.headers, "launcher/3.5") {
		t.Errorf("headers_json lost the whitelisted User-Agent: %s", r.headers)
	}
	if !strings.Contains(r.headers, "req-42") {
		t.Errorf("headers_json lost X-Request-Id: %s", r.headers)
	}
}

// (d) a captured credential always arrives with its own short deadline —
// credentials_until = captured_at + 24h, even though the retention option says
// 7 days. Making the credential clock follow the retention option fails here.
func TestIntegrationDetailCredentialClockIsFixed(t *testing.T) {
	m := openTestStore(t)
	ctx := context.Background()
	vals := map[string]string{accesslog.DetailRetentionOption: "7"}
	detWindow(vals)
	detClockAt(m, detBase)

	detLogger(m, vals).Log(ctx, accesslog.Entry{Kind: accesslog.YggAuthenticate,
		Outcome: accesslog.OutcomeOK, Capture: detCapture()})

	id := detSummaryID(t, m)
	r := detOne(t, m, id)
	if r.cred != detSecret {
		t.Fatalf("credential_ref = %q, want the plaintext token while the window is open", r.cred)
	}
	if !r.until.Valid {
		t.Fatal("credentials_until is NULL although a credential was captured")
	}
	// 24h written as a literal, NOT as accesslog.CredentialTTL: a mutation that
	// makes the credential deadline follow the retention option (7 days here)
	// has to fail this test, and reading the constant back would let it pass.
	want := r.captured.UTC().Add(24 * time.Hour)
	if !r.until.Time.UTC().Equal(want) {
		t.Errorf("credentials_until = %v, want captured_at+24h = %v (the 7d retention option must not govern it)",
			r.until.Time.UTC(), want)
	}

	// No credential ⇒ no deadline: the two columns are a pair, never half one.
	detClockAt(m, detBase.Add(time.Minute))
	if err := m.InsertAccessDetail(ctx, 770002, &accesslog.Capture{StatusCode: 200}); err != nil {
		t.Fatalf("insert without credential: %v", err)
	}
	if r2 := detOne(t, m, 770002); r2.cred != "" || r2.until.Valid {
		t.Errorf("credential columns set without a credential: %+v", r2)
	}
}

// (e) an oversized body is stored capped, not whole.
func TestIntegrationDetailBodyExcerptIsCapped(t *testing.T) {
	m := openTestStore(t)
	ctx := context.Background()
	detClockAt(m, detBase)

	long := strings.Repeat("x", accesslog.MaxBodyExcerpt*2+13)
	if err := m.InsertAccessDetail(ctx, 770003, &accesslog.Capture{Body: long}); err != nil {
		t.Fatalf("insert: %v", err)
	}
	r := detOne(t, m, 770003)
	if len(r.body) != accesslog.MaxBodyExcerpt {
		t.Fatalf("body_excerpt = %d bytes, want the cap %d (source was %d)",
			len(r.body), accesslog.MaxBodyExcerpt, len(long))
	}
	if r.body != long[:accesslog.MaxBodyExcerpt] {
		t.Error("body_excerpt is not the leading capped bytes of the source body")
	}
}

// (f) a duplicate detail id is handled explicitly: the direct call reports the
// collision as a typed error, the request path still answers nil, and neither
// the summary row nor the first detail row changes.
func TestIntegrationDetailDuplicateIDCannotCorruptAnything(t *testing.T) {
	m := openTestStore(t)
	ctx := context.Background()
	vals := map[string]string{}
	detWindow(vals)
	detClockAt(m, detBase)

	detLogger(m, vals).Log(ctx, accesslog.Entry{Kind: accesslog.WebLogin,
		Outcome: accesslog.OutcomeOK, Capture: &accesslog.Capture{Body: "first"}})
	id := detSummaryID(t, m)
	if detOne(t, m, id).body != "first" {
		t.Fatal("fixture lost the first detail row")
	}

	// The same id again, the direct way: a typed error, not a panic.
	err := m.InsertAccessDetail(ctx, id, &accesslog.Capture{Body: "second"})
	if err == nil {
		t.Fatal("a second detail row for the same id was accepted")
	}
	if !errors.Is(err, ErrAccessDetailDuplicate) {
		t.Errorf("duplicate error = %v, want ErrAccessDetailDuplicate", err)
	}

	// The same collision through the request path: pre-occupy the id the NEXT
	// summary row will get, then record. The writer must swallow that failure
	// (and log it), keep the summary line, and leave the detail row alone.
	next := id + 1
	if err := m.InsertAccessDetail(ctx, next, &accesslog.Capture{Body: "reserved"}); err != nil {
		t.Fatalf("reserve the next id: %v", err)
	}
	if err := m.InsertAccessLog(ctx, accesslog.Entry{Kind: accesslog.WebLogin,
		Outcome: accesslog.OutcomeOK, Detail: "keep me",
		Capture: &accesslog.Capture{Body: "loser"}}); err != nil {
		t.Errorf("InsertAccessLog reported the detail collision as a summary failure: %v", err)
	}
	if got := detSummaryID(t, m); got != next {
		t.Fatalf("summary id after the collision = %d, want %d (the summary row must still land)", got, next)
	}
	if r := detOne(t, m, next); r.body != "reserved" {
		t.Errorf("the colliding write overwrote the detail row: %q", r.body)
	}
	if r := detOne(t, m, id); r.body != "first" {
		t.Errorf("the first detail row changed: %q", r.body)
	}
}

// 5e (a)(b)(e): one retention clock, driven by captured_at and by the panel
// option — live, not baked in.
func TestIntegrationDetailRetentionPurgeFollowsOption(t *testing.T) {
	m := openTestStore(t)
	ctx := context.Background()
	vals := map[string]string{}
	diag := &accesslog.Diagnostic{Option: detOptions(vals), Now: detClock(detBase)}

	seed := func(id int64, age time.Duration) {
		t.Helper()
		detClockAt(m, detBase.Add(-age))
		if err := m.InsertAccessDetail(ctx, id, &accesslog.Capture{Body: "row"}); err != nil {
			t.Fatalf("seed %d: %v", id, err)
		}
	}
	// One row on each side of BOTH windows: 91d/89d straddle the 90-day default
	// (retention+1d must die, retention-1d must live) and 8d/6d straddle the
	// 7-day value the option is flipped to below.
	seed(780004, 91*24*time.Hour) // D: retention+1d on the default window
	seed(780003, 89*24*time.Hour) // C: retention-1d on the default window
	seed(780002, 8*24*time.Hour)  // B: past a 7d window
	seed(780001, 6*24*time.Hour)  // A: inside every window here
	detClockAt(m, detBase)

	// No option row at all = the 90-day default: only D goes.
	if got := diag.RetentionDays(ctx); got != accesslog.DefaultDetailRetentionDays {
		t.Fatalf("retention default = %d, want %d", got, accesslog.DefaultDetailRetentionDays)
	}
	if n, err := m.PurgeAccessDetails(ctx, diag.RetentionWindow(ctx)); err != nil || n != 1 {
		t.Fatalf("90d purge = (%d, %v), want 1 row deleted", n, err)
	}
	if _, err := detOneErr(m, 780004); err == nil {
		t.Error("a row at retention+1 day survived the purge")
	}
	if _, err := detOneErr(m, 780003); err != nil {
		t.Errorf("a row at retention-1 day died: %v", err)
	}

	// Flip the option to 7 days: the SAME call now reaches B and C. Nothing was
	// re-wired between the two purges — that is the live read.
	vals[accesslog.DetailRetentionOption] = "7"
	if n, err := m.PurgeAccessDetails(ctx, diag.RetentionWindow(ctx)); err != nil || n != 2 {
		t.Fatalf("7d purge = (%d, %v), want 2 rows deleted", n, err)
	}
	for _, id := range []int64{780002, 780003} {
		if _, err := detOneErr(m, id); err == nil {
			t.Errorf("retention option of 7 days did not change the cutoff (id %d survived)", id)
		}
	}
	if _, err := detOneErr(m, 780001); err != nil {
		t.Errorf("a 6d row died inside a 7d window: %v", err)
	}

	// Garbage option value = the documented default, never "purge everything".
	vals[accesslog.DetailRetentionOption] = "not-a-number"
	if n, err := m.PurgeAccessDetails(ctx, diag.RetentionWindow(ctx)); err != nil || n != 0 {
		t.Fatalf("default purge = (%d, %v), want 0 (the 6d row is inside 90d)", n, err)
	}
}

// 5e (c)(d): the credential clock is separate and shorter — an expired deadline
// wipes the column and KEEPS the row (retention has not had its say yet).
func TestIntegrationCredentialSweepIsIndependentOfRetention(t *testing.T) {
	m := openTestStore(t)
	ctx := context.Background()

	seed := func(id int64, captured time.Time, cred string) {
		t.Helper()
		detClockAt(m, captured)
		if err := m.InsertAccessDetail(ctx, id, &accesslog.Capture{Body: "row", Credential: cred}); err != nil {
			t.Fatalf("seed %d: %v", id, err)
		}
	}
	// credentials_until 25h in the past (captured 49h ago: deep inside 90d).
	seed(790001, detBase.Add(-49*time.Hour), detSecret)
	// credentials_until 23h from now (captured 1h ago).
	seed(790002, detBase.Add(-1*time.Hour), detSecret)
	// a detail row that never carried a credential.
	seed(790003, detBase.Add(-49*time.Hour), "")
	detClockAt(m, detBase)

	n, err := m.SweepAccessDetailCredentials(ctx)
	if err != nil {
		t.Fatalf("sweep: %v", err)
	}
	if n != 1 {
		t.Errorf("sweep wiped %d credential rows, want 1", n)
	}

	wiped := detOne(t, m, 790001)
	if wiped.cred != "" {
		t.Errorf("credential_ref survived its deadline: %q", wiped.cred)
	}
	if wiped.until.Valid {
		t.Errorf("credentials_until survived the wipe: %v", wiped.until.Time)
	}
	if wiped.body != "row" {
		t.Errorf("the sweep destroyed evidence it should have kept: %+v", wiped)
	}
	kept := detOne(t, m, 790002)
	if kept.cred != detSecret || !kept.until.Valid {
		t.Errorf("a credential still inside its 24h window was cleared: %+v", kept)
	}
	if _, err := detOneErr(m, 790003); err != nil {
		t.Errorf("a credential-free row vanished: %v", err)
	}

	// The wiped row is still a log row: the retention purge on the default
	// window leaves it alone, which is the whole point of two clocks.
	if n, err := m.PurgeAccessDetails(ctx, 90*24*time.Hour); err != nil || n != 0 {
		t.Fatalf("retention purge = (%d, %v), want the wiped row kept", n, err)
	}
	if _, err := detOneErr(m, 790001); err != nil {
		t.Errorf("the wiped detail row was deleted by retention: %v", err)
	}
}

// Both sweepers must be idempotent and cheap on an empty table — the 6h ticker
// runs them whether or not diagnostic mode was ever turned on.
func TestIntegrationDetailSweepersOnEmptyTable(t *testing.T) {
	m := openTestStore(t)
	ctx := context.Background()
	if n, err := m.SweepAccessDetailCredentials(ctx); err != nil || n != 0 {
		t.Fatalf("sweep on an empty table = (%d, %v), want (0, nil)", n, err)
	}
	if n, err := m.PurgeAccessDetails(ctx, 90*24*time.Hour); err != nil || n != 0 {
		t.Fatalf("purge on an empty table = (%d, %v), want (0, nil)", n, err)
	}
}

// (f) the row must learn the FINAL outcome. The recorder logs when the handler
// has decided the answer — before the status line is written — so a row whose
// status/duration were only ever snapshotted at log time reads 0 and a
// half-measured number: the two columns the panel shows. Observe hands the
// finished outcome back, and InsertAccessLog registers the write.
func TestIntegrationDetailRowLearnsTheFinalOutcome(t *testing.T) {
	m := openTestStore(t)
	vals := map[string]string{}
	detWindow(vals)
	detClockAt(m, detBase)
	l := detLogger(m, vals)

	h := accesslog.Observe(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		accesslog.MarkCredential(r.Context(), detSecret)
		// The tee only sees bytes the handler actually reads, so read the body
		// here exactly like a yggdrasil handler does before it logs.
		body, _ := io.ReadAll(r.Body)
		if len(body) == 0 {
			t.Error("the handler read no request body")
		}
		// Attaching here is what every recorder does: the status does not
		// exist yet, so the capture goes in with 0.
		l.Log(r.Context(), accesslog.Entry{Kind: accesslog.YggAuthenticate,
			Outcome: accesslog.OutcomeOK, UA: "rig/1", Capture: accesslog.Attach(r)})
		time.Sleep(15 * time.Millisecond)
		w.WriteHeader(http.StatusBadGateway)
	}))
	req := httptest.NewRequest(http.MethodPost, "/api/yggdrasil/authserver/authenticate",
		strings.NewReader(`{"username":"rig"}`))
	req.Header.Set("User-Agent", "rig/1")
	h.ServeHTTP(httptest.NewRecorder(), req)

	id := detSummaryID(t, m)
	r := detOne(t, m, id)
	if r.status != http.StatusBadGateway {
		t.Errorf("status_code = %d after the response, want 502 — 0 means the outcome never reached the row", r.status)
	}
	if r.duration < 15 {
		t.Errorf("duration_ms = %d, want >= 15 (measured only up to the log call)", r.duration)
	}
	// The backfill touches the outcome columns and nothing else: the credential
	// and its own short deadline are not collateral.
	if r.cred != detSecret {
		t.Errorf("credential_ref = %q after the backfill, want the plaintext token", r.cred)
	}
	if !r.until.Valid {
		t.Error("credentials_until was dropped by the outcome backfill")
	}
	if !strings.Contains(r.headers, "rig/1") {
		t.Errorf("headers_json was clobbered by the backfill: %s", r.headers)
	}
	// The body excerpt came from the tee, which has now read the request body.
	if !strings.Contains(r.body, "username") {
		t.Errorf("body_excerpt = %q, want the tee'd request body", r.body)
	}
}

// (g) a request that never passes through Observe keeps what the recorder saw
// and must not error — this is the boot/test/direct-call shape, where there is
// no outcome to hand back.
func TestIntegrationDetailWithoutObserveKeepsTheLoggedOutcome(t *testing.T) {
	m := openTestStore(t)
	vals := map[string]string{}
	detWindow(vals)
	detClockAt(m, detBase)

	detLogger(m, vals).Log(context.Background(), accesslog.Entry{Kind: accesslog.WebLogin,
		Outcome: accesslog.OutcomeOK, Capture: detCapture()})

	id := detSummaryID(t, m)
	r := detOne(t, m, id)
	if r.status != 502 || r.duration != 17 {
		t.Errorf("status/duration = %d/%d, want the recorder's own 502/17 (no Observe, no backfill)",
			r.status, r.duration)
	}
}

// (h) the backfill must not resurrect a credential the sweeper already wiped:
// updating by id only ever writes the two outcome columns.
func TestIntegrationDetailOutcomeBackfillDoesNotWriteCredentials(t *testing.T) {
	m := openTestStore(t)
	ctx := context.Background()
	detClockAt(m, detBase)
	if err := m.InsertAccessDetail(ctx, 790501, &accesslog.Capture{
		StatusCode: 0, Credential: detSecret}); err != nil {
		t.Fatalf("insert: %v", err)
	}
	// The credential deadline is captured_at+24h, so the sweep only wipes it
	// once the Go clock is past that — same rule the 6h ticker runs under.
	detClockAt(m, detBase.Add(25*time.Hour))
	if n, err := m.SweepAccessDetailCredentials(ctx); err != nil {
		t.Fatalf("sweep: %v", err)
	} else if n != 1 {
		t.Fatalf("swept %d rows, want 1", n)
	}
	if err := m.UpdateAccessDetailOutcome(ctx, 790501, http.StatusTeapot, 33); err != nil {
		t.Fatalf("backfill: %v", err)
	}
	r := detOne(t, m, 790501)
	if r.status != http.StatusTeapot || r.duration != 33 {
		t.Errorf("status/duration = %d/%d, want 418/33", r.status, r.duration)
	}
	if r.cred != "" || r.until.Valid {
		t.Errorf("outcome backfill re-wrote credential material: cred=%q until=%v", r.cred, r.until)
	}
	// A row that is not there is not an error: the logger never fails a request.
	if err := m.UpdateAccessDetailOutcome(ctx, 790502, 200, 5); err != nil {
		t.Errorf("backfill of a missing row = %v, want nil", err)
	}
	// And a status of 0 means "never observed", not "the response was 0":
	// writing it would replace a number the recorder chose with a fake one.
	if err := m.InsertAccessDetail(ctx, 790503, detCapture()); err != nil {
		t.Fatalf("insert: %v", err)
	}
	if err := m.UpdateAccessDetailOutcome(ctx, 790503, 0, 9999); err != nil {
		t.Fatalf("unobserved outcome = %v, want nil", err)
	}
	if r3 := detOne(t, m, 790503); r3.status != 502 || r3.duration != 17 {
		t.Errorf("a 0 status overwrote the recorder's numbers: %d/%d, want 502/17", r3.status, r3.duration)
	}
}

// (i) the hand-off must not overwrite a status the recorder chose on purpose.
// Union outbound reports the HUB's status code, not this station's response,
// and that number is the whole point of the row — a blanket backfill would
// replace "hub answered 502" with "we answered 200" and destroy the evidence.
func TestIntegrationDetailHandoffDoesNotOverwriteAChosenStatus(t *testing.T) {
	m := openTestStore(t)
	vals := map[string]string{}
	detWindow(vals)
	detClockAt(m, detBase)
	l := detLogger(m, vals)

	h := accesslog.Observe(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// OutcomePending stays false: this capture carries its own numbers.
		l.Log(r.Context(), accesslog.Entry{Kind: accesslog.UnionOut,
			Outcome: accesslog.OutcomeFail, Capture: detCapture()})
		w.WriteHeader(http.StatusOK)
	}))
	h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodPost, "/u", nil))

	r := detOne(t, m, detSummaryID(t, m))
	if r.status != 502 || r.duration != 17 {
		t.Errorf("status/duration = %d/%d, want the recorder's 502/17 — the hand-off clobbered a chosen status",
			r.status, r.duration)
	}
	if r.upstream != "hub.timeout" {
		t.Errorf("upstream_code = %q, want hub.timeout", r.upstream)
	}
}

// (j) the column itself, not just the helper: a caller that reached
// InsertAccessDetail with a raw authenticate body still must not put a
// password into access_log_details. This is the rig finding written as a test —
// the body_excerpt used to be the plaintext password, on a 7..90 day clock,
// readable by any admin with permission >= 1.
func TestIntegrationDetailBodyNeverStoresAPassword(t *testing.T) {
	m := openTestStore(t)
	ctx := context.Background()
	detClockAt(m, detBase)
	const pw = "hunter2-never-stored"

	h := http.Header{}
	h.Set("User-Agent", "launcher/3.5")
	c := &accesslog.Capture{
		StatusCode: 0,
		DurationMS: 4,
		Headers:    h,
		Body:       `{"username":"rig@localhost","password":"` + pw + `"}`,
	}
	if err := m.InsertAccessDetail(ctx, 790601, c); err != nil {
		t.Fatalf("insert: %v", err)
	}
	r := detOne(t, m, 790601)
	if strings.Contains(r.body, pw) {
		t.Fatalf("body_excerpt stored the password: %q", r.body)
	}
	if !strings.Contains(r.body, "rig@localhost") {
		t.Errorf("body_excerpt lost the debugging value too: %q", r.body)
	}
	// And through the reader API's own struct, which is what a panel sees.
	rows, err := m.AccessDetailRows(ctx, []int64{790601})
	if err != nil || len(rows) != 1 {
		t.Fatalf("read back = (%d rows, %v)", len(rows), err)
	}
	if strings.Contains(rows[0].Body, pw) || strings.Contains(rows[0].Headers, pw) {
		t.Fatalf("the reader returned a password: body=%q headers=%q", rows[0].Body, rows[0].Headers)
	}
	// A body whose shape is unknown is dropped rather than guessed at — the
	// marker must be visible, so an operator knows nothing was captured.
	if err := m.InsertAccessDetail(ctx, 790602, &accesslog.Capture{
		Body: "password=" + pw + "&username=rig"}); err != nil {
		t.Fatalf("insert form body: %v", err)
	}
	if r2 := detOne(t, m, 790602); strings.Contains(r2.body, pw) || !strings.Contains(strings.ToLower(r2.body), "redact") {
		t.Errorf("form-encoded body stored as %q, want a redaction marker", r2.body)
	}
}
