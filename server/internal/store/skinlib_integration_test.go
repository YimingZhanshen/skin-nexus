//go:build integration

// skinlib_integration_test.go runs the library query against real MySQL.
// The SQL shape — not just the WHERE string — is what had no coverage: an admin
// asking for the library without a filter produced `... WHERE ORDER BY`, and the
// unit test that now locks that case cannot see whether MySQL accepts the result.
package store

import (
	"context"
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
		name    string
		opts    SkinlibOpts
		wantTID int64 // 0 = no specific row asserted
	}{
		{"admin unfiltered", SkinlibOpts{ViewerUID: owner, ViewerAdmin: true}, pubSkin},
		{"admin unfiltered by likes", SkinlibOpts{ViewerUID: owner, ViewerAdmin: true, Sort: "likes"}, pubSkin},
		{"admin unknown filter", SkinlibOpts{ViewerUID: owner, ViewerAdmin: true, Type: "bogus"}, pubSkin},
		{"anonymous", SkinlibOpts{}, pubSkin},
		{"normal viewer", SkinlibOpts{ViewerUID: owner}, pubSkin},
		{"public only", SkinlibOpts{PublicOnly: true}, pubSkin},
		{"keyword only", SkinlibOpts{ViewerUID: owner, ViewerAdmin: true, Keyword: "matrix"}, pubSkin},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			opt := tc.opts
			opt.Page = 1
			rows, total, err := m.SkinlibList(ctx, opt)
			if err != nil {
				t.Fatalf("SkinlibList(%+v): %v", opt, err)
			}
			if total < 3 || len(rows) == 0 {
				t.Fatalf("total=%d rows=%d, want at least the three seeded entries", total, len(rows))
			}
			var found bool
			for _, r := range rows {
				if r.TID == tc.wantTID {
					found = true
					if r.Hash != strings.Repeat("b", 64) || r.Type != "skin" {
						t.Errorf("row %d came back as type=%q hash=%q", r.TID, r.Type, r.Hash)
					}
				}
			}
			if !found {
				t.Errorf("public skin %d missing from the page", tc.wantTID)
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
