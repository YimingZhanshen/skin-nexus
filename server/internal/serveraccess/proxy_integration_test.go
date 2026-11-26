package serveraccess

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"skin-nexus/server/internal/store"
	"skin-nexus/server/internal/verifyproxy"
	"skin-nexus/server/internal/ygg"
)

func TestProxyAndURLSecretProduceIdenticalPolicyDecisionsAndEvents(t *testing.T) {
	for _, scenario := range []string{"local allow", "whitelist deny", "official fallback", "quota deny"} {
		t.Run(scenario, func(t *testing.T) {
			type observation struct {
				status      int
				event       store.SAJoinEvent
				profileID   string
				profileName string
			}
			run := func(mode string) observation {
				f := newVerifyFixture(t)
				f.credential(t, mode)
				switch scenario {
				case "local allow":
					f.playerAndJoin(t)
				case "whitelist deny":
					enabled := true
					if err := f.st.UpdateSAServerOwnerPolicy(context.Background(), f.tenant, f.server, &enabled, nil); err != nil {
						t.Fatal(err)
					}
				case "official fallback":
					enabled := true
					if err := f.st.UpdateSAServerOwnerPolicy(context.Background(), f.tenant, f.server, nil, &enabled); err != nil {
						t.Fatal(err)
					}
					f.api.SetFallback(&ygg.Fallback{BaseURL: "https://official.example", Enabled: func(context.Context) bool { return true }, Client: &http.Client{Transport: verifyFallback(func(*http.Request) (*http.Response, error) {
						return verifyFallbackResponse(`{"id":"fedcba9876543210fedcba9876543210","name":"Official"}`), nil
					})}})
				case "quota deny":
					limit := 1
					if err := f.st.UpdateSAServerPlatformPolicy(context.Background(), f.tenant, f.server, &limit, nil, nil, nil, nil); err != nil {
						t.Fatal(err)
					}
					window := f.now.UTC().Truncate(time.Minute)
					if _, allowed, err := f.st.AdmitSAQuota(context.Background(), f.server, window, "verify", limit, f.now); err != nil || !allowed {
						t.Fatalf("quota seed allowed=%v err=%v", allowed, err)
					}
					if _, allowed, err := f.st.AdmitSAQuota(context.Background(), f.server, window, "verify", limit, f.now); err != nil || allowed {
						t.Fatalf("quota exhaustion allowed=%v err=%v", allowed, err)
					}
				}
				name := "Unknown"
				if scenario == "local allow" || scenario == "quota deny" {
					name = "Alice"
				}
				var rec *httptest.ResponseRecorder
				if mode == "url_secret" {
					rec = f.verify(t, mode, name, "server-id", "203.0.113.8")
				} else {
					upstream := httptest.NewServer(f.h)
					defer upstream.Close()
					handler, err := verifyproxy.NewHandler(verifyproxy.Config{PublicID: "srv_one", Upstream: upstream.URL, Credential: f.secret,
						Listen: "127.0.0.1:25565", ReadTimeout: time.Second, WriteTimeout: time.Second, HeaderTimeout: time.Second,
						RequestTimeout: time.Second, MaxBodyBytes: 1024, MaxHeaderBytes: 16384, Retries: 0, RetryBackoff: time.Millisecond}, upstream.Client())
					if err != nil {
						t.Fatal(err)
					}
					rec = httptest.NewRecorder()
					handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/sessionserver/session/minecraft/hasJoined?username="+name+"&serverId=server-id&ip=203.0.113.8", nil))
				}
				events, err := f.st.ListSAJoinEvents(context.Background(), f.tenant, f.server, f.now.Add(-time.Minute), f.now.Add(time.Minute), 20)
				if err != nil || len(events) != 1 {
					t.Fatalf("events = %+v, %v", events, err)
				}
				out := observation{status: rec.Code, event: events[0]}
				if rec.Code == http.StatusOK {
					var profile ygg.Profile
					if err := json.Unmarshal(rec.Body.Bytes(), &profile); err != nil {
						t.Fatal(err)
					}
					out.profileID, out.profileName = profile.ID, profile.Name
				}
				return out
			}
			direct, proxied := run("url_secret"), run("header")
			if direct.status != proxied.status || direct.profileID != proxied.profileID || direct.profileName != proxied.profileName {
				t.Fatalf("wire decision differs: direct=%+v proxy=%+v", direct, proxied)
			}
			a, b := direct.event, proxied.event
			if a.Outcome != b.Outcome || a.ReasonCode != b.ReasonCode || a.UUID != b.UUID || a.PlayerName != b.PlayerName || a.UID.Valid != b.UID.Valid || a.UID.Int64 != b.UID.Int64 {
				t.Fatalf("event policy differs: direct=%+v proxy=%+v", a, b)
			}
			if a.SourceMode != "url_secret" || b.SourceMode != "header" {
				t.Fatalf("credential provenance changed: direct=%q proxy=%q", a.SourceMode, b.SourceMode)
			}
		})
	}
}

func TestProxyHonorsCredentialRevocationAndServerSuspensionImmediately(t *testing.T) {
	f := newVerifyFixture(t)
	credentialID := f.credential(t, "header")
	f.playerAndJoin(t)
	upstream := httptest.NewServer(f.h)
	defer upstream.Close()
	handler, err := verifyproxy.NewHandler(verifyproxy.Config{PublicID: "srv_one", Upstream: upstream.URL, Credential: f.secret,
		Listen: "127.0.0.1:25565", ReadTimeout: time.Second, WriteTimeout: time.Second, HeaderTimeout: time.Second,
		RequestTimeout: time.Second, MaxBodyBytes: 1024, MaxHeaderBytes: 16384, Retries: 2, RetryBackoff: time.Millisecond}, upstream.Client())
	if err != nil {
		t.Fatal(err)
	}
	request := func() int {
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/sessionserver/session/minecraft/hasJoined?username=Alice&serverId=server-id&ip=203.0.113.8", nil))
		return rec.Code
	}
	if request() != http.StatusOK {
		t.Fatal("initial proxy verification failed")
	}
	if err := f.st.RevokeSAServerCredential(context.Background(), f.tenant, f.server, credentialID, f.now); err != nil {
		t.Fatal(err)
	}
	if request() != http.StatusNoContent {
		t.Fatal("revoked credential remained usable through proxy")
	}
	f.secret = "replacement-only-secret-0123456789abcdef"
	if _, err := f.st.CreateSAServerCredential(context.Background(), f.tenant, f.server, store.SACredential{
		Mode: "header", SecretHash: saHash(f.secret), Scopes: `["verify:join"]`, Status: "active",
	}); err != nil {
		t.Fatal(err)
	}
	// Rebuild the proxy with the replacement credential, then suspend the server.
	handler, err = verifyproxy.NewHandler(verifyproxy.Config{PublicID: "srv_one", Upstream: upstream.URL, Credential: f.secret,
		Listen: "127.0.0.1:25565", ReadTimeout: time.Second, WriteTimeout: time.Second, HeaderTimeout: time.Second,
		RequestTimeout: time.Second, MaxBodyBytes: 1024, MaxHeaderBytes: 16384, Retries: 2, RetryBackoff: time.Millisecond}, upstream.Client())
	if err != nil {
		t.Fatal(err)
	}
	if request() != http.StatusOK {
		t.Fatal("replacement credential failed before suspension")
	}
	if err := f.st.UpdateSAServer(context.Background(), f.tenant, f.server, "one", "suspended"); err != nil {
		t.Fatal(err)
	}
	if request() != http.StatusNoContent {
		t.Fatal("suspended server remained usable through proxy")
	}
}
