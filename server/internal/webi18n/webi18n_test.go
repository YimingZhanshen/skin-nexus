package webi18n

// Language resolution and catalog contract tests. The interesting part of this
// package is not the lookup — it is WHICH language a request gets, because the
// cookie is written by the SPA and the header by the browser, and they disagree
// on most real visits.

import (
	"net/http"
	"strings"
	"testing"
)

func TestTLookupAndFallback(t *testing.T) {
	if got := T(ZhCN, "detail.download"); got != "下载材质 PNG" {
		t.Errorf("zh download = %q", got)
	}
	if got := T(En, "detail.download"); got != "Download the PNG" {
		t.Errorf("en download = %q", got)
	}
	// An unknown language falls back to the site language, not to nothing.
	if got := T("de-DE", "detail.download"); got != "下载材质 PNG" {
		t.Errorf("unknown lang fell back to %q", got)
	}
	// A missing key comes back as itself so the gap is visible in the page.
	if got := T(En, "no.such.key"); got != "no.such.key" {
		t.Errorf("missing key = %q", got)
	}
	// Empty is a legitimate value (the English likes cell has no unit word).
	if got := T(En, "detail.likesUnit"); got != "" {
		t.Errorf("en likes unit = %q, want empty", got)
	}
	if got := T(ZhCN, "detail.likesUnit"); got != "人" {
		t.Errorf("zh likes unit = %q", got)
	}
}

func TestFSubstitutesPlaceholders(t *testing.T) {
	got := F(En, "detail.imgAlt", "name", "Big HD", "kind", "skin")
	if got != "Preview of Big HD, a Minecraft skin" {
		t.Errorf("imgAlt = %q", got)
	}
	// A placeholder with no pair is left alone rather than erased.
	if left := F(En, "detail.imgAlt", "name", "X"); !strings.Contains(left, "{kind}") {
		t.Errorf("unfilled placeholder vanished: %q", left)
	}
	if got := F(ZhCN, "detail.untitled", "tid", "42"); got != "材质 #42" {
		t.Errorf("untitled = %q", got)
	}
}

func TestResolveLang(t *testing.T) {
	cases := []struct {
		name   string
		cookie string
		accept string
		want   string
	}{
		{"no hints at all", "", "", ZhCN},
		{"cookie wins over header", En, "zh-CN,zh;q=0.9", En},
		{"cookie zh wins over en header", ZhCN, "en-US,en;q=0.9", ZhCN},
		{"junk cookie falls back to header", "fr", "en-GB,en;q=0.8", En},
		{"accept en", "", "en-US,en;q=0.9", En},
		{"accept zh", "", "zh-Hans-CN,zh;q=0.9", ZhCN},
		{"quality ordering", "", "de;q=0.9, en;q=0.3", En},
		{"preferred unsupported language", "", "fr-FR,ja;q=0.9", ZhCN},
		{"weird q ignored", "", "en;q=abc", En},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r, err := http.NewRequest(http.MethodGet, "/skinlib/1", nil)
			if err != nil {
				t.Fatal(err)
			}
			if c.cookie != "" {
				r.AddCookie(&http.Cookie{Name: CookieName, Value: c.cookie})
			}
			if c.accept != "" {
				r.Header.Set("Accept-Language", c.accept)
			}
			if got := ResolveLang(r); got != c.want {
				t.Errorf("ResolveLang = %q, want %q", got, c.want)
			}
		})
	}
}

func TestCatalogsAreParity(t *testing.T) {
	zh, en := catalogs[ZhCN], catalogs[En]
	for k := range en {
		if _, ok := zh[k]; !ok {
			t.Errorf("en key %q has no zh-CN entry", k)
		}
	}
	for k := range zh {
		if _, ok := en[k]; !ok {
			t.Errorf("zh-CN key %q has no en entry", k)
		}
	}
	for _, k := range viewerKeys {
		if _, ok := zh[k]; !ok {
			t.Errorf("viewer key %q is not in the catalog", k)
		}
	}
	// Every key must actually be used, or it is a copy that no page renders.
	// Checked here against the templates' call sites rather than a scanner.
	if len(zh) < 40 {
		t.Errorf("catalog has %d keys; the extractor probably read the wrong file", len(zh))
	}
}

func TestViewerPayloadIsSafeJSON(t *testing.T) {
	p := string(ViewerPayload(En))
	if !strings.HasPrefix(p, `{"lang":"en","strings":{`) {
		t.Fatalf("payload shape: %s", p)
	}
	if !strings.Contains(p, `"saveFailed"`) || !strings.Contains(p, `"network"`) {
		t.Errorf("payload missing viewer strings: %s", p)
	}
	// A catalog value containing </script> must not be able to close the element.
	for _, s := range []string{p} {
		if strings.Contains(s, "</script") {
			t.Errorf("payload can terminate its script element: %s", s)
		}
	}
	// Unknown language normalizes to the site language.
	if got := string(ViewerPayload("klingon")); !strings.HasPrefix(got, `{"lang":"zh-CN"`) {
		t.Errorf("payload lang: %s", got)
	}
}
