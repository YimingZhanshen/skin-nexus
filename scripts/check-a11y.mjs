// check-a11y.mjs — zero-dependency a11y gate (W6-4 / G11).
//
// Scans the SPA .vue sources and the SSR Go templates for the table + image
// accessibility contract:
//   - every <th> carries scope= (screen readers need the header association)
//   - every <table> carries a <caption>, aria-label, or aria-labelledby
//     (or aria-hidden when it is a decorative skeleton)
//   - every <img> carries alt= (decorative images use alt="")
//
// Comments are stripped first so prose that merely mentions <img> or <table>
// (e.g. "One <img> per card") never trips the gate. Exits 1 on any violation.
// Run: node scripts/check-a11y.mjs

import { readFileSync, readdirSync, statSync } from 'node:fs'
import { join, relative } from 'node:path'
import { fileURLToPath } from 'node:url'

const ROOT = fileURLToPath(new URL('..', import.meta.url))
const dirs = ['web/src', 'server/cmd/skin-nexus']

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
for (const d of dirs) {
  for (const file of walk(join(ROOT, d))) {
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
      if (!/\balt\s*=/.test(m[0])) errors.push(`${rel}: <img> without alt= — ${m[0].trim()}`)
    }
  }
}

if (errors.length) {
  console.error(`check-a11y: ${errors.length} violation(s)`)
  for (const e of errors) console.error('  ' + e)
  process.exit(1)
}
console.log('check-a11y: ok (th scope / table caption / img alt)')
