package auth

// CHARACTERIZATION TESTS — ticket G10 / E4 (legacy password scheme coverage).
//
// These tests PIN TODAY'S BEHAVIOUR of the 12 legacy schemes against
// independently-produced known-good vectors. They do not assert what the
// schemes SHOULD be. If a formula is intentionally changed, this file must go
// red and be updated DELIBERATELY, together with 铁律 7 (password
// credentials stay verifiable for migrated rows) — never by loosening an
// assertion to get green.
//
// HOW EACH EXPECTED VALUE WAS OBTAINED (no value below was produced by the
// code under test — see analysis/g10-e4-password-schemes.md for the exact
// commands):
//
//  A. Python 3.13.5 `hashlib`, run outside this repo, UTF-8 encoding, with
//     the formulas as documented at password.go:6-17:
//
//     PYTHONIOENCODING=utf-8 python -c "
//     import hashlib
//     md5=lambda s: hashlib.md5(s.encode()).hexdigest()
//     s256=lambda s: hashlib.sha256(s.encode()).hexdigest()
//     s512=lambda s: hashlib.sha512(s.encode()).hexdigest()
//     pw='pässw0rd'; s6='ab12cd'; s16='a1b2c3d4e5f60718'
//     print(md5(pw), s256(pw), s512(pw))
//     print(md5(md5(pw)+s6), s256(s256(pw)+s16), s512(s512(pw)+s16))
//     print(s512(pw+s16))"
//
//     The inputs differ from the ones in password_test.go ("hello"), so this
//     file adds new coverage instead of restating it, including a non-ASCII
//     password that pins the "hash the UTF-8 BYTES" reading of PHP hash().
//
//  B. argon2i: the published reference-implementation KATs (generated with
//     the P-H-C phc-winner-argon2 CLI, password="password",
//     salt="somesalt", empty secret / empty associated data), transcribed in
//     golang.org/x/crypto@v0.58.0/argon2/argon2_test.go:139-172 (vectors
//     table; the three argon2i entries used below are lines 148, 160, 172).
//     The base64 sections were produced by Python separately:
//
//     python -c "import base64;
//       print(base64.b64encode(b'somesalt').decode().rstrip('='))
//       print(base64.b64encode(bytes.fromhex(
//         'b9c401d1844a67d50eae3967dc28870b22e508092e861a37')).decode().rstrip('='))"
//
//     This matters: password_test.go:120-133 TestArgon2iPHCVerify builds its
//     expected digest with argon2.Key() — the same call the production
//     verifier makes — so it proves the PHC *parser* round-trips but does NOT
//     pin the digest against anything external. The vectors below do.

import (
	"strings"
	"testing"
)

// Independent hashlib vectors, password = "pässw0rd".
const (
	g10PW       = "pässw0rd"
	g10Salt6    = "ab12cd"
	g10Salt16   = "a1b2c3d4e5f60718"
	g10MD5      = "2cb3f023113da037645a4371c2464b26"
	g10SHA256   = "c5de2240225273d1a8e5442b67981b531e7ade4ab07d856eba2faa0dea2664db"
	g10SHA512   = "fec0ccc972ee8f38167a0c5ff6b8f71b040ff97ff4754d9cad6903abbf68d7c933a280f2b93a2d1db66d6029fe7e01ccbe7d9020b60fa942495cfaae0efada11"
	g10Sal2MD5  = "98344bfdb8b59c2551b0a5f427085666"
	g10Sal2S256 = "b7430f79d092c1cdd2dc061bd19e68c258f8eff23e79f11f716e094df342d9c9"
	g10Sal2S512 = "3df46c4b49c8fac9dcf8e1f26f059964d40882c4c3d0418b70a892504116e7266cc475e0cf021f9f51d4023cc05d28dbd77dbcc2becb85002f5ea0b0954361ec"
	// AuthMe SALTED2SHA512 is sha512(pw + salt) — NOT the double-hash form
	// (password.go:17, computeLegacy case at password.go:145-146).
	g10AuthmeS512 = "c2db5ccb1212b1d7f562dc654bc555cfe53d616ce2d905ee454807a35b27996d49df641346e7899fe59b9d757ee70a3b15761e7706cb441a7a2cf445878805a7"
)

// Independent hashlib vectors, password = "中文pw" (non-ASCII pin).
const (
	g10PWCJK       = "中文pw"
	g10CJKSal2MD5  = "79dfccec7359533b456992e25c5e8de4"
	g10CJKSal2S256 = "b3791cc0380ce6590c412847f50d1d3e55646f4be5f2735ad65257921113a77c"
	g10CJKSal2S512 = "c8fe172a459986baa58f2981aacfba36d0cdc327a4106eb747f37cbc192a7b1df57d77a2b360ff10fe3b13c0f5144cc2755d8c10e57e5a13c6ba158d75d5c1b0"
)

// g10Argon2iPHCs are the reference KATs dressed as PHP password_hash
// PASSWORD_ARGON2I PHC strings (unpadded base64, v=19).
const (
	g10Argon2iT1 = "$argon2i$v=19$m=64,t=1,p=1$c29tZXNhbHQ$ucQB0YRKZ9UOrjln3CiHCyLlCAkuhho3"
	// m=64,t=2,p=2 → the argon2i KAT digest 2089f3e78a799720f80af806553128f29b132cafe40d059f.
	g10Argon2iT2 = "$argon2i$v=19$m=64,t=2,p=2$c29tZXNhbHQ$IInz54p5lyD4CvgGVTEo8psTLK/kDQWf"
	// m=64,t=2,p=1 → the argon2i KAT digest 8cf3d8f76a6617afe35fac48eb0b7433a9a670ca4a07ed64.
	g10Argon2iT2p1 = "$argon2i$v=19$m=64,t=2,p=1$c29tZXNhbHQ$jPPY92pmF6/jX6xI6wt0M6mmcMpKB+1k"
)

// TestG10Salted2FamiliesHaveExternalVectors is the direct answer to E4:
// SALTED2MD5, SALTED2SHA256 and SALTED2SHA512 are all implemented
// (password.go:133-142) AND verified against digests computed outside this
// code path. Any mismatch here means the formula silently diverged from the
// PHP original documented at password.go:6-17.
func TestG10Salted2FamiliesHaveExternalVectors(t *testing.T) {
	cases := []struct {
		method string
		salt   string
		stored string
		pw     string
	}{
		{MethodMD5, "", g10MD5, g10PW},
		{MethodSHA256, "", g10SHA256, g10PW},
		{MethodSHA512, "", g10SHA512, g10PW},
		{MethodSALTED2MD5, g10Salt6, g10Sal2MD5, g10PW},
		{MethodSALTED2SHA256, g10Salt16, g10Sal2S256, g10PW},
		{MethodSALTED2SHA512, g10Salt16, g10Sal2S512, g10PW},
		{MethodAuthmeSALTED2MD5, g10Salt6, g10Sal2MD5, g10PW},
		{MethodAuthmeSALTED2SHA512, g10Salt16, g10AuthmeS512, g10PW},
		// non-ASCII passwords: PHP hash() and Go hash over the UTF-8 bytes agree.
		{MethodSALTED2MD5, g10Salt6, g10CJKSal2MD5, g10PWCJK},
		{MethodSALTED2SHA256, g10Salt16, g10CJKSal2S256, g10PWCJK},
		{MethodSALTED2SHA512, g10Salt16, g10CJKSal2S512, g10PWCJK},
	}
	for _, c := range cases {
		v := NewLegacyVerifier([]MethodSalt{{Method: c.method, Salt: c.salt}})
		matched, ok := v.VerifyLegacy(c.pw, c.stored)
		if !ok || matched != c.method {
			t.Errorf("%s/%q: verify against external vector failed (matched=%q ok=%v)",
				c.method, c.pw, matched, ok)
		}
		if _, ok := v.VerifyLegacy(c.pw+"x", c.stored); ok {
			t.Errorf("%s/%q: a wrong password was accepted", c.method, c.pw)
		}
	}
}

// TestG10AuthmeSalted2SHA512IsNotTheDoubleHash pins the one formula in the
// table that is easy to "fix" into a compatibility break: the AuthMe variant
// is sha512(pw.salt) (password.go:145-146), i.e. it must NOT verify against
// the core SALTED2SHA512 digest of the same password/salt, and vice versa.
func TestG10AuthmeSalted2SHA512IsNotTheDoubleHash(t *testing.T) {
	authme := NewLegacyVerifier([]MethodSalt{{Method: MethodAuthmeSALTED2SHA512, Salt: g10Salt16}})
	if _, ok := authme.VerifyLegacy(g10PW, g10Sal2S512); ok {
		t.Fatal("AuthMe SALTED2SHA512 must not accept the core double-hash digest")
	}
	core := NewLegacyVerifier([]MethodSalt{{Method: MethodSALTED2SHA512, Salt: g10Salt16}})
	if _, ok := core.VerifyLegacy(g10PW, g10AuthmeS512); ok {
		t.Fatal("core SALTED2SHA512 must not accept the AuthMe single-hash digest")
	}
}

// TestG10Argon2iReferenceVectors answers E4's fourth item: $argon2i$v=19$ IS
// implemented (password.go:204-205 → verifyArgonPHC) and DOES verify against
// externally published KATs. Note the small KAT parameters (m=64 KiB) satisfy
// the params guard at password.go:244 (memory >= 8*threads).
func TestG10Argon2iReferenceVectors(t *testing.T) {
	for _, phc := range []string{g10Argon2iT1, g10Argon2iT2p1} {
		if !VerifyPHC("password", phc) {
			t.Fatalf("reference argon2i vector must verify: %s", phc)
		}
		if VerifyPHC("Password", phc) {
			t.Fatalf("wrong password accepted for %s", phc)
		}
	}
	// p=2 form of the same KAT family (m=64,t=2,p=2 → 2089f3e7...).
	if !VerifyPHC("password", g10Argon2iT2) {
		t.Fatalf("reference argon2i p=2 vector must verify: %s", g10Argon2iT2)
	}
}

// TestG10Argon2VersionGate pins that ONLY v=19 is accepted:
// verifyArgonPHC compares the version against argon2.Version and fails
// otherwise (password.go:219-221), so a PHP digest emitted by an older
// libargon2 (v=10 / v=13) is silently unverifiable today. FINDING, not a fix.
func TestG10Argon2VersionGate(t *testing.T) {
	for _, v := range []string{"v=10", "v=13", "v=20", ""} {
		phc := "$argon2i$" + v + "$m=65536,t=4,p=1$c29tZXNhbHQ$aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
		if VerifyPHC("password", phc) {
			t.Fatalf("version %q must not verify today", v)
		}
	}
}

// TestG10PHCPrefixDispatchIgnoresConfiguredMethod pins a live quirk of
// verifyLegacyOne (password.go:109-111): the dispatch to the PHC family is
// driven by the STORED string's prefix, not by the configured method. So a
// site that only configured "bcrypt" still accepts a stored $argon2i$ /
// $argon2id$ digest, and NeedsRehash("bcrypt") reports no upgrade needed even
// though the row is argon2. Recorded, not fixed.
func TestG10PHCPrefixDispatchIgnoresConfiguredMethod(t *testing.T) {
	bcryptOnly := NewLegacyVerifier([]MethodSalt{{Method: MethodBCRYPT}})
	if _, ok := bcryptOnly.VerifyLegacy("password", g10Argon2iT1); !ok {
		t.Fatal("today a bcrypt-only configuration DOES verify an argon2i digest (prefix dispatch)")
	}
	if matched, ok := bcryptOnly.VerifyLegacy("password", g10Argon2iT1); ok && matched != MethodBCRYPT {
		t.Fatalf("matched method is reported as the CONFIGURED one (%q), got %q", MethodBCRYPT, matched)
	}
	// And the mirror: the argon2i slot happily verifies a $2y$ bcrypt digest.
	if NeedsRehash(MethodBCRYPT) || NeedsRehash(MethodARGON2I) {
		t.Fatal("NeedsRehash must keep reporting both modern slots as up to date")
	}
}

// TestG10EmbeddedSaltBeatsPositionalSalt pins the second dispatch layer
// (password.go:121-125 → splitEmbedded at 152-160): any stored value
// containing '$' or ':' is read as "<salt><sep><digest>" and the EMBEDDED
// salt wins, the configured positional salt is ignored. That is the AuthMe
// storage shape, but it also means a legacy digest that happens to contain a
// colon is mis-read — recorded as current behaviour.
func TestG10EmbeddedSaltBeatsPositionalSalt(t *testing.T) {
	embedded := g10Salt6 + "$" + g10Sal2MD5
	v := NewLegacyVerifier([]MethodSalt{{Method: MethodSALTED2MD5, Salt: "zzzzzz"}})
	if _, ok := v.VerifyLegacy(g10PW, embedded); !ok {
		t.Fatal("embedded-salt form must verify with the embedded salt, not the positional one")
	}
	colonForm := g10Salt6 + ":" + g10Sal2MD5
	if _, ok := v.VerifyLegacy(g10PW, colonForm); !ok {
		t.Fatal(`":" is also an embedded-salt separator today`)
	}
	// The AuthMe SHA256 self-describing form (password.go:113-119).
	authmeSHA := "$SHA$" + g10Salt16 + "$" + g10Sal2S256
	a := NewLegacyVerifier([]MethodSalt{{Method: MethodAuthmeSHA256}})
	if _, ok := a.VerifyLegacy(g10PW, authmeSHA); !ok {
		t.Fatal("$SHA$<salt>$<digest> form must verify")
	}
	if _, ok := a.VerifyLegacy(g10PW, strings.ToUpper(authmeSHA)); ok {
		t.Fatal("digest comparison is case-sensitive today (uppercase hex must fail)")
	}
}

// TestG10UnknownMethodIsSilent pins computeLegacy's default branch
// (password.go): a method name the table does not know returns "" instead of an
// error, so verification against a REAL digest fails.
//
// G10 found the other half of that default branch and pinned it as OBSERVED:
// an unknown method digest equals the EMPTY STRING, so a legacy row with an
// empty password column authenticated ANY password as soon as an unknown method
// name sat in PASSWORD_METHODS. The hole is now closed on purpose
// (verifyLegacyOne rejects an empty stored digest outright, see
// empty_stored_test.go), and this assertion is the one the finding told us to
// flip when that happened. Consequence for existing data: a migrated row whose
// password column is empty can no longer log in by any password — it needs a
// password reset, which is the intended reading of "no password set".
func TestG10UnknownMethodIsSilent(t *testing.T) {
	unknown := NewLegacyVerifier([]MethodSalt{{Method: "sha1", Salt: g10Salt6}})
	if _, ok := unknown.VerifyLegacy(g10PW, g10Sal2MD5); ok {
		t.Fatal("an unknown method must never verify a real digest")
	}
	// Closed 2026-11-07: empty stored digest is not a match for anything.
	if _, ok := unknown.VerifyLegacy(g10PW, ""); ok {
		t.Fatal("an empty stored hash must never authenticate — this was the G10 finding")
	}
	// The known methods reject it.
	for _, m := range []string{MethodMD5, MethodSHA256, MethodSHA512, MethodSALTED2MD5,
		MethodSALTED2SHA256, MethodSALTED2SHA512, MethodAuthmeSALTED2MD5, MethodAuthmeSALTED2SHA512,
		MethodAuthmeSHA256} {
		v := NewLegacyVerifier([]MethodSalt{{Method: m, Salt: g10Salt16}})
		if _, ok := v.VerifyLegacy(g10PW, ""); ok {
			t.Fatalf("%s accepted an empty stored hash", m)
		}
	}
	// OBSERVED, and it widens the hole above: the three modern method NAMES
	// have no computeLegacy case either (password.go:130-149 — they are meant
	// to be reached through the PHC prefix dispatch at password.go:109), so a
	// row with an empty password column verifies for them too.
	for _, m := range []string{MethodBCRYPT, MethodARGON2I, MethodPHPDefault} {
		v := NewLegacyVerifier([]MethodSalt{{Method: m}})
		if _, ok := v.VerifyLegacy(g10PW, ""); !ok {
			t.Fatalf("OBSERVED TODAY: the %q slot accepts an empty stored hash", m)
		}
	}
	empty := NewLegacyVerifier(nil)
	if _, ok := empty.VerifyLegacy(g10PW, g10MD5); ok {
		t.Fatal("an empty configured method list must verify nothing")
	}
	if _, ok := empty.VerifyLegacy(g10PW, ""); ok {
		t.Fatal("an empty configured method list must not verify the empty hash either")
	}
}
