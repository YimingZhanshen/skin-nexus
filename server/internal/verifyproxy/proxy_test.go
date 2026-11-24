package verifyproxy

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

const testCredential = "test-only-proxy-credential-0123456789abcdef"

func TestLoadConfigRejectsInvalidOrAmbiguousConfiguration(t *testing.T) {
	valid := map[string]string{
		"SKINNEXUS_VERIFY_PROXY_PUBLIC_ID":  "srv_0123456789abcdefghijklmnopqrstuvwxyzABCD",
		"SKINNEXUS_VERIFY_PROXY_UPSTREAM":   "https://skin.example.com",
		"SKINNEXUS_VERIFY_PROXY_CREDENTIAL": testCredential,
	}
	for _, tc := range []struct {
		name string
		env  map[string]string
		args []string
	}{
		{name: "missing public id", env: map[string]string{"SKINNEXUS_VERIFY_PROXY_UPSTREAM": valid["SKINNEXUS_VERIFY_PROXY_UPSTREAM"], "SKINNEXUS_VERIFY_PROXY_CREDENTIAL": testCredential}},
		{name: "path public id", env: map[string]string{"SKINNEXUS_VERIFY_PROXY_PUBLIC_ID": "../srv_bad", "SKINNEXUS_VERIFY_PROXY_UPSTREAM": valid["SKINNEXUS_VERIFY_PROXY_UPSTREAM"], "SKINNEXUS_VERIFY_PROXY_CREDENTIAL": testCredential}},
		{name: "upstream query", env: map[string]string{"SKINNEXUS_VERIFY_PROXY_PUBLIC_ID": valid["SKINNEXUS_VERIFY_PROXY_PUBLIC_ID"], "SKINNEXUS_VERIFY_PROXY_UPSTREAM": "https://skin.example.com?token=hidden", "SKINNEXUS_VERIFY_PROXY_CREDENTIAL": testCredential}},
		{name: "upstream userinfo", env: map[string]string{"SKINNEXUS_VERIFY_PROXY_PUBLIC_ID": valid["SKINNEXUS_VERIFY_PROXY_PUBLIC_ID"], "SKINNEXUS_VERIFY_PROXY_UPSTREAM": "https://user:pass@skin.example.com", "SKINNEXUS_VERIFY_PROXY_CREDENTIAL": testCredential}},
		{name: "short credential", env: map[string]string{"SKINNEXUS_VERIFY_PROXY_PUBLIC_ID": valid["SKINNEXUS_VERIFY_PROXY_PUBLIC_ID"], "SKINNEXUS_VERIFY_PROXY_UPSTREAM": valid["SKINNEXUS_VERIFY_PROXY_UPSTREAM"], "SKINNEXUS_VERIFY_PROXY_CREDENTIAL": "short"}},
		{name: "ambiguous credential", env: map[string]string{"SKINNEXUS_VERIFY_PROXY_PUBLIC_ID": valid["SKINNEXUS_VERIFY_PROXY_PUBLIC_ID"], "SKINNEXUS_VERIFY_PROXY_UPSTREAM": valid["SKINNEXUS_VERIFY_PROXY_UPSTREAM"], "SKINNEXUS_VERIFY_PROXY_CREDENTIAL": testCredential, "SKINNEXUS_VERIFY_PROXY_CREDENTIAL_FILE": "/tmp/secret"}},
		{name: "negative retries", env: valid, args: []string{"--retries=-1"}},
		{name: "unknown flag", env: valid, args: []string{"--unknown=value"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := LoadConfig(tc.args, tc.env)
			if err == nil {
				t.Fatal("expected invalid configuration")
			}
			if strings.Contains(err.Error(), testCredential) || strings.Contains(err.Error(), "user:pass") {
				t.Fatalf("configuration error leaked a secret: %v", err)
			}
		})
	}
}

func TestProxyHealthAndHeaderInjection(t *testing.T) {
	var got *http.Request
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = r
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"id":"0123456789abcdef0123456789abcdef","name":"Alice"}`)
	}))
	defer upstream.Close()
	cfg := Config{PublicID: "srv_0123456789abcdefghijklmnopqrstuvwxyzABCD", Upstream: upstream.URL, Credential: testCredential,
		Listen: "127.0.0.1:25565", ReadTimeout: 5 * time.Second, WriteTimeout: 5 * time.Second, HeaderTimeout: 5 * time.Second, RequestTimeout: 5 * time.Second,
		MaxBodyBytes: 1024, MaxHeaderBytes: 16384, Retries: 2, RetryBackoff: 1}
	handler, err := NewHandler(cfg, upstream.Client())
	if err != nil {
		t.Fatal(err)
	}
	health := httptest.NewRecorder()
	handler.ServeHTTP(health, httptest.NewRequest(http.MethodGet, "/healthz", nil))
	if health.Code != http.StatusOK || health.Body.String() != "ok\n" {
		t.Fatalf("health = %d %q", health.Code, health.Body.String())
	}
	if health.Header().Get("Cache-Control") != "no-store" || health.Header().Get("Referrer-Policy") != "no-referrer" {
		t.Fatalf("health headers = %v", health.Header())
	}

	req := httptest.NewRequest(http.MethodGet, "/sessionserver/session/minecraft/hasJoined?username=Alice&serverId=server-id&ip=203.0.113.8&ignored=secret", nil)
	req.Header.Set("Authorization", "Bearer incoming-secret")
	req.Header.Set("Cookie", "incoming=secret")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK || rec.Body.String() != `{"id":"0123456789abcdef0123456789abcdef","name":"Alice"}` {
		t.Fatalf("hasJoined = %d %s", rec.Code, rec.Body.String())
	}
	if got == nil || got.URL.Path != "/api/serveraccess/v1/servers/"+cfg.PublicID+"/hasJoined" || got.URL.Query().Get("ignored") != "" {
		t.Fatalf("upstream request = %+v", got)
	}
	if got.Header.Get("Authorization") != "Bearer "+testCredential || got.Header.Get("Cookie") != "" {
		t.Fatalf("upstream credential headers = %v", got.Header)
	}
	if got.URL.Query().Get("username") != "Alice" || got.URL.Query().Get("serverId") != "server-id" || got.URL.Query().Get("ip") != "203.0.113.8" {
		t.Fatalf("upstream query = %v", got.URL.Query())
	}
	if rec.Header().Get("X-Request-Id") == "" || got.Header.Get("X-Request-Id") != rec.Header().Get("X-Request-Id") {
		t.Fatalf("request correlation local=%q upstream=%q", rec.Header().Get("X-Request-Id"), got.Header.Get("X-Request-Id"))
	}
}
