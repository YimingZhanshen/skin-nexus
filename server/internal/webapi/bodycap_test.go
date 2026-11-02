package webapi

// bodycap_test.go pins the 1 MiB request-body cap and its single exemption.
// The cap exists because the CPU is the product under attack: an unbounded JSON
// body handed to argon2 is a cheap way to buy a server outage. The texture
// upload is exempt because a skin PNG is not argon2 bait — and capping it at
// 1 MiB anyway made the admin's own size knob unturnable (the file limit reaches
// 2 MiB) while turning "your file is too large" into "I could not parse your
// request", which no player can act on.

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

type readResult struct {
	n   int
	err error
}

// capProbe reports what a handler sees when it drains the body of a request
// that already passed through the given middleware.
func capProbe(t *testing.T, wrap func(http.Handler) http.Handler, method, path string, payload []byte) readResult {
	t.Helper()
	var got readResult
	h := wrap(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n, err := io.Copy(io.Discard, r.Body)
		got.n, got.err = int(n), err
	}))
	req := httptest.NewRequest(method, path, bytes.NewReader(payload))
	h.ServeHTTP(httptest.NewRecorder(), req)
	return got
}

func TestMaxBodyCapsEverythingButTheUpload(t *testing.T) {
	// 1.25 MiB: over the transport cap, under the file ceiling.
	payload := bytes.Repeat([]byte("a"), (1250 << 10))

	cases := []struct {
		name     string
		method   string
		path     string
		wantRead int
		wantCut  bool
	}{
		{"login stays capped", http.MethodPost, "/api/v1/login", 0, true},
		{"skin change stays capped", http.MethodPut, "/api/v1/me/players/1/skin", 0, true},
		{"same path with another method stays capped", http.MethodPut, "/api/v1/me/textures", 0, true},
		{"texture upload gets through", http.MethodPost, "/api/v1/me/textures", len(payload), false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := capProbe(t, maxBody, tc.method, tc.path, payload)
			if tc.wantCut {
				if got.err == nil {
					t.Fatalf("body of %d bytes read without error — the argon2 defence is gone", got.n)
				}
				if !bodyTooLarge(got.err) {
					t.Fatalf("read error = %v, want a *http.MaxBytesError so the handler can say \"too large\"", got.err)
				}
				return
			}
			if got.err != nil {
				t.Fatalf("upload body was cut at %d bytes: %v", got.n, got.err)
			}
			if got.n != tc.wantRead {
				t.Fatalf("read %d bytes, want the full %d", got.n, tc.wantRead)
			}
		})
	}
}
