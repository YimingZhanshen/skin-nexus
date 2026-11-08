// scripts/check-theme-contract.mjs — two narrow gates around the theme contract.
//
// Why these two and not a full lint-theme: both failures are invisible at
// runtime, and both have already happened once in this repo's history.
//
//   1. Spec drift. docs/theme-spec.md §3 lists the contract tokens with their
//      default values, because a theme author reads that block and copies from
//      it. The real values live in server/cmd/skin-nexus/theme-base.css, and
//      nothing compared the two — so the documented defaults went stale by all
//      seven keys while the CSS moved on, and every pack written from the doc
//      inherited colours the host no longer has.
//   2. Dangling token references. A published pack that says var(--x) where the
//      host stopped defining --x does not error: the declaration becomes a dead
//      variable that falls back, or resolves to nothing, on an installed pack
//      that cannot be re-signed as the same version. Renaming a token in the
//      source is a silent break of every pack that used the old name.
//
// Check 2 needs the packs, which live in a sibling repository (plugins-dist).
// When that directory is not reachable — a CI checkout of this repo alone — the
// check reports how many packs it saw, so "0 because the dir is absent" is a
// visible note rather than a passing test. Locally it runs against the real ones.
import { readFileSync, readdirSync, existsSync, statSync } from 'node:fs'
import { dirname, join, resolve } from 'node:path'
import { fileURLToPath } from 'node:url'

const here = dirname(fileURLToPath(import.meta.url))
const root = resolve(here, '..')
const problems = []
const fail = (msg) => problems.push(msg)
// The docs and the CSS are CRLF-checked-out on Windows; every regex below is
// written against \n, so line endings are normalised at read rather than
// duplicated in each pattern.
const readText = (p) => readFileSync(p, 'utf8').replace(/\r\n/g, '\n')

// ---- the single source of truth for tokens --------------------------------
const basePath = join(root, 'server', 'cmd', 'skin-nexus', 'theme-base.css')
const baseCss = readText(basePath)
const rootBlock = baseCss.match(/:root\s*\{([\s\S]*?)\n\}/)
if (!rootBlock) fail('theme-base.css: no `:root { … }` block found — this gate went blind')

// "rgba(169, 138, 95, .24)" and "rgba(169,138,95,.24)" are the same declaration.
const norm = (v) => v.replace(/\s+/g, ' ').trim()
const hostTokens = new Map()
if (rootBlock) {
  for (const m of rootBlock[1].matchAll(/^\s*(--[A-Za-z0-9_-]+)\s*:\s*([^;]+);/gm)) {
    hostTokens.set(m[1], norm(m[2]))
  }
}
if (hostTokens.size < 40) fail(`theme-base.css defines only ${hostTokens.size} tokens — the parser is blind`)

// ---- 1: docs/theme-spec.md §3 must state the real defaults ----------------
const CONTRACT_KEYS = ['--bg', '--panel', '--border', '--text', '--muted', '--accent', '--danger']
const spec = readText(join(root, 'docs', 'theme-spec.md'))
const sec3 = spec.match(/^## 3\.[\s\S]*?```css\n([\s\S]*?)^```/m)
if (!sec3) {
  fail('docs/theme-spec.md: no css fence under "## 3" — the contract block moved, or this gate went blind')
} else {
  const documented = new Map()
  for (const m of sec3[1].matchAll(/(--[A-Za-z0-9_-]+)\s*:\s*([^;]+);/g)) {
    // strip the trailing /* … */ comment the table carries for readability
    documented.set(m[1], norm(m[2].replace(/\/\*[\s\S]*?\*\//g, '')))
  }
  for (const key of CONTRACT_KEYS) {
    if (!documented.has(key)) fail(`docs/theme-spec.md §3 no longer documents ${key}`)
    else if (!hostTokens.has(key)) fail(`theme-base.css does not define ${key}, which §3 promises to themes`)
    else if (documented.get(key) !== hostTokens.get(key)) {
      fail(`docs/theme-spec.md §3 is stale on ${key}: doc says "${documented.get(key)}", theme-base.css says "${hostTokens.get(key)}"`)
    }
  }
}

// ---- 2: packs must not reference a token the host stopped defining --------
const packsDir = process.env.SKIN_THEME_PACKS_DIR || resolve(root, '..', 'plugins-dist', 'themes')
let checkedPacks = 0
if (!existsSync(packsDir)) {
  console.log(`check-theme-contract: note — no packs at ${packsDir} (sibling repo not checked out); contract-reference check skipped`)
} else {
  const packDirs = readdirSync(packsDir).filter((name) => {
    const p = join(packsDir, name)
    return statSync(p).isDirectory() && existsSync(join(p, 'theme.json'))
  })
  if (packDirs.length === 0) fail(`${packsDir} exists but holds no theme pack directory — the market layout moved, update this gate`)

  for (const dirName of packDirs) {
    checkedPacks++
    let manifest
    try {
      manifest = JSON.parse(readFileSync(join(packsDir, dirName, 'theme.json'), 'utf8'))
    } catch (e) {
      fail(`themes/${dirName}/theme.json: unparseable (${e.message})`)
      continue
    }
    const sheets = Array.isArray(manifest?.inject?.css) ? manifest.inject.css : []
    if (sheets.length === 0) {
      fail(`themes/${dirName}: theme.json declares no inject.css — nothing to check, so this pack is invisible to the gate`)
      continue
    }
    const text = sheets.map((rel) => {
      const p = join(packsDir, dirName, rel)
      if (!existsSync(p)) {
        fail(`themes/${dirName}: theme.json injects ${rel}, which is not in the pack`)
        return ''
      }
      return readText(p)
    }).join('\n')

    // A pack may define its own private tokens (--az-*); those are not the host's
    // business. Only a reference with neither a local definition nor a host
    // definition is dead.
    const localDefs = new Set([...text.matchAll(/(^|[;{}\s])(--[A-Za-z0-9_-]+)\s*:/gm)].map((m) => m[2]))
    const refs = new Set([...text.matchAll(/var\(\s*(--[A-Za-z0-9_-]+)/g)].map((m) => m[1]))
    for (const key of refs) {
      if (!localDefs.has(key) && !hostTokens.has(key)) {
        fail(`themes/${dirName} references var(${key}) but neither the pack nor the host defines it — the declaration is dead on an installed pack`)
      }
    }
    // Informational: which host tokens this pack overrides. Renaming any of them
    // in theme-base.css is what check 2 above catches, so the list is printed
    // rather than asserted.
    const overridden = [...refs].filter((k) => hostTokens.has(k)).sort()
    console.log(`  themes/${dirName}: ${overridden.length} host tokens overridden${overridden.length ? ' — ' + overridden.join(' ') : ''}`)
  }
}

if (problems.length) {
  console.error('check-theme-contract: FAILED')
  for (const p of problems) console.error('  - ' + p)
  process.exit(1)
}
console.log(`check-theme-contract: ok (${hostTokens.size} tokens in the source, ${CONTRACT_KEYS.length} documented in §3, ${checkedPacks} packs scanned for dead token references)`)
