//go:build integration

// access_detail_integration_test.go — 0029 access_log_details 的形状承诺。
//
// 这张表在 5a 里只有 DDL，读写分别在 5c/5d 接。这里钉的是三件跨引擎的语义，
// 它们一旦在某棵树上不成立，后面两片就会以更难查的方式红：
//  1. id 不是自增列 —— 写入方带 access_logs.id 进来；
//  2. 同 id 第二条必须失败 —— 1:1 是靠主键维持的，不是靠约定；
//  3. credential_ref 默认空 —— 没开诊断模式的明细行里不该有凭据字段可用值。
package store

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"skin-nexus/server/internal/accesslog"
)

func TestAccessLogDetailsShape(t *testing.T) {
	m := openTestStore(t)
	ctx := context.Background()

	if ok, err := m.TableExists(ctx, "access_log_details"); err != nil || !ok {
		t.Fatalf("access_log_details missing after the 0029 tree: ok=%v err=%v", ok, err)
	}

	// 一个 502 上游超时，带响应体节选与两个头。列名逐字与三棵树一致。
	if _, err := m.db.ExecContext(ctx, `INSERT INTO access_log_details
		(id, status_code, duration_ms, upstream_code, headers_json, body_excerpt)
		VALUES (4242, 502, 17, 'up.timeout', '{"User-Agent":"rig/1","X-Request-Id":"r-1"}', 'gateway went quiet')`); err != nil {
		t.Fatalf("insert with an explicit id (no autoincrement): %v", err)
	}

	var (
		status                  int
		durMS                   int64
		upstream, headers, body string
	)
	err := m.db.QueryRowContext(ctx, `SELECT status_code, duration_ms, upstream_code, headers_json, body_excerpt
		FROM access_log_details WHERE id = 4242`).Scan(&status, &durMS, &upstream, &headers, &body)
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	if status != 502 || durMS != 17 || upstream != "up.timeout" || body == "" || headers == "" {
		t.Errorf("round-trip changed the row: status=%d duration=%d upstream=%q headers=%q body=%q",
			status, durMS, upstream, headers, body)
	}

	// 1:1 由主键强制。今天不钉这条，5c 的写入路径一旦重试就会悄悄多塞一行，
	// 面板按 id 取明细时拿到的是任意一条。
	if _, err := m.db.ExecContext(ctx, `INSERT INTO access_log_details (id, status_code) VALUES (4242, 200)`); err == nil {
		t.Error("a second detail row for the same access_logs.id was accepted — the 1:1 promise is not enforced")
	}

	// 凭据列默认空：摘要行规则之外的东西，只有开着诊断模式才会写进来。
	// 判空写法按列的类型分开：TEXT 才谈「空串」；DATETIME 拿 '' 去比在
	// MySQL 8.0 是 Error 1525 Incorrect DATETIME value、在 PG 腿上是 invalid
	// input syntax for type timestamp，而 SQLite 会默默吞下这条 —— 2bcdfdb 的
	// 两条 CI 腿就是这么红的。时间列只判 IS NULL。
	if !columnBlank(ctx, t, m, "credential_ref", true) {
		t.Error("credential_ref defaulted to a non-empty value")
	}
	if !columnBlank(ctx, t, m, "credentials_until", false) {
		t.Error("credentials_until set without a credential")
	}
}

// columnBlank reports whether a column is NULL — and, for text columns only,
// also the empty string. `text` is what the engine may compare against ”
// without a type error; passing it explicitly keeps the SQLite leg from
// silently endorsing SQL the other two engines reject.
func columnBlank(ctx context.Context, t *testing.T, m *Mysql, col string, text bool) bool {
	t.Helper()
	var one int
	cond := col + " IS NULL"
	if text {
		cond += " OR " + col + " = ''"
	}
	q := "SELECT CASE WHEN " + cond + " THEN 1 ELSE 0 END FROM access_log_details WHERE id = 4242"
	if err := m.db.QueryRowContext(ctx, q).Scan(&one); err != nil {
		t.Fatalf("probe %s: %v", col, err)
	}
	return one == 1
}

// TestIntegrationAccessDetailRowsReadsARealIdList is the leg the 5d reader never
// had: every earlier test replaced the fetch seam with a stub, so the SQL that
// actually reaches a database was never executed. On the 2026-11-09 rig run it
// failed there — dialect.Placeholders already returns "(?, ?)" and the query
// wrapped it in another pair of parentheses, so SQLite answered
// "SQL logic error: row value misused" and the panel's expand button was a 500
// on every engine. Pinned here against a real table, with the three list shapes
// the panel produces: several ids, one id, and ids with no detail row.
func TestIntegrationAccessDetailRowsReadsARealIdList(t *testing.T) {
	m := openTestStore(t)
	ctx := context.Background()

	ids := []int64{881001, 881002, 881003}
	for i, id := range ids {
		h := http.Header{}
		h.Set("User-Agent", "rig/1")
		c := &accesslog.Capture{
			StatusCode:   200 + i,
			DurationMS:   int64(10 + i),
			UpstreamCode: "hub.test",
			Headers:      h,
			Body:         `{"n":1}`,
		}
		if id == 881002 {
			c.Credential = "token-881002-never-returned"
		}
		if err := m.InsertAccessDetail(ctx, id, c); err != nil {
			t.Fatalf("insert %d: %v", id, err)
		}
	}

	got, err := m.AccessDetailRows(ctx, ids)
	if err != nil {
		t.Fatalf("AccessDetailRows over 3 real ids: %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("rows = %d, want 3 (%+v)", len(got), got)
	}
	// Newest id first, matching the summary page's newest-first order.
	if got[0].ID != 881003 || got[2].ID != 881001 {
		t.Errorf("order = [%d %d %d], want 881003,881002,881001", got[0].ID, got[1].ID, got[2].ID)
	}
	if got[0].StatusCode != 202 || got[0].DurationMS != 12 {
		t.Errorf("row 881003 status/duration = %d/%d, want 202/12", got[0].StatusCode, got[0].DurationMS)
	}
	if got[0].UpstreamCode != "hub.test" {
		t.Errorf("upstream_code = %q, want hub.test", got[0].UpstreamCode)
	}
	if !strings.Contains(got[0].Headers, "rig/1") {
		t.Errorf("headers_json = %q, want the whitelisted User-Agent", got[0].Headers)
	}
	// The reader answers "is there a credential" and nothing else: the middle
	// row says yes, and the plaintext token must not be reachable through the
	// JSON the panel receives.
	if !got[1].CredentialSet || got[0].CredentialSet || got[2].CredentialSet {
		t.Errorf("credential_present = [%v %v %v], want only the middle row true",
			got[0].CredentialSet, got[1].CredentialSet, got[2].CredentialSet)
	}
	raw, err := json.Marshal(got)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if strings.Contains(string(raw), "token-881002-never-returned") {
		t.Fatalf("the reader serialised a credential: %s", raw)
	}

	// One id (a single expanded row) and an id with no detail row are the two
	// shapes left; neither may error.
	one, err := m.AccessDetailRows(ctx, []int64{881002})
	if err != nil || len(one) != 1 {
		t.Fatalf("single-id read = (%d rows, %v), want 1 row", len(one), err)
	}
	mixed, err := m.AccessDetailRows(ctx, []int64{881001, 881999})
	if err != nil {
		t.Fatalf("mixed read: %v", err)
	}
	if len(mixed) != 1 || mixed[0].ID != 881001 {
		t.Fatalf("mixed read = %+v, want only the id that exists", mixed)
	}
	// An empty list never reaches SQL and must stay a non-nil slice: the reader
	// API promises [] and not null.
	empty, err := m.AccessDetailRows(ctx, nil)
	if err != nil || empty == nil || len(empty) != 0 {
		t.Fatalf("empty read = (%v, %v), want an empty non-nil slice", empty, err)
	}
}
