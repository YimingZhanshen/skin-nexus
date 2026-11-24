package main

import (
	"context"
	"io"
	"net"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"
)

func TestVerifyProxyCommandIsDispatchedBeforeServerConfig(t *testing.T) {
	raw, err := os.ReadFile("main.go")
	if err != nil {
		t.Fatal(err)
	}
	src := string(raw)
	dispatch := strings.Index(src, `os.Args[1] == "verify-proxy"`)
	configLoad := strings.Index(src, "cfg, err := config.Load()")
	if dispatch < 0 || configLoad < 0 || dispatch > configLoad {
		t.Fatalf("verify-proxy must dispatch before server config: dispatch=%d config=%d", dispatch, configLoad)
	}
	if !strings.Contains(src, "runVerifyProxy(context.Background(), os.Args[2:])") {
		t.Fatal("verify-proxy dispatch does not call its runner")
	}
}

func TestVerifyProxyCommandServesHealthWithoutServerConfigOrDatabase(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := listener.Addr().String()
	if err := listener.Close(); err != nil {
		t.Fatal(err)
	}
	t.Setenv("SKINNEXUS_VERIFY_PROXY_PUBLIC_ID", "srv_0123456789abcdefghijklmnopqrstuvwxyzABCD")
	t.Setenv("SKINNEXUS_VERIFY_PROXY_UPSTREAM", "http://127.0.0.1:1")
	t.Setenv("SKINNEXUS_VERIFY_PROXY_CREDENTIAL", "test-only-proxy-secret-0123456789abcdef")
	t.Setenv("SKINNEXUS_VERIFY_PROXY_CREDENTIAL_FILE", "")
	t.Setenv("SKINNEXUS_VERIFY_PROXY_CA_FILE", "")
	t.Setenv("SKINNEXUS_VERIFY_PROXY_LISTEN", addr)
	t.Setenv("SKIN_DATABASE_DSN", "not-a-database")
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- runVerifyProxy(ctx, nil) }()
	client := &http.Client{Timeout: 200 * time.Millisecond}
	deadline := time.Now().Add(5 * time.Second)
	for {
		resp, err := client.Get("http://" + addr + "/healthz")
		if err == nil {
			body, readErr := io.ReadAll(resp.Body)
			_ = resp.Body.Close()
			if readErr != nil || resp.StatusCode != http.StatusOK || string(body) != "ok\n" {
				t.Fatalf("health = %d %q %v", resp.StatusCode, body, readErr)
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("proxy listener did not become healthy: %v", err)
		}
		<-time.After(10 * time.Millisecond)
	}
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("proxy shutdown: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("proxy did not shut down")
	}
}

func TestVerifyProxyInvalidConfigFailsClosedWithoutSecretOutput(t *testing.T) {
	t.Setenv("SKINNEXUS_VERIFY_PROXY_PUBLIC_ID", "../invalid")
	t.Setenv("SKINNEXUS_VERIFY_PROXY_UPSTREAM", "https://upstream.example")
	t.Setenv("SKINNEXUS_VERIFY_PROXY_CREDENTIAL", "test-only-proxy-secret-0123456789abcdef")
	t.Setenv("SKINNEXUS_VERIFY_PROXY_CREDENTIAL_FILE", "")
	t.Setenv("SKIN_DATABASE_DSN", "not-a-database")
	err := runVerifyProxy(context.Background(), nil)
	if err == nil {
		t.Fatal("invalid proxy configuration was accepted")
	}
	if strings.Contains(err.Error(), "test-only-proxy-secret") || strings.Contains(err.Error(), "not-a-database") {
		t.Fatalf("proxy error leaked configuration: %v", err)
	}
}
