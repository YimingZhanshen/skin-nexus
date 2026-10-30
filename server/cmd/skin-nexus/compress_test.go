package main

import (
	"compress/gzip"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestGzipTextResponse(t *testing.T) {
	h := gzipHandler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"hello":"world"}`))
	}))
	req := httptest.NewRequest(http.MethodGet, "/api/v1/stats", nil)
	req.Header.Set("Accept-Encoding", "gzip")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if got := rec.Header().Get("Content-Encoding"); got != "gzip" {
		t.Fatalf("Content-Encoding = %q, want gzip", got)
	}
	zr, err := gzip.NewReader(rec.Body)
	if err != nil {
		t.Fatal(err)
	}
	body, err := io.ReadAll(zr)
	if err != nil {
		t.Fatal(err)
	}
	if string(body) != `{"hello":"world"}` {
		t.Fatalf("body = %q", body)
	}
}

func TestGzipSkipsImagesAndRanges(t *testing.T) {
	h := gzipHandler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("png"))
	}))
	for _, tc := range []struct {
		name string
		path string
		rng  bool
	}{
		{"image", "/textures/abc", false},
		{"preview", "/preview/1", false},
		{"range", "/assets/main-abcdefgh.js", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, tc.path, nil)
			req.Header.Set("Accept-Encoding", "gzip")
			if tc.rng {
				req.Header.Set("Range", "bytes=0-1")
			}
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, req)
			if got := rec.Header().Get("Content-Encoding"); got != "" {
				t.Fatalf("Content-Encoding = %q, want passthrough", got)
			}
		})
	}
}

func TestHashedAsset(t *testing.T) {
	for name, want := range map[string]bool{
		"main-DDTQwvLG.js":           true,
		"main-CeYvY7ta.css":          true,
		"viewer.js":                  false,
		"skinview3d-fa4Yw2G9.js":     true,
		"preload-helper-ckwbz45p.js": true,
	} {
		if got := hashedAsset(name); got != want {
			t.Errorf("hashedAsset(%q) = %v, want %v", name, got, want)
		}
	}
}
