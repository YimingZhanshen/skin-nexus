//go:build integration

package store

import (
	"context"
	"errors"
	"testing"
	"time"
)

// The player-facing tracking view feeds /api/v1/me/reports, so the uid filter
// IS the security boundary: without it every report on the site leaks to any
// logged-in caller (the red reading below comes from the delegate stub that
// calls ListReports unfiltered). Pagination must not change the scoped total.
func TestIntegrationListMyReportsScopedAndPaged(t *testing.T) {
	m := openTestStore(t)
	ctx := context.Background()
	uidA := seedUserAs(t, m, "myrep-a@example.com", "myrep_user_a")
	uidB := seedUserAs(t, m, "myrep-b@example.com", "myrep_user_b")

	tidA, err := m.AddTexture(ctx, uidA, "skin", "myrephash00000000000000000000000000000000000000000000000000000", "", TextureMeta{Description: "a"})
	if err != nil {
		t.Fatalf("seed texture a: %v", err)
	}
	tidB, err := m.AddTexture(ctx, uidB, "skin", "myrephash00000000000000000000000000000000000000000000000000001", "", TextureMeta{Description: "b"})
	if err != nil {
		t.Fatalf("seed texture b: %v", err)
	}

	if _, err := m.CreateReport(ctx, tidA, uidA, "copyright", "first"); err != nil {
		t.Fatalf("report 1: %v", err)
	}
	time.Sleep(5 * time.Millisecond) // created_at DESC needs a strict order
	if _, err := m.CreateReport(ctx, tidB, uidA, "spam", "second"); err != nil {
		t.Fatalf("report 2: %v", err)
	}
	if _, err := m.CreateReport(ctx, tidA, uidB, "copyright", "not mine"); err != nil {
		t.Fatalf("report 3: %v", err)
	}

	rows, total, err := m.ListMyReports(ctx, uidA, ReportOpts{Page: 1, PerPage: 10})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if total != 2 || len(rows) != 2 {
		t.Fatalf("total=%d rows=%d, want 2 — another player's report leaked into the scope", total, len(rows))
	}
	for _, r := range rows {
		if r.ReporterUID != uidA {
			t.Fatalf("row %d reporter=%d, want %d", r.ID, r.ReporterUID, uidA)
		}
	}
	if rows[0].Detail != "second" || rows[1].Detail != "first" {
		t.Fatalf("order = %s, %s — want newest first", rows[0].Detail, rows[1].Detail)
	}

	// page 2 of size 1: one row, but the scoped total stays 2
	rows, total, err = m.ListMyReports(ctx, uidA, ReportOpts{Page: 2, PerPage: 1})
	if err != nil {
		t.Fatalf("page 2: %v", err)
	}
	if total != 2 || len(rows) != 1 || rows[0].Detail != "first" {
		t.Fatalf("page2 total=%d rows=%d (%+v), want total 2 / [first]", total, len(rows), rows)
	}

	// the other player sees only theirs
	rows, total, err = m.ListMyReports(ctx, uidB, ReportOpts{Page: 1, PerPage: 10})
	if err != nil {
		t.Fatalf("list b: %v", err)
	}
	if total != 1 || len(rows) != 1 || rows[0].ReporterUID != uidB {
		t.Fatalf("uidB total=%d rows=%+v, want exactly their own one", total, rows)
	}

	// unknown user → empty page, not an error
	rows, total, err = m.ListMyReports(ctx, 999999, ReportOpts{Page: 1, PerPage: 10})
	if err != nil || total != 0 || len(rows) != 0 {
		t.Fatalf("unknown uid: total=%d rows=%d err=%v, want empty", total, len(rows), err)
	}
	if errors.Is(err, ErrNotFound) {
		t.Fatal("empty scope must be an empty page, not ErrNotFound")
	}
}
