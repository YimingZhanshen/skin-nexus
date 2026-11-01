// Package migrate is the blessing-skin-server -> SkinNexus migration engine.
//
// Core pieces (docs/phase1-dev-plan.md §5 / analysis/blessing-skin-plugins.md):
//   - legacy .env parsing (PWD_METHOD + SALT positional alignment);
//   - hash distribution preflight (which of the 12 formulas the corpus uses);
//   - uuid table five-step cleaning (fix-uuid-table equivalent);
//   - texture tree scan: full re-hash + IHDR check (原样搬迁 policy:
//     report-only, never rewrite legacy bytes).
package migrate

import (
	"bufio"
	"fmt"
	"os"
	"strings"

	"skin-nexus/server/internal/auth"
)

// LegacyEnv holds the migration-relevant subset of the old .env.
type LegacyEnv struct {
	AppURL      string
	PWDMethods  []string // PASSWORD_METHODS order
	Salts       []string // SALT slots, positionally aligned with PWDMethods
	DBDSN       string   // legacy DB_* fields, concatenated for reference
	TexturesDir string
}

// MethodSalts returns the positional pairs for auth.LegacyVerifier.
func (e *LegacyEnv) MethodSalts() []auth.MethodSalt {
	out := make([]auth.MethodSalt, 0, len(e.PWDMethods))
	for i, m := range e.PWDMethods {
		salt := ""
		if i < len(e.Salts) {
			salt = e.Salts[i]
		}
		out = append(out, auth.MethodSalt{Method: normalizeMethod(m), Salt: salt})
	}
	return out
}

// ParseLegacyEnv reads a Laravel-style .env file. PASSWORD_METHODS and SALT
// are comma-separated lists that MUST stay positionally aligned (the legacy
// README warns misaligned commas silently break verification).
func ParseLegacyEnv(path string) (*LegacyEnv, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	env := &LegacyEnv{}
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		k, v, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		k = strings.TrimSpace(k)
		v = unquote(strings.TrimSpace(v))
		switch k {
		case "APP_URL":
			env.AppURL = v
		case "PASSWORD_METHODS", "PWD_METHOD":
			// Real legacy .env uses PWD_METHOD (single value, possibly a
			// comma list); some forks used PASSWORD_METHODS. Accept both.
			env.PWDMethods = splitCSV(v)
		case "SALT":
			env.Salts = splitCSV(v)
		case "DB_DATABASE", "DB_USERNAME", "DB_PASSWORD", "DB_HOST":
			env.DBDSN += k + "=" + v + " "
		case "TEXTURES_DIR":
			env.TexturesDir = v
		}
	}
	if len(env.PWDMethods) == 0 {
		return nil, fmt.Errorf("migrate: %s has no PWD_METHOD/PASSWORD_METHODS", path)
	}
	// SALT is optional: absent = empty salts (correct for BCRYPT/ARGON2I/
	// PHP_PASSWORD_HASH whose salt lives inside the digest; SALTED* families
	// without a configured salt will simply never match).
	if len(env.Salts) == 0 {
		env.Salts = make([]string, len(env.PWDMethods))
	}
	if len(env.Salts) != len(env.PWDMethods) {
		return nil, fmt.Errorf("migrate: SALT slots (%d) != PWD_METHOD (%d) — legacy misalignment, fix before migrating",
			len(env.Salts), len(env.PWDMethods))
	}
	return env, sc.Err()
}

func normalizeMethod(m string) string {
	switch strings.ToUpper(strings.TrimSpace(m)) {
	case "MD5":
		return auth.MethodMD5
	case "SALTED2MD5":
		return auth.MethodSALTED2MD5
	case "SHA256":
		return auth.MethodSHA256
	case "SHA512":
		return auth.MethodSHA512
	case "SALTED2SHA256":
		return auth.MethodSALTED2SHA256
	case "SALTED2SHA512":
		return auth.MethodSALTED2SHA512
	case "BCRYPT":
		return auth.MethodBCRYPT
	case "ARGON2I":
		return auth.MethodARGON2I
	case "PHP_PASSWORD_HASH", "PASSWORD_DEFAULT":
		return auth.MethodPHPDefault
	default:
		return strings.ToLower(strings.TrimSpace(m))
	}
}

func splitCSV(v string) []string {
	parts := strings.Split(v, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		out = append(out, unquote(strings.TrimSpace(p)))
	}
	return out
}

func unquote(v string) string {
	if len(v) >= 2 {
		if (v[0] == '"' && v[len(v)-1] == '"') || (v[0] == '\'' && v[len(v)-1] == '\'') {
			return v[1 : len(v)-1]
		}
	}
	return v
}

// ---- hash distribution preflight ----

// HashDistribution classifies stored hashes by shape (prefix-based; the
// ground-truth check is VerifyLegacy against real samples — see tests).
type HashDistribution struct {
	Bcrypt    int // $2y$/$2a$/$2b$
	Argon2    int // $argon2*
	AuthmeSHA int // $SHA$
	Hex32     int // raw md5 family
	Hex64     int // raw sha256 family
	Hex128    int // raw sha512 family
	Other     int
	WeakShare float64 // (Hex* + AuthmeSHA + Other) / total
}

func ClassifyHashes(stored []string) HashDistribution {
	var d HashDistribution
	total := len(stored)
	for _, h := range stored {
		switch {
		case strings.HasPrefix(h, "$2"):
			d.Bcrypt++
		case strings.HasPrefix(h, "$argon2"):
			d.Argon2++
		case strings.HasPrefix(h, "$SHA$"):
			d.AuthmeSHA++
		case isHexOfLen(h, 32):
			d.Hex32++
		case isHexOfLen(h, 64):
			d.Hex64++
		case isHexOfLen(h, 128):
			d.Hex128++
		default:
			d.Other++
		}
	}
	if total > 0 {
		d.WeakShare = float64(d.Hex32+d.Hex64+d.Hex128+d.AuthmeSHA+d.Other) / float64(total)
	}
	return d
}

func isHexOfLen(s string, n int) bool {
	if len(s) != n {
		return false
	}
	for _, c := range s {
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return false
		}
	}
	return true
}
