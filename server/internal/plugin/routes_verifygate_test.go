package plugin

// routes_verifygate_test.go — the email-verification gate must reach plugin
// routes that act as the signed-in player, not just /api/v1/.
//
// The web gate (webapi.verifyWebGate) wraps the /api/v1 subtree only. Plugin
// routes live under /api/plugins/ and resolve their own session identity, so a
// pending account could still read/write players and textures through the
// account bridge (which is tier-agnostic) while the web centre refused it. The
// host therefore consumes the SAME verdict function webapi/adminapi use; these
// tests pin which tiers it applies to and which stay open.

import (
	"context"
	"net/http"
	"os"
	"strings"
	"testing"
)

func sessionCookie(token string) *http.Cookie {
	return &http.Cookie{Name: "skin_session", Value: token}
}

// pendingFor returns a verdict function marking exactly the given uids pending,
// standing in for webapi.VerifyPending (which itself exempts super admins).
func pendingFor(uids ...int64) func(context.Context, int64) bool {
	return func(_ context.Context, uid int64) bool {
		for _, u := range uids {
			if u == uid {
				return true
			}
		}
		return false
	}
}

// Session-tier routes refuse a pending account with the shared stable code, so
// a consumer recognises the same refusal it gets from /api/v1/.
func TestPluginSessionTiersRefusePendingAccount(t *testing.T) {
	m := newRoutesHost(t)
	m.SetVerifyGate(pendingFor(5, 6))

	for _, target := range []string{"/api/plugins/routes/me", "/api/plugins/routes/item/7"} {
		rec := doPluginReq(m, "GET", target, "", sessionCookie("user-token"))
		if rec.Code != http.StatusForbidden {
			t.Fatalf("GET %s pending = %d %s, want 403", target, rec.Code, rec.Body.String())
		}
		if !strings.Contains(rec.Body.String(), "auth.email_verify_required") {
			t.Fatalf("GET %s body %s should carry the shared gate code", target, rec.Body.String())
		}
	}
	// permission=1 staff is pending like any player; only the injected verdict
	// (which exempts super admins in production) decides otherwise.
	if rec := doPluginReq(m, "GET", "/api/plugins/routes/admin", "", sessionCookie("admin-token")); rec.Code != http.StatusForbidden {
		t.Fatalf("pending admin = %d, want 403", rec.Code)
	}
	if rec := doPluginReq(m, "GET", "/api/plugins/routes/super", "", sessionCookie("super-token")); rec.Code != http.StatusOK {
		t.Fatalf("non-pending super = %d %s, want 200", rec.Code, rec.Body.String())
	}
}

func TestPluginVerifiedAccountPasses(t *testing.T) {
	m := newRoutesHost(t)
	m.SetVerifyGate(pendingFor()) // nobody pending
	if rec := doPluginReq(m, "GET", "/api/plugins/routes/me", "", sessionCookie("user-token")); rec.Code != http.StatusOK {
		t.Fatalf("verified session = %d %s, want 200", rec.Code, rec.Body.String())
	}
	if rec := doPluginReq(m, "GET", "/api/plugins/routes/admin", "", sessionCookie("admin-token")); rec.Code != http.StatusOK {
		t.Fatalf("verified admin = %d, want 200", rec.Code)
	}
}

// No verdict wired = gate off = byte-for-byte the shipped behaviour.
func TestPluginGateOffByDefault(t *testing.T) {
	m := newRoutesHost(t)
	if rec := doPluginReq(m, "GET", "/api/plugins/routes/me", "", sessionCookie("user-token")); rec.Code != http.StatusOK {
		t.Fatalf("gate off session = %d %s, want 200", rec.Code, rec.Body.String())
	}
}

// Public content and machine (bearer) clients are not "the player browsing";
// gating them would break launcher integrations and anonymous plugin pages.
func TestPluginPublicAndBearerIgnoreGate(t *testing.T) {
	m := newRoutesHost(t)
	m.SetVerifyGate(func(context.Context, int64) bool { return true })
	if rec := doPluginReq(m, "GET", "/api/plugins/routes/ping", ""); rec.Code != http.StatusOK {
		t.Fatalf("public with gate on = %d, want 200", rec.Code)
	}
	if rec := doPluginReq(m, "GET", "/api/plugins/routes/files/a/b", ""); rec.Code != http.StatusOK {
		t.Fatalf("public wildcard with gate on = %d, want 200", rec.Code)
	}

	b := newBearerHost(t, fakeBearerResolver{})
	b.SetVerifyGate(func(context.Context, int64) bool { return true })
	if rec := bearerReq(b, "Bearer good", `{}`); rec.Code != http.StatusOK {
		t.Fatalf("bearer with gate on = %d %s, want 200", rec.Code, rec.Body.String())
	}
}

// The CSRF bootstrap endpoint must stay reachable or a pending player cannot
// even obtain the token needed for the self-service resend/change-email calls.
func TestPluginCSRFEndpointIgnoresGate(t *testing.T) {
	m := newRoutesHost(t)
	m.SetVerifyGate(func(context.Context, int64) bool { return true })
	if rec := doPluginReq(m, "GET", "/api/plugins/routes/_csrf", "", sessionCookie("user-token")); rec.Code != http.StatusOK {
		t.Fatalf("_csrf with gate on = %d %s, want 200", rec.Code, rec.Body.String())
	}
}

// Placement gate: the verdict must run after the session identity is resolved
// and before the plugin VM is invoked, inside the session branch (so bearer and
// public never reach it). Behavioural tests above catch tier mistakes; this
// catches someone moving the call below callRoute (too late: the bridge already
// ran) or into the bearer branch.
func TestPluginGatePlacement(t *testing.T) {
	src, err := os.ReadFile("routes.go")
	if err != nil {
		t.Fatal(err)
	}
	s := string(src)
	sess := strings.Index(s, "m.resolveSession(r)")
	gate := strings.Index(s, "m.verifyGate()")
	call := strings.Index(s, "rt.callRoute(")
	if sess < 0 || gate < 0 || call < 0 {
		t.Fatalf("missing anchor: sess=%d gate=%d call=%d", sess, gate, call)
	}
	if !(sess < gate && gate < call) {
		t.Fatalf("gate must sit between resolveSession and callRoute: sess=%d gate=%d call=%d", sess, gate, call)
	}
}
