//go:build integration

// skinlib_integration_test.go runs the library query against real MySQL.
// The SQL shape — not just the WHERE string — is what had no coverage: an admin
// asking for the library without a filter produced `... WHERE ORDER BY`, and the
// unit test that now locks that case cannot see whether MySQL accepts the result.
package store

import (
	"context"
	"math"
	"strings"
	"testing"
)

func TestIntegrationSkinlibListViewerMatrix(t *testing.T) {
	m := openTestStore(t)
	ctx := context.Background()
	owner := seedUser(t, m)
	other := seedUser(t, m)

	pubSkin, err := m.AddTexture(ctx, owner, "skin", strings.Repeat("b", 64), "default",
		TextureMeta{Description: "matrix public skin"})
	if err != nil {
		t.Fatalf("add public skin: %v", err)
	}
	if _, err := m.AddTexturePrivate(ctx, other, "skin", strings.Repeat("c", 64), "slim",
		TextureMeta{Description: "matrix private skin"}); err != nil {
		t.Fatalf("add private skin: %v", err)
	}
	if _, err := m.AddTexture(ctx, owner, "cape", strings.Repeat("d", 64), "",
		TextureMeta{Description: "matrix cape"}); err != nil {
		t.Fatalf("add cape: %v", err)
	}

	cases := []struct {
		name       string
		opts       SkinlibOpts
		wantTotal  int   // 3 = the private entry is visible too
		wantPubTID int64 // 0 = no specific row asserted
	}{
		{"admin unfiltered", SkinlibOpts{ViewerUID: owner, ViewerAdmin: true}, 3, pubSkin},
		{"admin unfiltered by likes", SkinlibOpts{ViewerUID: owner, ViewerAdmin: true, Sort: "likes"}, 3, pubSkin},
		{"admin unknown filter", SkinlibOpts{ViewerUID: owner, ViewerAdmin: true, Type: "bogus"}, 3, pubSkin},
		{"anonymous", SkinlibOpts{}, 2, pubSkin},
		{"normal viewer", SkinlibOpts{ViewerUID: owner}, 2, pubSkin},
		{"public only", SkinlibOpts{PublicOnly: true}, 2, pubSkin},
		{"keyword only", SkinlibOpts{ViewerUID: owner, ViewerAdmin: true, Keyword: "matrix"}, 3, pubSkin},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			opt := tc.opts
			opt.Page = 1
			rows, total, err := m.SkinlibList(ctx, opt)
			if err != nil {
				t.Fatalf("SkinlibList(%+v): %v", opt, err)
			}
			if total != tc.wantTotal || len(rows) != tc.wantTotal {
				t.Fatalf("total=%d rows=%d, want %d (three seeded entries, one of them private)",
					total, len(rows), tc.wantTotal)
			}
			var found bool
			for _, r := range rows {
				if r.TID == tc.wantPubTID {
					found = true
					if r.Hash != strings.Repeat("b", 64) || r.Type != "skin" {
						t.Errorf("row %d came back as type=%q hash=%q", r.TID, r.Type, r.Hash)
					}
				}
			}
			if !found {
				t.Errorf("public skin %d missing from the page", tc.wantPubTID)
			}
		})
	}

	// Visibility is the other half of the same query: a private entry must show
	// for its owner and the admin, and never for an anonymous viewer.
	privateOf := func(opt SkinlibOpts) int {
		opt.Page = 1
		opt.Keyword = "matrix private"
		rows, _, err := m.SkinlibList(ctx, opt)
		if err != nil {
			t.Fatalf("private probe: %v", err)
		}
		return len(rows)
	}
	if n := privateOf(SkinlibOpts{ViewerUID: other}); n != 1 {
		t.Errorf("owner sees %d private rows, want 1", n)
	}
	if n := privateOf(SkinlibOpts{ViewerUID: owner, ViewerAdmin: true}); n != 1 {
		t.Errorf("admin sees %d private rows, want 1", n)
	}
	if n := privateOf(SkinlibOpts{}); n != 0 {
		t.Errorf("anonymous sees %d private rows, want 0", n)
	}
}

// The half of the paging bug SQLite cannot show. A wrapped negative offset is a
// hard error on MySQL and PostgreSQL — one anonymous query string becomes a
// database failure and a 500 — while SQLite reads the same value as "skip zero
// rows" and answers with the first page. Only the engine legs can tell those
// apart, so the assertion lives here rather than in the unit suite that passes
// on both.
func TestIntegrationSkinlibHugePageOffsetReachesTheEngine(t *testing.T) {
	m := openTestStore(t)
	ctx := context.Background()
	owner := seedUser(t, m)
	if _, err := m.AddTexture(ctx, owner, "skin", strings.Repeat("e", 64), "default",
		TextureMeta{Description: "offset overflow probe"}); err != nil {
		t.Fatalf("add skin: %v", err)
	}
	for _, page := range []int{math.MaxInt64, math.MaxInt64 - 7} {
		rows, total, err := m.SkinlibList(ctx, SkinlibOpts{PublicOnly: true, Page: page})
		if err != nil {
			t.Fatalf("page=%d: %v — the offset must be clamped before it reaches the engine", page, err)
		}
		if len(rows) != 0 {
			t.Errorf("page=%d returned %d rows, want an empty page, not the first one", page, len(rows))
		}
		if total != 1 {
			t.Errorf("page=%d total = %d, want the count to stay honest", page, total)
		}
	}
}
