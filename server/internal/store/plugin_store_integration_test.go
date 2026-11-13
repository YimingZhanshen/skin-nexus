//go:build integration

package store

import (
	"bytes"
	"context"
	"encoding/json"
	"testing"
	"time"
)

func TestPluginStoreRoundTrip(t *testing.T) {
	m := openTestStore(t)
	ctx := context.Background()
	name := "plugin-store-test"
	defer m.DeletePluginData(ctx, name)

	if err := m.UpsertPluginMeta(ctx, PluginMeta{
		Name: name, Version: "1.2.3", HookAPI: 2, Runtime: "goja",
		Manifest: []byte(`{"name":"plugin-store-test"}`), Config: []byte(`{"x":1}`),
		SchemaVersion: 3, Source: "manual", State: "disabled",
	}); err != nil {
		t.Fatal(err)
	}
	meta, err := m.GetPluginMeta(ctx, name)
	if err != nil {
		t.Fatal(err)
	}
	if meta.Version != "1.2.3" || meta.SchemaVersion != 3 || !jsonEqual(meta.Config, []byte(`{"x":1}`)) {
		t.Fatalf("meta = %+v", meta)
	}
	if err := m.UpdatePluginState(ctx, name, "loaded", ""); err != nil {
		t.Fatal(err)
	}
	meta, _ = m.GetPluginMeta(ctx, name)
	if meta.State != "loaded" {
		t.Fatalf("state = %q", meta.State)
	}

	if err := m.SetPluginConfig(ctx, name, []byte(`{"x":2}`)); err != nil {
		t.Fatal(err)
	}
	cfg, err := m.GetPluginConfig(ctx, name)
	if err != nil || !jsonEqual(cfg, []byte(`{"x":2}`)) {
		t.Fatalf("config=%s err=%v", cfg, err)
	}

	sec := PluginSecret{Ciphertext: []byte("ct"), Nonce: []byte("123456789012"), KeyVersion: 1}
	if err := m.SetPluginSecret(ctx, name, "token", sec); err != nil {
		t.Fatal(err)
	}
	gotSec, err := m.GetPluginSecret(ctx, name, "token")
	if err != nil || string(gotSec.Ciphertext) != "ct" || len(gotSec.Nonce) != 12 {
		t.Fatalf("secret=%+v err=%v", gotSec, err)
	}
	if err := m.DeletePluginSecret(ctx, name, "token"); err != nil {
		t.Fatal(err)
	}
	if _, err := m.GetPluginSecret(ctx, name, "token"); err == nil {
		t.Fatal("deleted secret still readable")
	}

	if err := m.SetPluginKV(ctx, name, "ns", "a", "1", nil); err != nil {
		t.Fatal(err)
	}
	if err := m.SetPluginKV(ctx, name, "ns", "b", "2", nil); err != nil {
		t.Fatal(err)
	}
	past := time.Now().Add(-time.Minute)
	if err := m.SetPluginKV(ctx, name, "ns", "expired", "x", &past); err != nil {
		t.Fatal(err)
	}
	if v, _, ok, err := m.GetPluginKV(ctx, name, "ns", "a"); err != nil || !ok || v != "1" {
		t.Fatalf("kv=%q ok=%v err=%v", v, ok, err)
	}
	if _, _, ok, _ := m.GetPluginKV(ctx, name, "ns", "expired"); ok {
		t.Fatal("expired kv visible")
	}
	list, err := m.ListPluginKV(ctx, name, "ns", "", 10)
	if err != nil || len(list) != 2 {
		t.Fatalf("kv list=%d err=%v", len(list), err)
	}
	// Expired keys are invisible to List/Get, so they must not consume the
	// quota either.
	if n, _ := m.CountPluginKV(ctx, name); n != 2 {
		t.Fatalf("kv count = %d, want 2 (expired excluded)", n)
	}
	if n, err := m.SweepPluginKV(ctx, 10); err != nil || n < 1 {
		t.Fatalf("sweep = %d err=%v", n, err)
	}

	if err := m.PutPluginDoc(ctx, name, "notes", "n1", []byte(`{"text":"hi"}`)); err != nil {
		t.Fatal(err)
	}
	doc, err := m.GetPluginDoc(ctx, name, "notes", "n1")
	if err != nil || !jsonEqual(doc.Doc, []byte(`{"text":"hi"}`)) {
		t.Fatalf("doc=%s err=%v", doc, err)
	}
	docs, err := m.ListPluginDocs(ctx, name, "notes", "n", 10)
	if err != nil || len(docs) != 1 {
		t.Fatalf("docs=%d err=%v", len(docs), err)
	}
	if n, _ := m.CountPluginDocs(ctx, name, "notes"); n != 1 {
		t.Fatalf("doc count = %d", n)
	}
	if err := m.DeletePluginDoc(ctx, name, "notes", "n1"); err != nil {
		t.Fatal(err)
	}

	if ok, _ := m.PluginMigrationApplied(ctx, name, 1); ok {
		t.Fatal("migration 1 already applied")
	}
	if err := m.RecordPluginMigration(ctx, name, 1); err != nil {
		t.Fatal(err)
	}
	if ok, _ := m.PluginMigrationApplied(ctx, name, 1); !ok {
		t.Fatal("migration 1 not recorded")
	}

	if err := m.DeletePluginData(ctx, name); err != nil {
		t.Fatal(err)
	}
	if _, err := m.GetPluginMeta(ctx, name); err == nil {
		t.Fatal("meta survived delete")
	}
}

func jsonEqual(a, b []byte) bool {
	var ab, bb bytes.Buffer
	if json.Compact(&ab, a) != nil || json.Compact(&bb, b) != nil {
		return false
	}
	return ab.String() == bb.String()
}

// plugin_kv keys and plugin_docs ids are case-sensitive identifiers: `Count`
// and `count` are two slots, and a prefix listing must not swallow the other
// case. SQLite/PG behaved that way all along; MySQL folded case until the
// 0032 column collation. Locally (SQLite) this is green from the start —
// the red reading for the fix lives in TestMySQLPluginKeysAreCaseSensitive;
// this test is the CI-MySQL behavioral pin that would have caught the merge.
func TestPluginKVAndDocsAreCaseSensitive(t *testing.T) {
	m := openTestStore(t)
	ctx := context.Background()
	name := "plugin-case-test"
	defer m.DeletePluginData(ctx, name)

	if err := m.SetPluginKV(ctx, name, "", "Count", "upper", nil); err != nil {
		t.Fatalf("set Count: %v", err)
	}
	if err := m.SetPluginKV(ctx, name, "", "count", "lower", nil); err != nil {
		t.Fatalf("set count: %v", err)
	}
	if n, err := m.CountPluginKV(ctx, name); err != nil || n != 2 {
		t.Fatalf("kv rows = %d (err %v), want 2 — case-distinct keys merged", n, err)
	}
	v, _, found, err := m.GetPluginKV(ctx, name, "", "Count")
	if err != nil || !found || v != "upper" {
		t.Fatalf("Get(Count) = %q found=%v err=%v, want upper", v, found, err)
	}
	v, _, found, err = m.GetPluginKV(ctx, name, "", "count")
	if err != nil || !found || v != "lower" {
		t.Fatalf("Get(count) = %q found=%v err=%v, want lower", v, found, err)
	}
	rows, err := m.ListPluginKV(ctx, name, "", "Cou", 100)
	if err != nil || len(rows) != 1 || rows[0].Key != "Count" {
		t.Fatalf("prefix Cou -> %+v err=%v, want only Count", rows, err)
	}

	if err := m.PutPluginDoc(ctx, name, "c", "Doc", []byte(`{"n":1}`)); err != nil {
		t.Fatalf("put Doc: %v", err)
	}
	if err := m.PutPluginDoc(ctx, name, "c", "doc", []byte(`{"n":2}`)); err != nil {
		t.Fatalf("put doc: %v", err)
	}
	if d, err := m.GetPluginDoc(ctx, name, "c", "Doc"); err != nil || string(d.Doc) != `{"n":1}` {
		t.Fatalf("Get(Doc) = %s err=%v, want {\"n\":1}", d.Doc, err)
	}
	if d, err := m.GetPluginDoc(ctx, name, "c", "doc"); err != nil || string(d.Doc) != `{"n":2}` {
		t.Fatalf("Get(doc) = %s err=%v, want {\"n\":2}", d.Doc, err)
	}
	docs, err := m.ListPluginDocs(ctx, name, "c", "Do", 100)
	if err != nil || len(docs) != 1 || docs[0].ID != "Doc" {
		t.Fatalf("prefix Do -> %+v err=%v, want only Doc", docs, err)
	}
}
