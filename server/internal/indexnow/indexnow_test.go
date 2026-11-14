package indexnow

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"
)

type capture struct {
	mu   sync.Mutex
	body [][]string
}

func (c *capture) handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var payload struct {
			Host    string   `json:"host"`
			Key     string   `json:"key"`
			URLList []string `json:"urlList"`
		}
		b, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(b, &payload)
		c.mu.Lock()
		c.body = append(c.body, payload.URLList)
		c.mu.Unlock()
		w.WriteHeader(http.StatusOK)
	})
}

func (c *capture) urls() [][]string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([][]string(nil), c.body...)
}

func activeClient(endpoint string) *Client {
	return &Client{
		Enabled:  func() bool { return true },
		Key:      "0123456789abcdef",
		Host:     "skins.example.com",
		Endpoint: endpoint,
	}
}

// With the feature off, nothing may leave the box — this is the promise that
// makes the default safe to ship.
func TestDisabledPushesNothing(t *testing.T) {
	var cap capture
	srv := httptest.NewServer(cap.handler())
	defer srv.Close()
	c := activeClient(srv.URL)
	c.Enabled = func() bool { return false }
	c.Push(context.Background(), []string{"https://skins.example.com/skinlib/1"})
	if got := cap.urls(); len(got) != 0 {
		t.Fatalf("disabled client sent %d requests, want 0", len(got))
	}
}

// The validation file exists only while the feature is on and the key is
// well-formed; a malformed key must not be published to the web.
func TestKeyFileOnlyWhileActive(t *testing.T) {
	c := activeClient("")
	if _, ok := c.KeyFile(); !ok {
		t.Fatal("active client with well-formed key should serve the key file")
	}
	c.Enabled = func() bool { return false }
	if _, ok := c.KeyFile(); ok {
		t.Fatal("disabled client must not serve the key file")
	}
	c.Enabled = func() bool { return true }
	c.Key = "short"
	if _, ok := c.KeyFile(); ok {
		t.Fatal("malformed key must not be served")
	}
}

// The protocol caps a request at 10 URLs; 25 must go out as 10/10/5.
func TestPushBatchesOfTen(t *testing.T) {
	var cap capture
	srv := httptest.NewServer(cap.handler())
	defer srv.Close()
	urls := make([]string, 25)
	for i := range urls {
		urls[i] = "https://skins.example.com/skinlib/" + string(rune('a'+i))
	}
	activeClient(srv.URL).Push(context.Background(), urls)
	got := cap.urls()
	if len(got) != 3 || len(got[0]) != 10 || len(got[1]) != 10 || len(got[2]) != 5 {
		t.Fatalf("batches = %v, want 10/10/5", got)
	}
}

// Engine-side failures are swallowed with a log line; a 500 must not panic
// and must not stop later batches.
func TestPushFailureIsSilent(t *testing.T) {
	var cap capture
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		cap.handler().ServeHTTP(w, r)
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()
	urls := make([]string, 15)
	for i := range urls {
		urls[i] = "https://skins.example.com/skinlib/1"
	}
	activeClient(srv.URL).Push(context.Background(), urls)
	if got := cap.urls(); len(got) != 2 {
		t.Fatalf("batches attempted = %d, want 2 despite failures", len(got))
	}
}

// The async wrapper delivers on its bounded goroutine.
func TestPushAsyncEventuallyDelivers(t *testing.T) {
	var cap capture
	srv := httptest.NewServer(cap.handler())
	defer srv.Close()
	activeClient(srv.URL).PushAsync([]string{"https://skins.example.com/skinlib/9"})
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if len(cap.urls()) > 0 {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("PushAsync delivered nothing within 2s")
}
