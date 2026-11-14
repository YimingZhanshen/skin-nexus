// Package indexnow pings search engines when a public page changes, via the
// IndexNow protocol. Disabled by default: enabling submits site URLs to
// Microsoft/Bing, so the operator opts in from the settings panel — the env
// variable only seeds the default.
package indexnow

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"regexp"
	"time"
)

// DefaultEndpoint is the protocol's submission host.
const DefaultEndpoint = "https://api.indexnow.org/indexnow"

// MaxBatch is the protocol's per-request URL cap.
const MaxBatch = 10

// KeyShape is the protocol's key format: 8-128 characters of [a-zA-Z0-9-_].
var KeyShape = regexp.MustCompile(`^[a-zA-Z0-9\-_]{8,128}$`)

// Client is safe for concurrent use. Enabled resolves per call so the
// settings-panel toggle applies without a restart.
type Client struct {
	Enabled  func() bool
	Key      string
	Host     string // site hostname, the "host" field per protocol
	Endpoint string // test seam; empty = DefaultEndpoint
	HTTP     *http.Client
}

func (c *Client) active() bool {
	return c.Enabled != nil && c.Enabled() && KeyShape.MatchString(c.Key)
}

// KeyFile reports the validation-file body and whether it should be served:
// only while the feature is on and the key is well-formed.
func (c *Client) KeyFile() (string, bool) {
	if !c.active() {
		return "", false
	}
	return c.Key, true
}

// Push submits URLs in batches of at most MaxBatch. Failures are logged and
// never surfaced: search-engine notification is best-effort, not user work.
func (c *Client) Push(ctx context.Context, urls []string) {
	if !c.active() {
		return
	}
	endpoint := c.Endpoint
	if endpoint == "" {
		endpoint = DefaultEndpoint
	}
	httpc := c.HTTP
	if httpc == nil {
		httpc = &http.Client{Timeout: 10 * time.Second}
	}
	for start := 0; start < len(urls); start += MaxBatch {
		end := start + MaxBatch
		if end > len(urls) {
			end = len(urls)
		}
		body, _ := json.Marshal(map[string]any{"host": c.Host, "key": c.Key, "urlList": urls[start:end]})
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
		if err != nil {
			slog.Warn("indexnow: build request", "error", err)
			continue
		}
		req.Header.Set("Content-Type", "application/json; charset=utf-8")
		resp, err := httpc.Do(req)
		if err != nil {
			slog.Warn("indexnow: submit", "error", err)
			continue
		}
		_ = resp.Body.Close()
		if resp.StatusCode >= 300 {
			slog.Warn("indexnow: submit", "status", resp.StatusCode)
		}
	}
}

// PushAsync fires Push on a bounded background goroutine — request handlers
// must not wait on a search engine.
func (c *Client) PushAsync(urls []string) {
	if !c.active() {
		return
	}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		c.Push(ctx, urls)
	}()
}
