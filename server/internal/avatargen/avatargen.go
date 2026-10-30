// Package avatargen renders Minecraft skin textures into avatar head shots
// and full-body previews, matching blessing-skin-server's TextureController
// output geometry (blessing/texture-renderer parity):
//
//   - 2d avatar: head front face + helmet overlay, flat (rotation 0)
//   - 3d avatar: head cube at -45° yaw showing front + side + top
//     (verified against live legacy output: face left, side panel right)
//   - preview:   full body, back (135°) and front (-45°) views side by side
//     with proportional padding (legacy renderSkin layout)
//
// Face UV layout is the standard 64×64 skin sheet (head 0..32×0..16, body
// 16..40×16..32, limbs per net [outer/inner, front, inner/outer, back] +
// top/bottom); 64×32 legacy skins mirror the left limbs from the right and
// have no helmet layer. Slim ("alex") arms are 3px wide with shifted UVs.
// Limb top faces sample u=x-width/v=z-depth consistently; slim top strips
// are stored 4×3 by vanilla (known minor deviation: 3×4 read → at most a
// sub-pixel smear on a 3×4-texel strip at steep angles).
//
// Output is always PNG: Go's x/image/webp is decode-only and the legacy
// API contract explicitly permits PNG-only servers (manual: clients must
// sniff Content-Type; ?png is accepted as a no-op).
package avatargen

import (
	"bytes"
	"embed"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"image/png"
	"io"
	"math"
	"os"
)

// RenderVersion is bumped whenever rasterizer output changes (UV fixes,
// sampling or scaling changes). Callers embed it in preview/avatar URLs
// (`/preview/{tid}?v=N`) so month-long immutable caches re-fetch instead of
// serving stale renders — 2026-10-25: v2 downscaled HD (lost detail), v3
// keeps the sheet native and maps UVs onto it (halo layer detection fixed),
// v4 drops the legacy background color (white overlay layers).
const RenderVersion = "4"

//go:embed av_default2d.png av_default3d.png
var defaultFS embed.FS

// uvRect is a texture rectangle on the skin sheet.
type uvRect struct{ u, v, w, h int }

// box is a model-space cuboid in skin pixels: y up (feet at 0), z toward
// the viewer (front of model at +z), x right on screen. faces order:
// -x, +x, +y (top), -y (bottom), +z (front), -z (back).
type box struct {
	min, max [3]float64
	faces    [6]uvRect
}

// faceCorners returns the four model-space corners (TL, TR, BR, BL in
// texture space) of face fi of box b.
func (b box) faceCorners(fi int) [4][3]float64 {
	x0, y0, z0 := b.min[0], b.min[1], b.min[2]
	x1, y1, z1 := b.max[0], b.max[1], b.max[2]
	switch fi {
	case 0: // -x (outer side of right limbs / head right)
		return [4][3]float64{{x0, y1, z0}, {x0, y1, z1}, {x0, y0, z1}, {x0, y0, z0}}
	case 1: // +x
		return [4][3]float64{{x1, y1, z1}, {x1, y1, z0}, {x1, y0, z0}, {x1, y0, z1}}
	case 2: // +y top
		return [4][3]float64{{x0, y1, z0}, {x1, y1, z0}, {x1, y1, z1}, {x0, y1, z1}}
	case 3: // -y bottom
		return [4][3]float64{{x0, y0, z1}, {x1, y0, z1}, {x1, y0, z0}, {x0, y0, z0}}
	case 4: // +z front
		return [4][3]float64{{x0, y1, z1}, {x1, y1, z1}, {x1, y0, z1}, {x0, y0, z1}}
	default: // -z back
		return [4][3]float64{{x1, y1, z0}, {x0, y1, z0}, {x0, y0, z0}, {x1, y0, z0}}
	}
}

// faceNormal returns the outward normal of face fi.
func faceNormal(fi int) [3]float64 {
	switch fi {
	case 0:
		return [3]float64{-1, 0, 0}
	case 1:
		return [3]float64{1, 0, 0}
	case 2:
		return [3]float64{0, 1, 0}
	case 3:
		return [3]float64{0, -1, 0}
	case 4:
		return [3]float64{0, 0, 1}
	default:
		return [3]float64{0, 0, -1}
	}
}

// rot rotates p by yaw around Y then pitch around X.
func rot(p [3]float64, cy, sy, cx, sx float64) [3]float64 {
	x := p[0]*cy + p[2]*sy
	z := -p[0]*sy + p[2]*cy
	y := p[1]*cx - z*sx
	z2 := p[1]*sx + z*cx
	return [3]float64{x, y, z2}
}

// modelParts returns the drawable boxes for a skin. slim selects 3px-wide
// arms; hasLayer2 enables the helmet overlay (64×64 sheets only).
func modelParts(slim, hasLayer2 bool) []box {
	legR := box{min: [3]float64{-4, 0, -2}, max: [3]float64{0, 12, 2}, faces: [6]uvRect{
		{0, 20, 4, 12}, {8, 20, 4, 12}, {4, 16, 4, 4}, {8, 16, 4, 4}, {4, 20, 4, 12}, {12, 20, 4, 12}}}
	armRW := 4
	armRFaces := [6]uvRect{{40, 20, 4, 12}, {48, 20, 4, 12}, {44, 16, 4, 4}, {48, 16, 4, 4}, {44, 20, 4, 12}, {52, 20, 4, 12}}
	if slim {
		armRW = 3
		armRFaces = [6]uvRect{{40, 20, 3, 12}, {46, 20, 3, 12}, {44, 16, 3, 4}, {48, 16, 3, 4}, {43, 20, 3, 12}, {49, 20, 3, 12}}
	}
	armR := box{min: [3]float64{float64(-4 - armRW), 12, -2}, max: [3]float64{-4, 24, 2}, faces: armRFaces}
	body := box{min: [3]float64{-4, 12, -2}, max: [3]float64{4, 24, 2}, faces: [6]uvRect{
		{16, 20, 4, 12}, {28, 20, 4, 12}, {20, 16, 8, 4}, {28, 16, 8, 4}, {20, 20, 8, 12}, {36, 20, 8, 12}}}
	head := box{min: [3]float64{-4, 24, -4}, max: [3]float64{4, 32, 4}, faces: [6]uvRect{
		{0, 8, 8, 8}, {16, 8, 8, 8}, {8, 0, 8, 8}, {16, 0, 8, 8}, {8, 8, 8, 8}, {24, 8, 8, 8}}}

	parts := []box{legR, armR, body, head}

	// Left limbs: 64×64 sheet has its own net at v≥48 (mirror of right:
	// first column = inner side facing the body); 64×32 mirrors the right
	// limb UVs (legacy SkinRenderer imagecopy trick).
	legLFaces, armLFaces := legR.faces, armRFaces
	if hasLayer2 {
		legLFaces = [6]uvRect{{16, 52, 4, 12}, {24, 52, 4, 12}, {20, 48, 4, 4}, {24, 48, 4, 4}, {20, 52, 4, 12}, {28, 52, 4, 12}}
		if slim {
			armLFaces = [6]uvRect{{32, 52, 3, 12}, {38, 52, 3, 12}, {36, 48, 3, 4}, {40, 48, 3, 4}, {35, 52, 3, 12}, {41, 52, 3, 12}}
		} else {
			armLFaces = [6]uvRect{{32, 52, 4, 12}, {40, 52, 4, 12}, {36, 48, 4, 4}, {40, 48, 4, 4}, {36, 52, 4, 12}, {44, 52, 4, 12}}
		}
	}
	legL := box{min: [3]float64{0, 0, -2}, max: [3]float64{4, 12, 2}, faces: legLFaces}
	armL := box{min: [3]float64{4, 12, -2}, max: [3]float64{4 + float64(armRW), 24, 2}, faces: armLFaces}
	parts = append(parts, legL, armL)

	if hasLayer2 {
		// Helmet: head inflated 0.5px per side, UV +32.
		helm := box{min: [3]float64{-4.5, 23.5, -4.5}, max: [3]float64{4.5, 32.5, 4.5}, faces: [6]uvRect{
			{32, 8, 8, 8}, {48, 8, 8, 8}, {40, 0, 8, 8}, {48, 0, 8, 8}, {40, 8, 8, 8}, {56, 8, 8, 8}}}
		parts = append(parts, helm)
	}
	return parts
}

// headOnlyParts returns just the head (+helmet) for avatar rendering.
func headOnlyParts(hasLayer2 bool) []box {
	head := box{min: [3]float64{-4, -4, -4}, max: [3]float64{4, 4, 4}, faces: [6]uvRect{
		{0, 8, 8, 8}, {16, 8, 8, 8}, {8, 0, 8, 8}, {16, 0, 8, 8}, {8, 8, 8, 8}, {24, 8, 8, 8}}}
	parts := []box{head}
	if hasLayer2 {
		helm := box{min: [3]float64{-4.5, -4.5, -4.5}, max: [3]float64{4.5, 4.5, 4.5}, faces: [6]uvRect{
			{32, 8, 8, 8}, {48, 8, 8, 8}, {40, 0, 8, 8}, {48, 0, 8, 8}, {40, 8, 8, 8}, {56, 8, 8, 8}}}
		parts = append(parts, helm)
	}
	return parts
}

// cam bundles the rotation trig for one view.
type cam struct{ cy, sy, cx, sx float64 }

func newCam(yawDeg, pitchDeg float64) cam {
	yaw := yawDeg * math.Pi / 180
	pitch := pitchDeg * math.Pi / 180
	return cam{math.Cos(yaw), math.Sin(yaw), math.Cos(pitch), math.Sin(pitch)}
}

func (c cam) rot(p [3]float64) [3]float64 { return rot(p, c.cy, c.sy, c.cx, c.sx) }

// proj orthographically projects a model point to screen (x right, y down).
func (c cam) proj(p [3]float64) (float64, float64) {
	r := c.rot(p)
	return r[0], -r[1]
}

// spread returns the projected bounding-box width and height in units.
func (c cam) spread(parts []box) (w, h float64) {
	minX, minY := math.Inf(1), math.Inf(1)
	maxX, maxY := math.Inf(-1), math.Inf(-1)
	for _, b := range parts {
		for fi := 0; fi < 6; fi++ {
			for _, p := range b.faceCorners(fi) {
				x, y := c.proj(p)
				minX, maxX = math.Min(minX, x), math.Max(maxX, x)
				minY, maxY = math.Min(minY, y), math.Max(maxY, y)
			}
		}
	}
	return maxX - minX, maxY - minY
}

// render projects parts with the given camera at pixels-per-unit ratio and
// returns the composited canvas. Content starts exactly at (pad,pad).
func render(skin *image.NRGBA, parts []box, c cam, ratio float64, pad int) *image.NRGBA {
	// Painter order: farther parts first (camera at +z looking -z, so
	// smaller rotated z = farther).
	type drawn struct {
		b     box
		depth float64
	}
	ds := make([]drawn, 0, len(parts))
	for _, b := range parts {
		mid := [3]float64{(b.min[0] + b.max[0]) / 2, (b.min[1] + b.max[1]) / 2, (b.min[2] + b.max[2]) / 2}
		ds = append(ds, drawn{b, c.rot(mid)[2]})
	}
	for i := 1; i < len(ds); i++ {
		for j := i; j > 0 && ds[j].depth < ds[j-1].depth; j-- {
			ds[j], ds[j-1] = ds[j-1], ds[j]
		}
	}

	w, h := c.spread(parts)
	canvas := image.NewNRGBA(image.Rect(0, 0, int(math.Ceil(w*ratio))+pad*2, int(math.Ceil(h*ratio))+pad*2))
	// Content origin: shift so the projection's min corner lands at (pad,pad).
	var ominX, ominY = math.Inf(1), math.Inf(1)
	for _, b := range parts {
		for fi := 0; fi < 6; fi++ {
			for _, p := range b.faceCorners(fi) {
				x, y := c.proj(p)
				ominX, ominY = math.Min(ominX, x), math.Min(ominY, y)
			}
		}
	}
	offX, offY := float64(pad)-ominX*ratio, float64(pad)-ominY*ratio

	// HD sheets keep their full resolution: the UV tables are 64-space, so
	// canonical texel coords are mapped onto the sheet's pixel grid (1024×1024
	// = 16×; legacy-ratio sheets are 2:1). Sampling each output pixel at
	// sub-texel precision preserves HD detail at the preview's 10 px per skin
	// pixel, where a downscale to 64 would have thrown the detail away.
	sbx := skin.Bounds()
	sxf := float64(sbx.Dx()) / 64
	syf := sxf
	if sbx.Dy()*2 == sbx.Dx() { // 64×32 legacy ratio (incl. its HD upscales)
		syf = float64(sbx.Dy()) / 32
	}
	key, keyed := backgroundKey(skin)
	sample := func(fu, fv float64) color.NRGBA {
		u, v := int(fu*sxf), int(fv*syf)
		if u < 0 || v < 0 || u >= sbx.Dx() || v >= sbx.Dy() {
			return color.NRGBA{}
		}
		c := skin.NRGBAAt(u, v)
		// Legacy renderer parity: an opaque uniform corner marks the sheet's
		// background color; pixels of that color outside the base UV regions
		// are dropped. This removes white 1.8 overlay layers from skins saved
		// without alpha (e.g. littleskin #594824) without eating white pixels
		// that belong to the base head/body.
		if keyed && c.A == 255 && c.R == key.R && c.G == key.G && c.B == key.B &&
			!baseOpaqueTexel(int(fu), int(fv)) {
			return color.NRGBA{}
		}
		return c
	}
	over := func(x, y int, cl color.NRGBA) {
		if cl.A == 0 {
			return
		}
		if cl.A == 255 {
			canvas.SetNRGBA(x, y, cl)
			return
		}
		b := canvas.NRGBAAt(x, y)
		a := uint32(cl.A)
		ia := 255 - a
		canvas.SetNRGBA(x, y, color.NRGBA{
			R: uint8((uint32(cl.R)*a + uint32(b.R)*ia) / 255),
			G: uint8((uint32(cl.G)*a + uint32(b.G)*ia) / 255),
			B: uint8((uint32(cl.B)*a + uint32(b.B)*ia) / 255),
			A: uint8(min(255, int(a)+int(b.A)*int(ia)/255)),
		})
	}

	cw, ch := float64(canvas.Bounds().Dx()), float64(canvas.Bounds().Dy())
	for _, d := range ds {
		for fi := 0; fi < 6; fi++ {
			if n := c.rot(faceNormal(fi)); n[2] <= 0 {
				continue // backface
			}
			uv := d.b.faces[fi]
			var pxy [4][2]float64
			for i, p := range d.b.faceCorners(fi) {
				x, y := c.proj(p)
				pxy[i][0] = x*ratio + offX
				pxy[i][1] = y*ratio + offY
			}
			// Affine inverse: P = TL + a*(TR-TL) + b*(BL-TL).
			ux, uy := pxy[1][0]-pxy[0][0], pxy[1][1]-pxy[0][1]
			vx, vy := pxy[3][0]-pxy[0][0], pxy[3][1]-pxy[0][1]
			det := ux*vy - uy*vx
			if math.Abs(det) < 1e-9 {
				continue
			}
			x0 := max(0, int(math.Floor(math.Min(math.Min(pxy[0][0], pxy[1][0]), math.Min(pxy[2][0], pxy[3][0])))))
			y0 := max(0, int(math.Floor(math.Min(math.Min(pxy[0][1], pxy[1][1]), math.Min(pxy[2][1], pxy[3][1])))))
			x1 := min(int(math.Ceil(math.Max(math.Max(pxy[0][0], pxy[1][0]), math.Max(pxy[2][0], pxy[3][0])))), int(cw))
			y1 := min(int(math.Ceil(math.Max(math.Max(pxy[0][1], pxy[1][1]), math.Max(pxy[2][1], pxy[3][1])))), int(ch))
			for y := y0; y < y1; y++ {
				for x := x0; x < x1; x++ {
					dx, dy := float64(x)+0.5-pxy[0][0], float64(y)+0.5-pxy[0][1]
					a := (dx*vy - dy*vx) / det
					b := (ux*dy - uy*dx) / det
					if a < 0 || a >= 1 || b < 0 || b >= 1 {
						continue
					}
					over(x, y, sample(float64(uv.u)+a*float64(uv.w), float64(uv.v)+b*float64(uv.h)))
				}
			}
		}
	}
	return canvas
}

// backgroundKey mirrors the legacy renderer's "fix for weird rendering
// skins": when the top-left 8×8 pixel block is one fully opaque color, that
// color is the sheet background. Images whose corner is transparent (normal
// alpha skins) never match, so their overlays are rendered unchanged.
func backgroundKey(skin *image.NRGBA) (color.NRGBA, bool) {
	b := skin.Bounds()
	if b.Dx() < 8 || b.Dy() < 8 {
		return color.NRGBA{}, false
	}
	key := skin.NRGBAAt(b.Min.X, b.Min.Y)
	if key.A != 255 {
		return color.NRGBA{}, false
	}
	for y := 0; y < 8; y++ {
		for x := 0; x < 8; x++ {
			if skin.NRGBAAt(b.Min.X+x, b.Min.Y+y) != key {
				return color.NRGBA{}, false
			}
		}
	}
	return key, true
}

// baseOpaqueTexel lists the 64-space rectangles the legacy renderer keeps
// opaque when it keys out the background color. Overlay rows (v 32..47) and
// the helmet faces (u 32..63) are intentionally absent so white overlay
// layers disappear; the left-limb overlay net (v 48..63) is kept.
func baseOpaqueTexel(u, v int) bool {
	switch {
	case u >= 8 && u <= 23 && v >= 0 && v <= 7:
		return true
	case u >= 0 && u <= 31 && v >= 8 && v <= 15:
		return true
	case u >= 4 && u <= 11 && v >= 16 && v <= 19:
		return true
	case u >= 20 && u <= 35 && v >= 16 && v <= 19:
		return true
	case u >= 44 && u <= 51 && v >= 16 && v <= 19:
		return true
	case u >= 0 && u <= 54 && v >= 20 && v <= 31:
		return true
	case u >= 20 && u <= 27 && v >= 48 && v <= 51:
		return true
	case u >= 36 && u <= 43 && v >= 48 && v <= 51:
		return true
	case u >= 16 && u <= 47 && v >= 52 && v <= 63:
		return true
	}
	return false
}

// NormalizeBackground returns a viewer-ready copy of skin with the legacy
// background color made transparent outside the base UV regions. The stored
// file and its hash stay untouched — this only feeds clients (skinview3d)
// that cannot apply the legacy white-overlay fix themselves.
func NormalizeBackground(skin *image.NRGBA) *image.NRGBA {
	key, ok := backgroundKey(skin)
	if !ok {
		return skin
	}
	out := image.NewNRGBA(skin.Bounds())
	draw.Draw(out, out.Bounds(), skin, skin.Bounds().Min, draw.Src)
	b := skin.Bounds()
	sx := float64(b.Dx()) / 64
	sy := sx
	if b.Dy()*2 == b.Dx() {
		sy = float64(b.Dy()) / 32
	}
	for y := b.Min.Y; y < b.Max.Y; y++ {
		for x := b.Min.X; x < b.Max.X; x++ {
			c := out.NRGBAAt(x, y)
			if c.A != 255 || c.R != key.R || c.G != key.G || c.B != key.B {
				continue
			}
			u := int(float64(x-b.Min.X) / sx)
			v := int(float64(y-b.Min.Y) / sy)
			if !baseOpaqueTexel(u, v) {
				out.SetNRGBA(x, y, color.NRGBA{})
			}
		}
	}
	return out
}

// tightBounds returns the alpha bounding box of img (content may be
// smaller than the canvas because of padding/ceil).
func tightBounds(img *image.NRGBA) image.Rectangle {
	b := img.Bounds()
	minX, minY := b.Max.X, b.Max.Y
	maxX, maxY := b.Min.X, b.Min.Y
	for y := b.Min.Y; y < b.Max.Y; y++ {
		for x := b.Min.X; x < b.Max.X; x++ {
			if img.NRGBAAt(x, y).A != 0 {
				if x < minX {
					minX = x
				}
				if x >= maxX {
					maxX = x + 1
				}
				if y < minY {
					minY = y
				}
				if y >= maxY {
					maxY = y + 1
				}
			}
		}
	}
	if maxX <= minX || maxY <= minY {
		return image.Rect(0, 0, 1, 1)
	}
	return image.Rect(minX, minY, maxX, maxY)
}

// resizeNearest scales src region r to w×h with nearest sampling.
func resizeNearest(src *image.NRGBA, r image.Rectangle, w, h int) *image.NRGBA {
	dst := image.NewNRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		sy := r.Min.Y + y*r.Dy()/h
		for x := 0; x < w; x++ {
			sx := r.Min.X + x*r.Dx()/w
			dst.SetNRGBA(x, y, src.NRGBAAt(sx, sy))
		}
	}
	return dst
}

// HasLayer2 reports whether the decoded skin carries the second (overlay)
// layer: only 64×64-shaped sheets do. Legacy 64×32 sheets — and their HD 2:1
// upscales (128×64, 640×320, …) — have no helmet/limb overlay at all.
func HasLayer2(img image.Image) bool {
	b := img.Bounds()
	if b.Dy()*2 == b.Dx() { // 2:1 legacy layout
		return false
	}
	return b.Dy() > 32
}

// RenderAvatar renders the head shot resized to size×size (legacy
// avatar() unconditionally resizes the render to size). threeD selects
// the 45° cube view (legacy render3dAvatar); flat2d renders the front
// face only (legacy render2dAvatar rotation 0).
func RenderAvatar(skin *image.NRGBA, size int, threeD bool) *image.NRGBA {
	parts := headOnlyParts(HasLayer2(skin))
	yaw, pitch := 0.0, 0.0
	if threeD {
		// -45° puts the face left and the head's left side panel right,
		// matching the live legacy probe (skin.example.com ?3d).
		yaw, pitch = -45, 22
	}
	c := newCam(yaw, pitch)
	w, h := c.spread(parts)
	ratio := float64(size) / math.Max(w, h)
	pad := int(math.Ceil(float64(size))) // safety offset; content cropped below
	canvas := render(skin, parts, c, ratio, pad)
	return resizeNearest(canvas, tightBounds(canvas), size, size)
}

// RenderPreview renders the full-body back (135°) and front (-45°) views
// side by side (legacy renderSkin layout: back left, front right) at a
// fixed ratio. The legacy `?height` query is parsed but ignored on the
// live site (probe: 100/400/default all return identical 430×437) and
// the manual documents only `?png` — so no size knob here either.
func RenderPreview(skin *image.NRGBA, slim bool) *image.NRGBA {
	parts := modelParts(slim, HasLayer2(skin))
	const ratio = 10.0
	cf := newCam(-45, 10)
	back := render(skin, parts, newCam(135, 10), ratio, 0)
	front := render(skin, parts, cf, ratio, 0)
	hp, vp, ip := 30, 15, 15
	canvas := image.NewNRGBA(image.Rect(0, 0,
		hp*2+back.Bounds().Dx()+front.Bounds().Dx()+ip*2,
		vp*2+max(back.Bounds().Dy(), front.Bounds().Dy())))
	draw.Draw(canvas, image.Rect(hp, vp, hp+back.Bounds().Dx(), vp+back.Bounds().Dy()), back, image.Point{}, draw.Over)
	draw.Draw(canvas, image.Rect(hp+back.Bounds().Dx()+ip*2, vp,
		hp+back.Bounds().Dx()+ip*2+front.Bounds().Dx(), vp+front.Bounds().Dy()), front, image.Point{}, draw.Over)
	return canvas
}

// DefaultAvatar returns the embedded fallback avatar (legacy
// resources/misc/textures/avatar{2d,3d}.png — captured from the live
// legacy site), resized to size×size.
func DefaultAvatar(size int, threeD bool) (*image.NRGBA, error) {
	name := "av_default2d.png"
	if threeD {
		name = "av_default3d.png"
	}
	raw, err := defaultFS.ReadFile(name)
	if err != nil {
		return nil, fmt.Errorf("default avatar asset: %w", err)
	}
	img, err := png.Decode(bytes.NewReader(raw))
	if err != nil {
		return nil, fmt.Errorf("decode default avatar: %w", err)
	}
	tmp := image.NewNRGBA(img.Bounds())
	draw.Draw(tmp, tmp.Bounds(), img, img.Bounds().Min, draw.Src)
	return resizeNearest(tmp, img.Bounds(), size, size), nil
}

// LoadSkin decodes a texture PNG file into NRGBA.
func LoadSkin(path string) (*image.NRGBA, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	return LoadSkinReader(f)
}

// LoadSkinReader decodes a texture PNG stream into NRGBA at its native
// resolution. HD sheets stay full-resolution — render() maps the 64-space UV
// tables onto whatever grid the sheet has, so no detail is discarded.
func LoadSkinReader(r io.Reader) (*image.NRGBA, error) {
	img, err := png.Decode(r)
	if err != nil {
		return nil, err
	}
	dst := image.NewNRGBA(img.Bounds())
	draw.Draw(dst, dst.Bounds(), img, img.Bounds().Min, draw.Src)
	return dst, nil
}
