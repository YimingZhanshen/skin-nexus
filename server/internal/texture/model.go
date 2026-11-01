package texture

import (
	"bytes"
	"fmt"
	"image"
	"image/png"
)

// Skin model values, matching the textures.model ENUM.
const (
	ModelDefault = "default"
	ModelSlim    = "slim"
)

// slimUnusedAreas are the two regions of the right arm's first layer that a
// slim (Alex) arm — 3px wide instead of 4px — leaves empty in the 64x64 layout,
// in logical 64x64 coordinates. This is Mojang's own rule (HttpTexture#isSlimSkin):
// any transparent pixel inside them means the skin was drawn for slim arms.
//
// skinview3d's inferModelType adds two more heuristics (left-arm region,
// solid-colour detection). We deliberately skip them: the model decides the arm
// width in-game, in /avatar, in /preview and in the library's steve/alex filter,
// so a wrong answer is wrong everywhere. A solid-colour classic skin really does
// paint those extra regions, and those heuristics would flip it to slim.
var slimUnusedAreas = []struct{ x, y, w, h int }{
	{50, 16, 2, 4},  // bottom face, outer two columns
	{54, 20, 2, 12}, // back face, outer two columns
}

// DetectSkinModel reads the pixels only — never the uploader's claim.
//
// Only square skins whose width is a multiple of 64 carry the information; the
// detection areas scale with the resolution, so HD skins are judged the same way.
// 64x32 and everything else report ModelDefault (the legacy layout has no
// slim-capable right-arm region at all), which is why the library keeps a
// manual correction path for those.
func DetectSkinModel(img image.Image) string {
	b := img.Bounds()
	w, h := b.Dx(), b.Dy()
	if w != h || w%64 != 0 {
		return ModelDefault
	}
	scale := w / 64
	for _, area := range slimUnusedAreas {
		x0, y0 := b.Min.X+area.x*scale, b.Min.Y+area.y*scale
		x1, y1 := x0+area.w*scale, y0+area.h*scale
		for y := y0; y < y1; y++ {
			for x := x0; x < x1; x++ {
				if _, _, _, a := img.At(x, y).RGBA(); a == 0 {
					return ModelSlim
				}
			}
		}
	}
	return ModelDefault
}

// DetectModelFromBytes reads the model out of a stored PNG. Used by the
// backfill tool, which must judge existing files exactly the way an upload is
// judged at write time.
func DetectModelFromBytes(raw []byte) (string, error) {
	img, err := png.Decode(bytes.NewReader(raw))
	if err != nil {
		return "", fmt.Errorf("texture: decode for model: %w", err)
	}
	return DetectSkinModel(img), nil
}
