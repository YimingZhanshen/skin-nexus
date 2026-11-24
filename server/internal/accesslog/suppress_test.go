package accesslog

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestSuppressCaptureDropsBodyCredentialAndHeaders(t *testing.T) {
	h := Observe(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		SuppressCapture(r.Context())
		_, _ = io.ReadAll(r.Body)
		MarkCredential(r.Context(), "never-capture-this")
		if capture := Attach(r); capture != nil {
			t.Fatalf("suppressed capture = %+v, want nil", capture)
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	r := httptest.NewRequest(http.MethodGet, "/v/secret", strings.NewReader("never-capture-this-body"))
	r.Header.Set("Authorization", "Bearer secret")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, r)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("status = %d", rec.Code)
	}
}
