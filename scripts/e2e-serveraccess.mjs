#!/usr/bin/env node
// Real-process E2E for the server-access invite-beta path.
// Builds the current binary, starts an isolated SQLite instance on loopback,
// seeds two tenants through the public APIs, then exercises authenticate → join
// → hasJoined for URL-secret and Header credentials. No production access.
import { spawn, spawnSync } from 'node:child_process'
import { mkdtempSync, rmSync } from 'node:fs'
import { tmpdir } from 'node:os'
import { join, resolve } from 'node:path'

const root = resolve(import.meta.dirname, '..')
const work = mkdtempSync(join(tmpdir(), 'skin-nexus-serveraccess-e2e-'))
const bin = join(work, process.platform === 'win32' ? 'skin-nexus.exe' : 'skin-nexus')
const port = 18081
const base = `http://127.0.0.1:${port}`
let child

function client() {
  const cookies = new Map()
  return {
    async request(path, { method = 'GET', body, headers = {} } = {}) {
      const res = await fetch(base + path, {
        method,
        headers: {
          'content-type': 'application/json',
          origin: base,
          ...headers,
          ...(cookies.size ? { cookie: [...cookies].map(([k, v]) => `${k}=${v}`).join('; ') } : {}),
        },
        body: body === undefined ? undefined : JSON.stringify(body),
      })
      for (const raw of res.headers.getSetCookie?.() ?? []) {
        const [pair] = raw.split(';')
        const i = pair.indexOf('=')
        if (i > 0) cookies.set(pair.slice(0, i), pair.slice(i + 1))
      }
      const text = await res.text()
      let json = null
      try { json = text ? JSON.parse(text) : null } catch {}
      return { status: res.status, json, text }
    },
  }
}

function assert(cond, message) {
  if (!cond) throw new Error(message)
}

async function waitReady() {
  for (let i = 0; i < 80; i++) {
    try {
      const res = await fetch(`${base}/api/v1/setup/status`)
      if (res.ok) return
    } catch {}
    await new Promise(r => setTimeout(r, 150))
  }
  throw new Error('server did not become ready')
}

try {
  const build = spawnSync('go', ['build', '-o', bin, './cmd/skin-nexus'], { cwd: join(root, 'server'), stdio: 'inherit' })
  assert(build.status === 0, 'go build failed')

  child = spawn(bin, [], {
    cwd: root,
    env: {
      ...process.env,
      SKIN_LISTEN: `127.0.0.1:${port}`,
      SKIN_BASE_URL: base,
      SKIN_DATABASE_DSN: `sqlite:${join(work, 'skin.db')}`,
      SKIN_DATA_DIR: join(work, 'data'),
      SKIN_MIGRATIONS_DIR: join(root, 'server', 'migrations'),
      SKIN_AUTO_MIGRATE: 'true',
      SKIN_SETUP_TOKEN: 'e2e-setup-token',
      SKIN_SERVERACCESS_ENABLED: 'true',
    },
    stdio: ['ignore', 'pipe', 'pipe'],
  })
  child.stdout.on('data', () => {})
  child.stderr.on('data', () => {})
  await waitReady()

  const admin = client(), ownerB = client(), player = client(), player2 = client()
  let r = await admin.request('/api/v1/setup', { method: 'POST', body: {
    site_name: 'SkinNexus E2E', site_url: base, nickname: 'platform_admin',
    email: 'admin@example.com', password: 'e2e-password-123', player_limit: 4,
    skin_domains: ['127.0.0.1'], setup_token: 'e2e-setup-token',
  } })
  assert(r.status === 200, `setup: ${r.status}`)
  for (const [c, nickname, email] of [[ownerB, 'owner_b', 'ownerb@example.com'], [player, 'player_a', 'playera@example.com'], [player2, 'player_b', 'playerb@example.com']]) {
    r = await c.request('/api/v1/register', { method: 'POST', body: { nickname, email, password: 'e2e-password-123' } })
    assert(r.status === 200 || r.status === 201, `register ${nickname}: ${r.status}`)
  }
  for (const [c, identifier] of [[admin, 'platform_admin'], [ownerB, 'owner_b'], [player, 'player_a'], [player2, 'player_b']]) {
    r = await c.request('/api/v1/login', { method: 'POST', body: { identifier, password: 'e2e-password-123' } })
    assert(r.status === 200, `login ${identifier}: ${r.status}`)
  }

  r = await admin.request('/api/admin/v1/serveraccess/tenants', { method: 'POST', body: { name: 'Server A', serverLimit: 5, ownerUid: 1 } })
  assert(r.status === 201, `tenant A: ${r.status}`)
  r = await admin.request('/api/admin/v1/serveraccess/tenants', { method: 'POST', body: { name: 'Server B', serverLimit: 5, ownerUid: 2 } })
  assert(r.status === 201, `tenant B: ${r.status}`)
  r = await admin.request('/api/serveraccess/v1/servers', { method: 'POST', body: { name: 'Survival A' } })
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

  const profiles = []
  for (const [c, name] of [[player, 'E2E_Player'], [player2, 'E2E_Player_Two']]) {
    r = await c.request('/api/v1/me/players', { method: 'POST', body: { name } })
    assert(r.status === 200 || r.status === 201, `player ${name}: ${r.status}`)
    profiles.push(r.json)
  }
  const [profile, profile2] = profiles
  for (const [c, id, fallback] of [[admin, serverA.id, false], [ownerB, serverB.id, true]]) {
    r = await c.request(`/api/serveraccess/v1/servers/${id}`, { method: 'PATCH', body: { policy: { whitelistEnabled: true, premiumFallbackEnabled: fallback } } })
    assert(r.status === 200, `policy ${id}: ${r.status}`)
  }
  for (const uid of [3, 4]) {
    r = await admin.request(`/api/serveraccess/v1/servers/${serverA.id}/whitelist`, { method: 'POST', body: { uid, note: `E2E allowed ${uid}` } })
    assert(r.status === 201 || r.status === 200, `whitelist A ${uid}: ${r.status}`)
  }

  const authenticate = async identifier => {
    const res = await fetch(`${base}/api/yggdrasil/authserver/authenticate`, { method: 'POST', headers: { 'content-type': 'application/json' }, body: JSON.stringify({ username: identifier, password: 'e2e-password-123', clientToken: `e2e-${identifier}` }) })
    assert(res.status === 200, `authenticate ${identifier}: ${res.status}`)
    return res.json()
  }
  const auth = await authenticate('player_a')
  const auth2 = await authenticate('player_b')
  const joinServer = async (token, selectedProfile, sid) => {
    const res = await fetch(`${base}/api/yggdrasil/sessionserver/session/minecraft/join`, { method: 'POST', headers: { 'content-type': 'application/json' }, body: JSON.stringify({ accessToken: token, selectedProfile: selectedProfile.uuid, serverId: sid }) })
    assert(res.status === 204, `join ${sid}: ${res.status}`)
  }
  const verifyA = async (selectedProfile, sid) => fetch(`${base}/v/${secretA}/sessionserver/session/minecraft/hasJoined?username=${selectedProfile.name}&serverId=${sid}&ip=127.0.0.1`)
  const verifyB = async (selectedProfile, sid) => fetch(`${base}/api/serveraccess/v1/servers/${serverB.publicId}/hasJoined?username=${selectedProfile.name}&serverId=${sid}&ip=127.0.0.1`, { headers: { authorization: `Bearer ${secretB}` } })

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

  r = await admin.request('/api/admin/v1/settings', { method: 'PUT', body: { key: 'serveraccess_enabled', value: '0' } })
  assert(r.status === 200, `module off: ${r.status}`)
  r = await fetch(`${base}/api/serveraccess/v1/health`)
  assert(r.status === 404, `module off health: ${r.status}`)
  r = await fetch(`${base}/api/yggdrasil/authserver/validate`, { method: 'POST', headers: { 'content-type': 'application/json' }, body: JSON.stringify({ accessToken: auth2.accessToken }) })
  assert(r.status === 204, `global Yggdrasil validate unavailable while module off: ${r.status}`)
  r = await admin.request('/api/admin/v1/settings', { method: 'PUT', body: { key: 'serveraccess_enabled', value: '1' } })
  assert(r.status === 200, `module on: ${r.status}`)
  r = await fetch(`${base}/api/serveraccess/v1/health`)
  assert(r.status === 200, `module on health: ${r.status}`)

  console.log('PASS serveraccess E2E: two tenants, two players, URL-secret + Header verification, whitelist and ban isolation, platform ban, full cross-tenant negatives, scoped events, credential redaction, module hot toggle, global Yggdrasil regression')
} finally {
  if (child && process.platform === 'win32') {
    spawnSync('taskkill', ['/PID', String(child.pid), '/T', '/F'], { stdio: 'ignore' })
    await new Promise(r => setTimeout(r, 800))
  } else if (child) {
    child.kill('SIGTERM')
  }
  rmSync(work, { recursive: true, force: true, maxRetries: 5, retryDelay: 200 })
}
