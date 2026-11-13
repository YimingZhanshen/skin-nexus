package main

// union_boot_domains_test.go — W1-2: what a member station contributes to the
// texture whitelist.
//
// union_skin_domains is written from the hub's server list and read back as
// ExtraSkinDomains by ygg.skinDomains, so whatever this extracts becomes a
// host every launcher on this station will trust for textures. Only a real
// http(s) station root may contribute: url.Parse happily reports a host for
// "ftp://internal:11211/", and the hub is a third party.
//
// The spelling the hub sent is preserved rather than folded here. The whitelist
// emitter publishes the lowercase spelling alongside the original (see
// ygg.skinDomains), so folding at this end would drop the one form a
// mixed-case member texture URL matches today.

import "testing"

func TestUnionSkinDomains(t *testing.T) {
	cases := []struct {
		name string
		list any
		want []string
	}{
		{
			name: "http station root keeps the hub's spelling",
			list: []map[string]any{{"bs_root": "https://Example.COM/path"}},
			want: []string{"Example.COM"},
		},
		{
			name: "https with a port",
			list: []map[string]any{{"bs_root": "https://skin.example.net:8443/"}},
			want: []string{"skin.example.net:8443"},
		},
		{
			name: "credentials never reach the host",
			list: []map[string]any{{"bs_root": "https://root:secret@skin.example.net"}},
			want: []string{"skin.example.net"},
		},
		{
			name: "non-http scheme refused",
			list: []map[string]any{{"bs_root": "ftp://internal:11211/"}},
			want: nil,
		},
		{
			name: "file scheme refused",
			list: []map[string]any{{"bs_root": "file:///etc/passwd"}},
			want: nil,
		},
		{
			name: "missing, empty or unparseable roots contribute nothing",
			list: []map[string]any{{"bs_root": "not a url"}, {"bs_root": ""}, {}},
			want: nil,
		},
		{
			name: "several members, several hosts",
			list: []map[string]any{
				{"bs_root": "https://a.example/"},
				{"bs_root": "https://b.example/"},
				{"bs_root": "ftp://c.example/"},
			},
			want: []string{"a.example", "b.example"},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, ok := unionSkinDomains(c.list)
			if !ok {
				t.Fatalf("decoding the hub list must succeed for %v", c.list)
			}
			if len(got) != len(c.want) {
				t.Fatalf("hosts = %v, want %v", got, c.want)
			}
			for i := range got {
				if got[i] != c.want[i] {
					t.Fatalf("hosts = %v, want %v", got, c.want)
				}
			}
		})
	}
}
