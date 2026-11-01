// skinModel.js — client-side twin of the server's arm-model detection
// (server/internal/texture/model.go). It exists only so the 3D preview shows
// the right arms before the upload finishes; the stored model is always the
// server's answer, never this one.
//
// Mojang's rule: the slim arm is 3px wide, so two regions of the right arm's
// first layer stay empty on a slim skin and are painted on a classic one. Any
// transparent pixel there means slim. Only square skins whose width is a
// multiple of 64 carry the information.

const AREAS = [
  [50, 16, 2, 4],   // bottom face, outer two columns
  [54, 20, 2, 12],  // back face, outer two columns
]

export async function detectSkinModel(file) {
  if (typeof createImageBitmap !== 'function' || typeof OffscreenCanvas !== 'function') {
    return 'default' // preview-only degradation; the server still decides
  }
  let bmp = null
  try {
    bmp = await createImageBitmap(file)
    const { width, height } = bmp
    if (width !== height || width % 64 !== 0) return 'default'
    const scale = width / 64
    const canvas = new OffscreenCanvas(width, height)
    const ctx = canvas.getContext('2d', { willReadFrequently: true })
    ctx.drawImage(bmp, 0, 0)
    for (const [x, y, w, h] of AREAS) {
      const d = ctx.getImageData(x * scale, y * scale, w * scale, h * scale).data
      for (let i = 3; i < d.length; i += 4) {
        if (d[i] === 0) return 'slim'
      }
    }
    return 'default'
  } catch {
    return 'default'
  } finally {
    if (bmp && bmp.close) bmp.close()
  }
}
