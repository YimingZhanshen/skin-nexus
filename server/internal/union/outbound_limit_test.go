package union

// The hub is an operator-configured upstream, and an upstream that streams
// forever used to be enough: every hub response was read with a bare
// io.ReadAll, so the only bound was the 5s per-call context — and five seconds
// of a fast pipe is hundreds of megabytes held in memory. The rest of the repo
// caps its reads (internal/outbound, ygg/fallback); this closes the last one.

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func hubServer(t *testing.T, handler http.HandlerFunc) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	return srv
}

func TestOutboundCapsTheJSONResponseBody(t *testing.T) {
	cases := []struct {
		name     string
		declare  bool // set Content-Length so the header check is what rejects
		sizeByte int
	}{
		{"streamed past the cap with no length header", false, maxResponseBytes + 1},
		{"declares a length over the cap", true, maxResponseBytes + 1},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			srv := hubServer(t, func(w http.ResponseWriter, r *http.Request) {
				body := strings.Repeat("a", c.sizeByte)
				if c.declare {
					w.Header().Set("Content-Length", fmt.Sprint(len(body)))
				}
				_, _ = w.Write([]byte(body))
			})
			o, err := NewOutbound(Config{Enabled: true, ApiRoot: srv.URL, MemberKey: "mk"}, nil)
			if err != nil {
				t.Fatalf("build client: %v", err)
			}
			_, _, err = o.doJSONInner(context.Background(), http.MethodGet, "/x", nil, nil)
			if err == nil {
				t.Fatalf("an over-cap hub response was accepted — the read is unbounded again")
			}
			if !strings.Contains(err.Error(), "exceeds") {
				t.Errorf("err = %v, want it to name the cap so an operator can see why the sync failed", err)
			}
		})
	}
}

// The cap must bound, not reshape: a legitimate hub answer a little under it
// still has to arrive whole and still has to decode.
func TestOutboundStillDeliversAResponseUnderTheCap(t *testing.T) {
	var out struct {
		OK   bool   `json:"ok"`
		Name string `json:"name"`
	}
	srv := hubServer(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"ok":true,"name":"hub-a"}`))
	})
	o, err := NewOutbound(Config{Enabled: true, ApiRoot: srv.URL, MemberKey: "mk"}, nil)
	if err != nil {
		t.Fatalf("build client: %v", err)
	}
	if _, _, err := o.doJSONInner(context.Background(), http.MethodGet, "/x", nil, &out); err != nil {
		t.Fatalf("small response: %v", err)
	}
	if !out.OK || out.Name != "hub-a" {
		t.Errorf("decoded = %+v, want the hub's payload intact", out)
	}
}

// The relay path passes whatever the hub said back to the member's admin UI. It
// gets the same ceiling, but the assertion here is the opposite direction: a
// multi-megabyte blacklist export is real traffic for this route, and silently
// truncating it would corrupt the data the panel renders.
func TestOutboundRelayPassesALargeButLegalBody(t *testing.T) {
	const size = 3 << 20 // 3 MiB: well over the JSON API's usual answers, under the ceiling
	srv := hubServer(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(strings.Repeat("b", size)))
	})
	o, err := NewOutbound(Config{Enabled: true, ApiRoot: srv.URL, MemberKey: "mk"}, nil)
	if err != nil {
		t.Fatalf("build client: %v", err)
	}
	res, err := o.doProxyInner(context.Background(), http.MethodGet, "/relay", nil)
	if err != nil {
		t.Fatalf("relay of a legal body: %v", err)
	}
	if len(res.Body) != size {
		t.Errorf("relayed %d bytes, want %d — the cap must not truncate passthrough", len(res.Body), size)
	}

	big := hubServer(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(strings.Repeat("c", maxResponseBytes+1)))
	})
	o2, err := NewOutbound(Config{Enabled: true, ApiRoot: big.URL, MemberKey: "mk"}, nil)
	if err != nil {
		t.Fatalf("build client: %v", err)
	}
	if _, err := o2.doProxyInner(context.Background(), http.MethodGet, "/relay", nil); err == nil {
		t.Error("an over-cap relay body was accepted")
	} else if !strings.Contains(err.Error(), "exceeds") {
		t.Errorf("err = %v, want it to name the cap", err)
	}
}
