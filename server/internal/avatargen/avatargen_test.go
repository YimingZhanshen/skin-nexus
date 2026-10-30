package avatargen

import (
	"bytes"
	"image"
	"image/color"
	"image/png"
	"testing"
)

// makeSkin builds a 64×64 test skin: solid per-face colors so renders are
// assertable by dominant color; the head front face is magenta.
func makeSkin() *image.NRGBA {
	s := image.NewNRGBA(image.Rect(0, 0, 64, 64))
	fill := func(x0, y0, w, h int, c color.NRGBA) {
		for y := y0; y < y0+h; y++ {
			for x := x0; x < x0+w; x++ {
				s.SetNRGBA(x, y, c)
			}
		}
	}
	// Head faces.
	fill(8, 8, 8, 8, color.NRGBA{255, 0, 255, 255})  // front
	fill(0, 8, 8, 8, color.NRGBA{255, 0, 0, 255})    // right side
	fill(16, 8, 8, 8, color.NRGBA{0, 255, 0, 255})   // left side
	fill(24, 8, 8, 8, color.NRGBA{0, 0, 255, 255})   // back
	fill(8, 0, 8, 8, color.NRGBA{255, 255, 0, 255})  // top
	fill(16, 0, 8, 8, color.NRGBA{0, 255, 255, 255}) // bottom
	// Body + limbs (opaque so full-body renders have content everywhere).
	fill(16, 16, 24, 16, color.NRGBA{128, 128, 128, 255})
	fill(0, 16, 16, 16, color.NRGBA{96, 96, 96, 255})
	fill(40, 16, 16, 16, color.NRGBA{96, 96, 96, 255})
	// Left-limb net (v≥48) so 64×64 path is covered.
	fill(16, 48, 16, 16, color.NRGBA{64, 64, 64, 255})
	fill(32, 48, 16, 16, color.NRGBA{64, 64, 64, 255})
	return s
}

func TestRenderAvatar2DFrontFaceDominant(t *testing.T) {
	skin := makeSkin()
	out := RenderAvatar(skin, 64, false)
	if out.Bounds().Dx() != 64 || out.Bounds().Dy() != 64 {
		t.Fatalf("avatar size = %v, want 64×64", out.Bounds())
	}
	// Center pixel must be the magenta front face (no side bleed at rot 0).
	c := out.NRGBAAt(32, 32)
	if c.R < 200 || c.B < 200 || c.G > 50 {
		t.Fatalf("center pixel = %v, want magenta front face", c)
	}
}

func TestRenderAvatar3DHasSideAndTop(t *testing.T) {
	skin := makeSkin()
	out := RenderAvatar(skin, 96, true)
	if out.Bounds().Dx() != 96 || out.Bounds().Dy() != 96 {
		t.Fatalf("3d avatar size = %v, want 96×96", out.Bounds())
	}
	// At -45° yaw the render shows front (UV 8,8 magenta) on the left and
	// the head's +x face (UV 16,8 green) on the right, plus the top
	// (UV 8,0 yellow) — matching the live legacy probe orientation.
	var magenta, green, yellow int
	for y := 0; y < 96; y++ {
		for x := 0; x < 96; x++ {
			c := out.NRGBAAt(x, y)
			if c.A == 0 {
				continue
			}
			switch {
			case c.R > 200 && c.B > 200 && c.G < 50:
				magenta++
			case c.G > 200 && c.R < 50 && c.B < 50:
				green++
			case c.R > 200 && c.G > 200 && c.B < 50:
				yellow++
			}
		}
	}
	if magenta == 0 || green == 0 || yellow == 0 {
		t.Fatalf("3d render faces: front=%d side=%d top=%d, all must be >0", magenta, green, yellow)
	}
}

func TestRenderAvatarLegacy64x32(t *testing.T) {
	s := image.NewNRGBA(image.Rect(0, 0, 64, 32))
	for y := 0; y < 32; y++ {
		for x := 0; x < 64; x++ {
			s.SetNRGBA(x, y, color.NRGBA{200, 100, 50, 255})
		}
	}
	for y := 8; y < 16; y++ {
		for x := 8; x < 16; x++ {
			s.SetNRGBA(x, y, color.NRGBA{255, 0, 255, 255})
		}
	}
	out := RenderAvatar(s, 48, true)
	if out.Bounds().Dx() != 48 {
		t.Fatalf("legacy avatar width = %d, want 48", out.Bounds().Dx())
	}
	// Must not panic on missing layer-2 region and must have opaque content.
	opaque := 0
	for y := 0; y < 48; y++ {
		for x := 0; x < 48; x++ {
			if out.NRGBAAt(x, y).A == 255 {
				opaque++
			}
		}
	}
	if opaque < 100 {
		t.Fatalf("legacy render too empty: %d opaque px", opaque)
	}
}

func TestRenderPreviewDimensions(t *testing.T) {
	skin := makeSkin()
	out := RenderPreview(skin, false)
	b := out.Bounds()
	// Fixed-ratio output (legacy ignores ?height; probe 100/400 → 430×437).
	if b.Dy() < 300 || b.Dy() > 500 {
		t.Fatalf("preview height = %d, want ≈430 (300..500)", b.Dy())
	}
	if b.Dx() < b.Dy()/2 {
		t.Fatalf("preview width %d too small for two side-by-side views", b.Dx())
	}
	// Both views must have opaque pixels (back left half, front right half).
	left, right := 0, 0
	mid := b.Dx() / 2
	for y := 0; y < b.Dy(); y++ {
		for x := 0; x < b.Dx(); x++ {
			if out.NRGBAAt(x, y).A != 0 {
				if x < mid {
					left++
				} else {
					right++
				}
			}
		}
	}
	if left == 0 || right == 0 {
		t.Fatalf("preview views missing: left=%d right=%d", left, right)
	}
}

func TestRenderPreviewSlim(t *testing.T) {
	skin := makeSkin()
	// Slim path must not panic and must produce content.
	out := RenderPreview(skin, true)
	if out.Bounds().Dx() == 0 {
		t.Fatal("slim preview empty canvas")
	}
}

func TestDefaultAvatar(t *testing.T) {
	for _, threeD := range []bool{false, true} {
		img, err := DefaultAvatar(64, threeD)
		if err != nil {
			t.Fatalf("DefaultAvatar(3d=%v): %v", threeD, err)
		}
		if img.Bounds().Dx() != 64 || img.Bounds().Dy() != 64 {
			t.Fatalf("default avatar size = %v", img.Bounds())
		}
	}
}

func TestRenderAvatarEmptySkinNoPanic(t *testing.T) {
	// Fully transparent 64×64: must not panic; output stays sized.
	s := image.NewNRGBA(image.Rect(0, 0, 64, 64))
	out := RenderAvatar(s, 32, true)
	if out.Bounds().Dx() != 32 {
		t.Fatalf("empty-skin avatar width = %d", out.Bounds().Dx())
	}
}

// TestLoadSkinReaderHD: HD sheets (128×128, 128×64) keep their native
// resolution; render() maps the 64-space UV tables onto the sheet grid, so
// faces land on the right texels AND HD detail survives (a downsample to 64
// would throw the extra detail away — the 2026-10-25 v2 regression).
func TestLoadSkinReaderHD(t *testing.T) {
	up := func(src *image.NRGBA, scale int) *image.NRGBA {
		b := src.Bounds()
		dst := image.NewNRGBA(image.Rect(0, 0, b.Dx()*scale, b.Dy()*scale))
		for y := 0; y < dst.Bounds().Dy(); y++ {
			for x := 0; x < dst.Bounds().Dx(); x++ {
				dst.SetNRGBA(x, y, src.NRGBAAt(x/scale, y/scale))
			}
		}
		return dst
	}
	decode := func(src *image.NRGBA) *image.NRGBA {
		var buf bytes.Buffer
		if err := png.Encode(&buf, src); err != nil {
			t.Fatalf("encode: %v", err)
		}
		got, err := LoadSkinReader(&buf)
		if err != nil {
			t.Fatalf("LoadSkinReader: %v", err)
		}
		return got
	}

	t.Run("128x128_native", func(t *testing.T) {
		got := decode(up(makeSkin(), 2))
		if b := got.Bounds(); b.Dx() != 128 || b.Dy() != 128 {
			t.Fatalf("hd sheet = %v, want native 128×128", b)
		}
		// Face texel at HD (16,16) is gray; canonical (8,8) is magenta and
		// the renderer must map onto it.
		if c := got.NRGBAAt(16, 16); c.R < 200 || c.B < 200 || c.G > 50 {
			t.Fatalf("hd face texel = %v, want magenta", c)
		}
		if c := RenderAvatar(got, 64, false).NRGBAAt(32, 32); c.R < 200 || c.B < 200 || c.G > 50 {
			t.Fatalf("hd avatar center = %v, want magenta front face", c)
		}
		if !HasLayer2(got) {
			t.Fatal("128×128 sheet must be treated as having the overlay layer")
		}
	})

	t.Run("hd_detail_preserved", func(t *testing.T) {
		hd := up(makeSkin(), 2)
		// A single HD texel column inside the head front face (canonical
		// x≈8.5) — survives only when sampling maps onto the HD grid.
		for y := 16; y < 32; y++ {
			hd.SetNRGBA(17, y, color.NRGBA{0, 255, 0, 255})
		}
		prev := RenderPreview(decode(hd), false)
		found := false
		for y := 0; y < prev.Bounds().Dy() && !found; y++ {
			for x := 0; x < prev.Bounds().Dx(); x++ {
				if c := prev.NRGBAAt(x, y); c.G > 200 && c.R < 100 && c.B < 100 {
					found = true
					break
				}
			}
		}
		if !found {
			t.Fatal("HD detail column lost: preview has no green pixel")
		}
	})

	t.Run("128x64_legacy_ratio", func(t *testing.T) {
		legacy := image.NewNRGBA(image.Rect(0, 0, 64, 32))
		for y := 0; y < 32; y++ {
			for x := 0; x < 64; x++ {
				legacy.SetNRGBA(x, y, color.NRGBA{200, 100, 50, 255})
			}
		}
		for y := 8; y < 16; y++ {
			for x := 8; x < 16; x++ {
				legacy.SetNRGBA(x, y, color.NRGBA{255, 0, 255, 255})
			}
		}
		got := decode(up(legacy, 2))
		if b := got.Bounds(); b.Dx() != 128 || b.Dy() != 64 {
			t.Fatalf("legacy hd sheet = %v, want native 128×64", b)
		}
		if c := got.NRGBAAt(16, 16); c.R < 200 || c.B < 200 || c.G > 50 {
			t.Fatalf("legacy face texel = %v, want magenta", c)
		}
		if c := RenderAvatar(got, 64, false).NRGBAAt(32, 32); c.R < 200 || c.B < 200 || c.G > 50 {
			t.Fatalf("legacy hd avatar center = %v, want magenta front face", c)
		}
		// 2:1 legacy layout has no helmet/limb overlay even in HD.
		if HasLayer2(got) {
			t.Fatal("128×64 legacy sheet must NOT have the overlay layer")
		}
	})
}

// A no-alpha sheet whose corner is one opaque color is the legacy
// "background color" case: white overlay layers must vanish, while white
// pixels inside the base head/body UVs stay opaque.
func TestBackgroundKeyDropsWhiteOverlay(t *testing.T) {
	white := color.NRGBA{255, 255, 255, 255}
	build := func(cornerAlpha uint8) *image.NRGBA {
		s := image.NewNRGBA(image.Rect(0, 0, 64, 64))
		for y := 0; y < 64; y++ {
			for x := 0; x < 64; x++ {
				s.SetNRGBA(x, y, white)
			}
		}
		for y := 8; y < 16; y++ {
			for x := 8; x < 16; x++ {
				s.SetNRGBA(x, y, color.NRGBA{255, 0, 255, 255})
			}
		}
		s.SetNRGBA(9, 9, white) // a legitimate white base pixel (eye)
		s.SetNRGBA(0, 0, color.NRGBA{255, 255, 255, cornerAlpha})
		return s
	}

	keyed := build(255)
	if k, ok := backgroundKey(keyed); !ok || k != white {
		t.Fatalf("backgroundKey = %v %v, want white/true", k, ok)
	}
	if !baseOpaqueTexel(9, 9) || baseOpaqueTexel(44, 12) {
		t.Fatal("baseOpaqueTexel must keep the head face and drop the helmet face")
	}
	out := RenderAvatar(keyed, 64, false)
	if c := out.NRGBAAt(32, 32); c.R < 200 || c.B < 200 || c.G > 50 {
		t.Fatalf("center = %v, want magenta face (white helmet dropped)", c)
	}
	whitePx := 0
	for y := 0; y < 64; y++ {
		for x := 0; x < 64; x++ {
			if c := out.NRGBAAt(x, y); c.A == 255 && c.R > 240 && c.G > 240 && c.B > 240 {
				whitePx++
			}
		}
	}
	if whitePx == 0 {
		t.Fatal("white base pixel inside the head face was also dropped")
	}

	// A normal alpha sheet (transparent corner) must keep its white overlay.
	alpha := build(0)
	if _, ok := backgroundKey(alpha); ok {
		t.Fatal("transparent corner must not be treated as a background color")
	}
	norm := NormalizeBackground(keyed)
	if c := norm.NRGBAAt(44, 12); c.A != 0 {
		t.Fatalf("viewer sheet helmet pixel = %v, want transparent", c)
	}
	if c := norm.NRGBAAt(9, 9); c.A != 255 {
		t.Fatalf("viewer sheet base pixel = %v, want opaque", c)
	}
	if c := RenderAvatar(alpha, 64, false).NRGBAAt(32, 32); c.R < 240 || c.G < 240 || c.B < 240 {
		t.Fatalf("alpha sheet center = %v, want the white helmet", c)
	}
}
