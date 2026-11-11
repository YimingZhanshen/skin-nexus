package adminapi

// Tests for the deployment self-check (U16). The store-backed cases run on an
// in-memory SQLite with the real migration tree applied, because half the value
// of these checks is the comparison against on-disk filenames — a fixture would
// pin a number that drifts the moment 0023 lands.

import (
	"context"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"skin-nexus/server/internal/migrate"
	"skin-nexus/server/internal/siteurl"
	"skin-nexus/server/internal/store"
)

func diagStore(t *testing.T) *store.Mysql {
	t.Helper()
	m, err := store.Open("sqlite", "sqlite::memory:", nil)
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	t.Cleanup(func() { _ = m.Close() })
	dir := filepath.Join("..", "..", "migrations")
	if err := migrate.ApplySQLDirTracked(m.DB(), dir, m.Engine()); err != nil {
		t.Fatalf("apply migrations: %v", err)
	}
	return m
}

func newDiagBackend(t *testing.T) *Backend {
	t.Helper()
	siteurl.Set("https://skin.test")
	b := New(diagStore(t), nil)
	b.SettingsDefaults = map[string]string{}
	b.SiteName = "Diag"
	return b
}

func TestIsPrivateBase(t *testing.T) {
	cases := []struct {
		in   string
		want bool
	}{
		{"https://skin.example.com", false},
		{"http://www.skin.example.com/", false},
		{"skin.example.com:8080", false},
		{"https://203.0.113.7", false},
		{"http://172.32.0.1", false}, // outside 172.16/12
		{"http://127.0.0.1:8080", true},
		{"http://localhost:8080", true},
		{"http://LOCALHOST", true},
		{"10.0.0.5", true},
		{"http://192.168.1.20", true},
		{"http://172.16.0.1", true},
		{"http://172.31.255.255", true},
		{"http://169.254.1.1", true},
		{"http://[::1]:8080", true},
		{"http://0.0.0.0:8080", true},
		{"", true}, // nothing resolved is not a public address
	}
	for _, c := range cases {
		if got := isPrivateBase(c.in); got != c.want {
			t.Errorf("isPrivateBase(%q) = %v, want %v", c.in, got, c.want)
		}
	}
}

func TestNewestMigrationFileUsesRealTree(t *testing.T) {
	dir := filepath.Join("..", "..", "migrations")
	for _, eng := range []string{"mysql", "sqlite", "postgres"} {
		got, err := newestMigrationFile(dir, eng)
		if err != nil {
			t.Fatalf("%s: %v", eng, err)
		}
		if got == "" {
			t.Fatalf("%s: no .sql found under %s", eng, dir)
		}
		// Every tree must carry the same watermark or the six-engine matrix
		// diverges; 0022 is the current floor and rises with each migration.
		if len(got) < 4 || got[:4] < "0022" {
			t.Errorf("%s newest = %q, want a file numbered >= 0022", eng, got)
		}
	}
	if got, err := newestMigrationFile(t.TempDir(), "sqlite"); err != nil || got != "" {
		t.Errorf("empty tree = %q, %v; want \"\", nil", got, err)
	}
}

func TestCheckWebAssets(t *testing.T) {
	b := newDiagBackend(t)
	if it := b.checkWebAssets(); it.State != diagSkip || it.ID != "assets" {
		t.Errorf("no dir configured = %+v, want skip", it)
	}

	dir := t.TempDir()
	b.Diagnostics.WebDist = dir
	if it := b.checkWebAssets(); it.State != diagFail || it.Fix != "assetsMissing" {
		t.Errorf("missing index.html = %+v, want fail/assetsMissing", it)
	}
	if err := os.WriteFile(filepath.Join(dir, "index.html"), []byte("<html></html>"), 0o644); err != nil {
		t.Fatal(err)
	}
	it := b.checkWebAssets()
	if it.State != diagOK || it.Value != filepath.Join(dir, "index.html") {
		t.Errorf("built bundle = %+v, want ok with the index path", it)
	}
}

func TestCheckKeyring(t *testing.T) {
	b := newDiagBackend(t)
	if it := b.checkKeyring(); it.State != diagSkip || it.Fix != "keyringNoProbe" {
		t.Errorf("no probe = %+v, want skip (never a silent green)", it)
	}
	b.Diagnostics.KeyringStatus = func() (int, string) { return 0, "" }
	if it := b.checkKeyring(); it.State != diagSkip || it.Fix != "keyringLocalOnly" {
		t.Errorf("local-only install = %+v, want skip", it)
	}
	b.Diagnostics.KeyringStatus = func() (int, string) { return 0, "https://market.example" }
	if it := b.checkKeyring(); it.State != diagFail || it.Fix != "keyringNoKeys" {
		t.Errorf("market with no keys = %+v, want fail", it)
	}
	b.Diagnostics.KeyringStatus = func() (int, string) { return 2, "https://market.example" }
	if it := b.checkKeyring(); it.State != diagOK || it.Value != "2" {
		t.Errorf("two keys = %+v, want ok/2", it)
	}
}

func TestCheckSiteURL(t *testing.T) {
	ctx := context.Background()
	b := newDiagBackend(t)
	b.BaseURL = "https://skin.example.com"
	it := b.checkSiteURL(ctx)
	if it.State != diagOK {
		t.Errorf("public option base = %+v, want ok", it)
	}

	b.BaseURL = "http://192.168.1.7:8080"
	if it := b.checkSiteURL(ctx); it.State != diagWarn || it.Fix != "siteLan" {
		t.Errorf("LAN base = %+v, want warn/siteLan", it)
	}

	// The live sequence this branch exists for: the operator saves a public
	// site_url in the panel, the process keeps the boot-resolved LAN address
	// until restart — and must not be told to go change the thing they changed.
	if err := b.Store.SetOption(ctx, "site_url", "https://skin.example.com"); err != nil {
		t.Fatal(err)
	}
	if it := b.checkSiteURL(ctx); it.State != diagWarn || it.Fix != "siteNeedsRestart" {
		t.Errorf("public option + stale boot base = %+v, want warn/siteNeedsRestart", it)
	}
	// After the restart the same option resolves, so it is simply ok.
	b.BaseURL = "https://skin.example.com"
	if it := b.checkSiteURL(ctx); it.State != diagOK {
		t.Errorf("restart applied the option = %+v, want ok", it)
	}
	// A LAN option on a LAN boot is still the LAN problem, not a restart hint.
	b.BaseURL = "http://127.0.0.1:8080"
	if err := b.Store.SetOption(ctx, "site_url", "http://10.1.2.3:8080"); err != nil {
		t.Fatal(err)
	}
	if it := b.checkSiteURL(ctx); it.State != diagWarn || it.Fix != "siteLan" {
		t.Errorf("LAN option, LAN boot = %+v, want warn/siteLan", it)
	}
	_ = b.Store.SetOption(ctx, "site_url", "")

	b.BaseURL = ""
	if it := b.checkSiteURL(ctx); it.State != diagFail || it.Fix != "siteEmpty" {
		t.Errorf("nothing resolved = %+v, want fail", it)
	}

	// Legacy option wins over env: works, but the env change is inert.
	b.BaseURL = "https://old.example.com"
	if err := b.Store.SetOption(ctx, "base_url", "https://old.example.com"); err != nil {
		t.Fatal(err)
	}
	if it := b.checkSiteURL(ctx); it.State != diagWarn || it.Fix != "siteLegacyBase" {
		t.Errorf("legacy base_url = %+v, want warn/siteLegacyBase", it)
	}
	// The option is named in the value, so the operator can find the field.
	if err := b.Store.SetOption(ctx, "site_url", "https://new.example.com"); err != nil {
		t.Fatal(err)
	}
	b.BaseURL = "https://new.example.com"
	it = b.checkSiteURL(ctx)
	if it.State != diagOK {
		t.Errorf("site_url set = %+v, want ok", it)
	}
	for _, want := range []string{"https://new.example.com", "https://old.example.com"} {
		if !strings.Contains(it.Value, want) {
			t.Errorf("value %q should name %q", it.Value, want)
		}
	}
}

func TestCheckMail(t *testing.T) {
	ctx := context.Background()
	b := newDiagBackend(t)
	if it := b.checkMail(ctx); it.State != diagSkip || it.Fix != "mailOff" {
		t.Errorf("no smtp = %+v, want skip", it)
	}
	// The live defect this line exists for: a host alone saves cleanly in the
	// panel and then delivers nothing.
	if err := b.Store.SetOption(ctx, "smtp_host", "smtp.example.com"); err != nil {
		t.Fatal(err)
	}
	if it := b.checkMail(ctx); it.State != diagFail || it.Fix != "mailNoSender" {
		t.Errorf("host without sender = %+v, want fail/mailNoSender", it)
	}
	if err := b.Store.SetOption(ctx, "smtp_from", "site@example.com"); err != nil {
		t.Fatal(err)
	}
	it := b.checkMail(ctx)
	if it.State != diagOK || !strings.Contains(it.Value, "smtp.example.com:465") || !strings.Contains(it.Value, "site@example.com") {
		t.Errorf("configured mail = %+v, want ok with host:port and sender", it)
	}
}

func TestCheckMigrations(t *testing.T) {
	ctx := context.Background()
	b := newDiagBackend(t)
	b.MigrationsDir = filepath.Join("..", "..", "migrations")
	it := b.checkMigrations(ctx)
	if it.State != diagOK {
		t.Fatalf("fresh tree applied = %+v, want ok", it)
	}
	// A tree newer than the database is the "upgraded the image but left
	// SKIN_AUTO_MIGRATE off" case — the watermark row alone cannot show it,
	// because the number looks plausible either way.
	stale := t.TempDir()
	tree := filepath.Join(stale, "sqlite")
	if err := os.MkdirAll(tree, 0o755); err != nil {
		t.Fatal(err)
	}
	applied, _ := b.Store.MigrationVersion(ctx)
	for _, f := range []string{applied, "9999_future.sql"} {
		if err := os.WriteFile(filepath.Join(tree, f), []byte("-- no statements\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	b.MigrationsDir = stale
	pending := b.checkMigrations(ctx)
	if pending.State != diagWarn || pending.Fix != "migrationsPending" {
		t.Errorf("tree newer than schema = %+v, want warn/migrationsPending", pending)
	}
	if !strings.Contains(pending.Value, "9999_future.sql") {
		t.Errorf("value %q should name the pending file", pending.Value)
	}
	// A directory that holds no tree for this engine cannot prove anything.
	b.MigrationsDir = t.TempDir()
	if it := b.checkMigrations(ctx); it.State != diagSkip || it.Fix != "migrationsNoTree" {
		t.Errorf("no tree = %+v, want skip", it)
	}
	b.MigrationsDir = ""
	if it := b.checkMigrations(ctx); it.State != diagSkip || it.Fix != "migrationsNoDir" {
		t.Errorf("no dir = %+v, want skip", it)
	}
}

func TestCheckPreview(t *testing.T) {
	ctx := context.Background()
	b := newDiagBackend(t)
	if it := b.checkPreview(ctx); it.State != diagSkip || it.Fix != "previewNoProbe" {
		t.Errorf("no probe = %+v, want skip", it)
	}
	b.Diagnostics.Preview = func(context.Context) (bool, error) { return false, nil }
	if it := b.checkPreview(ctx); it.State != diagSkip || it.Fix != "previewNothing" {
		t.Errorf("empty library = %+v, want skip (not a pass)", it)
	}
	b.Diagnostics.Preview = func(context.Context) (bool, error) { return true, nil }
	if it := b.checkPreview(ctx); it.State != diagOK {
		t.Errorf("rendered = %+v, want ok", it)
	}
	b.Diagnostics.Preview = func(context.Context) (bool, error) {
		return true, os.ErrNotExist
	}
	if it := b.checkPreview(ctx); it.State != diagFail || it.Fix != "previewBroken" {
		t.Errorf("missing file = %+v, want fail", it)
	}
}

func TestDiagnosticsRollupAndHandler(t *testing.T) {
	b := newDiagBackend(t)
	b.BaseURL = "https://skin.example.com"
	b.MigrationsDir = filepath.Join("..", "..", "migrations")
	b.Diagnostics.WebDist = filepath.Join("..", "..", "..", "web", "dist") // may or may not exist
	b.Diagnostics.Preview = func(context.Context) (bool, error) { return false, nil }
	b.Diagnostics.KeyringStatus = func() (int, string) { return 1, "" }

	ids := map[string]bool{}
	for _, it := range b.diagnostics(context.Background()) {
		if it.ID == "" || it.State == "" {
			t.Fatalf("every row needs an id and a state, got %+v", it)
		}
		if ids[it.ID] {
			t.Errorf("duplicate id %q", it.ID)
		}
		ids[it.ID] = true
		if it.Fix != "" && !knownDiagFix(it.Fix) {
			t.Errorf("fix %q has no dictionary key", it.Fix)
		}
	}
	for _, want := range []string{"site", "migrations", "mail", "mail_dns", "preview", "assets", "keyring"} {
		if !ids[want] {
			t.Errorf("the closed set must always carry %q, got %v", want, ids)
		}
	}
	if len(ids) != 7 {
		t.Errorf("the set is closed at 7, got %d: %v", len(ids), ids)
	}

	w := httptest.NewRecorder()
	b.handleDiagnostics(w, httptest.NewRequest("GET", "/api/admin/v1/diagnostics", nil))
	if w.Code != 200 {
		t.Fatalf("status = %d body %s", w.Code, w.Body.String())
	}
	body := w.Body.String()
	for _, want := range []string{`"checked_at"`, `"state"`, `"items"`, `"id":"site"`} {
		if !strings.Contains(body, want) {
			t.Errorf("envelope missing %s in %s", want, body)
		}
	}
}

// knownDiagFix mirrors the dictionary keys the SPA declares; a check that
// invents a fix string nobody translates would render as a raw key.
func knownDiagFix(fix string) bool {
	for _, k := range diagFixKeys {
		if k == fix {
			return true
		}
	}
	return false
}

// diagFixKeys is pinned against the locales by scripts/check-locales.mjs; kept
// here so a Go-side typo fails `go test` too.
var diagFixKeys = []string{
	"siteEmpty", "siteLan", "siteNeedsRestart", "siteLegacyBase",
	"migrationsUnreadable", "migrationsNoDir", "migrationsNoTree", "migrationsPending",
	"mailNoSender", "mailOff",
	"mailDnsOff", "mailDnsNoDomain", "mailDnsUnresolved", "mailDnsSpf", "mailDnsDmarc", "mailDnsBoth",
	"previewNoProbe", "previewNothing", "previewBroken",
	"assetsNoDir", "assetsMissing",
	"keyringNoProbe", "keyringLocalOnly", "keyringNoKeys",
}

func TestRankOrdersStates(t *testing.T) {
	if !(rank(diagOK) < rank(diagSkip) && rank(diagSkip) < rank(diagWarn) && rank(diagWarn) < rank(diagFail)) {
		t.Errorf("rank must order ok < skip < warn < fail, got %d %d %d %d",
			rank(diagOK), rank(diagSkip), rank(diagWarn), rank(diagFail))
	}
}
