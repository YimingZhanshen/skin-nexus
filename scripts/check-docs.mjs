// scripts/check-docs.mjs — documentation self-consistency gate.
//
// Docs here are operational promises, not prose: an env table an operator
// copy-pastes from, a compatibility matrix that says what works, a competitor
// study that must be traceable to a snapshot. Four failures are checked:
//
//   1. a relative link that goes nowhere (the reader finds out, not us);
//   2. a markdown table whose rows have differing column counts (GFM renders a
//      ragged table as a table with cells silently missing);
//   3. a competitor comparison without its evidence date — an undated table is
//      read as current forever, and "they don't have X" stops being checkable;
//   4. the deploy env table drifting from what the binary reads, in either
//      direction (a documented variable nobody reads, or a read variable that is
//      not documented and therefore never set).
import { readFileSync, readdirSync, existsSync, statSync } from 'node:fs'
import { dirname, join, resolve, relative } from 'node:path'
import { fileURLToPath } from 'node:url'

const here = dirname(fileURLToPath(import.meta.url))
const root = resolve(here, '..')
const problems = []
const fail = (msg) => problems.push(msg)

function walkFiles(dir, out = []) {
  for (const name of readdirSync(dir)) {
    const p = join(dir, name)
    const st = statSync(p)
    if (st.isDirectory()) {
      if (['node_modules', '.git', 'dist', 'data'].includes(name)) continue
      walkFiles(p, out)
    } else {
      out.push(p)
    }
  }
  return out
}

const docFiles = []
for (const dir of [join(root, 'docs'), root]) {
  for (const f of existsSync(dir) ? readdirSync(dir) : []) {
    const p = join(dir, f)
    if (statSync(p).isFile() && f.endsWith('.md')) docFiles.push(p)
  }
}
for (const sub of ['deploy', 'plugins']) {
  const p = join(root, sub)
  if (existsSync(p)) for (const f of readdirSync(p)) if (f.endsWith('.md')) docFiles.push(join(p, f))
}
if (docFiles.length < 10) fail(`only found ${docFiles.length} markdown files — the scan is broken`)

// ---- 1 and 2: links and tables -------------------------------------------
const LINK = /\]\(([^)\s]+)(?:\s+"[^"]*")?\)/g
const ANCHOR = /^#[\w-]+$/

for (const path of docFiles) {
  const rel = relative(root, path).replace(/\\/g, '/')
  const lines = readFileSync(path, 'utf8').split('\n')
  let inFence = false
  let table = null

  const flushTable = () => {
    if (!table || table.rows.length < 2) { table = null; return }
    const widths = table.rows.map((r) => r.cells)
    const header = widths[0]
    for (let i = 1; i < widths.length; i++) {
      if (widths[i] !== header) {
        fail(`${rel}: table at line ${table.start} has a row with ${widths[i]} columns, header has ${header}`)
        break
      }
    }
    table = null
  }

  lines.forEach((line, i) => {
    if (/^\s*```/.test(line)) { inFence = !inFence; return }
    if (inFence) return

    for (const m of line.matchAll(LINK)) {
      const target = m[1]
      if (/^(https?:|mailto:|data:)/.test(target) || ANCHOR.test(target)) continue
      const [filePart, anchor] = target.split('#')
      if (filePart === '') {
        if (!anchor) continue
        continue // same-file anchor: not checked (headings move too often)
      }
      const base = filePart.startsWith('/') ? root : dirname(path)
      const abs = resolve(base, filePart)
      if (!existsSync(abs)) fail(`${rel}:${i + 1} broken link: ${target}`)
    }

    const isRow = /^\s*\|.*\|\s*$/.test(line)
    if (!isRow) { flushTable(); return }
    // `\|` inside a cell is data (an alternation in a path or a grep pattern),
    // not a column separator.
    const cells = line.replace(/\\\|/g, '').replace(/^\s*\|/, '').replace(/\|\s*$/, '').split('|').length
    const isDivider = /^\s*\|[\s:|-]+\|\s*$/.test(line)
    if (!table) table = { start: i + 1, rows: [] }
    if (isDivider && table.rows.length === 1) return // separator row, not a data row
    table.rows.push({ line: i + 1, cells })
  })
  flushTable()
}

// ---- 3: the competitor table must carry its snapshot date -----------------
const comp = join(root, 'docs', 'competitor-analysis.md')
if (existsSync(comp)) {
  const text = readFileSync(comp, 'utf8')
  const dateRe = /快照口径[：:]\s*(\d{4}-\d{2}-\d{2})/
  const m = text.match(dateRe)
  if (!m) {
    fail('docs/competitor-analysis.md: no 快照口径：YYYY-MM-DD line — an undated comparison reads as current forever')
  } else {
    const lines = text.split('\n')
    const firstTable = lines.findIndex((l) => /^\s*\|/.test(l))
    const declaredAt = lines.slice(0, firstTable === -1 ? lines.length : firstTable).join('\n')
    if (!dateRe.test(declaredAt)) {
      fail('docs/competitor-analysis.md: the snapshot date must appear before the first table it governs')
    }
  }
}

// ---- 4: deploy.md §2.3 table vs the env list in Go -----------------------
const envGo = readFileSync(join(root, 'server/internal/config/env.go'), 'utf8')
// Only the KnownEnv block: TestOnlyEnv is read by the integration suite, not by
// a deployment, and ComposeEnv is documented where compose reads it.
const knownBlock = envGo.match(/var KnownEnv = \[\]string\{([\s\S]*?)\n\}/)
if (!knownBlock) fail('env.go: the KnownEnv block changed shape; update check-docs.mjs')
const known = [...(knownBlock ? knownBlock[1] : '').matchAll(/"(SKIN_[A-Z0-9_]+)"/g)].map((m) => m[1])
// Compose-only names belong in the table too (an operator has to know to set
// them), so they are accepted on the "documented but unread" side — but never
// demanded on the other side, since the binary genuinely does not read them.
const composeBlock = envGo.match(/var ComposeEnv = map\[string\]string\{([\s\S]*?)\n\}/)
if (!composeBlock) fail('env.go: the ComposeEnv block changed shape; update check-docs.mjs')
const composeOnly = [...(composeBlock ? composeBlock[1] : '').matchAll(/"(SKIN_[A-Z0-9_]+)"\s*:/g)].map((m) => m[1])
if (known.length < 35) fail(`env.go declares only ${known.length} SKIN_* names — the parser is blind`)

const deploy = readFileSync(join(root, 'docs', 'deploy.md'), 'utf8')
const documented = new Set()
for (const line of deploy.split('\n')) {
  const m = line.match(/^\s*\|\s*`(SKIN_[A-Z0-9_]+)`\s*\|/)
  if (m) documented.add(m[1])
}
if (documented.size < 30) fail(`deploy.md documents only ${documented.size} SKIN_* variables — the table or the parser changed shape`)

for (const name of known) {
  if (!documented.has(name)) fail(`deploy.md §2.3 does not document ${name}, which the binary reads`)
}
for (const name of documented) {
  if (!known.includes(name) && !composeOnly.includes(name)) {
    fail(`deploy.md documents ${name}, which no code reads (add it to env.go with its consumer, or drop the row)`)
  }
}

if (problems.length) {
  console.error('check-docs: FAILED')
  for (const p of problems) console.error('  - ' + p)
  process.exit(1)
}
console.log(`check-docs: ok (${docFiles.length} files, ${known.length} env vars documented both ways)`)
