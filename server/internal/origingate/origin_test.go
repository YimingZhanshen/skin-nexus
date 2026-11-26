package origingate

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestSameOrigin(t *testing.T) {
	cases := []struct {
		name   string
		origin string
		site   string
		want   bool
	}{
		{name: "matching origin ignores scheme and host case", origin: "https://SKIN.TEST", want: true},
		{name: "external origin", origin: "https://other.test", want: false},
		{name: "null origin without fetch metadata", origin: "null", want: false},
		{name: "null origin falls back to same-origin metadata", origin: "null", site: "same-origin", want: true},
		{name: "no evidence fails closed", want: false},
		{name: "same-origin fetch metadata", site: "same-origin", want: true},
		{name: "none fetch metadata", site: "none", want: true},
		{name: "cross-site fetch metadata", site: "cross-site", want: false},
		{name: "external origin overrides misleading metadata", origin: "https://other.test", site: "same-origin", want: false},
		{name: "malformed origin fails closed", origin: "://bad", want: false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := httptest.NewRequest(http.MethodPost, "http://skin.test/api/mutate", nil)
			if tc.origin != "" {
				r.Header.Set("Origin", tc.origin)
			}
			if tc.site != "" {
				r.Header.Set("Sec-Fetch-Site", tc.site)
			}
			if got := SameOrigin(r); got != tc.want {
				t.Fatalf("SameOrigin() = %v, want %v", got, tc.want)
			}
		})
	}
}
