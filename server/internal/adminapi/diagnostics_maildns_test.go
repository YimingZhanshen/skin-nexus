package adminapi

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// fakeTXT records the names it was asked for so a test can pin that DMARC is
// looked up under _dmarc.<domain>, not under the bare domain.
type fakeTXT struct {
	names  []string
	byName map[string][]string
	err    error
	delay  time.Duration
}

func (f *fakeTXT) lookup(ctx context.Context, name string) ([]string, error) {
	f.names = append(f.names, name)
	if f.delay > 0 {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(f.delay):
		}
	}
	if f.err != nil {
		return nil, f.err
	}
	return f.byName[name], nil
}

func dnsBackend(t *testing.T, f *fakeTXT) *Backend {
	t.Helper()
	b := newDiagBackend(t)
	ctx := context.Background()
	for _, kv := range [][2]string{
		{"smtp_host", "smtp.rig.test"}, {"smtp_port", "465"}, {"smtp_from", "site@rig.test"},
	} {
		if err := b.Store.SetOption(ctx, kv[0], kv[1]); err != nil {
			t.Fatal(err)
		}
	}
	b.Diagnostics.LookupTXT = f.lookup
	return b
}

func TestCheckMailDNS(t *testing.T) {
	cases := []struct {
		name      string
		byName    map[string][]string
		err       error
		wantState string
		wantFix   string
	}{
		{"both present", map[string][]string{"rig.test": {"v=spf1 include:spf.rig.test ~all"}, "_dmarc.rig.test": {"v=DMARC1; p=none"}}, nil, "ok", ""},
		{"case insensitive", map[string][]string{"rig.test": {"V=SPF1 ~all"}, "_dmarc.rig.test": {"v=dmarc1; p=none"}}, nil, "ok", ""},
		{"spf only", map[string][]string{"rig.test": {"v=spf1 ~all"}}, nil, "warn", "mailDnsDmarc"},
		{"dmarc only", map[string][]string{"_dmarc.rig.test": {"v=DMARC1; p=none"}}, nil, "warn", "mailDnsSpf"},
		{"neither", map[string][]string{}, nil, "warn", "mailDnsBoth"},
		{"wrong version is not spf", map[string][]string{"rig.test": {"v=spf2 ~all"}, "_dmarc.rig.test": {"v=DMARC1; p=none"}}, nil, "warn", "mailDnsSpf"},
		{"lookup error outranks missing", map[string][]string{}, errors.New("servfail"), "warn", "mailDnsUnresolved"},
	}
	for _, c := range cases {
		f := &fakeTXT{byName: c.byName, err: c.err}
		b := dnsBackend(t, f)
		it := b.checkMailDNS(context.Background())
		if it.State != c.wantState || it.Fix != c.wantFix {
			t.Errorf("%s: state/fix = %s/%s, want %s/%s", c.name, it.State, it.Fix, c.wantState, c.wantFix)
		}
		if it.State == "fail" {
			t.Errorf("%s: DNS must never roll up to fail", c.name)
		}
		if c.err == nil {
			if len(f.names) != 2 || f.names[0] != "rig.test" || f.names[1] != "_dmarc.rig.test" {
				t.Errorf("%s: queried %v, want [rig.test _dmarc.rig.test]", c.name, f.names)
			}
		}
	}
}

func TestCheckMailDNSTimeoutIsThreeSeconds(t *testing.T) {
	f := &fakeTXT{byName: map[string][]string{}, delay: 10 * time.Second}
	b := dnsBackend(t, f)
	start := time.Now()
	it := b.checkMailDNS(context.Background())
	if elapsed := time.Since(start); elapsed > 8*time.Second {
		t.Fatalf("two 3s lookups took %v — the per-query bound is missing", elapsed)
	}
	if it.State != "warn" || it.Fix != "mailDnsUnresolved" {
		t.Errorf("timeout = %s/%s, want warn/mailDnsUnresolved", it.State, it.Fix)
	}
}

func TestCheckMailDNSNoNetworkWhenOff(t *testing.T) {
	f := &fakeTXT{byName: map[string][]string{}}
	b := newDiagBackend(t)
	b.Diagnostics.LookupTXT = f.lookup
	it := b.checkMailDNS(context.Background())
	if it.State != "skip" || it.Fix != "mailDnsOff" {
		t.Fatalf("unconfigured = %s/%s, want skip/mailDnsOff", it.State, it.Fix)
	}
	if len(f.names) != 0 {
		t.Errorf("unconfigured site queried DNS %v — the nil-config check must run first", f.names)
	}
}

func TestCheckMailDNSPlaceholderDomainSkips(t *testing.T) {
	f := &fakeTXT{byName: map[string][]string{}}
	b := dnsBackend(t, f)
	if err := b.Store.SetOption(context.Background(), "smtp_from", "admin@localhost"); err != nil {
		t.Fatal(err)
	}
	it := b.checkMailDNS(context.Background())
	if it.State != "skip" || it.Fix != "mailDnsNoDomain" {
		t.Fatalf("placeholder domain = %s/%s, want skip/mailDnsNoDomain", it.State, it.Fix)
	}
	if len(f.names) != 0 {
		t.Errorf("placeholder domain queried DNS %v", f.names)
	}
}

// The Go mirror of the fix dictionary and the SPA locales must agree in both
// directions; the comment above diagFixKeys promised this, now it is machine-
// checked instead of remembered.
func TestDiagFixKeysMatchLocales(t *testing.T) {
	for _, lang := range []string{"zh-CN", "en"} {
		raw, err := os.ReadFile(filepath.Join("..", "..", "..", "web", "src", "locales", lang+".json"))
		if err != nil {
			t.Fatal(err)
		}
		var tree map[string]any
		if err := json.Unmarshal(raw, &tree); err != nil {
			t.Fatal(err)
		}
		admin, _ := tree["admin"].(map[string]any)
		fixes, _ := admin["diagFix"].(map[string]any)
		for _, k := range diagFixKeys {
			if _, ok := fixes[k]; !ok {
				t.Errorf("%s: diagFixKeys has %q but the locale does not", lang, k)
			}
		}
		for k := range fixes {
			found := false
			for _, want := range diagFixKeys {
				if want == k {
					found = true
				}
			}
			if !found {
				t.Errorf("%s: locale diagFix.%s has no Go mirror entry", lang, k)
			}
		}
		diag, _ := admin["diag"].(map[string]any)
		for _, id := range []string{"site", "migrations", "mail", "mail_dns", "preview", "assets", "keyring"} {
			if _, ok := diag[id]; !ok {
				t.Errorf("%s: locale admin.diag.%s missing", lang, id)
			}
		}
	}
}

func TestDomainOfSender(t *testing.T) {
	cases := []struct{ in, want string }{
		{"a@B.C.", "b.c"},
		{"a@b.c", "b.c"},
		{"localhost", ""},
		{"a@", ""},
		{"", ""},
	}
	for _, c := range cases {
		if got := domainOfSender(c.in); got != c.want {
			t.Errorf("domainOfSender(%q) = %q, want %q", c.in, got, c.want)
		}
	}
	if !strings.Contains(domainOfSender("x@y.z"), ".") {
		t.Error("sanity")
	}
}
