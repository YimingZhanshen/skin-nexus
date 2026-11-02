// Display conventions shared by the four texture surfaces (dashboard list,
// skinlib cards, closet, player pickers) so the same asset never shows three
// different hash widths.

export function shortHash(hash, n = 12) {
  const s = String(hash || '')
  if (!s) return '—'
  return s.length <= n ? s : `${s.slice(0, n)}…`
}

// The human label of a texture. `description` is what /me/textures exposes;
// the skinlib payload calls the same column `name`.
export function textureLabel(tx) {
  return tx.description || tx.name || shortHash(tx.hash)
}

// Blessing fills the upload name from the file name; we require a name, so do
// the same prefill instead of making people retype it.
export function nameFromFile(filename) {
  return String(filename || '').replace(/\.(png|jpg|jpeg|gif|webp)$/i, '').slice(0, 50).trim()
}
