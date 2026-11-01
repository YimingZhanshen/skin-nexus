package texture

import (
	"bytes"
	"image"
	"image/color"
	"image/png"
	"testing"
)

// opaqueCanvas paints every pixel so a fresh canvas reads as a classic (Steve)
// skin; holes then punches the given 64x64-logical rects fully transparent.
func opaqueCanvas(w, h int, holes ...image.Rectangle) *image.RGBA {
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	fill := color.RGBA{10, 20, 30, 255}
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			img.SetRGBA(x, y, fill)
		}
	}
	scale := 1
	if w == 64 || w%64 == 0 {
		scale = w / 64
	}
	for _, r := range holes {
		for y := r.Min.Y * scale; y < r.Max.Y*scale; y++ {
			for x := r.Min.X * scale; x < r.Max.X*scale; x++ {
				if x < w && y < h {
					img.SetRGBA(x, y, color.RGBA{0, 0, 0, 0})
				}
			}
		}
	}
	return img
}

func encodePNG(t *testing.T, img image.Image) []byte {
	t.Helper()
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func decodePNG(t *testing.T, raw []byte) image.Image {
	t.Helper()
	img, err := png.Decode(bytes.NewReader(raw))
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	return img
}

func TestDetectSkinModel(t *testing.T) {
	cases := []struct {
		name string
		img  image.Image
		want string
	}{
		{"fully painted classic", opaqueCanvas(64, 64), ModelDefault},
		{"one transparent pixel in the back strip",
			opaqueCanvas(64, 64, image.Rect(54, 20, 55, 21)), ModelSlim},
		{"transparent bottom-face columns",
			opaqueCanvas(64, 64, image.Rect(50, 16, 52, 20)), ModelSlim},
		{"transparent outside both areas stays classic",
			opaqueCanvas(64, 64, image.Rect(0, 0, 8, 8)), ModelDefault},
		{"legacy 64x32 carries no model information",
			opaqueCanvas(64, 32, image.Rect(50, 16, 56, 32)), ModelDefault},
		{"HD 128 scales the areas",
			opaqueCanvas(128, 128, image.Rect(54, 20, 55, 21)), ModelSlim},
		{"HD 128 painted stays classic", opaqueCanvas(128, 128), ModelDefault},
		{"HD 256 scales the areas",
			opaqueCanvas(256, 256, image.Rect(50, 16, 51, 17)), ModelSlim},
		{"square but not a 64 multiple", opaqueCanvas(96, 96, image.Rect(54, 20, 55, 21)), ModelDefault},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := DetectSkinModel(c.img); got != c.want {
				t.Fatalf("DetectSkinModel = %q, want %q", got, c.want)
			}
		})
	}
}

// Skins reach us as whatever the author's tool exported. The rule must read the
// same way through a palette (index + tRNS) and must not invent transparency in
// a greyscale image, which has no alpha channel at all.
func TestDetectSkinModelThroughEncodings(t *testing.T) {
	pal := color.Palette{color.RGBA{10, 20, 30, 255}, color.RGBA{0, 0, 0, 0}}
	img := image.NewPaletted(image.Rect(0, 0, 64, 64), pal)
	for y := 0; y < 64; y++ {
		for x := 0; x < 64; x++ {
			img.SetColorIndex(x, y, 0)
		}
	}
	for y := 20; y < 32; y++ {
		for x := 54; x < 56; x++ {
			img.SetColorIndex(x, y, 1)
		}
	}
	if got := DetectSkinModel(decodePNG(t, encodePNG(t, img))); got != ModelSlim {
		t.Fatalf("paletted skin = %q, want slim", got)
	}

	gray := image.NewGray(image.Rect(0, 0, 64, 64))
	for y := 0; y < 64; y++ {
		for x := 0; x < 64; x++ {
			gray.SetGray(x, y, color.Gray{Y: 0})
		}
	}
	if got := DetectSkinModel(decodePNG(t, encodePNG(t, gray))); got != ModelDefault {
		t.Fatalf("greyscale skin = %q, want default (no alpha to read)", got)
	}
}

// A solid-colour classic skin really does paint the regions skinview3d uses as
// extra heuristics. Dropping those rules is the point; this locks it in.
func TestSolidClassicSkinStaysClassic(t *testing.T) {
	img := opaqueCanvas(64, 64)
	for _, r := range []image.Rectangle{{Min: image.Pt(56, 20), Max: image.Pt(64, 32)}} {
		for y := r.Min.Y; y < r.Max.Y; y++ {
			for x := r.Min.X; x < r.Max.X; x++ {
				img.SetRGBA(x, y, color.RGBA{0, 0, 0, 255})
			}
		}
	}
	if got := DetectSkinModel(img); got != ModelDefault {
		t.Fatalf("solid classic = %q, want default", got)
	}
}

func TestProcessSkinModelComesFromPixels(t *testing.T) {
	classic := encodePNG(t, opaqueCanvas(64, 64))
	r, err := Process(bytes.NewReader(classic), TypeSkin)
	if err != nil {
		t.Fatal(err)
	}
	if r.Model != ModelDefault {
		t.Fatalf("painted skin model = %q, want default", r.Model)
	}

	slim := encodePNG(t, opaqueCanvas(64, 64, image.Rect(54, 20, 56, 32)))
	r2, err := Process(bytes.NewReader(slim), TypeSkin)
	if err != nil {
		t.Fatal(err)
	}
	if r2.Model != ModelSlim {
		t.Fatalf("skin with the slim gaps = %q, want slim", r2.Model)
	}

	// The model must not fork the content address: same bytes, same address.
	r3, _ := Process(bytes.NewReader(slim), TypeSkin)
	if r3.Hash != r2.Hash {
		t.Fatal("Process must be deterministic for identical input")
	}
}

// A cape never carries a model — and after padding it can be exactly 64x64,
// where a transparent border would otherwise read as a slim arm.
func TestProcessCapeHasNoModel(t *testing.T) {
	raw := encodePNG(t, opaqueCanvas(44, 34))
	r, err := Process(bytes.NewReader(raw), TypeCape)
	if err != nil {
		t.Fatal(err)
	}
	if r.Model != "" {
		t.Fatalf("cape model = %q, want empty", r.Model)
	}
	if b := decodePNG(t, r.Bytes).Bounds(); b.Dx() != 64 || b.Dy() != 64 {
		t.Fatalf("44x34 cape padded to %dx%d, want 64x64", b.Dx(), b.Dy())
	}
}

func TestProcessPadsLegacyCape(t *testing.T) {
	cases := []struct{ w, h, wantW, wantH int }{
		{22, 17, 64, 32},
		{44, 34, 64, 64},
		{66, 51, 128, 64},
		{220, 170, 256, 192},
		{64, 32, 64, 32}, // already the standard layout: untouched
	}
	for _, c := range cases {
		src := opaqueCanvas(c.w, c.h)
		raw := encodePNG(t, src)
		r, err := Process(bytes.NewReader(raw), TypeCape)
		if err != nil {
			t.Fatalf("%dx%d: %v", c.w, c.h, err)
		}
		b := decodePNG(t, r.Bytes).Bounds()
		if b.Dx() != c.wantW || b.Dy() != c.wantH {
			t.Errorf("%dx%d -> %dx%d, want %dx%d", c.w, c.h, b.Dx(), b.Dy(), c.wantW, c.wantH)
			continue
		}
		// Padding must equal "draw the cape at the top-left of that canvas",
		// so the stored pixels stay reproducible without the pipeline.
		want := image.NewRGBA(image.Rect(0, 0, c.wantW, c.wantH))
		for y := 0; y < c.h; y++ {
			for x := 0; x < c.w; x++ {
				want.SetRGBA(x, y, src.At(x, y).(color.RGBA))
			}
		}
		if !samePixels(want, decodePNG(t, r.Bytes)) {
			t.Errorf("%dx%d padded pixels differ from the expected canvas", c.w, c.h)
		}
	}
}

func TestPadTargetRejectsNonLegacy(t *testing.T) {
	for _, b := range []image.Rectangle{
		{Max: image.Pt(64, 32)},  // standard
		{Max: image.Pt(128, 64)}, // standard, scaled
		{Max: image.Pt(64, 64)},  // not a 22x17 multiple
	} {
		if _, _, ok := padTarget(image.NewRGBA(b)); ok {
			t.Errorf("%dx%d must not be padded", b.Dx(), b.Dy())
		}
	}
	if tw, th, ok := padTarget(opaqueCanvas(22, 17)); !ok || tw != 64 || th != 32 {
		t.Fatalf("22x17 -> (%d,%d,%v), want (64,32,true)", tw, th, ok)
	}
}

func samePixels(a, b image.Image) bool {
	ab, bb := a.Bounds(), b.Bounds()
	if ab != bb {
		return false
	}
	for y := ab.Min.Y; y < ab.Max.Y; y++ {
		for x := ab.Min.X; x < ab.Max.X; x++ {
			// Compared as premultiplied RGBA: the encoder may hand back NRGBA
			// where we built RGBA, and those are different Go types.
			ar, ag, ab2, aa := a.At(x, y).RGBA()
			br, bg, bb2, ba := b.At(x, y).RGBA()
			if ar != br || ag != bg || ab2 != bb2 || aa != ba {
				return false
			}
		}
	}
	return true
}
