package avatargen

// LoadSkinReader decoded at whatever resolution the file declared. The upload
// path has a ceiling (texture.MaxPixels, 1M), but a stored texture can arrive
// from the legacy migration or an importer, which re-hash and file the bytes
// without rejecting dimensions — and /avatar/hash/{h}, /preview/hash/{h} and
// /texture-render/{h} are reachable without a session. A PNG declaring
// 8192×8192 costs 268 MB the moment somebody opens the link, once per request.
//
// These cases pin the ceiling and, just as importantly, that a legitimate HD
// sheet still renders: a guard that truncates real skins is worse than the hole.

import (
	"bytes"
	"encoding/binary"
	"hash/crc32"
	"image"
	"image/png"
	"os"
	"strings"
	"testing"
)

// pngHeaderWithSize writes a syntactically valid PNG signature plus an IHDR
// declaring the given size, and nothing else. Decoding it for real would fail
// with a truncation error; the ceiling has to reject it before the pixels are
// allocated, which is exactly what distinguishes the guard from the decoder.
func pngHeaderWithSize(w, h int) []byte {
	var b bytes.Buffer
	b.Write([]byte{0x89, 'P', 'N', 'G', '\r', '\n', 0x1a, '\n'})
	ihdr := make([]byte, 13)
	binary.BigEndian.PutUint32(ihdr[0:4], uint32(w))
	binary.BigEndian.PutUint32(ihdr[4:8], uint32(h))
	ihdr[8] = 8  // bit depth
	ihdr[9] = 6  // RGBA
	ihdr[10] = 0 // compression
	ihdr[11] = 0 // filter
	ihdr[12] = 0 // interlace
	var lenBuf [4]byte
	binary.BigEndian.PutUint32(lenBuf[:], 13)
	b.Write(lenBuf[:])
	b.WriteString("IHDR")
	b.Write(ihdr)
	var crcBuf [4]byte
	binary.BigEndian.PutUint32(crcBuf[:], crc32.ChecksumIEEE(append([]byte("IHDR"), ihdr...)))
	b.Write(crcBuf[:])
	return b.Bytes()
}

func pngOf(t *testing.T, w, h int) []byte {
	t.Helper()
	img := image.NewNRGBA(image.Rect(0, 0, w, h))
	for i := range img.Pix {
		img.Pix[i] = byte(i % 251)
	}
	var b bytes.Buffer
	if err := png.Encode(&b, img); err != nil {
		t.Fatalf("encode fixture: %v", err)
	}
	return b.Bytes()
}

func TestLoadSkinReaderRejectsADeclaredSizeOverTheCeiling(t *testing.T) {
	for _, size := range [][2]int{{8192, 8192}, {4096, 4096}, {1, maxSkinPixels + 1}} {
		w, h := size[0], size[1]
		_, err := LoadSkinReader(bytes.NewReader(pngHeaderWithSize(w, h)))
		if err == nil {
			t.Fatalf("%dx%d decoded, want the ceiling to reject it before allocating", w, h)
		}
		if !strings.Contains(err.Error(), "pixels") {
			t.Errorf("%dx%d err = %v, want it to name the pixel ceiling (a decoder error means the guard did not fire)", w, h, err)
		}
	}
}

// The other direction: the largest sheet our own uploader accepts (2048×512 =
// exactly texture.MaxPixels) and anything below the ceiling must still render
// at native resolution — HD detail is the reason this decoder exists.
func TestLoadSkinReaderStillRendersLegitHDSheets(t *testing.T) {
	for _, size := range [][2]int{{64, 32}, {64, 64}, {128, 64}, {128, 128}, {2048, 512}, {1024, 1024}} {
		w, h := size[0], size[1]
		raw := pngOf(t, w, h)
		got, err := LoadSkinReader(bytes.NewReader(raw))
		if err != nil {
			t.Fatalf("%dx%d: %v", w, h, err)
		}
		if got.Bounds().Dx() != w || got.Bounds().Dy() != h {
			t.Errorf("%dx%d decoded as %v, want native resolution kept", w, h, got.Bounds())
		}
	}
}

// A file bigger than the encoded ceiling is refused by the size guard rather
// than buffered whole into memory first.
func TestLoadSkinReaderCapsEncodedBytesToo(t *testing.T) {
	garbage := bytes.Repeat([]byte("x"), maxSkinBytes+1024)
	_, err := LoadSkinReader(bytes.NewReader(garbage))
	if err == nil {
		t.Fatal("an over-size stream was accepted")
	}
	if !strings.Contains(err.Error(), "bytes") {
		t.Errorf("err = %v, want it to name the byte ceiling", err)
	}
}

// The file-based entry point shares the ceiling (it is the one the HTTP
// handlers use through an opened texture file).
func TestLoadSkinUsesTheSameCeiling(t *testing.T) {
	f, err := os.CreateTemp(t.TempDir(), "skin-*.png")
	if err != nil {
		t.Fatalf("temp file: %v", err)
	}
	if _, err := f.Write(pngHeaderWithSize(8192, 8192)); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	if err := f.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
	if _, err := LoadSkin(f.Name()); err == nil || !strings.Contains(err.Error(), "pixels") {
		t.Errorf("LoadSkin err = %v, want the pixel ceiling to reject it", err)
	}
}
