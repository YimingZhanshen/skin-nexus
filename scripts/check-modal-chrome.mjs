// scripts/check-modal-chrome.mjs — everything that stands in front of the
// page goes through the top layer.
//
// Why this is a gate and not a review note: the app already has the right
// shape (DialogPanel.vue wraps a native <dialog> and calls showModal(), which
// is what makes it immune to overflow and stacking contexts, traps Tab, and
// returns focus to the trigger). The diagnose modal was a hand-rolled
// `position: fixed` overlay that ignored that convention, and being fixed
// made it a victim of something no one could see in the source:
// web/src/style.css animates `view-rise` on `.user-main > *` with
// fill-mode both, and once that animation has run Chrome reports the
// computed transform as matrix(1,0,0,1,0,0) instead of the keyword none.
// A non-none transform turns that ancestor into the containing block of any
// position:fixed descendant — measured on a copy of the real tree, the
// overlay then covered the 1800px panel instead of the 1145px viewport, so
// the modal sat below the fold and slid away when the operator scrolled.
//
// Nothing about that is visible to a type checker or a build, and the next
// overlay written the same way would fail the same way. So: no hand-rolled
// fixed overlays, and the diagnose modal must stay a dialog.

import { readFileSync, readdirSync, statSync } from 'node:fs'
import { dirname, join, resolve, relative } from 'node:path'
import { fileURLToPath } from 'node:url'

const here = dirname(fileURLToPath(import.meta.url))
const root = resolve(here, '..')
const srcDir = join(root, 'web', 'src')
const problems = []
const fail = (msg) => problems.push(msg)
// Windows checkouts are CRLF; patterns below are written against \n.
const readText = (p) => readFileSync(p, 'utf8').replace(/\r\n/g, '\n')

// Comments are not declarations. Two self-inflicted misses came from scanning
// raw source: this file's own explanation contains the phrase position:fixed,
// and UnionView's rationale contains the literal showModal() — both checks
// passed against the prose while the code underneath was mutated. Stripping
// happens once, at the entry, for every check below. Whole-line // removal is
// deliberate: it cannot eat the slashes in an https:// string.
function codeOf(text) {
  return text
    .replace(/<!--[\s\S]*?-->/g, '')
    .replace(/\/\*[\s\S]*?\*\//g, '')
    .split('\n')
    .filter((line) => !/^\s*\/\//.test(line))
    .join('\n')
}

// ToastHub is the one fixed-position surface that is not a modal: a toast
// stack has to pin to the viewport corner and has no focus story to trap.
const FIXED_ALLOWLIST = new Set(['components/ToastHub.vue'])

function walk(dir) {
  const out = []
  for (const name of readdirSync(dir)) {
    const p = join(dir, name)
    if (statSync(p).isDirectory()) out.push(...walk(p))
    else if (/\.(vue|js)$/.test(name)) out.push(p)
  }
  return out
}

const files = walk(srcDir)
if (files.length < 20) fail(`scanned only ${files.length} files under web/src — this gate went blind`)

function cssOf(rel, code) {
  if (!rel.endsWith('.vue')) return code
  const blocks = [...code.matchAll(/<style[^>]*>([\s\S]*?)<\/style>/g)].map((m) => m[1])
  return blocks.join('\n')
}

let fixedFound = 0
for (const file of files) {
  const rel = relative(srcDir, file).replaceAll('\\', '/')
  const code = codeOf(readText(file))
  const css = cssOf(rel, code)
  if (rel.endsWith('.vue')) {
    // An inline style attribute can pin to the viewport too.
    const inline = code.match(/style="[^"]*position:\s*fixed[^"]*"/g) || []
    if (inline.length) fail(`${rel}: position:fixed in an inline style attribute — same containing-block trap`)
  }
  // `position: fixed` in a <style> block or an inline style object.
  const hits = css.match(/position:\s*fixed/g) || []
  if (hits.length && !FIXED_ALLOWLIST.has(rel)) {
    fixedFound += hits.length
    fail(`${rel}: ${hits.length} position:fixed. Front-of-page surfaces use <dialog> + showModal() `
      + '(DialogPanel.vue): the top layer cannot be captured by an ancestor that animates transform, '
      + 'and the focus trap, Esc and the accessible name come free. If this really is a non-modal '
      + 'viewport-pinned surface, add it to FIXED_ALLOWLIST in this script and say why.')
  }
}

// The diagnose modal specifically: it was the offender, so its own shape is
// asserted rather than left to the generic rule above.
const unionPath = join(srcDir, 'components', 'admin', 'UnionView.vue')
const union = codeOf(readText(unionPath))
if (!/<dialog[\s\S]*?class="diag-modal"/.test(union)) {
  fail('UnionView.vue: the diagnose modal is not a native <dialog class="diag-modal">')
}
if (!union.includes('showModal()')) {
  fail('UnionView.vue: the diagnose dialog never calls showModal(). A bare <dialog open> stays in '
    + 'the document flow — measured, it renders at the top of the animated ancestor instead of '
    + 'centred in the viewport, which is worse than the overlay it replaced.')
}
if (union.includes('diag-overlay')) {
  fail('UnionView.vue: diag-overlay is back. It was the hand-rolled fixed backdrop; the ::backdrop '
    + 'pseudo-element of the dialog does that job now.')
}

if (problems.length) {
  console.error(`check-modal-chrome: ${problems.length} problem(s)`)
  for (const p of problems) console.error('  - ' + p)
  process.exit(1)
}
console.log(`check-modal-chrome: ok (${files.length} files scanned, `
  + `${fixedFound} non-allowlisted fixed surface(s), diagnose modal is a top-layer dialog)`)
