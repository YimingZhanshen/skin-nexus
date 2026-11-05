package store

// Plugin KV and document scans are prefix scans: the SQL concatenates "%" onto
// the caller's prefix and carries ESCAPE '!'. That is only half of a literal
// prefix — the caller's own "_", "%" and "!" have to be escaped too, or a key
// prefix silently matches other keys ("cfg_" also matches "cfgXb") and an
// escape character in the prefix matches nothing at all. The assertions below
// are the pairs an unescaped bind cannot both satisfy.

import (
	"context"
	"sort"
	"testing"
)

func keysOf(rows []PluginKV) []string {
	out := make([]string, 0, len(rows))
	for _, r := range rows {
		out = append(out, r.Key)
	}
	sort.Strings(out)
	return out
}

func docIDsOf(rows []PluginDoc) []string {
	out := make([]string, 0, len(rows))
	for _, r := range rows {
		out = append(out, r.ID)
	}
	sort.Strings(out)
	return out
}

func TestPluginKVPrefixMatchesLiteralsNotWildcards(t *testing.T) {
	st := openSQLiteStore(t)
	ctx := context.Background()
	const plugin, ns = "kv-prefix-test", "default"
	for _, k := range []string{"cfg_a", "cfgXb", "urgent!key", "50%off", "plain"} {
		if err := st.SetPluginKV(ctx, plugin, ns, k, "v", nil); err != nil {
			t.Fatal(err)
		}
	}

	cases := []struct{ prefix, want string }{
		{"cfg_", "cfg_a"},         // "_" is one character: cfgXb must not ride along
		{"urgent!", "urgent!key"}, // "!" is the escape character, not a wildcard
		{"50%", "50%off"},         // "%" is a wildcard, not a length
		{"plain", "plain"},        // a plain prefix still works
		{"nothing", ""},
	}
	for _, c := range cases {
		rows, err := st.ListPluginKV(ctx, plugin, ns, c.prefix, 50)
		if err != nil {
			t.Fatalf("list %q: %v", c.prefix, err)
		}
		got := ""
		if len(rows) > 0 {
			got = keysOf(rows)[0]
		}
		if len(rows) > 1 || got != c.want {
			t.Errorf("prefix %q matched %v, want exactly [%s]", c.prefix, keysOf(rows), c.want)
		}
	}
}

func TestPluginDocPrefixMatchesLiteralsNotWildcards(t *testing.T) {
	st := openSQLiteStore(t)
	ctx := context.Background()
	const plugin, coll = "docs-prefix-test", "notes"
	for _, id := range []string{"note_1", "noteX2", "misc"} {
		if err := st.PutPluginDoc(ctx, plugin, coll, id, []byte(`{}`)); err != nil {
			t.Fatal(err)
		}
	}
	rows, err := st.ListPluginDocs(ctx, plugin, coll, "note_", 50)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || rows[0].ID != "note_1" {
		t.Fatalf("prefix \"note_\" matched %v, want exactly [note_1]", docIDsOf(rows))
	}
}
