package verifyproxy

import (
	"encoding/pem"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestCredentialFilePermissionsAndTLSValidation(t *testing.T) {
	dir := t.TempDir()
	secretPath := filepath.Join(dir, "credential")
	if err := os.WriteFile(secretPath, []byte(testCredential+"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	env := map[string]string{
		"SKINNEXUS_VERIFY_PROXY_PUBLIC_ID":       "srv_0123456789abcdefghijklmnopqrstuvwxyzABCD",
		"SKINNEXUS_VERIFY_PROXY_UPSTREAM":        "https://skin.example.com",
		"SKINNEXUS_VERIFY_PROXY_CREDENTIAL_FILE": secretPath,
	}
	cfg, err := LoadConfig(nil, env)
	if err != nil || cfg.Credential != testCredential {
		t.Fatalf("credential file config = %+v, %v", cfg, err)
	}
	if runtime.GOOS != "windows" {
		if err := os.Chmod(secretPath, 0644); err != nil {
			t.Fatal(err)
		}
		if _, err := LoadConfig(nil, env); err == nil {
			t.Fatal("group/world-readable credential file was accepted")
		}
	}

	upstream := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"id":"0123456789abcdef0123456789abcdef","name":"Alice"}`))
	}))
	defer upstream.Close()
	cfg.Upstream, cfg.CAFile, cfg.CredentialFile = upstream.URL, "", ""
	if _, err := NewHTTPClient(cfg); err != nil {
		t.Fatalf("default client config: %v", err)
	}
	untrusted, err := NewHTTPClient(cfg)
	if err != nil {
		t.Fatal(err)
	}
	handler, err := NewHandler(cfg, untrusted)
	if err != nil {
		t.Fatal(err)
	}
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/sessionserver/session/minecraft/hasJoined?username=Alice&serverId=server-id", nil))
	if rec.Code != http.StatusNoContent || rec.Body.Len() != 0 {
		t.Fatalf("untrusted TLS did not fail closed: %d %s", rec.Code, rec.Body.String())
	}

	caPath := filepath.Join(dir, "upstream-ca.pem")
	caPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: upstream.Certificate().Raw})
	if err := os.WriteFile(caPath, caPEM, 0600); err != nil {
		t.Fatal(err)
	}
	cfg.CAFile = caPath
	trusted, err := NewHTTPClient(cfg)
	if err != nil {
		t.Fatal(err)
	}
	handler, err = NewHandler(cfg, trusted)
	if err != nil {
		t.Fatal(err)
	}
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/sessionserver/session/minecraft/hasJoined?username=Alice&serverId=server-id", nil))
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"name":"Alice"`) {
		t.Fatalf("trusted TLS = %d %s", rec.Code, rec.Body.String())
	}
}

func TestProxyServerAppliesLocalTimeoutAndHeaderLimits(t *testing.T) {
	server, err := NewServer(proxyTestConfig(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if server.Addr != proxyTestConfig().Listen || server.ReadTimeout != 5*time.Second || server.WriteTimeout != 5*time.Second || server.ReadHeaderTimeout != 5*time.Second || server.MaxHeaderBytes != 16384 {
		t.Fatalf("proxy server settings = %+v", server)
	}
	if server.Handler == nil {
		t.Fatal("proxy server handler is missing")
	}
}
