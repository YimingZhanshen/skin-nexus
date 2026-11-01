package texture

import (
	"bytes"
	"encoding/binary"
	"image"
	"image/color"
	"image/png"
	"os"
	"regexp"
	"strings"
	"testing"
)

func testPNG(t *testing.T, w, h int) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	img.Set(0, 0, color.RGBA{255, 0, 0, 255})
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// fakeHeader builds a raw PNG header with arbitrary IHDR dimensions
// (no real pixels — used for the bomb gate, which must reject BEFORE decode).
func fakeHeader(w, h uint32) []byte {
	b := make([]byte, 33)
	copy(b[:8], []byte{0x89, 'P', 'N', 'G', 0x0d, 0x0a, 0x1a, 0x0a})
	binary.BigEndian.PutUint32(b[8:12], 13)
	copy(b[12:16], []byte("IHDR"))
	binary.BigEndian.PutUint32(b[16:20], w)
	binary.BigEndian.PutUint32(b[20:24], h)
	b[24] = 8 // bit depth
	b[25] = 6 // RGBA
	return b
}

func TestProcessSkinRoundTrip(t *testing.T) {
	raw := testPNG(t, 64, 64)
	r, err := Process(bytes.NewReader(raw), TypeSkin)
	if err != nil {
		t.Fatal(err)
	}
	if !regexp.MustCompile(`^[0-9a-f]{64}$`).MatchString(r.Hash) {
		t.Fatalf("hash = %q", r.Hash)
	}
	// Determinism: identical input -> identical content address.
	r2, _ := Process(bytes.NewReader(raw), TypeSkin)
	if r.Hash != r2.Hash {
		t.Fatal("re-encode must be deterministic within a build")
	}
}

func TestPolyglotPayloadDropped(t *testing.T) {
	raw := testPNG(t, 64, 32)
	poly := append(append([]byte{}, raw...), []byte("<script>alert(1)</script>")...)
	r, err := Process(bytes.NewReader(poly), TypeSkin)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(r.Bytes, []byte("<script>")) {
		t.Fatal("trailing polyglot payload survived re-encode")
	}
	// Re-encode normalizes polyglot input to clean pixels: same address.
	rClean, _ := Process(bytes.NewReader(raw), TypeSkin)
	if r.Hash != rClean.Hash {
		t.Fatal("re-encode should normalize polyglot to clean bytes")
	}
}

func TestPNGBombGate(t *testing.T) {
	// 65535x65535 = 4.29G pixels — must be rejected at IHDR, never decoded.
	_, err := Process(bytes.NewReader(fakeHeader(65535, 65535)), TypeSkin)
	if err != ErrPixelBomb {
		t.Fatalf("want ErrPixelBomb, got %v", err)
	}
}

func TestDimensionRules(t *testing.T) {
	cases := []struct {
		typ    Type
		w, h   int
		wantOK bool
	}{
		{TypeSkin, 64, 64, true},
		{TypeSkin, 64, 32, true},
		{TypeSkin, 128, 128, true}, // 2x
		{TypeSkin, 32, 32, false},
		{TypeSkin, 64, 63, false},
		{TypeCape, 64, 32, true},
		{TypeCape, 22, 17, true},
		{TypeCape, 64, 64, false},
	}
	for _, c := range cases {
		_, err := Process(bytes.NewReader(testPNG(t, c.w, c.h)), c.typ)
		if c.wantOK && err != nil {
			t.Errorf("%s %dx%d: want ok, got %v", c.typ, c.w, c.h, err)
		}
		if !c.wantOK && err == nil {
			t.Errorf("%s %dx%d: want reject", c.typ, c.w, c.h)
		}
	}
}

func TestNonPNGRejected(t *testing.T) {
	_, err := Process(strings.NewReader("GIF89a not a png"), TypeSkin)
	if err != ErrBadPNG {
		t.Fatalf("want ErrBadPNG, got %v", err)
	}
}

func TestSizeLimit(t *testing.T) {
	big := bytes.Repeat([]byte{0}, MaxUploadBytes+1)
	_, err := Process(bytes.NewReader(big), TypeSkin)
	if err != ErrTooLarge {
		t.Fatalf("want ErrTooLarge, got %v", err)
	}
}

func TestPathTraversalGate(t *testing.T) {
	s, err := NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	for _, bad := range []string{"../../etc/passwd", strings.Repeat("g", 64), "", "ab"} {
		if _, err := s.Path(bad); err == nil {
			t.Errorf("hash %q must be rejected", bad)
		}
	}
	if _, err := s.Path(strings.Repeat("ab", 32)); err != nil {
		t.Fatalf("valid hash rejected: %v", err)
	}
}

func TestVerifyStored(t *testing.T) {
	dir := t.TempDir()
	s, _ := NewStore(dir)
	r, err := Process(bytes.NewReader(testPNG(t, 64, 64)), TypeSkin)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Save(r); err != nil {
		t.Fatal(err)
	}
	if err := VerifyStored(s, r.Hash); err != nil {
		t.Fatal(err)
	}
	// Corrupt the file: mismatch must be detected.
	p, _ := s.Path(r.Hash)
	if err := os.WriteFile(p, []byte("tampered"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := VerifyStored(s, r.Hash); err == nil {
		t.Fatal("tampered file must fail verification")
	}
}
