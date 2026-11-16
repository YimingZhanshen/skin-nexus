// check-a11y.mjs — zero-dependency a11y gate (W6-4 / G11).
//
// Scans the SPA .vue sources, the SPA shell (web/index.html) and the SSR Go
// templates for the table + image accessibility contract:
//   - every <th> carries scope= (screen readers need the header association)
//   - every <table> carries a <caption>, aria-label, or aria-labelledby
//     (or aria-hidden when it is a decorative skeleton)
//   - every <img> carries alt= (decorative images use alt="")
//   - brand marks (the favicon.svg artwork) never carry alt="" — Bing's
//     img-alt report counts empty alt as missing, and a logo is not
//     decorative to search engines or screen readers
//
// Comments are stripped first so prose that merely mentions <img> or <table>
// (e.g. "One <img> per card") never trips the gate. Exits 1 on any violation.
// Run: node scripts/check-a11y.mjs

import { readFileSync, readdirSync, statSync } from 'node:fs'
import { join, relative } from 'node:path'
import { fileURLToPath } from 'node:url'

const ROOT = fileURLToPath(new URL('..', import.meta.url))
const dirs = ['web/src', 'server/cmd/skin-nexus']
const extraFiles = ['web/index.html']

function walk(dir) {
  const out = []
  for (const e of readdirSync(dir)) {
    const p = join(dir, e)
    if (statSync(p).isDirectory()) out.push(...walk(p))
    else if (/\.(vue|go)$/.test(p)) out.push(p)
  }
  return out
}

function strip(src) {
  return src
    .replace(/\/\*[\s\S]*?\*\//g, '')
    .replace(/<!--[\s\S]*?-->/g, '')
    .replace(/^\s*\/\/.*$/gm, '')
}

const errors = []
const files = [
  ...dirs.flatMap((d) => walk(join(ROOT, d))),
  ...extraFiles.map((f) => join(ROOT, f)),
]
for (const file of files) {
  const rel = relative(ROOT, file)
  const src = strip(readFileSync(file, 'utf8'))

  for (const m of src.matchAll(/<th\b[^>]*>/g)) {
    if (!/\bscope\s*=/.test(m[0])) errors.push(`${rel}: <th> without scope= — ${m[0].trim()}`)
  }

  for (const m of src.matchAll(/<table\b[^>]*>/g)) {
    const tag = m[0]
    const after = src.slice(m.index, m.index + 400)
    const named = /\b(aria-label|aria-labelledby|aria-hidden)\s*=/.test(tag) || /<caption\b/.test(after)
    if (!named) errors.push(`${rel}: <table> without <caption>/aria-label/aria-hidden — ${tag.trim()}`)
  }

  for (const m of src.matchAll(/<img\b[^>]*>/g)) {
    const tag = m[0]
    const alt = tag.match(/\balt\s*=\s*(?:"([^"]*)"|'([^']*)'|([^\s>]+))/)
    if (!alt) {
      errors.push(`${rel}: <img> without alt= — ${tag.trim()}`)
    } else if (/favicon\.svg/.test(tag) && !(alt[1] ?? alt[2] ?? alt[3])) {
      errors.push(`${rel}: brand logo <img> with empty alt= — ${tag.trim()}`)
    }
  }

  // The dialog prompt input (ModalHost's dialog-field) speaks for itself via
  // the prompt message; without a name a screen reader announces a bare
  // "edit text". Other inputs are tracked separately — this rule pins the
  // one residual the G11 sweep registered.
  for (const m of src.matchAll(/<input\b[^>]*>/g)) {
    const tag = m[0]
    if (/dialog-field/.test(tag) && !/\b(aria-label|aria-labelledby)\s*=/.test(tag)) {
      errors.push(`${rel}: dialog prompt input without an accessible name — ${tag.trim()}`)
    }
  }
}

if (errors.length) {
  console.error(`check-a11y: ${errors.length} violation(s)`)
  for (const e of errors) console.error('  ' + e)
  process.exit(1)
}
console.log('check-a11y: ok (th scope / table caption / img alt / brand logo alt / dialog input name)')
