#!/usr/bin/env node
// Real-process E2E for the server-access invite-beta path.
// Builds the current binary, starts an isolated SQLite instance on loopback,
// seeds two tenants through the public APIs, then exercises authenticate → join
// → hasJoined for URL-secret and Header credentials. No production access.
import { spawn, spawnSync } from 'node:child_process'
import { createHash } from 'node:crypto'
import { once } from 'node:events'
import { mkdirSync, mkdtempSync, rmSync } from 'node:fs'
import { createServer } from 'node:net'
import { tmpdir } from 'node:os'
import { join, resolve, sep } from 'node:path'

const root = resolve(import.meta.dirname, '..')
const work = mkdtempSync(join(tmpdir(), 'skin-nexus-serveraccess-e2e-'))
const dbPath = join(work, 'skin.db')
const bin = join(work, process.platform === 'win32' ? 'skin-nexus.exe' : 'skin-nexus')
const logLimit = 64 * 1024
const stopTimeout = 5000
const sqliteTimeout = 15000
const texturePNG = Buffer.from('iVBORw0KGgoAAAANSUhEUgAAAEAAAAAgCAYAAACinX6EAAAAXElEQVR4nO3QMREAIBDAsJeIRIThBWRkoEP2Xmftc382OkBrgA7QGqADtAboAK0BOkBrgA7QGqADtAboAK0BOkBrgA7QGqADtAboAK0BOkBrgA7QGqADtAboAO0BhD9ZS/BowDYAAAAASUVORK5CYII=', 'base64')
let base
let port
let child
let childDone
const logs = {
  stdout: { chunks: [], bytes: 0, omitted: false },
  stderr: { chunks: [], bytes: 0, omitted: false },
}

let DatabaseSync
try { ({ DatabaseSync } = await import('node:sqlite')) } catch {}

const pythonSQLiteRunner = String.raw`
import json
import sqlite3
import sys

request = json.load(sys.stdin)
connection = sqlite3.connect(request["path"], timeout=5)
connection.execute("PRAGMA busy_timeout=5000")
try:
    if request["mode"] == "exec":
        before = connection.total_changes
        connection.execute("BEGIN IMMEDIATE")
        for statement in request["statements"]:
            connection.execute(statement["sql"], statement.get("params", []))
        connection.commit()
        result = connection.total_changes - before
    else:
        connection.row_factory = sqlite3.Row
        cursor = connection.execute(request["sql"], request.get("params", []))
        if request["mode"] == "all":
            result = [dict(row) for row in cursor.fetchall()]
        else:
            row = cursor.fetchone()
            result = dict(row) if row is not None else None
    print(json.dumps(result))
finally:
    connection.close()
`

function sqliteRequest(mode, payload) {
  assert(resolve(dbPath).startsWith(resolve(work) + sep), 'SQLite fixture escaped its disposable work directory')
  if (DatabaseSync) {
    const db = new DatabaseSync(dbPath)
    db.exec('PRAGMA busy_timeout=5000')
    let committed = false
    try {
      if (mode === 'exec') {
        db.exec('BEGIN IMMEDIATE')
        let changes = 0
        for (const statement of payload.statements) {
          changes += Number(db.prepare(statement.sql).run(...(statement.params ?? [])).changes)
        }
        db.exec('COMMIT')
        committed = true
        return changes
      }
      const statement = db.prepare(payload.sql)
      return (mode === 'all' ? statement.all(...(payload.params ?? [])) : statement.get(...(payload.params ?? []))) ?? null
    } finally {
      if (!committed && mode === 'exec') {
        try { db.exec('ROLLBACK') } catch {}
      }
      db.close()
    }
  }
  const python = process.platform === 'win32' ? 'python' : 'python3'
  const result = spawnSync(python, ['-c', pythonSQLiteRunner], {
    input: JSON.stringify({ ...payload, mode, path: dbPath }),
    encoding: 'utf8',
    stdio: ['pipe', 'pipe', 'pipe'],
    timeout: sqliteTimeout,
    maxBuffer: logLimit,
  })
  assert(!result.error, `disposable SQLite ${mode} failed: ${result.error?.message ?? result.stderr}`)
  assert(result.status === 0, `disposable SQLite ${mode} failed (${result.status}): ${result.stderr}`)
  try {
    return JSON.parse(result.stdout)
  } catch (error) {
    throw new Error(`disposable SQLite ${mode} returned invalid JSON: ${result.stdout}`, { cause: error })
  }
}

function sqliteExec(input) {
  const statements = (Array.isArray(input) ? input : [input]).map(statement => {
    assert(statement && typeof statement.sql === 'string' && (!statement.params || Array.isArray(statement.params)), 'invalid SQLite fixture statement')
    return { sql: statement.sql, params: statement.params ?? [] }
  })
  return sqliteRequest('exec', { statements })
}

function sqliteAll(sql, params = []) {
  return sqliteRequest('all', { sql, params })
}

function sqliteGet(sql, params = []) {
  return sqliteRequest('get', { sql, params })
}

function assert(cond, message) {
  if (!cond) throw new Error(message)
}

function fetchE2E(input, init = {}) {
  return fetch(input, { ...init, signal: init.signal ?? AbortSignal.timeout(10000) })
}

async function reserveLoopbackPort() {
  const server = createServer(socket => socket.destroy())
  let bound = false
  try {
    await new Promise((resolve, reject) => {
      server.once('error', reject)
      server.listen({ host: '127.0.0.1', port: 0, exclusive: true }, resolve)
    })
    bound = true
    const address = server.address()
    assert(address && typeof address === 'object' && address.address === '127.0.0.1', 'port reservation did not return a loopback address')
    return address.port
  } catch (error) {
    throw new Error(`cannot reserve an ephemeral loopback port for serveraccess E2E: ${error.message}`, { cause: error })
  } finally {
    if (bound) {
      await new Promise((resolve, reject) => server.close(error => {
        if (error) reject(new Error(`cannot release reserved loopback port: ${error.message}`, { cause: error }))
        else resolve()
      }))
    }
  }
}

function childEnvironment() {
  const temp = join(work, 'tmp')
  mkdirSync(temp, { recursive: true })
  const env = process.platform === 'win32'
    ? { SystemRoot: process.env.SystemRoot, TEMP: temp, TMP: temp }
    : { TMPDIR: temp }
  for (const [key, value] of Object.entries(env)) {
    if (value) env[key] = value
    else delete env[key]
  }
  return {
    ...env,
    TZ: 'UTC',
    SKIN_LISTEN: `127.0.0.1:${port}`,
    SKIN_BASE_URL: base,
    SKIN_DATABASE_DSN: `sqlite:${join(work, 'skin.db')}`,
    SKIN_DATA_DIR: join(work, 'data'),
    SKIN_MIGRATIONS_DIR: join(root, 'server', 'migrations'),
    SKIN_AUTO_MIGRATE: 'true',
    SKIN_SETUP_TOKEN: 'e2e-setup-token',
    SKIN_ADMIN_TOKEN: 'e2e-machine-token',
    SKIN_SERVERACCESS_ENABLED: 'true',
    SKIN_OIDC_ENABLED: 'false',
    SKIN_UNION_ENABLED: 'false',
    SKIN_INDEXNOW_ENABLED: 'false',
    SKIN_MOJANG_FALLBACK: '0',
    SKIN_PLUGIN_MARKET_URL: '',
    SKIN_THEME_MARKET_URL: '',
  }
}

function captureOutput(stream, name) {
  const log = logs[name]
  stream.on('data', chunk => {
    const buffer = Buffer.isBuffer(chunk) ? chunk : Buffer.from(chunk)
    log.chunks.push(buffer)
    log.bytes += buffer.length
    while (log.bytes > logLimit) {
      const excess = log.bytes - logLimit
      if (log.chunks.length === 1) {
        log.chunks[0] = log.chunks[0].subarray(excess)
        log.bytes -= excess
      } else {
        log.bytes -= log.chunks.shift().length
      }
      log.omitted = true
    }
  })
}

function printChildLogs() {
  for (const name of ['stdout', 'stderr']) {
    const log = logs[name]
    console.error(`--- child ${name} (last ${log.bytes} bytes${log.omitted ? '; earlier output omitted' : ''}) ---`)
    if (log.bytes) {
      process.stderr.write(Buffer.concat(log.chunks))
      if (log.chunks.at(-1).at(-1) !== 10) process.stderr.write('\n')
    }
  }
}

function assertChildRunning() {
  assert(child && child.pid && child.exitCode === null && child.signalCode === null,
    `server exited before becoming ready (code=${child?.exitCode}, signal=${child?.signalCode})`)
}

async function waitForChildExit(timeout) {
  let timer
  try {
    return await Promise.race([
      childDone.then(() => true),
      new Promise(resolve => { timer = setTimeout(() => resolve(false), timeout) }),
    ])
  } finally {
    clearTimeout(timer)
  }
}

function killWindowsTree() {
  const result = spawnSync('taskkill', ['/PID', String(child.pid), '/T', '/F'], { stdio: 'ignore', timeout: stopTimeout })
  if (result.error || result.status !== 0) child.kill('SIGKILL')
}

async function stopChild() {
  if (!child) return
  if (child.pid && child.exitCode === null && child.signalCode === null) {
    if (process.platform === 'win32') killWindowsTree()
    else child.kill('SIGTERM')
  }
  if (await waitForChildExit(stopTimeout)) return
  if (child.pid) {
    if (process.platform === 'win32') killWindowsTree()
    else child.kill('SIGKILL')
  }
  assert(await waitForChildExit(stopTimeout),
    `child process ${child.pid ?? '<unknown>'} did not exit within ${stopTimeout * 2}ms; temporary data retained at ${work}`)
}

async function startChild() {
  child = spawn(bin, [], {
    cwd: root,
    env: childEnvironment(),
    stdio: ['ignore', 'pipe', 'pipe'],
  })
  child.on('error', () => {})
  childDone = new Promise(resolve => child.once('close', resolve))
  captureOutput(child.stdout, 'stdout')
  captureOutput(child.stderr, 'stderr')
  await once(child, 'spawn')
  assertChildRunning()
  await waitReady()
}

// Task 20: exercise due deletion across a real restart without a clock hook.
async function restartChild() {
  await stopChild()
  await startChild()
}

function client() {
  const cookies = new Map()
  return {
    async request(path, { method = 'GET', body, headers = {} } = {}) {
      const isForm = body instanceof FormData
      const res = await fetchE2E(base + path, {
        method,
        headers: {
          ...(isForm ? {} : { 'content-type': 'application/json' }),
          origin: base,
          ...headers,
          ...(cookies.size ? { cookie: [...cookies].map(([k, v]) => `${k}=${v}`).join('; ') } : {}),
        },
        body: body === undefined ? undefined : isForm ? body : JSON.stringify(body),
      })
      for (const raw of res.headers.getSetCookie?.() ?? []) {
        const [pair] = raw.split(';')
        const i = pair.indexOf('=')
        if (i > 0) cookies.set(pair.slice(0, i), pair.slice(i + 1))
      }
      const text = await res.text()
      let json = null
      try { json = text ? JSON.parse(text) : null } catch {}
      return { status: res.status, json, text, headers: res.headers }
    },
  }
}

async function waitReady() {
  for (let i = 0; i < 80; i++) {
    assertChildRunning()
    try {
      const res = await fetchE2E(`${base}/api/v1/setup/status`, { signal: AbortSignal.timeout(1000) })
      assertChildRunning()
      if (res.ok) return
    } catch {
      assertChildRunning()
    }
    await new Promise(r => setTimeout(r, 150))
  }
  assertChildRunning()
  throw new Error('server did not become ready')
}

function expectStatus(response, status, label, code) {
  assert(response.status === status, `${label}: ${response.status} ${response.text}`)
  if (code) assert(response.json?.code === code, `${label} code: ${response.json?.code ?? '<missing>'} (${response.text})`)
}

function sameJSON(actual, expected, label) {
  assert(JSON.stringify(actual) === JSON.stringify(expected), `${label}: ${JSON.stringify(actual)} != ${JSON.stringify(expected)}`)
}

function tokenHash(token) {
  return createHash('sha256').update(token).digest('hex')
}

function insertMany(table, columns, rows) {
  assert(/^[a-z_]+$/.test(table) && columns.every(column => /^[a-z_]+$/.test(column)), 'invalid fixture identifier')
  assert(rows.length > 0 && rows.every(row => row.length === columns.length), `invalid ${table} fixture rows`)
  const placeholders = rows.map(() => `(${columns.map(() => '?').join(',')})`).join(',')
  return sqliteExec({
    sql: `INSERT INTO ${table} (${columns.join(',')}) VALUES ${placeholders}`,
    params: rows.flat(),
  })
}

function assertExport(response, tenantID) {
  expectStatus(response, 200, `tenant ${tenantID} export`)
  assert(response.headers.get('content-type') === 'application/json', `export ${tenantID} content-type: ${response.headers.get('content-type')}`)
  assert(response.headers.get('content-disposition') === `attachment; filename="serveraccess-tenant-${tenantID}.json"`,
    `export ${tenantID} disposition: ${response.headers.get('content-disposition')}`)
  assert(response.headers.get('x-content-type-options') === 'nosniff', `export ${tenantID} nosniff missing`)
  const document = response.json
  assert(document?.schemaVersion === 1 && document?.tenant?.id === tenantID && Array.isArray(document.servers), `export ${tenantID} document incomplete`)
  sameJSON(Object.keys(document).sort(), ['exportedAt', 'schemaVersion', 'servers', 'tenant'], `export ${tenantID} top-level fields`)
  return document
}

async function assertOwnerPage(client, path, key, firstID, label) {
  const defaults = await client.request(path)
  expectStatus(defaults, 200, `${label} default page`)
  assert(defaults.json.page === 1 && defaults.json.size === 50 && defaults.json.total === 201 && defaults.json[key].length === 50,
    `${label} default page metadata: ${defaults.text}`)
  if (key === 'servers') assert(defaults.json.quotaUsed === 201, `${label} default quotaUsed: ${defaults.json.quotaUsed}`)
  for (let i = 1; i < defaults.json[key].length; i++) {
    assert(defaults.json[key][i - 1].id > defaults.json[key][i].id, `${label} default page is not id DESC`)
  }

  const clamped = await client.request(`${path}?size=201`)
  expectStatus(clamped, 200, `${label} clamped page`)
  assert(clamped.json.page === 1 && clamped.json.size === 200 && clamped.json.total === 201 && clamped.json[key].length === 200,
    `${label} clamped page metadata: ${clamped.text}`)
  if (key === 'servers') assert(clamped.json.quotaUsed === 201, `${label} clamped quotaUsed: ${clamped.json.quotaUsed}`)

  const fifth = await client.request(`${path}?page=5`)
  expectStatus(fifth, 200, `${label} fifth page`)
  assert(fifth.json.page === 5 && fifth.json.size === 50 && fifth.json.total === 201 && fifth.json[key].length === 1,
    `${label} fifth page metadata: ${fifth.text}`)
  assert(fifth.json[key][0].id === firstID, `${label} fifth page first id: ${fifth.json[key][0].id} != ${firstID}`)
  if (key === 'servers') assert(fifth.json.quotaUsed === 201, `${label} fifth-page quotaUsed: ${fifth.json.quotaUsed}`)
}

function globalIdentitySnapshot() {
  const queries = {
    users: 'SELECT id,nickname,email,email_verified,permission FROM users ORDER BY id',
    players: 'SELECT id,uid,name,tid_skin,tid_cape FROM players ORDER BY id',
    uuid_map: 'SELECT name,uuid,player_id FROM uuid_map ORDER BY name',
    textures: 'SELECT id,uid,type,hash,model FROM textures ORDER BY id',
  }
  return Object.fromEntries(Object.entries(queries).map(([table, sql]) => [table, sqliteAll(sql)]))
}

let failed = false
try {
  const build = spawnSync('go', ['build', '-o', bin, './cmd/skin-nexus'], { cwd: join(root, 'server'), stdio: 'inherit' })
  assert(build.status === 0, 'go build failed')

  port = await reserveLoopbackPort()
  base = `http://127.0.0.1:${port}`
  await startChild()

  const admin = client(), ownerB = client(), player = client(), player2 = client()
  const invitee = client(), tenantAdmin = client(), viewer = client(), superadmin = client(), machine = client()
  let r = await admin.request('/api/v1/setup', { method: 'POST', body: {
    site_name: 'SkinNexus E2E', site_url: base, nickname: 'platform_admin',
    email: 'admin@example.com', password: 'e2e-password-123', player_limit: 4,
    skin_domains: ['127.0.0.1'], setup_token: 'e2e-setup-token',
  } })
  assert(r.status === 200, `setup: ${r.status}`)
  r = await admin.request('/api/v1/login', { method: 'POST', body: { identifier: 'platform_admin', password: 'e2e-password-123' } })
  assert(r.status === 200, `initial admin login: ${r.status}`)
  r = await admin.request('/api/admin/v1/settings', { method: 'PUT', body: { key: 'regs_per_ip', value: '0' } })
  assert(r.status === 200, `disable disposable per-IP registration cap: ${r.status} ${r.text}`)
  const accounts = [
    [ownerB, 'owner_b', 'ownerb@example.com'],
    [player, 'player_a', 'playera@example.com'],
    [player2, 'player_b', 'playerb@example.com'],
    [invitee, 'invitee', 'invitee@example.com'],
    [tenantAdmin, 'tenant_admin', 'tenantadmin@example.com'],
    [viewer, 'tenant_viewer', 'viewer@example.com'],
    [superadmin, 'platform_super', 'super@example.com'],
  ]
  for (const [c, nickname, email] of accounts) {
    r = await c.request('/api/v1/register', { method: 'POST', body: { nickname, email, password: 'e2e-password-123' } })
    assert(r.status === 200 || r.status === 201, `register ${nickname}: ${r.status}`)
  }
  for (const [c, identifier] of [[admin, 'platform_admin'], ...accounts.map(([c, nickname]) => [c, nickname])]) {
    r = await c.request('/api/v1/login', { method: 'POST', body: { identifier, password: 'e2e-password-123' } })
    assert(r.status === 200, `login ${identifier}: ${r.status}`)
  }

  r = await admin.request('/api/admin/v1/serveraccess/tenants', { method: 'POST', body: { name: 'Tenant %_ A', serverLimit: 250, ownerUid: 1 } })
  assert(r.status === 201, `tenant A: ${r.status}`)
  const tenantA = r.json.tenant
  r = await admin.request('/api/admin/v1/serveraccess/tenants', { method: 'POST', body: { name: 'Server B', serverLimit: 5, ownerUid: 2 } })
  assert(r.status === 201, `tenant B: ${r.status}`)
  const tenantB = r.json.tenant
  r = await admin.request('/api/serveraccess/v1/servers', { method: 'POST', body: { name: 'Survival %_ A' } })
  assert(r.status === 201, `server A: ${r.status}`)
  const serverA = r.json
  r = await ownerB.request('/api/serveraccess/v1/servers', { method: 'POST', body: { name: 'Creative B' } })
  assert(r.status === 201, `server B: ${r.status}`)
  const serverB = r.json

  r = await admin.request(`/api/serveraccess/v1/servers/${serverA.id}/credentials`, { method: 'POST', body: { mode: 'url_secret' } })
  assert(r.status === 201, `credential A: ${r.status}`)
  const secretA = r.json.secret
  r = await ownerB.request(`/api/serveraccess/v1/servers/${serverB.id}/credentials`, { method: 'POST', body: { mode: 'header' } })
  assert(r.status === 201, `credential B: ${r.status}`)
  const secretB = r.json.secret
  assert(secretA && secretB, 'credential secrets missing')

  const inviteAccount = async (email, role) => {
    const created = await admin.request('/api/serveraccess/v1/tenants/me/members/invite', { method: 'POST', body: { email, role } })
    expectStatus(created, 201, `invite ${email}`)
    assert(created.json.token && created.json.id > 0, `invite ${email} token missing`)
    return created.json
  }
  const acceptInvite = async (account, token) => account.request('/api/serveraccess/v1/invites/accept', { method: 'POST', body: { token } })
  const verifyAccount = async email => {
    const user = sqliteGet('SELECT id FROM users WHERE email=?', [email])
    assert(user?.id > 0, `fixture user ${email} missing`)
    assert(sqliteExec({ sql: 'UPDATE users SET email_verified=1 WHERE id=?', params: [user.id] }) === 1, `verify fixture user ${email}`)
    return user.id
  }

  const unverifiedInvite = await inviteAccount('invitee@example.com', 'viewer')
  r = await acceptInvite(invitee, unverifiedInvite.token)
  expectStatus(r, 403, 'unverified invite acceptance', 'auth.email_verify_required')
  await verifyAccount('invitee@example.com')
  r = await acceptInvite(invitee, unverifiedInvite.token)
  expectStatus(r, 200, 'verified invite acceptance')
  assert(r.json.tenantId === tenantA.id, `verified invite tenant: ${r.json.tenantId}`)
  r = await acceptInvite(invitee, unverifiedInvite.token)
  expectStatus(r, 404, 'invite replay', 'serveraccess.invite_invalid')
  r = await acceptInvite(invitee, 'not-a-valid-invite-token')
  expectStatus(r, 404, 'invalid invite', 'serveraccess.invite_invalid')

  const adminInvite = await inviteAccount('tenantadmin@example.com', 'admin')
  await verifyAccount('tenantadmin@example.com')
  r = await acceptInvite(tenantAdmin, adminInvite.token)
  expectStatus(r, 200, 'tenant admin invite acceptance')
  assert(r.json.tenantId === tenantA.id, `tenant admin invite tenant: ${r.json.tenantId}`)
  const viewerInvite = await inviteAccount('viewer@example.com', 'viewer')
  await verifyAccount('viewer@example.com')
  r = await acceptInvite(viewer, viewerInvite.token)
  expectStatus(r, 200, 'viewer invite acceptance')
  assert(r.json.tenantId === tenantA.id, `viewer invite tenant: ${r.json.tenantId}`)

  const superUser = sqliteGet('SELECT id FROM users WHERE email=?', ['super@example.com'])
  assert(superUser?.id > 0, 'platform superadmin fixture user missing')
  assert(sqliteExec({ sql: 'UPDATE users SET permission=2 WHERE id=?', params: [superUser.id] }) === 1, 'promote platform superadmin fixture')

  for (const path of [
    '/api/serveraccess/v1/servers',
    `/api/serveraccess/v1/servers/${serverA.id}`,
    `/api/serveraccess/v1/servers/${serverA.id}/whitelist`,
    `/api/serveraccess/v1/servers/${serverA.id}/bans`,
    `/api/serveraccess/v1/servers/${serverA.id}/credentials`,
    `/api/serveraccess/v1/servers/${serverA.id}/events`,
    '/api/serveraccess/v1/tenants/me/members',
  ]) {
    r = await viewer.request(path)
    expectStatus(r, 200, `viewer read ${path}`)
  }
  for (const [method, path, body] of [
    ['PATCH', `/api/serveraccess/v1/servers/${serverA.id}`, { name: 'viewer must not write' }],
    ['POST', `/api/serveraccess/v1/servers/${serverA.id}/whitelist`, { uid: 999999 }],
    ['DELETE', `/api/serveraccess/v1/servers/${serverA.id}/whitelist/3`, undefined],
    ['POST', '/api/serveraccess/v1/tenants/me/members/invite', { email: 'blocked@example.com', role: 'viewer' }],
    ['POST', '/api/serveraccess/v1/tenants/me/delete-request', { confirmation: tenantA.name }],
  ]) {
    r = await viewer.request(path, { method, body })
    expectStatus(r, 403, `viewer write ${method} ${path}`, 'serveraccess.forbidden')
  }
  r = await viewer.request('/api/serveraccess/v1/tenants/me/invites')
  expectStatus(r, 403, 'viewer invite list', 'serveraccess.forbidden')
  r = await viewer.request('/api/serveraccess/v1/tenants/me/export')
  expectStatus(r, 403, 'viewer export', 'serveraccess.forbidden')
  r = await viewer.request('/api/serveraccess/v1/players/lookup?q=E2E_Player')
  expectStatus(r, 403, 'viewer player lookup', 'serveraccess.forbidden')

  r = await tenantAdmin.request('/api/serveraccess/v1/servers')
  expectStatus(r, 200, 'tenant admin server list')
  r = await tenantAdmin.request(`/api/serveraccess/v1/servers/${serverA.id}`, { method: 'PATCH', body: { name: 'Role Matrix %_ A' } })
  expectStatus(r, 200, 'tenant admin server update')
  assert(r.json.id === serverA.id && r.json.name === 'Role Matrix %_ A' && r.json.status === 'active', `tenant admin update body: ${r.text}`)
  r = await tenantAdmin.request('/api/serveraccess/v1/tenants/me/members/invite', { method: 'POST', body: { email: 'admin-blocked@example.com', role: 'viewer' } })
  expectStatus(r, 403, 'tenant admin invite management', 'serveraccess.forbidden')
  r = await tenantAdmin.request('/api/serveraccess/v1/tenants/me/delete-request', { method: 'POST', body: { confirmation: tenantA.name } })
  expectStatus(r, 403, 'tenant admin deletion request', 'serveraccess.forbidden')

  r = await superadmin.request('/api/admin/v1/serveraccess/tenants')
  expectStatus(r, 200, 'platform superadmin tenant list')
  r = await superadmin.request('/api/admin/v1/serveraccess/fallback')
  expectStatus(r, 200, 'platform superadmin fallback read')
  const fallbackBefore = r.json.enabled
  r = await superadmin.request('/api/admin/v1/serveraccess/fallback', { method: 'PUT', body: { enabled: !fallbackBefore } })
  expectStatus(r, 200, 'platform superadmin fallback update')
  assert(r.json.enabled === !fallbackBefore, `platform fallback update body: ${r.text}`)
  r = await superadmin.request('/api/admin/v1/serveraccess/fallback', { method: 'PUT', body: { enabled: fallbackBefore } })
  expectStatus(r, 200, 'platform superadmin fallback restore')
  for (const [account, label] of [[viewer, 'viewer'], [tenantAdmin, 'tenant admin'], [ownerB, 'other tenant owner']]) {
    r = await account.request('/api/admin/v1/serveraccess/tenants')
    expectStatus(r, 403, `${label} platform list`, 'admin.super_admin_required')
  }
  r = await machine.request('/api/admin/v1/users', { headers: { 'x-admin-token': 'e2e-machine-token' } })
  expectStatus(r, 200, 'machine token accepted by ordinary admin API')
  r = await machine.request('/api/admin/v1/serveraccess/tenants', { headers: { 'x-admin-token': 'e2e-machine-token' } })
  expectStatus(r, 403, 'machine token rejected by serveraccess platform API', 'admin.web_session_required')

  for (const suffix of ['', '/']) {
    r = await fetchE2E(`${base}/v/${secretA}${suffix}`, { redirect: 'manual' })
    assert(r.status === 200 && r.headers.get('x-authlib-injector-api-location') === `${base}/v/${secretA}`, `authlib metadata ${suffix || 'no slash'}: ${r.status}`)
    const metadata = await r.json()
    assert(metadata.meta?.implementationName === 'SkinNexus' && metadata.signaturePublickey, 'authlib metadata document incomplete')
    assert(!JSON.stringify(metadata).includes(secretA), 'authlib metadata leaked the URL secret')
  }
  r = await fetchE2E(`${base}/v/${secretB}/`, { headers: { authorization: `Bearer ${secretB}` }, redirect: 'manual' })
  assert(r.status === 404, `header credential on authlib metadata: ${r.status}`)
  r = await admin.request('/v/unknown-only-secret-0123456789abcdef/')
  assert(r.status === 404, `unknown authlib metadata: ${r.status}`)

  const profiles = []
  for (const [c, name] of [[player, 'E2E_Player'], [player2, 'E2E_Player_Two']]) {
    r = await c.request('/api/v1/me/players', { method: 'POST', body: { name } })
    assert(r.status === 200 || r.status === 201, `player ${name}: ${r.status}`)
    profiles.push(r.json)
  }
  const [profile, profile2] = profiles
  r = await tenantAdmin.request('/api/serveraccess/v1/players/lookup?q=E2E_Player')
  expectStatus(r, 200, 'tenant admin player lookup')
  assert(r.json.uid === 3 && r.json.name === 'E2E_Player' && /^[0-9a-f]{32}$/.test(r.json.uuid), `tenant admin lookup body: ${r.text}`)
  for (const [c, id, fallback] of [[admin, serverA.id, false], [ownerB, serverB.id, true]]) {
    r = await c.request(`/api/serveraccess/v1/servers/${id}`, { method: 'PATCH', body: { policy: { whitelistEnabled: true, premiumFallbackEnabled: fallback } } })
    assert(r.status === 200, `policy ${id}: ${r.status}`)
  }
  for (const uid of [3, 4]) {
    r = await admin.request(`/api/serveraccess/v1/servers/${serverA.id}/whitelist`, { method: 'POST', body: { uid, note: `E2E allowed ${uid}` } })
    assert(r.status === 201 || r.status === 200, `whitelist A ${uid}: ${r.status}`)
  }

  const authenticate = async identifier => {
    const res = await fetchE2E(`${base}/api/yggdrasil/authserver/authenticate`, { method: 'POST', headers: { 'content-type': 'application/json' }, body: JSON.stringify({ username: identifier, password: 'e2e-password-123', clientToken: `e2e-${identifier}` }) })
    assert(res.status === 200, `authenticate ${identifier}: ${res.status}`)
    return res.json()
  }
  const auth = await authenticate('player_a')
  const auth2 = await authenticate('player_b')
  const joinServer = async (token, selectedProfile, sid) => {
    const res = await fetchE2E(`${base}/api/yggdrasil/sessionserver/session/minecraft/join`, { method: 'POST', headers: { 'content-type': 'application/json' }, body: JSON.stringify({ accessToken: token, selectedProfile: selectedProfile.uuid, serverId: sid }) })
    assert(res.status === 204, `join ${sid}: ${res.status}`)
  }
  const verifyA = async (selectedProfile, sid) => fetchE2E(`${base}/v/${secretA}/sessionserver/session/minecraft/hasJoined?username=${selectedProfile.name}&serverId=${sid}&ip=127.0.0.1`)
  const verifyB = async (selectedProfile, sid) => fetchE2E(`${base}/api/serveraccess/v1/servers/${serverB.publicId}/hasJoined?username=${selectedProfile.name}&serverId=${sid}&ip=127.0.0.1`, { headers: { authorization: `Bearer ${secretB}` } })

  await joinServer(auth.accessToken, profile, 'e2e-a1')
  r = await verifyA(profile, 'e2e-a1')
  assert(r.status === 200, `A player 1 allow: ${r.status}`)
  await joinServer(auth2.accessToken, profile2, 'e2e-a2')
  r = await verifyA(profile2, 'e2e-a2')
  assert(r.status === 200, `A player 2 allow: ${r.status}`)

  await joinServer(auth.accessToken, profile, 'e2e-b-deny')
  r = await verifyB(profile, 'e2e-b-deny')
  assert(r.status === 204, `B deny before whitelist: ${r.status}`)
  for (const uid of [3, 4]) {
    r = await ownerB.request(`/api/serveraccess/v1/servers/${serverB.id}/whitelist`, { method: 'POST', body: { uid, note: `E2E allowed B ${uid}` } })
    assert(r.status === 201 || r.status === 200, `whitelist B ${uid}: ${r.status}`)
  }
  await joinServer(auth.accessToken, profile, 'e2e-b-allow')
  r = await verifyB(profile, 'e2e-b-allow')
  assert(r.status === 200, `B player 1 allow: ${r.status}`)

  r = await ownerB.request(`/api/serveraccess/v1/servers/${serverB.id}/bans`, { method: 'POST', body: { uid: 4, reason: 'E2E tenant ban' } })
  assert(r.status === 201 || r.status === 200, `tenant ban B: ${r.status}`)
  await joinServer(auth2.accessToken, profile2, 'e2e-b-banned')
  r = await verifyB(profile2, 'e2e-b-banned')
  assert(r.status === 204, `B tenant ban: ${r.status}`)
  await joinServer(auth2.accessToken, profile2, 'e2e-a-unaffected')
  r = await verifyA(profile2, 'e2e-a-unaffected')
  assert(r.status === 200, `A unaffected by B ban: ${r.status}`)
  r = await ownerB.request(`/api/serveraccess/v1/servers/${serverB.id}/bans/4`, { method: 'DELETE' })
  assert(r.status === 200, `remove tenant ban B: ${r.status}`)

  await joinServer(auth.accessToken, profile, 'e2e-a-platform-ban')
  await joinServer(auth.accessToken, profile, 'e2e-b-platform-ban')
  r = await admin.request('/api/admin/v1/users/3/permission', { method: 'PUT', body: { permission: -1 } })
  assert(r.status === 200, `platform ban: ${r.status} ${r.text}`)
  r = await verifyA(profile, 'e2e-a-platform-ban')
  assert(r.status === 204, `A platform ban: ${r.status}`)
  r = await verifyB(profile, 'e2e-b-platform-ban')
  assert(r.status === 204, `B platform ban: ${r.status}`)

  r = await admin.request(`/api/serveraccess/v1/servers/${serverA.id}/credentials`)
  assert(r.status === 200, `credential A list: ${r.status}`)
  const credentialAId = r.json.credentials[0].id
  assert(credentialAId > 0, 'credential A id missing')
  const crossTenant = [
    ['GET', `/api/serveraccess/v1/servers/${serverA.id}`],
    ['PATCH', `/api/serveraccess/v1/servers/${serverA.id}`, { name: 'cross tenant' }],
    ['POST', `/api/serveraccess/v1/servers/${serverA.id}/whitelist`, { uid: 4 }],
    ['DELETE', `/api/serveraccess/v1/servers/${serverA.id}/whitelist/4`],
    ['POST', `/api/serveraccess/v1/servers/${serverA.id}/credentials`, { mode: 'url_secret' }],
    ['POST', `/api/serveraccess/v1/servers/${serverA.id}/credentials/${credentialAId}/rotate`, { mode: 'url_secret' }],
    ['POST', `/api/serveraccess/v1/servers/${serverA.id}/credentials/${credentialAId}/revoke`, {}],
    ['GET', `/api/serveraccess/v1/servers/${serverA.id}/export`],
  ]
  for (const [method, path, body] of crossTenant) {
    r = await ownerB.request(path, { method, body })
    assert(r.status === 404, `cross-tenant ${method} ${path}: ${r.status}`)
  }
  r = await admin.request(`/api/serveraccess/v1/servers/${serverA.id}/events`)
  const eventsA = r.json.events ?? []
  r = await ownerB.request(`/api/serveraccess/v1/servers/${serverB.id}/events`)
  const eventsB = r.json.events ?? []
  assert(eventsA.some(e => e.outcome === 'allow' && e.sourceMode === 'url_secret'), 'A event missing')
  assert(eventsA.some(e => e.outcome === 'deny' && e.reasonCode === 'platform_banned'), 'A platform ban event missing')
  assert(eventsB.some(e => e.outcome === 'deny' && e.reasonCode === 'not_whitelisted'), 'B deny event missing')
  assert(eventsB.some(e => e.outcome === 'deny' && e.reasonCode === 'tenant_banned'), 'B tenant ban event missing')
  assert(eventsB.some(e => e.outcome === 'deny' && e.reasonCode === 'platform_banned'), 'B platform ban event missing')
  assert(eventsB.some(e => e.outcome === 'allow' && e.sourceMode === 'header'), 'B allow event missing')

  r = await admin.request(`/api/serveraccess/v1/servers/${serverA.id}/credentials`)
  const credentialText = JSON.stringify(r.json)
  assert(!credentialText.includes('secretHash') && !credentialText.includes('"secret"'), 'credential list leaked secret material')

  r = await admin.request('/api/serveraccess/v1/tenants/me/export')
  const exportA = assertExport(r, tenantA.id)
  assert(exportA.tenant.name === tenantA.name && exportA.tenant.serverLimit === 250 && exportA.servers.length === 1, `tenant A export scope: ${r.text}`)
  assert(exportA.servers[0].id === serverA.id && exportA.servers[0].name === 'Role Matrix %_ A', `tenant A export server: ${r.text}`)
  r = await tenantAdmin.request('/api/serveraccess/v1/tenants/me/export')
  const exportAdminA = assertExport(r, tenantA.id)
  sameJSON(exportAdminA.tenant, exportA.tenant, 'tenant admin export tenant')
  r = await ownerB.request('/api/serveraccess/v1/tenants/me/export')
  const exportB = assertExport(r, tenantB.id)
  assert(exportB.tenant.name === tenantB.name && exportB.servers.length === 1 && exportB.servers[0].id === serverB.id, `tenant B export scope: ${r.text}`)
  const exportAText = JSON.stringify(exportA)
  const exportBText = JSON.stringify(exportB)
  for (const foreign of [tenantB.name, serverB.name, serverB.publicId, secretB]) {
    assert(!exportAText.includes(foreign), `tenant A export leaked tenant B content: ${foreign}`)
  }
  for (const foreign of [tenantA.name, 'Role Matrix %_ A', serverA.publicId, secretA]) {
    assert(!exportBText.includes(foreign), `tenant B export leaked tenant A content: ${foreign}`)
  }
  for (const document of [exportA, exportB]) {
    assert(!exportAText.includes('secretHash') && !exportBText.includes('secretHash'), 'export leaked credential hash')
    for (const server of document.servers) {
      for (const event of server.events) {
        sameJSON(Object.keys(event).sort(), ['createdAt', 'durationMs', 'id', 'outcome', 'reasonCode', 'sourceMode'], 'export event fields')
      }
      for (const credential of server.credentials) {
        sameJSON(Object.keys(credential).sort(), ['createdAt', 'expiresAt', 'id', 'lastUsedAt', 'mode', 'status'], 'export credential fields')
      }
    }
  }

  const upload = new FormData()
  upload.set('type', 'skin')
  upload.set('model', 'default')
  upload.set('description', 'E2E global texture retained after tenant deletion')
  upload.set('file', new Blob([texturePNG], { type: 'image/png' }), 'e2e-skin.png')
  r = await player2.request('/api/v1/me/textures', { method: 'POST', body: upload })
  assert(r.status === 200 || r.status === 201, `global texture upload: ${r.status} ${r.text}`)
  assert(r.json.id > 0 && /^[0-9a-f]{64}$/.test(r.json.hash), `global texture upload body: ${r.text}`)

  r = await admin.request('/api/admin/v1/settings', { method: 'PUT', body: { key: 'serveraccess_enabled', value: '0' } })
  assert(r.status === 200, `module off: ${r.status}`)
  r = await fetchE2E(`${base}/api/serveraccess/v1/health`)
  assert(r.status === 404, `module off health: ${r.status}`)
  r = await fetchE2E(`${base}/v/${secretA}/`)
  assert(r.status === 404, `module off authlib metadata: ${r.status}`)
  r = await fetchE2E(`${base}/api/yggdrasil/authserver/validate`, { method: 'POST', headers: { 'content-type': 'application/json' }, body: JSON.stringify({ accessToken: auth2.accessToken }) })
  assert(r.status === 204, `global Yggdrasil validate unavailable while module off: ${r.status}`)
  r = await admin.request('/api/admin/v1/settings', { method: 'PUT', body: { key: 'serveraccess_enabled', value: '1' } })
  assert(r.status === 200, `module on: ${r.status}`)
  r = await fetchE2E(`${base}/api/serveraccess/v1/health`)
  assert(r.status === 200, `module on health: ${r.status}`)

  const tenantFixture = sqliteGet('SELECT id,name,server_limit FROM sa_tenants WHERE id=?', [tenantA.id])
  assert(tenantFixture?.server_limit === 250, `tenant A fixture quota: ${JSON.stringify(tenantFixture)}`)
  const serverRows = Array.from({ length: 200 }, (_, i) => [tenantA.id, `srv_e2e_page_${i}`, `Pagination ${String(i).padStart(3, '0')}`, 'active', 'url_secret'])
  assert(insertMany('sa_servers', ['tenant_id', 'public_id', 'name', 'status', 'source_mode'], serverRows) === 200, 'insert pagination servers')
  const fixtureServers = sqliteAll('SELECT id FROM sa_servers WHERE tenant_id=? AND id<>? ORDER BY id', [tenantA.id, serverA.id])
  assert(fixtureServers.length === 200, `pagination server fixture count: ${fixtureServers.length}`)
  assert(insertMany('sa_server_policies', ['server_id'], fixtureServers.map(row => [row.id])) === 200, 'insert pagination policies')

  const inviteRows = Array.from({ length: 198 }, (_, i) => [
    tenantA.id, `page-${i}@example.com`, 'viewer', tokenHash(`bulk-invite-${i}`), '2099-01-01T00:00:00Z',
  ])
  assert(insertMany('sa_tenant_invites', ['tenant_id', 'email', 'role', 'token_hash', 'expires_at'], inviteRows) === 198, 'insert pagination invites')
  const whitelistRows = Array.from({ length: 199 }, (_, i) => [serverA.id, 10000 + i, `page-whitelist-${i}`, 1])
  assert(insertMany('sa_server_whitelist', ['server_id', 'uid', 'note', 'created_by'], whitelistRows) === 199, 'insert pagination whitelist')
  const banRows = Array.from({ length: 201 }, (_, i) => [serverA.id, 20000 + i, `page-ban-${i}`, 1])
  assert(insertMany('sa_server_bans', ['server_id', 'uid', 'reason', 'created_by'], banRows) === 201, 'insert pagination bans')

  for (const [label, sql, params] of [
    ['servers', 'SELECT COUNT(*) AS count FROM sa_servers WHERE tenant_id=?', [tenantA.id]],
    ['invites', 'SELECT COUNT(*) AS count FROM sa_tenant_invites WHERE tenant_id=?', [tenantA.id]],
    ['whitelist', 'SELECT COUNT(*) AS count FROM sa_server_whitelist WHERE server_id=?', [serverA.id]],
    ['bans', 'SELECT COUNT(*) AS count FROM sa_server_bans WHERE server_id=?', [serverA.id]],
  ]) {
    assert(sqliteGet(sql, params)?.count === 201, `${label} fixture total: ${JSON.stringify(sqliteGet(sql, params))}`)
  }
  const firstServerID = sqliteGet('SELECT id FROM sa_servers WHERE tenant_id=? ORDER BY id LIMIT 1', [tenantA.id]).id
  const firstInviteID = sqliteGet('SELECT id FROM sa_tenant_invites WHERE tenant_id=? ORDER BY id LIMIT 1', [tenantA.id]).id
  const firstWhitelistID = sqliteGet('SELECT id FROM sa_server_whitelist WHERE server_id=? ORDER BY id LIMIT 1', [serverA.id]).id
  const firstBanID = sqliteGet('SELECT id FROM sa_server_bans WHERE server_id=? ORDER BY id LIMIT 1', [serverA.id]).id
  await assertOwnerPage(admin, '/api/serveraccess/v1/servers', 'servers', firstServerID, 'servers')
  await assertOwnerPage(admin, '/api/serveraccess/v1/tenants/me/invites', 'invites', firstInviteID, 'invites')
  await assertOwnerPage(admin, `/api/serveraccess/v1/servers/${serverA.id}/whitelist`, 'whitelist', firstWhitelistID, 'whitelist')
  await assertOwnerPage(admin, `/api/serveraccess/v1/servers/${serverA.id}/bans`, 'bans', firstBanID, 'bans')
  r = await admin.request('/api/serveraccess/v1/servers?status=active&page=5')
  expectStatus(r, 200, 'servers filtered fifth page')
  assert(r.json.total === 201 && r.json.quotaUsed === 201 && r.json.page === 5 && r.json.servers.length === 1, `servers filtered quota metadata: ${r.text}`)

  for (const [query, expectedID] of [['%', tenantA.id], ['_', tenantA.id]]) {
    r = await superadmin.request(`/api/admin/v1/serveraccess/tenants?q=${encodeURIComponent(query)}`)
    expectStatus(r, 200, `literal tenant search ${query}`)
    assert(r.json.total === 1 && r.json.tenants.length === 1 && r.json.tenants[0].id === expectedID, `literal tenant search ${query}: ${r.text}`)
  }
  for (const [query, expectedID] of [['%', serverA.id], ['_', serverA.id]]) {
    r = await superadmin.request(`/api/admin/v1/serveraccess/servers?q=${encodeURIComponent(query)}`)
    expectStatus(r, 200, `literal server search ${query}`)
    assert(r.json.total === 1 && r.json.servers.length === 1 && r.json.servers[0].id === expectedID, `literal server search ${query}: ${r.text}`)
  }

  const identityBeforeDelete = globalIdentitySnapshot()
  const wrongConfirmations = ['', tenantA.name.slice(0, -1), tenantA.name.toLowerCase(), tenantA.name.toUpperCase(), `${tenantA.name} `]
  for (const confirmation of wrongConfirmations) {
    r = await admin.request('/api/serveraccess/v1/tenants/me/delete-request', { method: 'POST', body: { confirmation } })
    expectStatus(r, 400, 'delete request requires exact tenant name', 'serveraccess.invalid')
  }
  r = await admin.request('/api/serveraccess/v1/tenants/me')
  expectStatus(r, 200, 'tenant active before exact delete request')
  assert(r.json.tenant.status === 'active' && r.json.tenant.deletionDueAt === null, `tenant changed after mismatched delete request: ${r.text}`)
  r = await admin.request('/api/serveraccess/v1/tenants/me/delete-request', { method: 'POST', body: { confirmation: tenantA.name } })
  expectStatus(r, 200, 'exact tenant delete request')
  assert(r.json.status === 'deleting' && r.json.deletionRequestedAt && r.json.deletionDueAt, `delete request body: ${r.text}`)
  const deletionDueAt = r.json.deletionDueAt
  const deletionRequestedAt = r.json.deletionRequestedAt
  assert(new Date(deletionDueAt) - new Date(deletionRequestedAt) === 7 * 24 * 60 * 60 * 1000, `deletion grace period: ${deletionRequestedAt} -> ${deletionDueAt}`)
  r = await admin.request('/api/serveraccess/v1/tenants/me/delete-request', { method: 'POST', body: { confirmation: tenantA.name } })
  expectStatus(r, 200, 'exact delete-request replay')
  assert(r.json.deletionDueAt === deletionDueAt && r.json.deletionRequestedAt === deletionRequestedAt, `delete-request replay extended deadline: ${r.text}`)
  r = await admin.request('/api/serveraccess/v1/tenants/me/delete-request', { method: 'POST', body: { confirmation: `${tenantA.name} ` } })
  expectStatus(r, 400, 'delete-request replay requires exact name', 'serveraccess.invalid')
  r = await admin.request('/api/serveraccess/v1/tenants/me')
  expectStatus(r, 403, 'deleting tenant owner membership read', 'serveraccess.forbidden')
  r = await admin.request('/api/serveraccess/v1/servers')
  expectStatus(r, 403, 'deleting tenant owner server list', 'serveraccess.forbidden')
  r = await fetchE2E(`${base}/v/${secretA}/`)
  assert(r.status === 404, `deleting tenant credential metadata: ${r.status}`)

  r = await superadmin.request(`/api/admin/v1/serveraccess/servers?q=${encodeURIComponent('Role Matrix %_ A')}`)
  expectStatus(r, 200, 'platform suspended server read')
  assert(r.json.total === 1 && r.json.servers[0].id === serverA.id && r.json.servers[0].status === 'suspended', `suspended server body: ${r.text}`)
  assert(r.json.servers[0].credentials.length === 1 && r.json.servers[0].credentials[0].status === 'revoked', `revoked credential body: ${r.text}`)

  r = await admin.request('/api/serveraccess/v1/tenants/me/export')
  const graceExport = assertExport(r, tenantA.id)
  assert(graceExport.tenant.status === 'deleting' && graceExport.tenant.deletionRequestedAt === deletionRequestedAt && graceExport.tenant.deletionDueAt === deletionDueAt,
    `grace export lifecycle: ${r.text}`)
  assert(graceExport.servers.length === 201 && graceExport.servers.every(server => server.status === 'suspended'), 'grace export did not suspend every server')
  const graceCredentials = graceExport.servers.flatMap(server => server.credentials)
  assert(graceCredentials.length === 1 && graceCredentials.every(credential => credential.status === 'revoked'), 'grace export did not revoke credentials')

  const deletePath = `/api/admin/v1/serveraccess/tenants/${tenantA.id}/delete`
  r = await superadmin.request(deletePath, { method: 'POST', body: { confirmation: tenantA.name } })
  expectStatus(r, 409, 'early physical tenant delete', 'serveraccess.conflict')
  for (const confirmation of wrongConfirmations) {
    r = await superadmin.request(deletePath, { method: 'POST', body: { confirmation } })
    expectStatus(r, 400, 'early physical delete exact-name check', 'serveraccess.invalid')
  }
  r = await superadmin.request(`/api/admin/v1/serveraccess/tenants?q=${encodeURIComponent(tenantA.name)}`)
  expectStatus(r, 200, 'tenant remains before due delete')
  assert(r.json.total === 1 && r.json.tenants[0].status === 'deleting', `tenant disappeared before due delete: ${r.text}`)

  const lifecycleBefore = sqliteGet('SELECT * FROM sa_tenants WHERE id=?', [tenantA.id])
  assert(lifecycleBefore?.status === 'deleting' && lifecycleBefore.deletion_due_at, 'lifecycle fixture row missing before due update')
  await stopChild()
  const pastDue = '2000-01-01 00:00:00.000 +0000 UTC'
  assert(sqliteExec({ sql: 'UPDATE sa_tenants SET deletion_due_at=? WHERE id=?', params: [pastDue, tenantA.id] }) === 1, 'advance disposable deletion_due_at')
  const lifecycleAfter = sqliteGet('SELECT * FROM sa_tenants WHERE id=?', [tenantA.id])
  assert(lifecycleAfter?.deletion_due_at === pastDue, `deletion_due_at fixture update: ${JSON.stringify(lifecycleAfter)}`)
  for (const key of Object.keys(lifecycleBefore)) {
    if (key !== 'deletion_due_at') sameJSON(lifecycleAfter[key], lifecycleBefore[key], `lifecycle row ${key} changed outside deletion_due_at`)
  }
  await restartChild()

  for (const confirmation of wrongConfirmations) {
    r = await superadmin.request(deletePath, { method: 'POST', body: { confirmation } })
    expectStatus(r, 400, 'due physical delete exact-name check', 'serveraccess.invalid')
  }
  r = await superadmin.request(`/api/admin/v1/serveraccess/tenants?q=${encodeURIComponent(tenantA.name)}`)
  expectStatus(r, 200, 'due tenant remains after mismatched delete')
  assert(r.json.total === 1 && r.json.tenants[0].id === tenantA.id, `mismatched due delete removed tenant: ${r.text}`)
  r = await superadmin.request(deletePath, { method: 'POST', body: { confirmation: tenantA.name } })
  expectStatus(r, 200, 'due exact-name physical tenant delete')
  assert(r.json.deleted === true, `due delete body: ${r.text}`)
  r = await superadmin.request(deletePath, { method: 'POST', body: { confirmation: tenantA.name } })
  expectStatus(r, 404, 'physical delete replay', 'serveraccess.not_found')
  r = await superadmin.request(`/api/admin/v1/serveraccess/tenants?q=${encodeURIComponent(tenantA.name)}`)
  expectStatus(r, 200, 'platform tenant list after delete')
  assert(r.json.total === 0 && r.json.tenants.length === 0, `deleted tenant still listed: ${r.text}`)
  r = await admin.request('/api/serveraccess/v1/tenants/me')
  expectStatus(r, 403, 'deleted tenant owner membership', 'serveraccess.forbidden')
  r = await ownerB.request('/api/serveraccess/v1/tenants/me')
  expectStatus(r, 200, 'other tenant survives physical delete')
  assert(r.json.tenantId === tenantB.id && r.json.tenant.name === tenantB.name, `other tenant after delete: ${r.text}`)

  for (const [label, sql, params] of [
    ['tenant', 'SELECT COUNT(*) AS count FROM sa_tenants WHERE id=?', [tenantA.id]],
    ['members', 'SELECT COUNT(*) AS count FROM sa_tenant_members WHERE tenant_id=?', [tenantA.id]],
    ['invites', 'SELECT COUNT(*) AS count FROM sa_tenant_invites WHERE tenant_id=?', [tenantA.id]],
    ['servers', 'SELECT COUNT(*) AS count FROM sa_servers WHERE tenant_id=?', [tenantA.id]],
    ['policies', 'SELECT COUNT(*) AS count FROM sa_server_policies WHERE server_id IN (SELECT id FROM sa_servers WHERE tenant_id=?)', [tenantA.id]],
    ['credentials', 'SELECT COUNT(*) AS count FROM sa_server_credentials WHERE server_id IN (SELECT id FROM sa_servers WHERE tenant_id=?)', [tenantA.id]],
    ['whitelist', 'SELECT COUNT(*) AS count FROM sa_server_whitelist WHERE server_id IN (SELECT id FROM sa_servers WHERE tenant_id=?)', [tenantA.id]],
    ['bans', 'SELECT COUNT(*) AS count FROM sa_server_bans WHERE server_id IN (SELECT id FROM sa_servers WHERE tenant_id=?)', [tenantA.id]],
    ['events', 'SELECT COUNT(*) AS count FROM sa_join_events WHERE tenant_id=?', [tenantA.id]],
    ['quota usage', 'SELECT COUNT(*) AS count FROM sa_quota_usage WHERE server_id IN (SELECT id FROM sa_servers WHERE tenant_id=?)', [tenantA.id]],
  ]) {
    assert(sqliteGet(sql, params)?.count === 0, `${label} rows survived tenant delete: ${JSON.stringify(sqliteGet(sql, params))}`)
  }
  assert(sqliteGet('SELECT COUNT(*) AS count FROM sa_tenants WHERE id=?', [tenantB.id])?.count === 1, 'tenant B row removed')
  assert(sqliteGet('SELECT COUNT(*) AS count FROM sa_servers WHERE tenant_id=?', [tenantB.id])?.count === 1, 'tenant B server removed')
  sameJSON(globalIdentitySnapshot(), identityBeforeDelete, 'global users/players/uuid_map/textures changed after tenant delete')

  await joinServer(auth2.accessToken, profile2, 'e2e-b-after-delete')
  r = await verifyB(profile2, 'e2e-b-after-delete')
  assert(r.status === 200, `tenant B verification after tenant A delete: ${r.status}`)
  r = await fetchE2E(`${base}/api/yggdrasil/authserver/validate`, { method: 'POST', headers: { 'content-type': 'application/json' }, body: JSON.stringify({ accessToken: auth2.accessToken }) })
  assert(r.status === 204, `global Yggdrasil validate after tenant delete: ${r.status}`)

} catch (error) {
  failed = true
  process.exitCode = 1
  console.error(`serveraccess E2E failed: ${error.stack ?? error}`)
  printChildLogs()
} finally {
  try {
    await stopChild()
    rmSync(work, { recursive: true, force: true, maxRetries: 5, retryDelay: 200 })
  } catch (error) {
    failed = true
    process.exitCode = 1
    console.error(`serveraccess E2E cleanup failed: ${error.stack ?? error}`)
    printChildLogs()
  }
}

if (!failed) {
  console.log('PASS serveraccess E2E: invite lifecycle and role matrix, 201-row pagination/quota and literal platform search, tenant export attachment/isolation, exact-name deletion with disposable due fixture and global identity preservation, authlib metadata + URL-secret/Header verification, whitelist/ban/event isolation, credential redaction, module hot toggle')
}
