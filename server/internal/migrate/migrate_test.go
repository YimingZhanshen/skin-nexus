package migrate

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"image"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"skin-nexus/server/internal/auth"
)

func TestParseLegacyEnvPositionalSalts(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, ".env")
	content := `APP_URL=https://skin.example.com
PASSWORD_METHODS=SALTED2MD5,BCRYPT
SALT=ab12cd,unused
TEXTURES_DIR=/var/www/storage/textures
`
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	env, err := ParseLegacyEnv(p)
	if err != nil {
		t.Fatal(err)
	}
	if env.AppURL != "https://skin.example.com" {
		t.Fatalf("AppURL = %q", env.AppURL)
	}
	ms := env.MethodSalts()
	if len(ms) != 2 || ms[0].Method != auth.MethodSALTED2MD5 || ms[0].Salt != "ab12cd" || ms[1].Method != auth.MethodBCRYPT {
		t.Fatalf("MethodSalts = %+v", ms)
	}

	// Misaligned SALT slots must be a hard error (legacy README's #1 trap).
	bad := filepath.Join(dir, "bad.env")
	_ = os.WriteFile(bad, []byte("PASSWORD_METHODS=A,B\nSALT=only-one\n"), 0o644)
	if _, err := ParseLegacyEnv(bad); err == nil {
		t.Fatal("misaligned SALT must fail")
	}
}

// TestParseRealLegacyEnv pins the REAL legacy-site .env shape (2026-10-22
// provided by YMZS): PWD_METHOD single value, no SALT key at all.
func TestParseRealLegacyEnv(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, ".env")
	content := `APP_ENV=production
DB_HOST=localhost
DB_DATABASE=legacy
DB_USERNAME=legacy
DB_PASSWORD=redacted

# Hash Algorithm for Passwords
# - BCRYPT, ARGON2I, PHP_PASSWORD_HASH ...
PWD_METHOD=BCRYPT
APP_KEY=base64:xxxx
`
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	env, err := ParseLegacyEnv(p)
	if err != nil {
		t.Fatal(err)
	}
	if len(env.PWDMethods) != 1 || env.PWDMethods[0] != "BCRYPT" {
		t.Fatalf("PWDMethods = %v", env.PWDMethods)
	}
	if len(env.Salts) != 1 || env.Salts[0] != "" {
		t.Fatalf("Salts = %v (must be 1 empty slot)", env.Salts)
	}
	ms := env.MethodSalts()
	if ms[0].Method != auth.MethodBCRYPT {
		t.Fatalf("MethodSalts = %+v", ms)
	}
	if env.DBDSN == "" || !strings.Contains(env.DBDSN, "DB_DATABASE=legacy") {
		t.Fatalf("DBDSN = %q", env.DBDSN)
	}
}

func TestMethodNormalize(t *testing.T) {
	cases := map[string]string{
		"MD5": auth.MethodMD5, "salted2md5": auth.MethodSALTED2MD5,
		"ARGON2I": auth.MethodARGON2I, "PHP_PASSWORD_HASH": auth.MethodPHPDefault,
	}
	for in, want := range cases {
		if got := normalizeMethod(in); got != want {
			t.Errorf("normalizeMethod(%q) = %q want %q", in, got, want)
		}
	}
}

func TestClassifyHashes(t *testing.T) {
	d := ClassifyHashes([]string{
		"$2y$04$abcdefghijklmnopqrstuv",                                    // bcrypt
		"$argon2i$v=19$m=65536,t=4,p=1$c2FsdA$aGFzaA",                      // argon2
		"$SHA$a1b2c3d4e5f60718$deadbeef",                                   // authme
		"5d41402abc4b2a76b9719d911017c592",                                 // hex32 (weak)
		"2cf24dba5fb0a30e26e83b2ac5b9e29e1b161e5c1fa7425e73043362938b9824", // hex64 (weak)
		"garbage",
	})
	if d.Bcrypt != 1 || d.Argon2 != 1 || d.AuthmeSHA != 1 || d.Hex32 != 1 || d.Hex64 != 1 || d.Other != 1 {
		t.Fatalf("distribution = %+v", d)
	}
	if d.WeakShare < 0.49 { // 4 of 6 non-modern
		t.Fatalf("WeakShare = %f", d.WeakShare)
	}
}

func TestCleanUuidTableFiveSteps(t *testing.T) {
	rows := []UuidRow{
		{ID: 1, Name: "Steve", UUID: "aaa", PID: 10},
		{ID: 2, Name: "Steve", UUID: "bbb", PID: 0}, // dup name -> step1 drop
		{ID: 3, Name: "Alex", UUID: "aaa", PID: 11}, // dup uuid -> step2 drop
		{ID: 4, Name: "Noor", UUID: "ccc", PID: 0},  // backfillable -> step3
		{ID: 5, Name: "Ghost", UUID: "ddd", PID: 0}, // orphan -> step4 drop
	}
	players := map[string]int64{"Steve": 10, "Alex": 11, "Noor": 12}
	cleaned, rep := CleanUuidTable(rows, players)

	if rep.Input != 5 || rep.DupNameDropped != 1 || rep.DupUUIDDropped != 1 || rep.OrphansDropped != 1 {
		t.Fatalf("report = %s", rep)
	}
	if rep.PIDBackfilled != 1 {
		t.Fatalf("pidBackfill = %d", rep.PIDBackfilled)
	}
	if len(cleaned) != 2 {
		t.Fatalf("cleaned = %+v", cleaned)
	}
	for _, r := range cleaned {
		if r.Name == "Ghost" {
			t.Fatal("orphan survived")
		}
		if r.Name == "Noor" && r.PID != 12 {
			t.Fatalf("pid backfill failed: %+v", r)
		}
	}
	if rep.Output != 2 {
		t.Fatalf("output count = %d", rep.Output)
	}
}

func TestScanTextureTree(t *testing.T) {
	dir := t.TempDir()
	// ok file: real tiny PNG whose name == sha256(bytes)
	var pngBuf bytes.Buffer
	img := image.NewRGBA(image.Rect(0, 0, 8, 8))
	if err := png.Encode(&pngBuf, img); err != nil {
		t.Fatal(err)
	}
	ok := pngBuf.Bytes()
	sum := sha256.Sum256(ok)
	okName := hex.EncodeToString(sum[:])
	_ = os.WriteFile(filepath.Join(dir, okName), ok, 0o644)

	// hash mismatch: name lies about content
	_ = os.WriteFile(filepath.Join(dir, hex.EncodeToString(make([]byte, 32))), []byte("different"), 0o644)
	// bad name
	_ = os.WriteFile(filepath.Join(dir, "not-hex"), ok, 0o644)
	// bad IHDR: name matches content but content is not PNG
	junk := []byte("junk-junk-junk-junk-junk-junk")
	jsum := sha256.Sum256(junk)
	_ = os.WriteFile(filepath.Join(dir, hex.EncodeToString(jsum[:])), junk, 0o644)

	s, err := ScanTextureTree(dir)
	if err != nil {
		t.Fatal(err)
	}
	if s.Checked != 4 || s.OK != 1 || len(s.HashMismatch) != 1 || len(s.BadIHDR) != 1 || len(s.NotHexName) != 1 {
		t.Fatalf("scan = %s (%+v)", s, s)
	}
}
