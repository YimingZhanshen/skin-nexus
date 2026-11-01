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
// Run from web/ (the npm script does) or with --fix to print the measured sets.
import { readFileSync } from 'node:fs'
import { dirname, resolve } from 'node:path'
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

if (errors.length) {
  console.error('check-locales: FAILED')
  for (const e of errors) console.error('  - ' + e)
  process.exit(1)
}
console.log(`check-locales: ok (${declared.size} codes x ${LOCALES.length} locales, ${sets[0].size} keys)`)
