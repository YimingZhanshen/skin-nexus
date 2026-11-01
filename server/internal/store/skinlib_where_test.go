package store

import (
	"fmt"
	"strings"
	"testing"
)

// TestSkinlibWhereNeverEmpty is the regression lock for a 500 that only an API
// client could hit: an admin viewer asking for the library without any filter
// produced an empty WHERE clause, so the query read `... textures t WHERE ORDER
// BY` and MySQL rejected it. The SPA always sends a type filter, which is why no
// browser request ever failed.
func TestSkinlibWhereNeverEmpty(t *testing.T) {
	cases := []struct {
		name string
		opts SkinlibOpts
	}{
		{"admin unfiltered", SkinlibOpts{ViewerUID: 8, ViewerAdmin: true, Page: 1}},
		{"admin unfiltered sorted by likes", SkinlibOpts{ViewerUID: 8, ViewerAdmin: true, Sort: "likes", Page: 1}},
		{"anonymous", SkinlibOpts{Page: 1}},
		{"normal viewer", SkinlibOpts{ViewerUID: 3, Page: 1}},
		{"public only", SkinlibOpts{PublicOnly: true, Page: 1}},
		{"keyword only", SkinlibOpts{ViewerUID: 8, ViewerAdmin: true, Keyword: "a", Page: 1}},
		{"unknown filter value", SkinlibOpts{ViewerUID: 8, ViewerAdmin: true, Type: "bogus", Page: 1}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			opt := tc.opts
			opt.normalize()
			clause, args := opt.where()
			if strings.TrimSpace(clause) == "" {
				t.Fatalf("empty WHERE clause; the query becomes invalid SQL")
			}
			sql := fmt.Sprintf("SELECT COUNT(*) FROM textures t WHERE %s", clause)
			if strings.HasSuffix(sql, "WHERE ") {
				t.Fatalf("query ends in a dangling WHERE: %q", sql)
			}
			if got := strings.Count(sql, "?"); got != len(args) {
				t.Errorf("%q binds %d placeholders but %d args", sql, got, len(args))
			}
		})
	}
}
