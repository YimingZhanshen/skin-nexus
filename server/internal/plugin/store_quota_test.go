package plugin

import (
	"testing"
)

func TestStoreKVQuotaOverride(t *testing.T) {
	svc := newStoreService(newMemPlatformStore(), "importer",
		&StoreSpec{KV: &KVSpec{MaxKeys: 2}}, true, false)
	if err := svc.kvSet("a", "1", nil); err != nil {
		t.Fatal(err)
	}
	if err := svc.kvSet("b", "2", nil); err != nil {
		t.Fatal(err)
	}
	if err := svc.kvSet("c", "3", nil); err == nil {
		t.Fatal("third key must exceed the per-plugin quota")
	}
	if err := svc.kvSet("a", "updated", nil); err != nil {
		t.Fatalf("updating an existing key must stay allowed: %v", err)
	}
}

func TestValidateKVSpecRequiresPermission(t *testing.T) {
	m := Manifest{
		Name: "quota-test", Version: "1.0.0", HookAPI: 2, Entrypoint: "index.js",
		Store: &StoreSpec{KV: &KVSpec{MaxKeys: 50000}},
	}
	if err := validateV2Manifest(&m, "."); err == nil {
		t.Fatal("store.kv without store:kv must fail")
	}
	m.Permissions = append(m.Permissions, PermStoreKV)
	if err := validateV2Manifest(&m, "."); err != nil {
		t.Fatalf("good kv spec: %v", err)
	}
	m.Store.KV.MaxKeys = hardMaxKVKeys + 1
	if err := validateV2Manifest(&m, "."); err == nil {
		t.Fatal("maxKeys above the hard cap must fail")
	}
}
