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
const SSR_NS = new Set(['page', 'ui', 'detail', 'lib', 'edit', 'report', 'notfound', 'seo', 'viewer', 'landing', 'device'])
// Dotted literals that sit in page-rendering code but are not copy keys. Each is
// a name some other layer owns; the gate cannot tell them from a typo'd key, so
// the list is where the judgement is recorded — add to it deliberately, the same
// way a new SSR_NS member is a deliberate decision.
const SSR_NONCOPY_LITERALS = new Set([
  'vcs.revision', 'vcs.time', // debug.ReadBuildInfo setting keys
  'favicon.ico', 'manifest.webmanifest', // static file names
  'user.deleted', // audit action names (the SPA's audit.action.* keys mirror them)
  'oidc.device_approve', 'oidc.device_deny', 'oidc.tokens_revoke_all', 'oidc.token_revoke', // audit events
  'skinlib.detail.sidebar', // plugin slot id (internal/plugin/spec.go owns it)
  'auth.invalid_token', 'auth.account_disabled', // bearer codes written inline, not as const declarations
])
// Only these trees render page copy. Widening the scan to every Go file would
// read the backend's error-code declarations (`report.resolved`, `common.bad_id`)
// as if they were page keys and demand a catalog entry for each.
// internal/seo is scanned for the same reason the pages are: it writes the
// <title> and description shipped in a crawlable response. It sits below
// webi18n and must not import it (seo.go:201-203), so the literals here are
// checked as references — the caller injects the translated copy.
const SSR_PAGE_DIRS = ['server/cmd/skin-nexus', 'server/internal/oidc', 'server/internal/webi18n', 'server/internal/seo']
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
    const line = 1 + src.slice(0, m.index).split('\n').length - 1
    if (SSR_FILE_EXT.has(key.split('.').pop())) continue
    // An error code that happens to read like a page key (`report.resolved`) is
    // the envelope's, not this catalog's.
    if (declared.has(key)) continue
    if (!SSR_NS.has(key.split('.')[0])) {
      // Silently skipping an unknown namespace is how a typo'd key — or a new
      // namespace nobody agreed to — passes for copy-free and stays untranslated.
      if (!SSR_NONCOPY_LITERALS.has(key)) {
        errors.push(`${rel}:${line}: dotted literal '${key}' is neither a catalog namespace (${[...SSR_NS].join('/')}) nor a declared non-copy literal — add the namespace deliberately, or list the literal with the reason`)
      }
      continue
    }
    if (!ssrReferenced.has(key)) ssrReferenced.set(key, `${rel}:${line}`)
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
const CJK = /\p{Script=Han}/u
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
      // Match on the whole hit: with /u a Han code point above the BMP is two UTF-16
      // units, and slicing the string at a lone surrogate locates the wrong place.
      const offset = raw.index + 1 + body.indexOf(m[0])
      const line = 1 + src.slice(0, offset).split('\n').length - 1
      errors.push(`${rel}:${line}: Chinese left inside a rendered template (${JSON.stringify(visible.slice(visible.indexOf(m[0]), visible.indexOf(m[0]) + 24))}) — add a catalog key`)
    }
  }
}

// 5. every key a view asks for must exist. vue-i18n renders the raw path when a
//    lookup misses, and parity between the two dictionaries cannot catch it: a key
//    missing from *both* files is perfectly "equal". This scans the literals, plus
//    every dynamically built key — `t(\`admin.kind.${x}\`)`, `t('admin.diag.' + x)`,
//    and the same shapes assigned to a variable before the call.
//
//    A dynamic key can only be checked as a family: the gate requires at least one
//    entry under the prefix in each locale. An empty object is not a family — the
//    lookup misses for every value, which is the failure this rule exists to catch.
//
//    The floor below is the other half. Dead UI keys cannot be listed by a rule
//    (a dynamic lookup looks dead to any literal scan, and the 22 keys removed in
//    the same round as this floor prove the heuristic would false-positive), so
//    the dictionary may not silently shrink: raise UI_KEY_FLOOR when keys are
//    added, and never lower it without a reason in the commit message.
const UI_KEY_FLOOR = 992
const UI_DIRS = ['web/src']

function walk(dir) {
  const out = []
  for (const item of readdirSync(join(root, dir), { withFileTypes: true })) {
    if (item.name === 'node_modules' || item.name === 'dist') continue
    const rel = `${dir}/${item.name}`
    if (item.isDirectory()) out.push(...walk(rel))
    else if (/\.(?:vue|js)$/.test(item.name)) out.push(rel)
  }
  return out
}

function at(obj, path) {
  let node = obj
  for (const part of path.split('.')) {
    if (!node || typeof node !== 'object' || !(part in node)) return undefined
    node = node[part]
  }
  return node
}

const LITERAL_KEY = /\bt\(\s*['"]([a-z][\w-]*(?:\.[\w-]+)+)['"]/g
// Every template-literal key: the static part before the first `${`. Two shapes
// reach a family — `admin.kind.${row.kind}` (group, then leaf) and
// `admin.perm${p}` (stem, then leaf). The stem shape used to be invisible: the
// old pattern demanded a dot immediately before `${`, so an enumeration could
// lose entries in both locales and the gate stayed green.
const TEMPLATE_KEY = /\bt\(\s*`([^`$]*)\$\{/g
// Keys built one step away from the call site: a dotted string that ends where
// the interpolation begins (`const key = \`admin.settingsGroup.${g}\`; t(key)`),
// and a quoted prefix concatenated with a variable (`t('admin.diag.' + it.id)`).
// The slash-free shape is what keeps API paths out of it.
const DETACHED_KEY = /[`'"]([a-z][\w-]*(?:\.[\w-]+)*\.)\$\{/g
const CONCAT_KEY = /['"]([a-z][\w-]*(?:\.[\w-]+)*\.)['"]\s*\+/g

function leafCount(node) {
  if (node && typeof node === 'object') return Object.values(node).reduce((n, v) => n + leafCount(v), 0)
  return node === undefined ? 0 : 1
}

// familyExists — does this dynamic key have somewhere to land? An empty group
// counts as nowhere: `t(\`admin.kind.${x}\`)` renders a raw path for every x.
function familyExists(obj, staticPart) {
  if (staticPart === '') return false
  if (staticPart.endsWith('.')) return leafCount(at(obj, staticPart.slice(0, -1))) > 0
  const cut = staticPart.lastIndexOf('.')
  const parent = cut < 0 ? null : staticPart.slice(0, cut)
  const stem = cut < 0 ? staticPart : staticPart.slice(cut + 1)
  const node = parent === null ? obj : at(obj, parent)
  if (!node || typeof node !== 'object') return false
  return Object.keys(node).some((k) => k.startsWith(stem))
}

for (const rel of UI_DIRS.flatMap(walk)) {
  if (rel.endsWith('locales/i18n.js') || rel.endsWith('errors.js')) {
    // errors.js builds `errors.${code}` from the server payload; rule 2 covers those.
    continue
  }
  const text = readFileSync(resolve(root, rel), 'utf8')
  const lineOf = (index) => 1 + text.slice(0, index).split('\n').length - 1
  for (const m of text.matchAll(LITERAL_KEY)) {
    const missing = LOCALES.filter((l, i) => !sets[i].has(m[1]))
    if (missing.length) {
      errors.push(`${rel}:${lineOf(m.index)}: t('${m[1]}') has no entry in ${missing.join('/')}`)
    }
  }
  for (const m of text.matchAll(TEMPLATE_KEY)) {
    const missing = LOCALES.filter((l) => !familyExists(loaded[l], m[1]))
    if (missing.length) {
      errors.push(`${rel}:${lineOf(m.index)}: t(\`${m[1]}\${…}\`) has no family under '${m[1].replace(/\.$/, '')}' in ${missing.join('/')} — an enumeration needs at least one entry per locale`)
    }
  }
  for (const pat of [DETACHED_KEY, CONCAT_KEY]) {
    for (const m of text.matchAll(pat)) {
      const missing = LOCALES.filter((l) => !familyExists(loaded[l], m[1]))
      if (missing.length) {
        errors.push(`${rel}:${lineOf(m.index)}: dynamic key under '${m[1].replace(/\.$/, '')}' has no entry in ${missing.join('/')} — an enumeration needs at least one entry per locale`)
      }
    }
  }
}

if (sets[0].size < UI_KEY_FLOOR) {
  errors.push(`UI dictionary has only ${sets[0].size} keys, floor is ${UI_KEY_FLOOR} — keys were removed without a reason`)
}


// 6. a .vue template must carry no untranslated copy either. The SPA does have a
//    fallback locale, which is exactly why this needs a gate: vue-i18n silently
//    serves zh-CN inside an English UI, so a leftover Chinese label is invisible
//    to whoever writes it and visible to every visitor. Same contract as the SSR
//    rule above, three differences: HTML comments keep their Chinese (design
//    notes, and a block comment's continuation lines are not copy), `<script>`
//    and `<style>` blocks are outside the region, and the region ends where the
//    <template> tag does — matched by depth pairing, because a named slot nests a
//    second `</template>` and an interval scan would stop there and call the rest
//    of the file untranslated-free.
function templateRegions(src) {
  const out = []
  const tag = /<template[\s>]|<\/template>/g
  let depth = 0, start = -1, m
  while ((m = tag.exec(src))) {
    if (m[0].startsWith('</')) {
      if (depth === 1) out.push([start, m.index])
      depth = Math.max(0, depth - 1)
    } else {
      if (depth === 0) start = m.index + m[0].length
      depth++
    }
  }
  return out
}

for (const rel of UI_DIRS.flatMap(walk).filter((r) => r.endsWith('.vue'))) {
  const src = readFileSync(resolve(root, rel), 'utf8')
  for (const [from, to] of templateRegions(src)) {
    const body = src.slice(from, to).replace(/<!--[\s\S]*?-->/g, (c) => c.replace(/[^\n]/g, ' '))
    // One report per line, and the whole match before slicing: with /u a Han
    // code point above the BMP is two UTF-16 units, so cutting at a lone
    // surrogate would point the operator at the wrong line.
    for (const line of body.matchAll(/[^\n]*\p{Script=Han}[^\n]*/gu)) {
      const offset = from + line.index
      const at = 1 + src.slice(0, offset).split('\n').length - 1
      const text = line[0].trim()
      errors.push(`${rel}:${at}: Chinese left inside a rendered template (${JSON.stringify(text.slice(0, 60))}) — add a locale key`)
    }
  }
}


if (errors.length) {
  console.error('check-locales: FAILED')
  for (const e of errors) console.error('  - ' + e)
  process.exit(1)
}
console.log(`check-locales: ok (${declared.size} codes x ${LOCALES.length} locales, ${sets[0].size} UI keys, ${catalogs[0].size} SSR keys)`)
