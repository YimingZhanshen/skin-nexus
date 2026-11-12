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

function leafValues(obj, prefix = '') {
  const out = []
  for (const [key, value] of Object.entries(obj)) {
    const path = prefix ? `${prefix}.${key}` : key
    if (value && typeof value === 'object') out.push(...leafValues(value, path))
    else out.push([path, value])
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

// 1b. Messages that cannot compile. vue-i18n reads @ as the start of a linked
// message ("@:other.key", "@.lower:key"). A bare @ — including inside an
// address like a@b.com — throws "Invalid linked format" the first time the key
// renders, so the symptom is a blank panel rather than a wrong word. The escape
// this repo already uses is {'@'} (settings.smtp_from.desc). Every shape above
// was checked against the installed @intlify/message-compiler, not from memory:
// parse() accepts a bare @, baseCompile() is the one that throws, so a
// parse-only check would have passed the very string this rule catches.
const LINKED_OK = /@(?!(?:\.[a-zA-Z]+)?:)/
for (const locale of LOCALES) {
  for (const [path, value] of leafValues(loaded[locale])) {
    if (typeof value !== 'string') continue
    if (LINKED_OK.test(value.replaceAll("{'@'}", ''))) {
      errors.push(`${locale}: ${path} has a bare @ — vue-i18n treats it as a linked message and throws when the key renders. Write {'@'} to keep the character.`)
    }
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
const SSR_NS = new Set(['page', 'ui', 'detail', 'lib', 'edit', 'report', 'notfound', 'seo', 'viewer', 'landing', 'device', 'mail'])
// Dotted literals that sit in page-rendering code but are not copy keys. Each is
// a name some other layer owns; the gate cannot tell them from a typo'd key, so
// the list is where the judgement is recorded — add to it deliberately, the same
// way a new SSR_NS member is a deliberate decision.
const SSR_NONCOPY_LITERALS = new Set([
  'vcs.revision', 'vcs.time', // debug.ReadBuildInfo setting keys
  'favicon.ico', 'manifest.webmanifest', // static file names
  'user.deleted', // plugin event name (internal/plugin/events_bridge.go owns the vocabulary)
  'oidc.device_approve', 'oidc.device_deny', 'oidc.tokens_revoke_all', 'oidc.token_revoke', // audit actions, labelled by the SPA's audit.action.oidc__* keys
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


// 7. every settings knob must carry a description key in both locales. The panel
//    receives `settings.<key>.desc` as a key, not as copy: the sentence is written
//    once per language here, so an English panel cannot show a Chinese label — and
//    the whitelist in settings.go is the only list of what exists. Missing and
//    surplus entries are both failures: the first renders a raw key, the second is
//    copy nobody can reach.
const SETTINGS_SOURCE = 'server/internal/adminapi/settings.go'
const settingsSrc = readFileSync(resolve(root, SETTINGS_SOURCE), 'utf8')
const settingsEntry = /^\s*\{"([a-z0-9_]+)",\s*(?:true|false),\s*"([a-z]+)",\s*(true|false)\}/gm
const settingKnobs = [...settingsSrc.matchAll(settingsEntry)].map((m) => ({ key: m[1], secret: m[3] === 'true' }))
const settingKeys = settingKnobs.map((k) => k.key)
if (settingKeys.length < 30) {
  errors.push(`${SETTINGS_SOURCE}: only ${settingKeys.length} whitelist entries parsed — the struct shape changed and this check is now blind`)
}
// A credential field has to say so in its own sentence: an operator who types a
// secret into a box that keeps whatever is stored has written it into a mask.
// The claim lives in the copy, so it is checked against the copy.
const WRITE_ONLY = { 'zh-CN': '不回显', en: /never echoed back/i }
for (const locale of LOCALES) {
  const block = loaded[locale].settings || {}
  const want = new Set(settingKeys)
  for (const knob of settingKnobs) {
    const entry = block[knob.key] && block[knob.key].desc
    if (typeof entry !== 'string' || entry.trim() === '') {
      errors.push(`${locale}: settings.${knob.key}.desc is missing — the panel has no sentence to show for this knob`)
      continue
    }
    if (knob.secret) {
      const test = WRITE_ONLY[locale]
      if (typeof test === 'string' ? !entry.includes(test) : !test.test(entry)) {
        errors.push(`${locale}: settings.${knob.key}.desc must tell the operator the field is write-only (looked for ${test})`)
      }
    }
    // vue-i18n compiles every message, so @ and | are syntax: a sentence that
// mentions an email address (`Name <a@b.c>`) threw SyntaxError in the parser and
    // blanked the whole settings screen. Reserved characters must arrive escaped
    // as {'@'} / {'|'}, which is what the placeholder-stripped form leaves clean.
    const bare = entry.replace(/\{[^{}]*\}/g, '')
    if (/[@|]/.test(bare)) {
      errors.push(`${locale}: settings.${knob.key}.desc contains a bare @ or | that vue-i18n reads as message syntax — write it as {'@'} / {'|'} (found at "${bare.slice(bare.search(/[@|]/), 24)}")`)
    }
  }
  for (const key of Object.keys(block)) {
    if (!want.has(key)) errors.push(`${locale}: settings.${key} has no whitelist entry in ${SETTINGS_SOURCE} (dead copy)`)
  }
}

// 8. every audit action must carry a label in both locales.
//
// The audit table stores a machine name per row, and the panel used to render
// that column verbatim: an English panel showing `admin_user_password_reset`
// is not bilingual, it is a log dump. The dictionary owns the sentences; the
// Go source owns the vocabulary — so the vocabulary is read back out of the
// source and checked, the same way rule 2 checks the error codes and rule 7 the
// settings knobs.
//
// The scan is lexical rather than regex-per-line because an audit call carries
// a detail map on the same line: `InsertAudit(ctx, uid, "seo_save",
// map[string]any{"name": …})` has to yield `seo_save` and not the keys of the
// blob. Top-level string arguments of an emitter call are exactly the actions
// (account's bridge passes two — the web-side name and the plugin-side one);
// anything nested inside a map or a call is not.
const AUDIT_EMITTERS = new Set(['InsertAudit', 'detailInsertAudit', 'Audit', 'audit', 'auditEvent'])
const AUDIT_ACTION = /^[a-z][a-z0-9_]*(?:\.[a-z0-9_]+)*$/
const AUDIT_FLOOR = 60

function skipQuoted(src, i) {
  // returns [contents, index-after] for a Go string/rune literal starting at i
  const q = src[i]
  let j = i + 1
  let out = ''
  while (j < src.length && src[j] !== q) {
    if (src[j] === '\\' && q !== '`') { out += src.slice(j, j + 2); j += 2; continue }
    out += src[j]
    j++
  }
  return [out, j + 1]
}

function auditArgs(src, start) {
  // start = index just after the emitter's '('; collect depth-1 double-quoted literals
  const found = []
  let i = start
  let depth = 1
  while (i < src.length && depth > 0) {
    const c = src[i]
    if (c === '/' && src[i + 1] === '/') { while (i < src.length && src[i] !== '\n') i++; continue }
    if (c === '/' && src[i + 1] === '*') { i += 2; while (i < src.length && !(src[i] === '*' && src[i + 1] === '/')) i++; i += 2; continue }
    if (c === '"' || c === '`' || c === "'") {
      const at = i
      const [text, next] = skipQuoted(src, i)
      if (c === '"' && depth === 1 && AUDIT_ACTION.test(text)) found.push([text, at])
      i = next
      continue
    }
    if (c === '(' || c === '[' || c === '{') depth++
    else if (c === ')' || c === ']' || c === '}') depth--
    i++
  }
  return found
}

const auditActions = new Map()
for (const rel of goSources('server')) {
  const src = readFileSync(resolve(root, rel), 'utf8')
  let i = 0
  while (i < src.length) {
    const c = src[i]
    if (c === '/' && src[i + 1] === '/') { while (i < src.length && src[i] !== '\n') i++; continue }
    if (c === '/' && src[i + 1] === '*') { i += 2; while (i < src.length && !(src[i] === '*' && src[i + 1] === '/')) i++; i += 2; continue }
    if (c === '"' || c === '`' || c === "'") { i = skipQuoted(src, i)[1]; continue }
    if (/[A-Za-z_]/.test(c)) {
      let j = i
      while (j < src.length && /[A-Za-z0-9_]/.test(src[j])) j++
      const ident = src.slice(i, j)
      let k = j
      while (k < src.length && /\s/.test(src[k])) k++
      if (src[k] === '(' && AUDIT_EMITTERS.has(ident)) {
        for (const [action, at] of auditArgs(src, k + 1)) {
          if (!auditActions.has(action)) {
            const line = 1 + src.slice(0, at).split('\n').length - 1
            auditActions.set(action, `${rel}:${line}`)
          }
        }
      }
      i = j
      continue
    }
    i++
  }
}
if (auditActions.size < AUDIT_FLOOR) {
  errors.push(`audit actions: only ${auditActions.size} parsed — the emitter call shape changed and this check is now blind`)
}
// `oidc.device_approve` would nest two levels deep inside `audit.action`, so a
// dotted action maps to a double underscore and stays a single key.
const auditKeyOf = (action) => `audit.action.${action.replace(/\./g, '__')}`
{
  const want = new Set([...auditActions.keys()].map(auditKeyOf))
  for (const locale of LOCALES) {
    for (const [action, where] of auditActions) {
      const key = auditKeyOf(action)
      if (!sets[locale === LOCALES[0] ? 0 : 1].has(key)) {
        errors.push(`${locale}: no ${key} entry for the audit action written at ${where}`)
      }
    }
    const block = loaded[locale].audit && loaded[locale].audit.action
    if (!block || typeof block !== 'object') {
      errors.push(`${locale}: audit.action is missing — the panel has no label to fall back to`)
      continue
    }
    for (const key of Object.keys(block)) {
      if (!want.has(`audit.action.${key}`)) errors.push(`${locale}: audit.action.${key} matches no audit action in the server (dead copy)`)
      // Same message-syntax rule as the settings sentences: these strings are
      // compiled by vue-i18n on first use, so a bare @ in one label throws when
      // the operator filters by that action rather than on page load.
      const bare = String(block[key]).replace(/\{[^{}]*\}/g, '')
      if (/[@|]/.test(bare)) errors.push(`${locale}: audit.action.${key} contains a bare @ or | that vue-i18n reads as message syntax — write it as {'@'} / {'|'}`)
    }
  }
}

// 9. every access-log kind must carry a label in both locales AND stay in the
//    panel's filter list.
//
// The Kind column renders `t('admin.kind.' + row.kind)` — a key composed at
// runtime, so the literal scan that protects rules 1-3 never sees it. The Go
// source owns the vocabulary, exactly like the audit actions in rule 8: the
// kinds are read out of internal/accesslog's Kinds block and every one of them
// has to have a label and a filter option.
//
// A caller that writes `Kind: "something"` as a bare string in an
// accesslog.Entry is a defect on its own terms: it adds a value the gate cannot
// enumerate, which is precisely how plugin-route rows ended up rendering the
// key path `admin.kind.plugin` in an English panel.
const ACCESS_LOG_KINDS_FILE = 'server/internal/accesslog/accesslog.go'
const ACCESS_LOG_VIEW = 'web/src/components/admin/AccessLogsView.vue'
const KIND_FLOOR = 10

function kindConstants(src) {
  const at = src.indexOf('// Kinds')
  if (at < 0) return null
  const open = src.indexOf('const (', at)
  const close = src.indexOf('\n)', open)
  if (open < 0 || close < 0) return null
  const out = new Map()
  for (const m of src.slice(open, close).matchAll(/([A-Z]\w*)\s*=\s*"([a-z_]+)"/g)) out.set(m[2], m[1])
  return out
}

function entryKindLiterals(src, rel) {
  const marker = 'accesslog.Entry{'
  const out = []
  for (let i = src.indexOf(marker); i >= 0; i = src.indexOf(marker, i + marker.length)) {
    const brace = i + marker.length - 1
    let depth = 0
    let end = brace
    while (end < src.length) {
      const c = src[end]
      if (c === '"' || c === '`' || c === "'") { end = skipQuoted(src, end)[1]; continue }
      if (c === '{') depth++
      else if (c === '}') { depth--; if (depth === 0) break }
      end++
    }
    for (const m of src.slice(brace, end).matchAll(/\bKind:\s*"([^"]*)"/g)) {
      const line = 1 + src.slice(0, brace + m.index).split('\n').length - 1
      out.push([m[1], `${rel}:${line}`])
    }
    i = end
  }
  return out
}

const accessKinds = new Map()
{
  const src = readFileSync(resolve(root, ACCESS_LOG_KINDS_FILE), 'utf8')
  const consts = kindConstants(src)
  if (!consts || consts.size < KIND_FLOOR) {
    errors.push(`${ACCESS_LOG_KINDS_FILE}: only ${consts ? consts.size : 0} kind constants parsed — the Kinds block moved and this check is now blind`)
  }
  if (consts) for (const value of consts.keys()) accessKinds.set(value, ACCESS_LOG_KINDS_FILE)
}
for (const rel of goSources('server')) {
  const src = readFileSync(resolve(root, rel), 'utf8')
  for (const [value, where] of entryKindLiterals(src, rel)) {
    errors.push(`${where}: accesslog.Entry writes kind "${value}" as a bare string — declare it in the Kinds block of ${ACCESS_LOG_KINDS_FILE} so the panel vocabulary stays enumerable`)
  }
}
{
  const src = readFileSync(resolve(root, ACCESS_LOG_VIEW), 'utf8')
  const block = src.match(/const KINDS = \[([\s\S]*?)\]/)
  if (!block) {
    errors.push(`${ACCESS_LOG_VIEW}: no KINDS array — the filter list moved and this check is now blind`)
  }
  const listed = new Set()
  for (const m of (block ? block[1] : '').matchAll(/'([a-z_]+)'/g)) listed.add(m[1])
  for (const [value, where] of accessKinds) {
    LOCALES.forEach((locale, idx) => {
      if (!sets[idx].has(`admin.kind.${value}`)) {
        errors.push(`${locale}: no admin.kind.${value} entry — the Kind column renders the key path for rows written at ${where}`)
      }
    })
    if (!listed.has(value)) {
      errors.push(`${ACCESS_LOG_VIEW}: the server writes kind "${value}" but the filter dropdown never offers it`)
    }
  }
  for (const value of listed) {
    if (!accessKinds.has(value)) {
      errors.push(`${ACCESS_LOG_VIEW}: the filter offers kind "${value}" which no server code writes (dead option)`)
    }
  }
}

// 10. cross-language claim parity. A sentence that promises a detached action
//     already happened ("已发送 / sent / on its way") must make the SAME promise
//     in both languages. The 2026-11-12 leak was one language keeping an
//     overclaim the other had already been corrected to remove; rule 1 cannot
//     see it because both keys exist. Only the claim's presence is compared, so
//     an earned synchronous confirmation ("已发出 / was sent") stays legal in
//     both languages at once.
const CLAIM_ZH = /已发送|已发出|已经发送|已在路上|已送达|自动发出|发送到|发信测试成功/
const CLAIM_EN = /\b(was|were|has been|have been|is|are) sent\b|on its way|sent automatically|automatically sent|(email|mail|message|link) sent\b/i
const claimSources = [
  ['web/src/locales', new Map(leafValues(loaded[LOCALES[0]])), new Map(leafValues(loaded[LOCALES[1]]))],
  [SSR_CATALOG[0][0], catalogs[0], catalogs[1]],
]
for (const [label, a, b] of claimSources) {
  for (const [key, zh] of a) {
    const en = b.get(key)
    if (typeof zh !== 'string' || typeof en !== 'string') continue
    const zhClaims = CLAIM_ZH.test(zh)
    const enClaims = CLAIM_EN.test(en)
    if (zhClaims !== enClaims) {
      errors.push(`${label}: ${key} promises a completed send in one language only (zh=${zhClaims}, en=${enClaims}) — align the claim in both, do not delete the key`)
    }
  }
}

if (errors.length) {

  console.error('check-locales: FAILED')
  for (const e of errors) console.error('  - ' + e)
  process.exit(1)
}
console.log(`check-locales: ok (${declared.size} codes x ${LOCALES.length} locales, ${sets[0].size} UI keys, ${catalogs[0].size} SSR keys)`)
