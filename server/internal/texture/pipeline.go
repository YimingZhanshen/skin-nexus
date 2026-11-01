// Package texture implements the upload pipeline and content-addressed store.
//
// Pipeline (docs/phase1-dev-plan.md §4, security-critical — public upload face):
//
//  1. size limit        (bytes, before any parsing)
//  2. IHDR precheck     (width/height/pixels BEFORE full decode — PNG bomb gate)
//  3. dimension rules   (skin 64x32 / 64x64 family; cape 64x32 / 22x17 family)
//  4. strict decode     (image/png only)
//  5. pixel semantics   (skin model from pixels; legacy cape padded to 64x32)
//  6. re-encode         (drops ancillary chunks / polyglot payloads)
//  7. content address   (sha256 of the RE-ENCODED bytes = filename = cache key)
//
// Legacy migration files are never rewritten: they are served byte-exact and
// only IHDR-scanned (phase-1 decision: 原样搬迁). Step 5 therefore applies to
// new uploads only — padding an existing 22x17 cape would move its content
// address and break every stored reference to it.
package texture

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"image"
	"image/draw"
	"image/png"
	"io"
	"os"
	"path/filepath"
	"regexp"
)

var (
	ErrTooLarge   = errors.New("texture: file exceeds size limit")
	ErrBadPNG     = errors.New("texture: not a valid PNG")
	ErrDimensions = errors.New("texture: dimensions not allowed")
	ErrPixelBomb  = errors.New("texture: pixel count exceeds limit")
)

// Limits — tuned for MC textures (typical skin PNG < 100KB).
const (
	MaxUploadBytes = 2 << 20 // 2 MiB
	MaxPixels      = 1 << 20 // 1M pixels (largest legit: 2048x512 alt skins)
)

// Type classifies the upload.
type Type string

const (
	TypeSkin Type = "skin"
	TypeCape Type = "cape"
)

// Result of a successful pipeline run.
type Result struct {
	Hash  string // sha256 hex of stored bytes
	Type  Type
	Model string // skin: read from the pixels ("default" | "slim"); cape: ""
	Bytes []byte // re-encoded PNG
}

// Store is a content-addressed file store: <dir>/<hash>.
type Store struct {
	dir string
}

func NewStore(dir string) (*Store, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	return &Store{dir: dir}, nil
}

// Path validates a hash and returns its file path. Strict 64-hex check is the
// path-traversal gate (docs/phase1-dev-plan.md §4).
var hashRe = regexp.MustCompile(`^[0-9a-f]{64}$`)

func (s *Store) Path(hash string) (string, error) {
	if !hashRe.MatchString(hash) {
		return "", ErrBadPNG
	}
	return filepath.Join(s.dir, hash), nil
}

// Save writes re-encoded bytes; idempotent for identical content.
func (s *Store) Save(r *Result) error {
	p, err := s.Path(r.Hash)
	if err != nil {
		return err
	}
	if _, err := os.Stat(p); err == nil {
		return nil // already present; rows sharing it are counted at delete time
	}
	return os.WriteFile(p, r.Bytes, 0o644)
}

// Open returns a reader for a stored texture (legacy files included, byte-exact).
func (s *Store) Open(hash string) (*os.File, error) {
	p, err := s.Path(hash)
	if err != nil {
		return nil, err
	}
	return os.Open(p)
}

// ---- pipeline ----

// Process runs the full pipeline for a new upload. The skin model is decided
// here, from pixels — the uploader's own claim never reaches the database.
func Process(r io.Reader, typ Type) (*Result, error) {
	raw, err := io.ReadAll(io.LimitReader(r, MaxUploadBytes+1))
	if err != nil {
		return nil, err
	}
	if len(raw) > MaxUploadBytes {
		return nil, ErrTooLarge
	}
	w, h, err := scanIHDR(raw)
	if err != nil {
		return nil, err
	}
	if w*h > MaxPixels {
		return nil, ErrPixelBomb
	}
	if err := checkDimensions(typ, w, h); err != nil {
		return nil, err
	}
	img, err := png.Decode(bytes.NewReader(raw)) // strict: PNG only
	if err != nil {
		return nil, ErrBadPNG
	}
	out, model := img, ""
	switch typ {
	case TypeSkin:
		model = DetectSkinModel(img)
	case TypeCape:
		// Gated by type on purpose: a padded cape becomes 64x64 and its fresh
		// transparent border would read as a slim arm.
		if tw, th, ok := padTarget(img); ok {
			out = padTo(img, tw, th)
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, out); err != nil {
		return nil, err
	}
	sum := sha256.Sum256(buf.Bytes())
	return &Result{
		Hash:  hex.EncodeToString(sum[:]),
		Type:  typ,
		Model: model,
		Bytes: buf.Bytes(),
	}, nil
}

// padTarget returns the storage dimensions for a legacy-layout cape: the 22x17
// family is padded with transparency up to the smallest 64x32 multiple that
// fits it, because the game reads capes on the 64x32 grid and a raw 22x17
// texture lands misaligned. Width and height are rounded independently —
// forcing one common multiple would blow a 22x34 cape up to 128x64.
func padTarget(img image.Image) (int, int, bool) {
	b := img.Bounds()
	w, h := b.Dx(), b.Dy()
	if w%22 != 0 || h%17 != 0 {
		return 0, 0, false
	}
	tw := ((w + 63) / 64) * 64
	if tw < 64 {
		tw = 64
	}
	th := ((h + 31) / 32) * 32
	if th < 32 {
		th = 32
	}
	if tw == w && th == h {
		return 0, 0, false
	}
	return tw, th, true
}

// padTo copies src to the top-left of a transparent canvas.
func padTo(src image.Image, tw, th int) *image.RGBA {
	b := src.Bounds()
	out := image.NewRGBA(image.Rect(0, 0, tw, th))
	draw.Draw(out, image.Rect(0, 0, b.Dx(), b.Dy()), src, b.Min, draw.Src)
	return out
}

// ScanLegacyIHDR is the non-destructive migration scan: validates IHDR only,
// never decodes full images (phase-1: 原样搬迁, full decode is a background
// audit task). Returns width/height for the report.
func ScanLegacyIHDR(raw []byte) (w, h uint32, err error) {
	return scanIHDR(raw)
}

// scanIHDR parses the PNG signature + IHDR without decoding pixel data.
func scanIHDR(raw []byte) (uint32, uint32, error) {
	if len(raw) < 33 { // signature(8) + len(4) + type(4) + IHDR(13) + crc(4)
		return 0, 0, ErrBadPNG
	}
	if !bytes.Equal(raw[:8], []byte{0x89, 'P', 'N', 'G', 0x0d, 0x0a, 0x1a, 0x0a}) {
		return 0, 0, ErrBadPNG
	}
	if binary.BigEndian.Uint32(raw[8:12]) != 13 || !bytes.Equal(raw[12:16], []byte("IHDR")) {
		return 0, 0, ErrBadPNG
	}
	w := binary.BigEndian.Uint32(raw[16:20])
	h := binary.BigEndian.Uint32(raw[20:24])
	if w == 0 || h == 0 {
		return 0, 0, ErrBadPNG
	}
	return w, h, nil
}

// checkDimensions enforces the legacy dimension families:
// skin 64x32 / 64x64 and integer multiples up to MaxPixels;
// cape 64x32 / 22x17 and integer multiples.
func checkDimensions(typ Type, w, h uint32) error {
	allowed := func(baseW, baseH uint32) bool {
		if w%baseW == 0 && h%baseH == 0 {
			return w/baseW == h/baseH // uniform scale
		}
		return false
	}
	switch typ {
	case TypeSkin:
		if allowed(64, 32) || allowed(64, 64) {
			return nil
		}
	case TypeCape:
		if allowed(64, 32) || allowed(22, 17) {
			return nil
		}
	}
	return fmt.Errorf("%w: %dx%d for %s", ErrDimensions, w, h, typ)
}

// VerifyStored re-hashes a stored file and checks it matches its name.
// The migrate tool runs this over the legacy tree (full re-hash, minutes).
func VerifyStored(s *Store, hash string) error {
	p, err := s.Path(hash)
	if err != nil {
		return err
	}
	f, err := os.Open(p)
	if err != nil {
		return err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return err
	}
	if hex.EncodeToString(h.Sum(nil)) != hash {
		return fmt.Errorf("texture: content mismatch for %s", hash)
	}
	return nil
}
