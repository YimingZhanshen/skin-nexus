package ygg

import (
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"
)

// Phase-2 additive tests (frozen contract assertions in http_test.go stay
// untouched): metadata exposes feature.openid_configuration_url only when
// configured (analysis/oidc-janus-contract.md §6 item 1).

func TestMetadataOpenIDConfigURL(t *testing.T) {
	api, _ := newTestAPI(t)
	rec := do(t, api.Handler(), "GET", "/api/yggdrasil/", "", nil)
	var doc map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &doc); err != nil {
		t.Fatal(err)
	}
	meta, _ := doc["meta"].(map[string]any)
	if _, ok := meta["feature.openid_configuration_url"]; ok {
		t.Fatalf("feature.openid_configuration_url must be absent when unconfigured")
	}

	api2, _ := newTestAPI(t)
	api2.cfg.OpenIDConfigurationURL = "https://skin.test/api/yggdrasil/.well-known/openid-configuration"
	rec2 := do(t, api2.Handler(), "GET", "/api/yggdrasil/", "", nil)
	var doc2 map[string]any
	if err := json.Unmarshal(rec2.Body.Bytes(), &doc2); err != nil {
		t.Fatal(err)
	}
	meta2, _ := doc2["meta"].(map[string]any)
	got, _ := meta2["feature.openid_configuration_url"].(string)
	if !strings.HasSuffix(got, "/.well-known/openid-configuration") {
		t.Fatalf("feature.openid_configuration_url = %q", got)
	}
}

// TestJoinRejectsForeignPlayer pins the ownership gate added with the
// dual-state token work: a token may only join as a player it owns and that
// matches its selected profile (legacy player.not-match semantics).
func TestJoinRejectsForeignPlayer(t *testing.T) {
	api, st := newTestAPI(t)
	// Issue a token for user 1 (player "Steve" id 1 per memStore fixture).
	rec := do(t, api.Handler(), "POST", "/api/yggdrasil/authserver/authenticate",
		`{"username":"steve","password":"pw-ok"}`, map[string]string{"Content-Type": "application/json"})
	var tok struct {
		AccessToken string `json:"accessToken"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &tok); err != nil {
		t.Fatal(err)
	}
	if tok.AccessToken == "" {
		t.Fatalf("authenticate failed: %s", rec.Body.String())
	}
	_ = st

	reqBody := `{"accessToken":"` + tok.AccessToken + `","selectedProfile":{"id":"ffffccccddddeeeebbbbaaaa00001111"},"serverId":"x"}`
	jr := do(t, api.Handler(), "POST", "/api/yggdrasil/sessionserver/session/minecraft/join",
		reqBody, map[string]string{"Content-Type": "application/json"})
	// Live-captured legacy semantics (2026-10-23): join rejects a foreign or
	// unbound profile with 403 ForbiddenOperationException, not a silent 204.
	if jr.Code != 403 {
		t.Fatalf("join status %d (want 403 player.not-match)", jr.Code)
	}
	if !strings.Contains(jr.Body.String(), "ForbiddenOperationException") {
		t.Fatalf("join error body = %s", jr.Body.String())
	}
	// A join as a foreign uuid must not create a live session.
	hr := do(t, api.Handler(), "GET", "/api/yggdrasil/sessionserver/session/minecraft/hasJoined?username=Steve&serverId=x", "", nil)
	if hr.Code != 204 {
		t.Fatalf("hasJoined after foreign join = %d (want 204)", hr.Code)
	}
}

// TestJoinAcceptsWireForms pins the tolerant selectedProfile parsing required
// by real clients: authlib-injector sends a bare UUID string while Yggdrasil
// refresh sends {"id": ...}; both UUID spellings must work (live 2026-10-23).
func TestJoinAcceptsWireForms(t *testing.T) {
	api, _ := newTestAPI(t)
	rec := do(t, api.Handler(), "POST", "/api/yggdrasil/authserver/authenticate",
		`{"username":"steve","password":"pw-ok","clientToken":"ct"}`, map[string]string{"Content-Type": "application/json"})
	var tok struct {
		AccessToken string `json:"accessToken"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &tok); err != nil {
		t.Fatal(err)
	}
	if tok.AccessToken == "" {
		t.Fatalf("authenticate failed: %s", rec.Body.String())
	}
	for i, body := range []string{
		`{"accessToken":"` + tok.AccessToken + `","selectedProfile":"` + testUUID + `","serverId":"w1"}`,
		`{"accessToken":"` + tok.AccessToken + `","selectedProfile":"069a79f4-44e9-4726-a5be-fca90e38aaf5","serverId":"w2"}`,
	} {
		jr := do(t, api.Handler(), "POST", "/api/yggdrasil/sessionserver/session/minecraft/join",
			body, map[string]string{"Content-Type": "application/json"})
		if jr.Code != 204 {
			t.Fatalf("wire form %d: status %d (want 204) body %s", i, jr.Code, jr.Body.String())
		}
	}
	// missing selectedProfile is a parameter error (legacy 400 wording)
	mr := do(t, api.Handler(), "POST", "/api/yggdrasil/sessionserver/session/minecraft/join",
		`{"accessToken":"`+tok.AccessToken+`","serverId":"w3"}`, map[string]string{"Content-Type": "application/json"})
	if mr.Code != 400 {
		t.Fatalf("missing selectedProfile = %d (want 400)", mr.Code)
	}
}

// TestMetadataSkinDomainsFormula pins the live legacy whitelist formula:
// config ∪ union hosts ∪ site_url host ∪ request host, deduplicated.
func TestMetadataSkinDomainsFormula(t *testing.T) {
	api, _ := newTestAPI(t)
	api.cfg.SkinDomains = []string{"skin.example.com"}
	api.cfg.Homepage = "https://site.example"
	api.cfg.ExtraSkinDomains = func() []string {
		return []string{"skin.union-a.example", "skin.example.com", "site.example:8443"}
	}
	rec := do(t, api.Handler(), "GET", "/api/yggdrasil/", "", nil)
	var doc struct {
		SkinDomains []string `json:"skinDomains"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &doc); err != nil {
		t.Fatal(err)
	}
	want := map[string]bool{
		"skin.example.com":     true,
		"skin.union-a.example": true,
		"site.example:8443":    true,
		"site.example":         true,
		"example.com":          true, // httptest request host
	}
	if len(doc.SkinDomains) != len(want) {
		t.Fatalf("skinDomains = %v (want %d unique)", doc.SkinDomains, len(want))
	}
	for _, d := range doc.SkinDomains {
		if !want[d] {
			t.Fatalf("unexpected domain %q in %v", d, doc.SkinDomains)
		}
	}
}

// TestJoinEmailVerificationGate pins the configurable require_verification
// gate: default off (no mail flow yet — gate would dead-end every new user),
// on blocks unverified users like the fork did.
func TestJoinEmailVerificationGate(t *testing.T) {
	api, st := newTestAPI(t)
	rec := do(t, api.Handler(), "POST", "/api/yggdrasil/authserver/authenticate",
		`{"username":"steve","password":"pw-ok","clientToken":"ct"}`, map[string]string{"Content-Type": "application/json"})
	var tok struct {
		AccessToken string `json:"accessToken"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &tok); err != nil {
		t.Fatal(err)
	}
	u := st.byID[1]
	u.EmailVerified = false
	st.byID[1] = u

	jr := do(t, api.Handler(), "POST", "/api/yggdrasil/sessionserver/session/minecraft/join",
		`{"accessToken":"`+tok.AccessToken+`","selectedProfile":"`+testUUID+`","serverId":"g1"}`,
		map[string]string{"Content-Type": "application/json"})
	if jr.Code != 204 {
		t.Fatalf("gate off: join = %d (want 204)", jr.Code)
	}
	api.svc.RequireVerification = true
	jr2 := do(t, api.Handler(), "POST", "/api/yggdrasil/sessionserver/session/minecraft/join",
		`{"accessToken":"`+tok.AccessToken+`","selectedProfile":"`+testUUID+`","serverId":"g2"}`,
		map[string]string{"Content-Type": "application/json"})
	if jr2.Code != 403 || !strings.Contains(jr2.Body.String(), "Email not verified") {
		t.Fatalf("gate on: join = %d %s (want 403 email gate)", jr2.Code, jr2.Body.String())
	}
}

var _ = httptest.NewRecorder
