package webapi

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"skin-nexus/server/internal/indexnow"
	"skin-nexus/server/internal/siteurl"
)

// The push must carry exactly the detail-page URLs the sitemap advertises
// (/skinlib/{tid} under the configured site URL) — two URL shapes for one
// page is how index coverage silently drifts.
func TestIndexNowPushBuildsDetailURLs(t *testing.T) {
	siteurl.Set("https://skin.test")
	var mu sync.Mutex
	var got [][]string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var payload struct {
			URLList []string `json:"urlList"`
		}
		b, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(b, &payload)
		mu.Lock()
		got = append(got, payload.URLList)
		mu.Unlock()
	}))
	defer srv.Close()
	b := &Backend{IndexNow: &indexnow.Client{
		Enabled:  func() bool { return true },
		Key:      "0123456789abcdef",
		Host:     "skin.test",
		Endpoint: srv.URL,
	}}
	b.indexNowPush(42, 7)
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		mu.Lock()
		done := len(got) > 0
		mu.Unlock()
		if done {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(got) != 1 || len(got[0]) != 2 {
		t.Fatalf("push batches = %v, want one batch of two", got)
	}
	if got[0][0] != "https://skin.test/skinlib/42" || got[0][1] != "https://skin.test/skinlib/7" {
		t.Fatalf("urls = %v, want /skinlib/{tid} detail pages", got[0])
	}
}

// A nil client (feature removed) or empty id list must be a no-op, not a
// panic in a request path.
func TestIndexNowPushNoopWithoutClient(t *testing.T) {
	var b Backend
	b.indexNowPush(1)
	b.indexNowPush()
}

// All three registered events (publish / visibility flip / delete) must route
// through the traced helper — a missing call site is how "off by default"
// turns into "never notifies".
func TestIndexNowWiringCoversAllThreeEvents(t *testing.T) {
	total := 0
	for _, name := range []string{"webapi.go", "skinlib.go"} {
		src, err := os.ReadFile(name)
		if err != nil {
			t.Fatal(err)
		}
		total += strings.Count(string(src), "b.indexNowPush(")
	}
	// upload + patch + delete
	if total != 3 {
		t.Fatalf("indexNowPush call sites = %d, want 3 (upload + visibility + delete)", total)
	}
}
