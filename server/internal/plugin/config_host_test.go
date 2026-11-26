package plugin

import (
	"context"
	"os"
	"path/filepath"
	goruntime "runtime"
	"strings"
	"sync"
	"testing"
	"time"
)

type memSecret struct {
	ct, nonce []byte
	version   int
}

type memPlatformStore struct {
	mu         sync.Mutex
	opts       map[string]string
	configs    map[string][]byte
	secrets    map[string]memSecret
	metas      map[string]PluginMetaRecord
	kvs        map[string]KVRecord
	docs       map[string]DocRecord
	migrations map[string]int
}

func newMemPlatformStore() *memPlatformStore {
	return &memPlatformStore{
		opts: map[string]string{}, configs: map[string][]byte{},
		secrets: map[string]memSecret{}, metas: map[string]PluginMetaRecord{},
		kvs: map[string]KVRecord{}, docs: map[string]DocRecord{}, migrations: map[string]int{},
	}
}

func (m *memPlatformStore) GetOption(_ context.Context, k string) (string, bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	v, ok := m.opts[k]
	return v, ok, nil
}
func (m *memPlatformStore) SetOption(_ context.Context, k, v string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.opts[k] = v
	return nil
}
func (m *memPlatformStore) GetPluginConfig(_ context.Context, name string) ([]byte, bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	v, ok := m.configs[name]
	return v, ok, nil
}
func (m *memPlatformStore) SetPluginConfig(_ context.Context, name string, raw []byte) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.configs[name] = append([]byte(nil), raw...)
	return nil
}
func (m *memPlatformStore) GetPluginSecret(_ context.Context, plugin, key string) ([]byte, []byte, int, bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	s, ok := m.secrets[plugin+"/"+key]
	return s.ct, s.nonce, s.version, ok, nil
}
func (m *memPlatformStore) SetPluginSecret(_ context.Context, plugin, key string, ct, nonce []byte, version int) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.secrets[plugin+"/"+key] = memSecret{ct: ct, nonce: nonce, version: version}
	return nil
}
func (m *memPlatformStore) DeletePluginSecret(_ context.Context, plugin, key string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.secrets, plugin+"/"+key)
	return nil
}
func (m *memPlatformStore) UpsertPluginMeta(_ context.Context, meta PluginMetaRecord) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.metas[meta.Name] = meta
	return nil
}
func (m *memPlatformStore) GetPluginMeta(_ context.Context, name string) (PluginMetaRecord, bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	rec, ok := m.metas[name]
	return rec, ok, nil
}
func (m *memPlatformStore) UpdatePluginState(_ context.Context, name, state, lastErr string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	rec, ok := m.metas[name]
	if !ok {
		rec = PluginMetaRecord{Name: name}
	}
	rec.State, rec.LastError = state, lastErr
	m.metas[name] = rec
	return nil
}

const configPluginManifest = `{
  "name": "cfg",
  "version": "1.0.0",
  "hookApi": 2,
  "entrypoint": "index.js",
  "permissions": ["config"],
  "config": {"schema": {"type": "object", "required": ["token"], "properties": {
    "name": {"type": "string", "default": "hello"},
    "count": {"type": "integer", "minimum": 1, "default": 2},
    "token": {"type": "string", "format": "secret"}
  }}}
}`

func newConfigHost(t *testing.T) (*Manager, *memPlatformStore, string) {
	t.Helper()
	root := t.TempDir()
	writePlugin(t, root, "cfg", configPluginManifest, "skin.log('cfg');")
	box, err := NewSecretBox("", filepath.Join(root, "secret.key"))
	if err != nil {
		t.Fatal(err)
	}
	st := newMemPlatformStore()
	host := NewHost(RuntimeConfig{Dir: root, HostVersion: "0.4.0", Store: st, SecretBox: box})
	t.Cleanup(func() { _ = host.Close() })
	if err := host.Load(root); err != nil {
		t.Fatal(err)
	}
	return host, st, root
}

func TestPluginConfigSaveMasksSecretAndLoads(t *testing.T) {
	host, st, _ := newConfigHost(t)
	view, err := host.PluginConfig("cfg")
	if err != nil {
		t.Fatal(err)
	}
	if len(view.Schema) == 0 || view.Values["name"] != "hello" {
		t.Fatalf("view = %+v", view)
	}
	if _, err := host.SavePluginConfig(context.Background(), "cfg", map[string]any{
		"name": "site", "count": 3, "token": "s3cret",
	}); err != nil {
		t.Fatal(err)
	}
	view, _ = host.PluginConfig("cfg")
	if view.Values["token"] != "********" || view.Values["name"] != "site" {
		t.Fatalf("masked view = %+v", view.Values)
	}
	if _, _, _, ok, err := st.GetPluginSecret(context.Background(), "cfg", "token"); err != nil || !ok {
		t.Fatalf("secret not persisted: ok=%v err=%v", ok, err)
	}
	// Saving the masked form must preserve the stored secret.
	if _, err := host.SavePluginConfig(context.Background(), "cfg", map[string]any{
		"name": "site2", "count": 3, "token": "********",
	}); err != nil {
		t.Fatal(err)
	}
	if err := host.Enable(context.Background(), "cfg"); err != nil {
		t.Fatal(err)
	}
	got, err := host.PluginConfig("cfg")
	if err != nil || got.Values["name"] != "site2" {
		t.Fatalf("after enable = %+v/%v", got, err)
	}
}

func TestPluginConfigRequiredSecretMissingBlocksStart(t *testing.T) {
	host, _, _ := newConfigHost(t)
	if err := host.Enable(context.Background(), "cfg"); err == nil {
		t.Fatal("enable should fail without required secret")
	}
	st := host.List()[0]
	if st.State != "error" || st.LastError == "" {
		t.Fatalf("status = %+v", st)
	}
	// The schema remains available so the admin can fix it.
	if _, err := host.PluginConfig("cfg"); err != nil {
		t.Fatalf("schema lost: %v", err)
	}
}

func TestMinServerBlocksLoad(t *testing.T) {
	root := t.TempDir()
	writePlugin(t, root, "future", `{"name":"future","version":"1","hookApi":2,"entrypoint":"index.js","permissions":[],"minServer":">=9.0.0"}`, "skin.log('x');")
	host := NewHost(RuntimeConfig{Dir: root, HostVersion: "0.4.0"})
	t.Cleanup(func() { _ = host.Close() })
	if err := host.Load(root); err != nil {
		t.Fatal(err)
	}
	st := host.List()
	if len(st) != 1 || st[0].State != "error" || st[0].Health != "error" {
		t.Fatalf("status = %+v", st)
	}
}

func TestActorReachesAudit(t *testing.T) {
	root := t.TempDir()
	writePlugin(t, root, "demo", `{"name":"demo","version":"1","hookApi":2,"entrypoint":"index.js","permissions":[]}`, "skin.log('x');")
	st := newMemPlatformStore()
	var got int64
	host := NewHost(RuntimeConfig{Dir: root, HostVersion: "0.4.0", Store: st, Audit: func(ctx context.Context, _ string, _ map[string]any) {
		if u := ActorFromContext(ctx); u > 0 {
			got = u
		}
	}})
	t.Cleanup(func() { _ = host.Close() })
	if err := host.Load(root); err != nil {
		t.Fatal(err)
	}
	if err := host.Enable(WithActor(context.Background(), 7), "demo"); err != nil {
		t.Fatal(err)
	}
	if got != 7 {
		t.Fatalf("actor = %d", got)
	}
}

func TestPluginMetaPersisted(t *testing.T) {
	root := t.TempDir()
	writePlugin(t, root, "demo", `{"name":"demo","version":"2.0.0","hookApi":2,"entrypoint":"index.js","permissions":[]}`, "skin.log('x');")
	st := newMemPlatformStore()
	host := NewHost(RuntimeConfig{Dir: root, HostVersion: "0.4.0", Store: st})
	t.Cleanup(func() { _ = host.Close() })
	if err := host.Load(root); err != nil {
		t.Fatal(err)
	}
	meta, ok, err := st.GetPluginMeta(context.Background(), "demo")
	if err != nil || !ok || meta.Version != "2.0.0" {
		t.Fatalf("meta = %+v ok=%v err=%v", meta, ok, err)
	}
}

func TestSecretBoxKeyFilePermissions(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "k")
	if _, err := NewSecretBox("", path); err != nil {
		t.Fatal(err)
	}
	fi, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if goruntime.GOOS != "windows" && fi.Mode().Perm()&0o077 != 0 {
		t.Fatalf("key mode = %o", fi.Mode().Perm())
	}
}

func TestEntrypointLoadTimeout(t *testing.T) {
	root := t.TempDir()
	writePlugin(t, root, "loop", `{"name":"loop","version":"1","hookApi":2,"entrypoint":"index.js","permissions":[]}`, "while (true) {}")
	host := NewHost(RuntimeConfig{Dir: root, HostVersion: "0.4.0", LoadTimeout: 100 * time.Millisecond})
	t.Cleanup(func() { _ = host.Close() })
	if err := host.Load(root); err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	if err := host.Enable(context.Background(), "loop"); err == nil {
		t.Fatal("enable should time out")
	}
	if time.Since(start) > 2*time.Second {
		t.Fatalf("enable blocked for %s", time.Since(start))
	}
	list := host.List()
	if len(list) != 1 || list[0].State != "error" {
		t.Fatalf("status = %+v", list)
	}
}

func (m *memPlatformStore) GetPluginKV(_ context.Context, p, ns, k string) (string, *time.Time, bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	r, ok := m.kvs[p+"/"+ns+"/"+k]
	return r.Value, r.ExpiresAt, ok, nil
}
func (m *memPlatformStore) SetPluginKV(_ context.Context, p, ns, k, v string, exp *time.Time) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.kvs[p+"/"+ns+"/"+k] = KVRecord{Namespace: ns, Key: k, Value: v, ExpiresAt: exp}
	return nil
}
func (m *memPlatformStore) DeletePluginKV(_ context.Context, p, ns, k string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.kvs, p+"/"+ns+"/"+k)
	return nil
}
func (m *memPlatformStore) ListPluginKV(_ context.Context, p, ns, prefix string, limit int) ([]KVRecord, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []KVRecord
	for _, r := range m.kvs {
		if r.Namespace == ns && strings.HasPrefix(r.Key, prefix) {
			out = append(out, r)
		}
		if len(out) >= limit {
			break
		}
	}
	return out, nil
}
func (m *memPlatformStore) CountPluginKV(_ context.Context, p string) (int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	n := 0
	for k := range m.kvs {
		if strings.HasPrefix(k, p+"/") {
			n++
		}
	}
	return n, nil
}
func (m *memPlatformStore) GetPluginDoc(_ context.Context, p, coll, id string) ([]byte, bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	d, ok := m.docs[p+"/"+coll+"/"+id]
	return d.Doc, ok, nil
}
func (m *memPlatformStore) PutPluginDoc(_ context.Context, p, coll, id string, doc []byte) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.docs[p+"/"+coll+"/"+id] = DocRecord{Collection: coll, ID: id, Doc: doc}
	return nil
}
func (m *memPlatformStore) DeletePluginDoc(_ context.Context, p, coll, id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.docs, p+"/"+coll+"/"+id)
	return nil
}
func (m *memPlatformStore) ListPluginDocs(_ context.Context, p, coll, prefix string, limit int) ([]DocRecord, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []DocRecord
	for _, d := range m.docs {
		if d.Collection == coll && strings.HasPrefix(d.ID, prefix) {
			out = append(out, d)
		}
		if len(out) >= limit {
			break
		}
	}
	return out, nil
}
func (m *memPlatformStore) CountPluginDocs(_ context.Context, p, coll string) (int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	n := 0
	for k := range m.docs {
		if strings.HasPrefix(k, p+"/"+coll+"/") {
			n++
		}
	}
	return n, nil
}
func (m *memPlatformStore) PluginMigrationLatest(_ context.Context, p string) (int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.migrations[p], nil
}
func (m *memPlatformStore) RecordPluginMigration(_ context.Context, p string, v int) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.migrations[p] = v
	return nil
}
