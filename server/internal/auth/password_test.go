package auth

import (
	"encoding/base64"
	"strings"
	"testing"

	"golang.org/x/crypto/argon2"
	"golang.org/x/crypto/bcrypt"
)

// Cross-implementation vectors generated with Python hashlib (independent of
// this code path), input password="hello":
//
//	s6  = "ab12cd"          (SALTED2MD5 / AuthMe SALTED2MD5 salt)
//	s16 = "a1b2c3d4e5f60718" (SALTED2SHA* / AuthMe salts)
//
// md5/sha256/sha512 bare values additionally match the well-known public test
// vectors for "hello", confirming the generation pipeline itself.
var (
	vMD5                 = "5d41402abc4b2a76b9719d911017c592"
	vSHA256              = "2cf24dba5fb0a30e26e83b2ac5b9e29e1b161e5c1fa7425e73043362938b9824"
	vSHA512              = "9b71d224bd62f3785d96d46ad3ea3d73319bfbc2890caadae2dff72519673ca72323c3d99ba5c11d7c7acc6e14b8c5da0c4663475c2e5c3adef46f73bcdec043"
	vSALTED2MD5          = "60202731227559b460de2289e6dc072f" // md5(md5("hello")+"ab12cd")
	vSALTED2SHA256       = "5879f8a3ba6e99c603c79a4964ae4bdf844062a0ec15a02d799d2d85cd6ad7f5"
	vSALTED2SHA512       = "8e6779149ed5949d2a0fbbb4f1dbfaafb69976e623ab9b8fdb040c3383562b245d187a1166d432a19d71094dab6ec915e7de9779785cdd12b9b8764cc224da01"
	vAuthmeSALTED2SHA512 = "16261c0a5a7d90c72b8c85787e4886db866c353935abbd9260a30b56b5ae0ada9791b65aeda947d1fcf1edb941d48f36fd6f522f315266ceb4ddcfbecfb648d5"
	vAuthmeSHA256Str     = "$SHA$a1b2c3d4e5f60718$5879f8a3ba6e99c603c79a4964ae4bdf844062a0ec15a02d799d2d85cd6ad7f5"
)

func TestLegacyVectors(t *testing.T) {
	cases := []struct {
		method string
		salt   string
		stored string
	}{
		{MethodMD5, "", vMD5},
		{MethodSHA256, "", vSHA256},
		{MethodSHA512, "", vSHA512},
		{MethodSALTED2MD5, "ab12cd", vSALTED2MD5},
		{MethodSALTED2SHA256, "a1b2c3d4e5f60718", vSALTED2SHA256},
		{MethodSALTED2SHA512, "a1b2c3d4e5f60718", vSALTED2SHA512},
		{MethodAuthmeSALTED2MD5, "ab12cd", vSALTED2MD5}, // same formula as core
		{MethodAuthmeSALTED2SHA512, "a1b2c3d4e5f60718", vAuthmeSALTED2SHA512},
		{MethodAuthmeSHA256, "", vAuthmeSHA256Str},
	}
	for _, c := range cases {
		v := NewLegacyVerifier([]MethodSalt{{Method: c.method, Salt: c.salt}})
		matched, ok := v.VerifyLegacy("hello", c.stored)
		if !ok || matched != c.method {
			t.Errorf("%s: verify failed (matched=%q ok=%v)", c.method, matched, ok)
		}
		if _, ok := v.VerifyLegacy("wrong-password", c.stored); ok {
			t.Errorf("%s: wrong password accepted", c.method)
		}
	}
}

// TestPositionalSalts mirrors the legacy "SALT 按位对应" rule: method[i] uses
// salt[i], and a misaligned salt must fail.
func TestPositionalSalts(t *testing.T) {
	v := NewLegacyVerifier([]MethodSalt{
		{Method: MethodSALTED2MD5, Salt: "ab12cd"},
		{Method: MethodSALTED2SHA256, Salt: "a1b2c3d4e5f60718"},
	})
	if _, ok := v.VerifyLegacy("hello", vSALTED2MD5); !ok {
		t.Fatal("positional salt 0 failed")
	}
	if _, ok := v.VerifyLegacy("hello", vSALTED2SHA256); !ok {
		t.Fatal("positional salt 1 failed")
	}
	mis := NewLegacyVerifier([]MethodSalt{{Method: MethodSALTED2MD5, Salt: "a1b2c3d4e5f60718"}})
	if _, ok := mis.VerifyLegacy("hello", vSALTED2MD5); ok {
		t.Fatal("misaligned salt must not verify")
	}
}

// TestCaseSensitivity: PHP hash_equals is case-sensitive; uppercase hex of the
// same digest must NOT verify (prevents double-normalization bugs).
func TestCaseSensitivity(t *testing.T) {
	v := NewLegacyVerifier([]MethodSalt{{Method: MethodMD5}})
	if _, ok := v.VerifyLegacy("hello", strings.ToUpper(vMD5)); ok {
		t.Fatal("uppercase hex must not verify")
	}
}

func TestPHCArgon2idRoundTrip(t *testing.T) {
	h, err := HashArgon2id("s3cret")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(h, "$argon2id$v=19$m=65536,t=4,p=1$") {
		t.Fatalf("unexpected PHC: %s", h)
	}
	if !VerifyPHC("s3cret", h) {
		t.Fatal("argon2id round-trip failed")
	}
	if VerifyPHC("wrong", h) {
		t.Fatal("wrong password accepted")
	}
}

// TestBcryptPHP2yPrefix: PHP emits $2y$; x/crypto emits $2a$. Generate a
// hash, rewrite the prefix to PHP style, and verify our adaptation accepts it.
func TestBcryptPHP2yPrefix(t *testing.T) {
	gen, err := bcrypt.GenerateFromPassword([]byte("abc"), bcrypt.MinCost)
	if err != nil {
		t.Fatal(err)
	}
	phpStyle := "$2y$" + string(gen)[4:]
	v := NewLegacyVerifier([]MethodSalt{{Method: MethodBCRYPT}})
	if _, ok := v.VerifyLegacy("abc", phpStyle); !ok {
		t.Fatal("$2y$ hash must verify via $2a$ adaptation")
	}
	if _, ok := v.VerifyLegacy("abd", phpStyle); ok {
		t.Fatal("wrong password accepted")
	}
}

func TestArgon2iPHCVerify(t *testing.T) {
	// Build a real $argon2i$ PHC (argon2i variant) and verify it.
	salt := []byte("0123456789abcdef")
	key := argon2.Key([]byte("pw"), salt, 4, 64*1024, 1, 32)
	phc := "$argon2i$v=19$m=65536,t=4,p=1$" +
		base64.RawStdEncoding.EncodeToString(salt) + "$" +
		base64.RawStdEncoding.EncodeToString(key)
	if !VerifyPHC("pw", phc) {
		t.Fatal("argon2i PHC verify failed")
	}
	if VerifyPHC("wrong", phc) {
		t.Fatal("wrong password accepted")
	}
}

func TestNeedsRehash(t *testing.T) {
	if !NeedsRehash(MethodMD5) || !NeedsRehash(MethodSALTED2SHA512) {
		t.Fatal("legacy methods must need rehash")
	}
	if NeedsRehash(MethodBCRYPT) || NeedsRehash(MethodARGON2I) {
		t.Fatal("modern methods must not need rehash")
	}
}
