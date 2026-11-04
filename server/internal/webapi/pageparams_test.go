package webapi

// pageParams is the second place a page number reaches the database (the first
// is skinlib, covered by paging_test.go). It is tested at the helper because the
// two lists behind it are the user's own closet and uploads: an unbounded
// ?pageSize= would render a 700-texture account in one response, and a page
// number past int64 turns the offset negative — which MySQL and PostgreSQL
// reject and SQLite answers by handing back the first page.

import (
	"net/http/httptest"
	"testing"
)

func TestPageParamsClampsSizeAndPage(t *testing.T) {
	const maxOffset = 1<<31 - 1
	cases := []struct {
		name              string
		query             string
		wantLimit, wantAt int
	}{
		{"defaults", "", 50, 0},
		{"client-picked size", "?pageSize=10&page=1", 10, 0},
		{"third page of ten", "?pageSize=10&page=3", 10, 20},
		{"size above the ceiling", "?pageSize=100000", 200, 0},
		{"size exactly at the ceiling", "?pageSize=200", 200, 0},
		{"zero size falls back to the default", "?pageSize=0", 50, 0},
		{"negative size falls back", "?pageSize=-7", 50, 0},
		{"non-numeric size falls back", "?pageSize=all", 50, 0},
		{"page zero is page one", "?pageSize=10&page=0", 10, 0},
		{"negative page is page one", "?pageSize=10&page=-4", 10, 0},
		{"a page past int64 clamps instead of wrapping", "?pageSize=10&page=9223372036854775807", 10, maxOffset},
		{"widest size with the same page", "?pageSize=200&page=9223372036854775807", 200, maxOffset},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			limit, offset := pageParams(httptest.NewRequest("GET", "/api/v1/closet"+c.query, nil))
			if limit != c.wantLimit {
				t.Errorf("limit = %d, want %d", limit, c.wantLimit)
			}
			if offset != c.wantAt {
				t.Errorf("offset = %d, want %d", offset, c.wantAt)
			}
			if offset < 0 {
				t.Errorf("offset = %d, must never be negative", offset)
			}
		})
	}
}

// A non-numeric page must not be confused with page 1 by a client that sent a
// stale URL: the answer is the first page, and it is the same answer an absent
// page gives, so no caller can read a different meaning into it.
func TestPageParamsTreatsGarbageAsFirstPage(t *testing.T) {
	for _, q := range []string{"?page=1abc", "?page=+1x", "?page=", "?page=%20"} {
		_, offset := pageParams(httptest.NewRequest("GET", "/api/v1/closet"+q, nil))
		if offset != 0 {
			t.Errorf("%s offset = %d, want 0", q, offset)
		}
	}
}
