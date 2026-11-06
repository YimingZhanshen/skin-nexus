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
	"testing"
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
// also the empty string. `text` is what the engine may compare against ''
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
