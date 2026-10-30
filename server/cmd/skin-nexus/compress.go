package main

import (
	"compress/gzip"
	"net/http"
	"path"
	"strings"
)

// gzipHandler compresses text responses when the client supports it. The
// origin sits in Hangzhou behind a distant CDN edge; shipping the 439 KiB SPA
// bundle uncompressed dominates first load on a 3 Mbps link. Images and other
// already-compressed payloads are passed through untouched.
func gzipHandler(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodHead || r.Header.Get("Range") != "" ||
			!strings.Contains(r.Header.Get("Accept-Encoding"), "gzip") ||
			!compressiblePath(r.URL.Path) {
			next.ServeHTTP(w, r)
			return
		}
		gw := &gzipWriter{ResponseWriter: w}
		defer gw.Close()
		next.ServeHTTP(gw, r)
	})
}

func compressiblePath(p string) bool {
	if strings.HasPrefix(p, "/textures/") || strings.HasPrefix(p, "/preview/") ||
		strings.HasPrefix(p, "/avatar/") || strings.HasPrefix(p, "/texture-render/") ||
		strings.HasPrefix(p, "/healthz") {
		return false
	}
	switch strings.ToLower(path.Ext(p)) {
	case ".png", ".jpg", ".jpeg", ".webp", ".gif", ".ico", ".woff", ".woff2", ".zip", ".gz":
		return false
	}
	return true
}

type gzipWriter struct {
	http.ResponseWriter
	zw     *gzip.Writer
	status int
	wrote  bool
	gzip   bool
}

func (w *gzipWriter) WriteHeader(code int) {
	if w.wrote {
		return
	}
	w.wrote = true
	w.status = code
	// Do not label empty/redirect responses as gzip: 304 has no body and a
	// 204 must not carry Content-Encoding.
	if code >= 200 && code < 300 && code != http.StatusNoContent && code != http.StatusNotModified {
		w.gzip = true
		w.Header().Set("Content-Encoding", "gzip")
		w.Header().Add("Vary", "Accept-Encoding")
		w.Header().Del("Content-Length")
		w.Header().Del("ETag") // compressed bytes differ from the identity variant
		w.zw = gzip.NewWriter(w.ResponseWriter)
	}
	w.ResponseWriter.WriteHeader(code)
}

func (w *gzipWriter) Write(b []byte) (int, error) {
	if !w.wrote {
		w.WriteHeader(http.StatusOK)
	}
	if w.gzip && w.zw != nil {
		return w.zw.Write(b)
	}
	return w.ResponseWriter.Write(b)
}

func (w *gzipWriter) Flush() {
	if w.zw != nil {
		_ = w.zw.Flush()
	}
	if f, ok := w.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

func (w *gzipWriter) Close() error {
	if w.zw != nil {
		return w.zw.Close()
	}
	return nil
}
