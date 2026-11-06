// Legacy password verification for blessing-skin-server migrations.
//
// Formulas are byte-exact ports of the PHP implementations documented in
// analysis/blessing-skin-plugins.md §3.2 (source line evidence there):
//
//	MD5             md5(pw)
//	SALTED2MD5      md5(md5(pw).salt)                       salt=global SALT slot
//	SHA256          hash('sha256', pw)
//	SHA512          hash('sha512', pw)
//	SALTED2SHA256   hash('sha256', hash('sha256', pw).salt)
//	SALTED2SHA512   hash('sha512', hash('sha512', pw).salt)
//	BCRYPT          password_hash(PASSWORD_BCRYPT)          PHC $2y$
//	ARGON2I         password_hash(PASSWORD_ARGON2I)         PHC $argon2i$
//	PHP_DEFAULT     password_hash(PASSWORD_DEFAULT)         PHC
//	AuthMe SHA256   '$SHA$'.salt.'$'.hash('sha256', hash('sha256', pw).salt)  salt=16hex, embedded
//	AuthMe SALTED2MD5      md5(md5(pw).salt)                salt=6hex
//	AuthMe SALTED2SHA512   hash('sha512', pw.salt)          NOTE: pw+salt, NOT sha512(pw)+salt
//
// Legacy semantics preserved deliberately:
//   - comparison is case-sensitive lowercase-hex string equality (hash_equals);
//   - SALT is positionally aligned with PASSWORD_METHODS ("SALT 按位对应");
//   - with no per-user algorithm marker, verification TRIES EVERY configured
//     method in order (official strategy: password-algorithm-transition
//     Cipher.php:24-49), then rehashes lazily on first login.
//
// The AuthMe storage layout for SALTED2MD5/SALTED2SHA512 (embedded salt vs
// positional SALT) is calibrated against real legacy rows at migration time;
// both layouts are supported here.
package auth

import (
	"crypto/hmac"
	"crypto/md5" //nolint:gosec // legacy migration compatibility
	"crypto/rand"
	"crypto/sha256"
	"crypto/sha512"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"strconv"
	"strings"

	"golang.org/x/crypto/argon2"
	"golang.org/x/crypto/bcrypt"
)

// Method names (stable config keys).
const (
	MethodMD5                 = "md5"
	MethodSALTED2MD5          = "salted2md5"
	MethodSHA256              = "sha256"
	MethodSHA512              = "sha512"
	MethodSALTED2SHA256       = "salted2sha256"
	MethodSALTED2SHA512       = "salted2sha512"
	MethodBCRYPT              = "bcrypt"
	MethodARGON2I             = "argon2i"
	MethodPHPDefault          = "php_password_hash"
	MethodAuthmeSHA256        = "authme_sha256"
	MethodAuthmeSALTED2MD5    = "authme_salted2md5"
	MethodAuthmeSALTED2SHA512 = "authme_salted2sha512"
)

// MethodSalt is one entry of the legacy PASSWORD_METHODS + SALT pair,
// positionally aligned (index i of both arrays).
type MethodSalt struct {
	Method string
	Salt   string
}

// LegacyVerifier verifies legacy hashes against the configured method list.
type LegacyVerifier struct {
	methods []MethodSalt
}

func NewLegacyVerifier(methods []MethodSalt) *LegacyVerifier {
	return &LegacyVerifier{methods: methods}
}

// VerifyLegacy tries each configured method in order and returns the matched
// method name (mirrors hash_equals semantics: case-sensitive).
func (v *LegacyVerifier) VerifyLegacy(password, stored string) (matched string, ok bool) {
	for _, ms := range v.methods {
		if verifyLegacyOne(ms.Method, password, ms.Salt, stored) {
			return ms.Method, true
		}
	}
	return "", false
}

// VerifyOne verifies against a single pinned method (used when the legacy
// row's algorithm is known, e.g. password_algo = "legacy:sha256").
func (v *LegacyVerifier) VerifyOne(password, stored, method string) bool {
	for _, ms := range v.methods {
		if ms.Method == method {
			return verifyLegacyOne(method, password, ms.Salt, stored)
		}
	}
	return false
}

// NeedsRehash reports whether a verified legacy hash should be lazily
// upgraded (anything that is not already modern argon2id/bcrypt).
func NeedsRehash(method string) bool {
	return method != MethodBCRYPT && method != MethodARGON2I && method != MethodPHPDefault
}

func verifyLegacyOne(method, password, salt, stored string) bool {
	// 空存储摘要不是「没设密码」，是「什么都能登」：下面最后一行拿
	// computeLegacy 的结果与它比，而对 legacy 表里没有计算分支的 method
	// （bcrypt / argon2i / php_password_hash，以及任何将来新增的名字）
	// computeLegacy 返回空串，空串 == 空串 就通过了。存量脏行（blessing 导入
	// 里 password 为空的老用户）走的正是这条路，2026-11-07 通宵轮 G10 取证发现，
	// 断言在 empty_stored_test.go。
	if stored == "" {
		return false
	}
	// PHC family first (contains '$' — must not hit embedded-salt parsing).
	if strings.HasPrefix(stored, "$2") || strings.HasPrefix(stored, "$argon2") {
		return verifyPHC(password, stored)
	}
	// AuthMe SHA256: self-describing "$SHA$<salt>$<hash>".
	if strings.HasPrefix(stored, "$SHA$") {
		s, h, ok := parseAuthmeSHA256(stored)
		if !ok {
			return false
		}
		return hmac.Equal([]byte(hexSHA256(hexSHA256(password)+s)), []byte(h))
	}
	// Embedded-salt forms "salt$hash" / "salt:hash" (AuthMe storage variants).
	if saltE, hashE, ok := splitEmbedded(stored); ok {
		return hmac.Equal([]byte(computeLegacy(method, password, saltE)), []byte(hashE))
	}
	// Plain digest with positional salt (core legacy family).
	return hmac.Equal([]byte(computeLegacy(method, password, salt)), []byte(stored))
}

// computeLegacy returns the lowercase-hex digest for one method.
func computeLegacy(method, password, salt string) string {
	switch method {
	case MethodMD5:
		return hexMD5(password)
	case MethodSALTED2MD5, MethodAuthmeSALTED2MD5:
		return hexMD5(hexMD5(password) + salt)
	case MethodSHA256:
		return hexSHA256(password)
	case MethodSHA512:
		return hexSHA512(password)
	case MethodSALTED2SHA256:
		return hexSHA256(hexSHA256(password) + salt)
	case MethodSALTED2SHA512:
		return hexSHA512(hexSHA512(password) + salt)
	case MethodAuthmeSHA256:
		return formatAuthmeSHA256(password, salt)
	case MethodAuthmeSALTED2SHA512:
		return hexSHA512(password + salt)
	default:
		return ""
	}
}

// splitEmbedded splits "salt$hash" / "salt:hash" forms.
func splitEmbedded(stored string) (salt, hash string, ok bool) {
	for _, sep := range []string{"$", ":"} {
		if i := strings.Index(stored, sep); i > 0 {
			return stored[:i], stored[i+1:], true
		}
	}
	return "", "", false
}

// parseAuthmeSHA256 parses "$SHA$<salt>$<hash>".
func parseAuthmeSHA256(stored string) (salt, hash string, ok bool) {
	parts := strings.Split(stored, "$")
	if len(parts) != 4 || parts[1] != "SHA" {
		return "", "", false
	}
	return parts[2], parts[3], true
}

func formatAuthmeSHA256(password, salt string) string {
	return "$SHA$" + salt + "$" + hexSHA256(hexSHA256(password)+salt)
}

// ---- modern hashing (PHC) for lazy rehash ----

// HashArgon2id produces a PHP-compatible PHC string ($argon2id$).
func HashArgon2id(password string) (string, error) {
	salt := make([]byte, 16)
	if _, err := rand.Read(salt); err != nil {
		return "", err
	}
	const time, memory, threads, keyLen = 4, 64 * 1024, 1, 32
	key := argon2.IDKey([]byte(password), salt, time, memory, threads, keyLen)
	return fmt.Sprintf("$argon2id$v=%d$m=%d,t=%d,p=%d$%s$%s",
		argon2.Version, memory, time, threads,
		base64.RawStdEncoding.EncodeToString(salt),
		base64.RawStdEncoding.EncodeToString(key)), nil
}

// VerifyPHC verifies any PHP password_hash-produced digest
// ($2y$ bcrypt, $argon2i$, $argon2id$).
func VerifyPHC(password, stored string) bool { return verifyPHC(password, stored) }

func verifyPHC(password, stored string) bool {
	switch {
	case strings.HasPrefix(stored, "$2"):
		// PHP emits $2y$; x/crypto accepts $2a$/$2b$ (same algorithm).
		norm := stored
		if strings.HasPrefix(norm, "$2y$") {
			norm = "$2a$" + norm[4:]
		}
		return bcrypt.CompareHashAndPassword([]byte(norm), []byte(password)) == nil
	case strings.HasPrefix(stored, "$argon2i$"):
		return verifyArgonPHC(password, stored, false)
	case strings.HasPrefix(stored, "$argon2id$"):
		return verifyArgonPHC(password, stored, true)
	}
	return false
}

func verifyArgonPHC(password, stored string, idVariant bool) bool {
	parts := strings.Split(stored, "$")
	// ["", "argon2i", "v=19", "m=..,t=..,p=..", salt, hash]
	if len(parts) != 6 {
		return false
	}
	var version int
	if _, err := fmt.Sscanf(parts[2], "v=%d", &version); err != nil || version != argon2.Version {
		return false
	}
	var memory, time, threads uint64
	for _, kv := range strings.Split(parts[3], ",") {
		switch {
		case strings.HasPrefix(kv, "m="):
			memory, _ = strconv.ParseUint(kv[2:], 10, 32)
		case strings.HasPrefix(kv, "t="):
			time, _ = strconv.ParseUint(kv[2:], 10, 32)
		case strings.HasPrefix(kv, "p="):
			threads, _ = strconv.ParseUint(kv[2:], 10, 32)
		}
	}
	salt, err := base64.RawStdEncoding.DecodeString(parts[4])
	if err != nil {
		return false
	}
	want, err := base64.RawStdEncoding.DecodeString(parts[5])
	if err != nil {
		return false
	}
	// Guard the params (review A2 P3): ignored ParseUint errors used to feed
	// zeros into argon2, which panics ("rounds too small" / "parallelism
	// degree too low") on a corrupt crafted digest.
	if time < 1 || memory < 8*threads || threads < 1 || threads > 255 || len(want) == 0 {
		return false
	}
	var got []byte
	if idVariant {
		got = argon2.IDKey([]byte(password), salt, uint32(time), uint32(memory), uint8(threads), uint32(len(want)))
	} else {
		got = argon2.Key([]byte(password), salt, uint32(time), uint32(memory), uint8(threads), uint32(len(want)))
	}
	return hmac.Equal(got, want)
}

// ---- hex helpers (PHP hash() emits lowercase hex) ----

func hexMD5(s string) string {
	h := md5.Sum([]byte(s)) //nolint:gosec
	return hex.EncodeToString(h[:])
}

func hexSHA256(s string) string {
	h := sha256.Sum256([]byte(s))
	return hex.EncodeToString(h[:])
}

func hexSHA512(s string) string {
	h := sha512.Sum512([]byte(s))
	return hex.EncodeToString(h[:])
}
