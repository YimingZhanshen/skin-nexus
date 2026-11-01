// env.go is the authoritative list of `SKIN_*` environment variables the
// container may set, and the warning that fires when it sets one nobody reads.
//
// The failure this prevents is the quiet kind: an operator copies
// `SKIN_TEXUTRES_DIR` from an old compose file, the typo'd variable is ignored,
// textures land in the default volume and the site "works" until the disk fills.
// A deploy that sets nothing unknown logs nothing, so the warning is safe to
// leave on permanently.
//
// Two things keep the list honest:
//   - TestKnownEnvMatchesSourceReads walks every non-test source file and fails
//     if an `os.Getenv`/`os.LookupEnv`/`envOr` literal is not declared here;
//   - scripts/check-docs.mjs compares this list against the table in
//     docs/deploy.md §2.3 in both directions.
//
// `compose:` entries are read by docker-compose.yml, not by the process — they
// are listed so a normal deploy does not warn about its own file, and each one
// says who consumes it.
package config

import (
	"log"
	"os"
	"sort"
	"strings"
)

// KnownEnv is every SKIN_* variable the process itself reads.
var KnownEnv = []string{
	// core
	"SKIN_DATABASE_DSN", "SKIN_LISTEN", "SKIN_BASE_URL", "SKIN_ENV",
	"SKIN_DATA_DIR", "SKIN_TEXTURES_DIR", "SKIN_THEMES_DIR", "SKIN_PLUGINS_DIR",
	"SKIN_MIGRATIONS_DIR", "SKIN_AUTO_MIGRATE", "SKIN_LEGACY_ENV",
	// identity and branding
	"SKIN_SITE_NAME", "SKIN_SERVER_NAME", "SKIN_SKIN_DOMAINS",
	"SKIN_SIGN_KEY", "SKIN_ADMIN_TOKEN",
	// registration and auth
	"SKIN_OIDC_ENABLED", "SKIN_OIDC_ISSUER", "SKIN_OIDC_SIGN_KEY",
	"SKIN_OIDC_SHARED_CLIENT_ID", "SKIN_REQUIRE_VERIFICATION", "SKIN_INVITE_REQUIRED",
	"SKIN_LOGIN_FAIL_LIMIT", "SKIN_RATE_LIMIT_AUTH",
	// mail (A1 password recovery)
	"SKIN_SMTP_HOST", "SKIN_SMTP_PORT", "SKIN_SMTP_USER", "SKIN_SMTP_PASS", "SKIN_SMTP_FROM",
	// plugin platform and markets
	"SKIN_PLUGIN_SECRET_KEY", "SKIN_PLUGIN_STATE_DIR", "SKIN_PLUGIN_MARKET_URL",
	"SKIN_PLUGIN_TRUST_KEYS", "SKIN_PLUGIN_UPLOAD_MAX",
	"SKIN_THEME_MARKET_URL", "SKIN_THEME_TRUST_KEYS",
	// yggdrasil / union federation
	"SKIN_MOJANG_FALLBACK",
	"SKIN_UNION_ENABLED", "SKIN_UNION_API_ROOT", "SKIN_UNION_MEMBER_KEY",
	"SKIN_UNION_HOST_PUBKEY", "SKIN_UNION_ALLOW_UPDATEBACKENDKEY",
	"SKIN_UNION_QUERYEMAIL_MODE", "SKIN_UNION_OAUTH2_SIG_KEY", "SKIN_UNION_HUB_ORIGIN",
}

// TestOnlyEnv is read by _test.go files, which the source walk deliberately
// skips — a test setting a scratch variable would otherwise look like a
// production read. Declared here so the boot warning stays quiet and the
// "everything listed is read" check can say why these are different.
var TestOnlyEnv = []string{"SKIN_TEST_DSN"}

// ComposeEnv are SKIN_* names docker-compose.yml reads and no process does.
// Each entry records who consumes it, so nobody "fixes" the list by deleting a
// line they cannot find in Go.
var ComposeEnv = map[string]string{
	"SKIN_PORT":             "compose: host port mapped to the container's 8080",
	"SKIN_IMAGE_TAG":        "compose: which GHCR tag to pull",
	"SKIN_DB_PASSWORD":      "compose: interpolated into SKIN_DATABASE_DSN, and MYSQL_PASSWORD for the db service",
	"SKIN_DB_ROOT_PASSWORD": "compose: MYSQL_ROOT_PASSWORD for the db service only",
}

// unknownEnv lists the set SKIN_* variables that neither the process nor the
// compose template reads. Split out from the warning so the rule is testable.
func unknownEnv() []string {
	known := map[string]bool{}
	for _, k := range KnownEnv {
		known[k] = true
	}
	for k := range ComposeEnv {
		known[k] = true
	}
	for _, k := range TestOnlyEnv {
		known[k] = true
	}
	var unknown []string
	for _, kv := range os.Environ() {
		name, _, _ := strings.Cut(kv, "=")
		if strings.HasPrefix(name, "SKIN_") && !known[name] {
			unknown = append(unknown, name)
		}
	}
	sort.Strings(unknown)
	return unknown
}

// WarnUnknownEnv logs them once at boot. A clean environment logs nothing.
func WarnUnknownEnv() {
	unknown := unknownEnv()
	if len(unknown) == 0 {
		return
	}
	log.Printf("WARNING: %d SKIN_* variable(s) are set but nothing reads them: %s "+
		"(check the spelling against docs/deploy.md §2.3)", len(unknown), strings.Join(unknown, ", "))
}
