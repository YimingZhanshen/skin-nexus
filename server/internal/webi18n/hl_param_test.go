package webi18n

// A language that only exists in a cookie or an Accept-Language header is not a
// page a crawler can be pointed at: Google's rendering service clears cookies
// and localStorage during page load, and hreflang alternates must resolve to
// distinct URLs. `?hl=` makes each locale a URL of its own, so it outranks both
// of those signals — it is the one the link tag promises.

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestLangParamOutranksCookieAndHeader(t *testing.T) {
	r := httptest.NewRequest("GET", "/skinlib/42?hl=en", nil)
	r.AddCookie(&http.Cookie{Name: CookieName, Value: ZhCN})
	r.Header.Set("Accept-Language", "zh-CN,zh;q=0.9")
	if got := ResolveLang(r); got != En {
		t.Fatalf("?hl=en with a zh cookie and a zh Accept-Language = %q, want en", got)
	}

	r2 := httptest.NewRequest("GET", "/skinlib/42?hl=zh-CN", nil)
	r2.Header.Set("Accept-Language", "en-US,en;q=0.9")
	if got := ResolveLang(r2); got != ZhCN {
		t.Fatalf("?hl=zh-CN against an English browser = %q, want zh-CN", got)
	}
}

// An unknown or empty value is not a preference: it must fall through to the
// usual order instead of silently pinning the page to the origin language.
func TestUnknownLangParamIsNoPreference(t *testing.T) {
	r := httptest.NewRequest("GET", "/skinlib/42?hl=fr", nil)
	r.Header.Set("Accept-Language", "en-US,en;q=0.9")
	if got := ResolveLang(r); got != En {
		t.Fatalf("?hl=fr with an English browser = %q, want the header to decide", got)
	}
	r2 := httptest.NewRequest("GET", "/skinlib/42?hl=", nil)
	r2.AddCookie(&http.Cookie{Name: CookieName, Value: En})
	if got := ResolveLang(r2); got != En {
		t.Fatalf("empty ?hl with an English cookie = %q, want en", got)
	}
}

// The emitter needs to know whether the locale came from the URL: a page served
// by negotiation should not promise a `?hl=` canonical it did not use.
func TestExplicitLangOnlyWhenTheURLSays(t *testing.T) {
	r := httptest.NewRequest("GET", "/skinlib/42?hl=en", nil)
	if got := ExplicitLang(r); got != En {
		t.Fatalf("ExplicitLang = %q, want en", got)
	}
	r2 := httptest.NewRequest("GET", "/skinlib/42", nil)
	r2.AddCookie(&http.Cookie{Name: CookieName, Value: En})
	if got := ExplicitLang(r2); got != "" {
		t.Fatalf("ExplicitLang with a cookie but no URL parameter = %q, want empty", got)
	}
	r3 := httptest.NewRequest("GET", "/skinlib/42?hl=fr", nil)
	if got := ExplicitLang(r3); got != "" {
		t.Fatalf("ExplicitLang for a language we do not ship = %q, want empty", got)
	}
}
