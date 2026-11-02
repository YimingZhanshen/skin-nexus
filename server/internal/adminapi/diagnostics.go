package adminapi

// Diagnostics: the "is this deployment actually wired up?" self-report, shown on
// the 运行状态 page.
//
// Why it exists: every item below is a mistake operators really make, and each
// one currently shows up as a support question — the site loads, nothing looks
// broken, and skins simply do not appear. The status snapshot (`/status`) can't
// carry these: it is a cacheable facts snapshot, while these checks read
// mutable configuration, the filesystem and the renderer.
//
// The set is deliberately CLOSED (six items). A self-check surface grows into a
// wishlist otherwise, and every added item has to be something an operator can
// act on in the panel.
//
// Wording rule: the server sends ids, states and data (paths, versions, hosts).
// The sentences are the SPA's dictionary (`admin.diag.*` / `admin.diagFix.*`),
// like notification kinds — so a Chinese panel does not get half-translated
// English prose. The two exceptions are a filesystem/render error string and a
// path: both are data an operator greps for, not prose to translate.

import (
	"context"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"skin-nexus/server/internal/mailer"
)

// diagItem is one row of the report.
type diagItem struct {
	ID    string `json:"id"`
	State string `json:"state"` // ok | warn | fail | skip
	Value string `json:"value,omitempty"`
	Fix   string `json:"fix,omitempty"` // suffix under admin.diagFix.
}

const (
	diagOK   = "ok"
	diagWarn = "warn"
	diagFail = "fail"
	diagSkip = "skip"
)

// DiagnosticsDeps carries what only the boot knows: where the SPA bundle lives,
// the untouched SKIN_BASE_URL, and two probes. Wired in main.go, nil-tolerant
// by design — a missing probe reports `skip`, never a false green.
//
// The migration tree (`MigrationsDir`) and the address the process actually
// resolved (`BaseURL`) are already fields on Backend; they are not repeated here.
type DiagnosticsDeps struct {
	WebDist string
	EnvBase string // raw SKIN_BASE_URL, empty when the operator never set it
	// Preview renders the newest public skin through the real pipeline and
	// reports whether there was anything to render.
	Preview func(ctx context.Context) (bool, error)
	// KeyringStatus reports how many plugin-signing keys loaded and the market URL.
	KeyringStatus func() (keys int, marketURL string)
}

// diagnostics runs the six checks in a fixed order.
func (b *Backend) diagnostics(ctx context.Context) []diagItem {
	return []diagItem{
		b.checkSiteURL(ctx),
		b.checkMigrations(ctx),
		b.checkMail(ctx),
		b.checkPreview(ctx),
		b.checkWebAssets(),
		b.checkKeyring(),
	}
}

func (b *Backend) handleDiagnostics(w http.ResponseWriter, r *http.Request) {
	items := b.diagnostics(r.Context())
	worst := diagOK
	for _, it := range items {
		if rank(it.State) > rank(worst) {
			worst = it.State
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"checked_at": time.Now().UTC().Format(time.RFC3339),
		"state":      worst,
		"items":      items,
	})
}

// rank orders the states so one row's severity can roll up to the page badge.
func rank(state string) int {
	switch state {
	case diagSkip:
		return 1
	case diagWarn:
		return 2
	case diagFail:
		return 3
	}
	return 0
}

// ---- 1. site address -------------------------------------------------------

// checkSiteURL is the single most useful line here. A base URL resolving to a
// loopback or LAN address makes every material link unfetchable by a player or
// a launcher, and the option that caused it used to be invisible in this panel
// (the setup wizard wrote it from `location.origin`, see the U15 note).
//
// `value` lists all four sources because "which one wins" is the question
// operators ask; the resolved address is `BaseURL`, injected after the boot
// resolution ran.
func (b *Backend) checkSiteURL(ctx context.Context) diagItem {
	siteURL, _, _ := b.Store.GetOption(ctx, "site_url")
	legacyBase, _, _ := b.Store.GetOption(ctx, "base_url")
	envBase := strings.TrimSpace(b.Diagnostics.EnvBase)
	resolved := strings.TrimRight(strings.TrimSpace(b.BaseURL), "/")
	value := orNone(resolved) + " | option site_url: " + orNone(siteURL) +
		" | option base_url: " + orNone(legacyBase) + " | env SKIN_BASE_URL: " + orNone(envBase)
	switch {
	case resolved == "":
		return diagItem{ID: "site", State: diagFail, Fix: "siteEmpty"}
	case isPrivateBase(resolved) && publicSiteOption(siteURL, resolved):
		// The operator already fixed it — the panel save only takes effect on
		// restart, because siteurl is written once during boot. Saying "use a
		// public address" here would blame them for the one thing they did.
		return diagItem{ID: "site", State: diagWarn, Value: value, Fix: "siteNeedsRestart"}
	case isPrivateBase(resolved):
		// Reachable by the operator's own browser, not by a player.
		return diagItem{ID: "site", State: diagWarn, Value: value, Fix: "siteLan"}
	case siteURL == "" && legacyBase != "":
		// Still works, but the legacy key outranks the env default, so a
		// changed SKIN_BASE_URL silently does nothing.
		return diagItem{ID: "site", State: diagWarn, Value: value, Fix: "siteLegacyBase"}
	}
	return diagItem{ID: "site", State: diagOK, Value: value}
}

// publicSiteOption reports whether the saved option is a public address that
// differs from the one this process resolved at boot (= restart pending).
func publicSiteOption(siteURL, resolved string) bool {
	siteURL = strings.TrimSpace(siteURL)
	return siteURL != "" && !isPrivateBase(siteURL) && !sameBase(siteURL, resolved)
}

func sameBase(a, b string) bool {
	trim := func(s string) string {
		s = strings.ToLower(strings.TrimRight(strings.TrimSpace(s), "/"))
		if !strings.Contains(s, "://") {
			s = "//" + s
		}
		return s
	}
	return trim(a) == trim(b)
}

// isPrivateBase reports loopback / RFC1918 / link-local hosts — the addresses a
// wizard happily fills in from `location.origin` while installing over SSH.
func isPrivateBase(raw string) bool {
	u := strings.ToLower(strings.TrimSpace(raw))
	if i := strings.Index(u, "://"); i >= 0 {
		u = u[i+3:]
	}
	if i := strings.IndexAny(u, "/?#"); i >= 0 {
		u = u[:i]
	}
	u = strings.TrimPrefix(u, "www.")
	if h, _, ok := strings.Cut(u, ":"); ok {
		u = h
	}
	u = strings.Trim(u, "[]")
	switch {
	case u == "" || u == "localhost" || u == "::1" || u == "0.0.0.0":
		return true
	case strings.HasPrefix(u, "127."):
		return true
	case strings.HasPrefix(u, "10."), strings.HasPrefix(u, "192.168."):
		return true
	case strings.HasPrefix(u, "172."):
		// 172.16.0.0/12 only — 172.32.x.x is public.
		if parts := strings.Split(u, "."); len(parts) >= 2 {
			if n := atoiSafe(parts[1]); n >= 16 && n <= 31 {
				return true
			}
		}
	case strings.HasPrefix(u, "169.254."):
		return true
	}
	return false
}

func orNone(s string) string {
	if strings.TrimSpace(s) == "" {
		return "—"
	}
	return strings.TrimSpace(s)
}

// ---- 2. migration watermark -----------------------------------------------

// checkMigrations catches the upgrade that booted an older binary (or skipped
// its DDL because SKIN_AUTO_MIGRATE was off): the app answers, one screen
// errors, and the cause is a schema the code has never seen. The status page
// already prints the applied marker; only the comparison against the tree that
// shipped with this binary says "pending".
func (b *Backend) checkMigrations(ctx context.Context) diagItem {
	applied, err := b.Store.MigrationVersion(ctx)
	if err != nil {
		return diagItem{ID: "migrations", State: diagFail, Value: err.Error(), Fix: "migrationsUnreadable"}
	}
	dir := b.MigrationsDir
	if dir == "" {
		return diagItem{ID: "migrations", State: diagSkip, Fix: "migrationsNoDir"}
	}
	expected, err := newestMigrationFile(dir, string(b.Store.Engine()))
	if err != nil || expected == "" {
		return diagItem{ID: "migrations", State: diagSkip, Value: dir, Fix: "migrationsNoTree"}
	}
	value := orNone(applied) + " → " + expected
	if applied == "" {
		return diagItem{ID: "migrations", State: diagFail, Value: value, Fix: "migrationsPending"}
	}
	if applied < expected {
		return diagItem{ID: "migrations", State: diagWarn, Value: value, Fix: "migrationsPending"}
	}
	return diagItem{ID: "migrations", State: diagOK, Value: value}
}

// newestMigrationFile returns the highest-numbered file of one engine's tree.
// The names are zero-padded (`0022_…`), so the lexicographic max is the numeric
// max — the same ordering `schema_migrations.file` relies on.
func newestMigrationFile(root, engine string) (string, error) {
	matches, err := filepath.Glob(filepath.Join(root, engine, "*.sql"))
	if err != nil {
		return "", err
	}
	names := make([]string, 0, len(matches))
	for _, m := range matches {
		names = append(names, filepath.Base(m))
	}
	if len(names) == 0 {
		return "", nil
	}
	sort.Strings(names)
	return names[len(names)-1], nil
}

// ---- 3. mail ---------------------------------------------------------------

// checkMail states what the password-reset and notification flows will do. The
// half-configured case is the valuable one: a host with no sender address saves
// cleanly in the panel and then delivers nothing.
func (b *Backend) checkMail(ctx context.Context) diagItem {
	if cfg := b.mailConfig(ctx); cfg != nil {
		sender := cfg.From
		if sender == "" {
			sender = cfg.User
		}
		return diagItem{ID: "mail", State: diagOK, Value: cfg.Host + ":" + itoa(cfg.Port) + " → " + sender}
	}
	host, _, _ := b.Store.GetOption(ctx, "smtp_host")
	if strings.TrimSpace(host) != "" {
		return diagItem{ID: "mail", State: diagFail, Value: host, Fix: "mailNoSender"}
	}
	return diagItem{ID: "mail", State: diagSkip, Fix: "mailOff"}
}

// mailConfig answers the same question the user-facing password reset does; the
// precedence rule itself lives in mailer.ConfigFrom so the two cannot drift.
func (b *Backend) mailConfig(ctx context.Context) *mailer.Config {
	return mailer.ConfigFrom(func(key string) string {
		if v, ok, err := b.Store.GetOption(ctx, key); err == nil && ok {
			return v
		}
		return b.SettingsDefaults[key]
	}, b.SiteName)
}

// ---- 4. preview pipeline ---------------------------------------------------

// checkPreview proves the material pipeline end to end on real data. The
// failure that bites operators is never "the renderer is missing from the
// binary" — it is "the texture row exists and its file does not", which is what
// a moved data volume or a file-skipping import looks like.
func (b *Backend) checkPreview(ctx context.Context) diagItem {
	probe := b.Diagnostics.Preview
	if probe == nil {
		return diagItem{ID: "preview", State: diagSkip, Fix: "previewNoProbe"}
	}
	tested, err := probe(ctx)
	if err != nil {
		return diagItem{ID: "preview", State: diagFail, Value: err.Error(), Fix: "previewBroken"}
	}
	if !tested {
		return diagItem{ID: "preview", State: diagSkip, Fix: "previewNothing"}
	}
	return diagItem{ID: "preview", State: diagOK}
}

// ---- 5. SPA bundle ---------------------------------------------------------

// checkWebAssets is the placeholder-page trap: with the API answering and no
// web/dist next to the binary, every page except the server-rendered ones shows
// the "assets missing" placeholder.
func (b *Backend) checkWebAssets() diagItem {
	dir := b.Diagnostics.WebDist
	if dir == "" {
		return diagItem{ID: "assets", State: diagSkip, Fix: "assetsNoDir"}
	}
	idx := filepath.Join(dir, "index.html")
	if st, err := os.Stat(idx); err != nil || st.IsDir() {
		return diagItem{ID: "assets", State: diagFail, Value: idx, Fix: "assetsMissing"}
	}
	return diagItem{ID: "assets", State: diagOK, Value: idx}
}

// ---- 6. plugin signing keyring --------------------------------------------

// checkKeyring: an official market URL with zero trusted keys means every
// install and update fails at verification, which reads like a broken registry
// rather than an unsigned build.
func (b *Backend) checkKeyring() diagItem {
	probe := b.Diagnostics.KeyringStatus
	if probe == nil {
		return diagItem{ID: "keyring", State: diagSkip, Fix: "keyringNoProbe"}
	}
	keys, market := probe()
	switch {
	case keys == 0 && strings.TrimSpace(market) == "":
		return diagItem{ID: "keyring", State: diagSkip, Fix: "keyringLocalOnly"}
	case keys == 0:
		return diagItem{ID: "keyring", State: diagFail, Value: market, Fix: "keyringNoKeys"}
	}
	return diagItem{ID: "keyring", State: diagOK, Value: strconv.Itoa(keys)}
}

func itoa(n int) string { return strconv.Itoa(n) }

func atoiSafe(s string) int {
	n, err := strconv.Atoi(s)
	if err != nil {
		return -1
	}
	return n
}
