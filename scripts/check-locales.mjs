// scripts/check-locales.mjs — the locale and error-code parity gate.
//
// Three failures it prevents:
//   1. a key present in one language and missing in the other, which renders as
//      the raw key path (vue-i18n falls back to zh-CN, so an English UI silently
//      serves Chinese);
//   2. a backend error code with no dictionary entry, which is the whole point of
//      the `{error, code, message}` envelope — without the entry the user gets
//      the server's prose and an English UI gets Chinese;
//   3. a dictionary entry whose code no longer exists, which hides a retired code
//      behind a message nobody can see and nobody removes.
//
// Plus the server-rendered catalog (webi18n), which has no vue-i18n fallback to
// hide behind: parity, no dead copy, no typo pointing at a missing key, and no
// Chinese left inside a rendered template.
//
// Run from web/ (the npm script does) or with --fix to print the measured sets.
import { readFileSync, readdirSync } from 'node:fs'
import { dirname, join, resolve } from 'node:path'
import { fileURLToPath } from 'node:url'

const here = dirname(fileURLToPath(import.meta.url))
const root = resolve(here, '..')
const LOCALES = ['zh-CN', 'en']
const BACKEND_CODE_FILES = [
  'server/internal/i18n/codes.go',
  'server/internal/account/service.go',
]

const errors = []

function leafPaths(obj, prefix = '') {
  const out = new Set()
  for (const [key, value] of Object.entries(obj)) {
    const path = prefix ? `${prefix}.${key}` : key
    if (value && typeof value === 'object') {
      for (const p of leafPaths(value, path)) out.add(p)
    } else {
      out.add(path)
    }
  }
  return out
}

const loaded = {}
for (const locale of LOCALES) {
  const path = resolve(root, `web/src/locales/${locale}.json`)
  try {
    loaded[locale] = JSON.parse(readFileSync(path, 'utf8'))
  } catch (e) {
    errors.push(`cannot read ${locale} locale: ${e.message}`)
    loaded[locale] = {}
  }
}

// 1. key-set parity
const sets = LOCALES.map((l) => leafPaths(loaded[l]))
for (let i = 1; i < LOCALES.length; i++) {
  for (const key of sets[0]) {
    if (!sets[i].has(key)) errors.push(`${LOCALES[0]} has '${key}', ${LOCALES[i]} does not`)
  }
  for (const key of sets[i]) {
    if (!sets[0].has(key)) errors.push(`${LOCALES[i]} has '${key}', ${LOCALES[0]} does not`)
  }
}

// 2 and 3. coverage of the backend code catalogue. Codes are declared as Go
// string constants; the shape `<area>.<reason>` (plus the bare `internal`) is
// what the envelope gate allows, so matching it here is enough to find the set.
const CODE_VALUE = /=\s*"([a-z][a-z0-9_]*(?:\.[a-z0-9_]+)+|internal)"/g
const declared = new Set()
for (const rel of BACKEND_CODE_FILES) {
  let src
  try {
    src = readFileSync(resolve(root, rel), 'utf8')
  } catch (e) {
    errors.push(`cannot read ${rel}: ${e.message}`)
    continue
  }
  for (const m of src.matchAll(CODE_VALUE)) declared.add(m[1])
}
if (declared.size < 50) {
  errors.push(`only found ${declared.size} backend error codes — the declaration shape changed and this check is now blind`)
}

function hasPath(obj, dotted) {
  return dotted.split('.').reduce((node, part) => (node && typeof node === 'object' ? node[part] : undefined), obj) !== undefined
}

for (const code of declared) {
  for (const locale of LOCALES) {
    if (!hasPath(loaded[locale], `errors.${code}`)) {
      errors.push(`${locale}: no errors.${code} entry for a backend code`)
    }
  }
}

for (const locale of LOCALES) {
  const dict = loaded[locale].errors || {}
  for (const key of leafPaths(dict)) {
    // network/networkTimeout are this file's own transport failures, not codes.
    if (key === 'network' || key === 'networkTimeout') continue
    if (!declared.has(key)) errors.push(`${locale}: errors.${key} is not a code the backend declares`)
  }
}

// ---------------------------------------------------------------------------
// Server-rendered catalog (internal/webi18n).
//
// The SPA has a fallback locale and a visible bundle; the SSR pages have
// neither — a missing key prints its own name in a crawlable page, and a
// leftover Chinese literal ships silently in English output. So this catalog
// gets four checks: parity, no dead copy, no reference to a key that does not
// exist, and no CJK surviving inside a rendered template.
// ---------------------------------------------------------------------------

const SSR_CATALOG = ['zh-CN', 'en'].map((l) => [`server/internal/webi18n/catalogs/${l}.json`, JSON.parse(readFileSync(resolve(root, `server/internal/webi18n/catalogs/${l}.json`), 'utf8'))])
// The vocabulary is a closed set: a new namespace is a deliberate decision.
// `errors.*` must never appear here (that namespace belongs to the envelope
// gate above), and page labels sit under `ui.` rather than `common.` because
// `common.*` is already an error-code namespace the backend declares.
const SSR_NS = new Set(['page', 'ui', 'detail', 'edit', 'report', 'notfound', 'seo', 'viewer', 'landing', 'device'])
// Only these trees render page copy. Widening the scan to every Go file would
// read the backend's error-code declarations (`report.resolved`, `common.bad_id`)
// as if they were page keys and demand a catalog entry for each.
const SSR_PAGE_DIRS = ['server/cmd/skin-nexus', 'server/internal/oidc', 'server/internal/webi18n']
const SSR_FILE_EXT = new Set(['js', 'css', 'html', 'htm', 'json', 'md', 'png', 'svg', 'txt', 'wasm', 'pem'])

function goSources(dir, out = []) {
  for (const e of readdirSync(resolve(root, dir), { withFileTypes: true })) {
    const rel = join(dir, e.name)
    if (e.isDirectory()) goSources(rel, out)
    else if (e.name.endsWith('.go') && !e.name.endsWith('_test.go')) out.push(rel)
  }
  return out
}

const ssrSources = SSR_PAGE_DIRS.flatMap((dir) => goSources(dir))

const catalogs = SSR_CATALOG.map(([path, obj]) => {
  if (!obj || typeof obj !== 'object') {
    errors.push(`${path}: catalog must be a flat key/value object`)
    return new Map()
  }
  for (const [k, v] of Object.entries(obj)) {
    if (typeof v !== 'string') errors.push(`${path}: ${k} is not a string (the SSR catalog is flat)`)
    if (k.startsWith('errors.')) errors.push(`${path}: ${k} sits in the error-code namespace; page copy must not`)
  }
  return new Map(Object.entries(obj))
})

for (let i = 1; i < catalogs.length; i++) {
  const [nameA, a] = [SSR_CATALOG[0][0], catalogs[0]]
  const [nameB, b] = [SSR_CATALOG[i][0], catalogs[i]]
  for (const k of a.keys()) if (!b.has(k)) errors.push(`${nameA} has SSR key '${k}', ${nameB} does not`)
  for (const k of b.keys()) if (!a.has(k)) errors.push(`${nameB} has SSR key '${k}', ${nameA} does not`)
}
if (catalogs[0].size < 60) {
  errors.push(`SSR catalog has only ${catalogs[0].size} keys — the catalog file moved or the loader is blind`)
}

// Which keys the Go side actually asks for.
const ssrReferenced = new Map()
for (const rel of ssrSources) {
  const src = readFileSync(resolve(root, rel), 'utf8')
  for (const m of src.matchAll(/"([a-z][a-zA-Z0-9]*(?:\.[a-zA-Z0-9_]+)+)"/g)) {
    const key = m[1]
    if (!SSR_NS.has(key.split('.')[0])) continue
    if (SSR_FILE_EXT.has(key.split('.').pop())) continue
    // An error code that happens to read like a page key (`report.resolved`) is
    // the envelope's, not this catalog's.
    if (declared.has(key)) continue
    if (!ssrReferenced.has(key)) ssrReferenced.set(key, `${rel}:${1 + src.slice(0, m.index).split('\n').length - 1}`)
  }
}
for (const key of catalogs[0].keys()) {
  if (!ssrReferenced.has(key)) errors.push(`SSR key '${key}' is in the catalog but no Go source asks for it (dead copy)`)
}
for (const [key, where] of ssrReferenced) {
  for (const [i, cat] of catalogs.entries()) {
    if (!cat.has(key)) errors.push(`${SSR_CATALOG[i][0]}: no '${key}' entry, referenced at ${where}`)
  }
}

// Rendered templates must carry no untranslated copy. CSS blocks and comments
// keep their Chinese (design notes, 月下仙山 token comments), so they are
// stripped first — that is what makes landing.go's style block a non-hit.
const CJK = /[㐀-䶿一-鿿豈-﫿]/
for (const rel of SSR_PAGE_DIRS.flatMap((dir) => goSources(dir))) {
  const src = readFileSync(resolve(root, rel), 'utf8')
  for (const raw of src.matchAll(/`([^`]*)`/gs)) {
    const body = raw[1]
    if (!/<html[\s>]/i.test(body) && !/<!doctype\s+html/i.test(body)) continue
    const visible = body
      .replace(/<style[\s\S]*?<\/style>/gi, '')
      .replace(/<!--[\s\S]*?-->/g, '')
      .replace(/\/\*[\s\S]*?\*\//g, '')
    const m = visible.match(CJK)
    if (m) {
      const offset = raw.index + 1 + body.indexOf(m[0].charAt(0))
      const line = 1 + src.slice(0, offset).split('\n').length - 1
      errors.push(`${rel}:${line}: Chinese left inside a rendered template (${JSON.stringify(visible.slice(visible.indexOf(m[0]), visible.indexOf(m[0]) + 24))}) — add a catalog key`)
    }
  }
}

if (errors.length) {
  console.error('check-locales: FAILED')
  for (const e of errors) console.error('  - ' + e)
  process.exit(1)
}
console.log(`check-locales: ok (${declared.size} codes x ${LOCALES.length} locales, ${sets[0].size} UI keys, ${catalogs[0].size} SSR keys)`)
