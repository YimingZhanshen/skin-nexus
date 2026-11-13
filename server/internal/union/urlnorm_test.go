package union

// urlnorm_test.go — W1-2: one station, one identity.
//
// The hub list is hand-written on fifty stations, so the same station arrives
// spelled several ways (upper host, trailing slash, credentials left in, the
// API path appended). The panel renders one row per (code, bs_root) — the Vue
// key is exactly that pair — so two spellings of one station are two rows of
// the same station, and a duplicate key once the values are folded.
//
// CanonicalRoot is the fold: scheme and host lowercased, credentials, query
// and fragment dropped (they are not part of who the station is), a trailing
// /authserver — the yggdrasil API path, not the station — stripped, and the
// rest of the path kept because a station deployed under a subpath is a
// different station from the one at the root.

import "testing"

func TestCanonicalRootCollapsesSpellingVariants(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"plain", "https://skin.example.net", "https://skin.example.net"},
		{"upper host", "https://SKIN.Example.NET", "https://skin.example.net"},
		{"upper scheme", "HTTPS://skin.example.net", "https://skin.example.net"},
		{"trailing slash", "https://skin.example.net/", "https://skin.example.net"},
		{"credentials dropped", "https://root:secret@skin.example.net", "https://skin.example.net"},
		{"query and fragment dropped", "https://skin.example.net/admin?debug=1#frag", "https://skin.example.net/admin"},
		{"authserver path stripped", "https://skin.example.net/authserver", "https://skin.example.net"},
		{"authserver under the api path stripped", "https://skin.example.net/api/yggdrasil/authserver", "https://skin.example.net/api/yggdrasil"},
		{"port kept", "https://skin.example.net:8443/", "https://skin.example.net:8443"},
		{"path case is significant and survives", "https://skin.example.net/Admin/", "https://skin.example.net/Admin"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, ok := CanonicalRoot(c.in)
			if !ok {
				t.Fatalf("CanonicalRoot(%q) refused a valid station root", c.in)
			}
			if got != c.want {
				t.Fatalf("CanonicalRoot(%q) = %q, want %q", c.in, got, c.want)
			}
		})
	}
}

// Refusal is part of the contract: anything that is not an http(s) station
// root gets no canonical spelling, so callers keep the raw value instead of
// inventing one.
func TestCanonicalRootRefusesNonHTTP(t *testing.T) {
	for _, in := range []string{"", "   ", "file:///etc/passwd", "ftp://example.com", "example.com", "javascript:alert(1)"} {
		if got, ok := CanonicalRoot(in); ok {
			t.Fatalf("CanonicalRoot(%q) = %q, want refusal", in, got)
		}
	}
}

// Two spellings of one (code, station) are one row; a different code or a
// different station is a different row and must survive — the hub really does
// ship one code twice with two addresses, and that is two rows on purpose.
func TestCanonicalizeServersCollapsesSpellingDuplicates(t *testing.T) {
	in := []ServerEntry{
		{Code: "A", BSRoot: "https://Example.COM"},
		{Code: "A", BSRoot: "https://example.com/"},
		{Code: "A", BSRoot: "https://example.com"},
		{Code: "B", BSRoot: "https://example.com"},
		{Code: "A", BSRoot: "https://example.org"},
		{Code: "A"}, // no address at all: keyed by the empty root, kept once
		{Code: "A"},
	}
	out := CanonicalizeServers(in)
	if len(out) != 4 {
		t.Fatalf("rows = %d, want 4 (A/example.com, B/example.com, A/example.org, A/empty): %+v", len(out), out)
	}
	if out[0].BSRoot != "https://example.com" {
		t.Fatalf("first surviving row must carry the canonical spelling, got %q", out[0].BSRoot)
	}
	if out[3].Code != "A" || out[3].BSRoot != "" {
		t.Fatalf("the address-less row must survive once, got %+v", out[3])
	}
}
