import test from 'node:test'
import assert from 'node:assert/strict'
import { useServerTenant } from './useServerTenant.js'
import { useServerCatalog } from './useServerCatalog.js'
import { useServerWhitelist } from './useServerWhitelist.js'
import { useServerBans } from './useServerBans.js'
import { useServerEvents } from './useServerEvents.js'
import { useServerCredentials } from './useServerCredentials.js'
import { buildServerQuotaPatch, isTenantDeletionDue, useServerAccessPlatform } from './useServerAccessPlatform.js'

const tenant = { id: 1, name: 'Example tenant', status: 'active', serverLimit: 5 }
const server = { id: 9, publicId: 'srv_example', name: 'Example server', status: 'active', sourceMode: 'url_secret' }

test('deletion due gating accepts only the exact deadline or later for a deleting tenant', () => {
  const due = '2027-01-31T00:00:00Z'
  const row = { status: 'deleting', deletionDueAt: due }
  const atDeadline = Date.parse(due)
  assert.equal(isTenantDeletionDue(row, atDeadline - 1), false)
  assert.equal(isTenantDeletionDue(row, atDeadline), true)
  assert.equal(isTenantDeletionDue(row, atDeadline + 1), true)
  assert.equal(isTenantDeletionDue({ ...row, status: 'active' }, atDeadline), false)
  assert.equal(isTenantDeletionDue({ ...row, deletionDueAt: null }, atDeadline), false)
  assert.equal(isTenantDeletionDue({ ...row, deletionDueAt: 'invalid' }, atDeadline), false)
})

test('tenant portal loads membership and invitation state without hiding failures', async () => {
  const portal = useServerTenant({
    tenantMe: async () => ({ tenant, role: 'viewer' }),
    members: async () => ({ members: [{ uid: 7, role: 'viewer', nickname: 'Example viewer' }] }),
    invites: async () => ({ invites: [] }),
  })
  await portal.load()
  assert.equal(portal.tenant.value.id, 1)
  assert.equal(portal.role.value, 'viewer')
  assert.equal(portal.members.value.length, 1)
  assert.equal(portal.loading.value, false)

  const failed = useServerTenant({
    tenantMe: async () => { throw new Error('offline') },
    members: async () => ({ members: [] }),
    invites: async () => ({ invites: [] }),
  })
  await failed.load()
  assert.equal(failed.error.value, 'offline')
  assert.equal(failed.tenant.value, null)
})

test('viewer portal never requests owner-only invitations', async () => {
  let inviteCalls = 0
  const portal = useServerTenant({
    tenantMe: async () => ({ tenant, role: 'viewer' }),
    members: async () => ({ members: [] }),
    invites: async () => { inviteCalls++; throw new Error('must not be called') },
  })
  assert.equal(await portal.load(), true)
  assert.equal(inviteCalls, 0)
  assert.deepEqual(portal.invites.value, [])
})

test('server catalog selects scoped detail and creates through the owner client', async () => {
  const calls = []
  const catalog = useServerCatalog({
    servers: async () => ({ servers: [server] }),
    server: async (id) => { calls.push(id); return { server: { ...server, name: 'Loaded' }, policy: { whitelistEnabled: true } } },
    createServer: async (name) => ({ ...server, id: 10, name }),
    updateServer: async () => ({ id: 9, name: 'Renamed', status: 'disabled' }),
  })
  await catalog.load()
  await catalog.select(9)
  assert.deepEqual(calls, [9])
  assert.equal(catalog.selected.value.server.name, 'Loaded')
  assert.equal(catalog.policy.value.whitelistEnabled, true)
  await catalog.create('Created')
  assert.equal(catalog.servers.value.find((item) => item.id === 10).name, 'Created')
  await catalog.update(9, { name: 'Renamed', status: 'disabled' })
  assert.equal(catalog.selected.value.server.status, 'disabled')
  await catalog.update(9, { policy: { whitelistEnabled: false, premiumFallbackEnabled: true } })
  assert.equal(catalog.policy.value.whitelistEnabled, false)
  assert.equal(catalog.policy.value.premiumFallbackEnabled, true)
  assert.equal(catalog.selected.value.policy.whitelistEnabled, false)
})

test('whitelist resolves identity then refreshes real rows after add and remove', async () => {
  let rows = []
  const panel = useServerWhitelist({
    whitelist: async () => ({ whitelist: rows }),
    addWhitelist: async (_id, uid) => { rows = [{ id: 1, uid, note: 'added' }] },
    removeWhitelist: async () => { rows = [] },
  })
  await panel.load(9)
  assert.deepEqual(panel.rows.value, [])
  await panel.add(9, { uid: 7, name: 'ExamplePlayer', uuid: '0123456789abcdef0123456789abcdef' }, 'added')
  assert.equal(panel.rows.value[0].uid, 7)
  panel.clearResolution()
  assert.equal(panel.resolved.value, null)
  await panel.remove(9, 7)
  assert.deepEqual(panel.rows.value, [])
})

test('whitelist clears the previous server and ignores a late player lookup', async () => {
  let releaseLookup
  let releaseRows
  const panel = useServerWhitelist({
    whitelist: async (id) => {
      if (id === 9) return { whitelist: [{ id: 1, uid: 7 }] }
      return new Promise((resolve) => { releaseRows = resolve })
    },
    lookupPlayer: async () => new Promise((resolve) => { releaseLookup = resolve }),
  })
  await panel.load(9)
  const lookup = panel.lookup('ExamplePlayer')
  panel.clearResolution()
  const loading = panel.load(10)
  assert.deepEqual(panel.rows.value, [])
  releaseLookup({ uid: 7, name: 'ExamplePlayer', uuid: '0123456789abcdef0123456789abcdef' })
  assert.equal(await lookup, null)
  assert.equal(panel.resolved.value, null)
  releaseRows({ whitelist: [{ id: 2, uid: 8 }] })
  await loading
  assert.deepEqual(panel.rows.value.map((row) => row.uid), [8])
})

test('ban workflow sends expiry and reason and keeps server errors visible', async () => {
  let payload
  const panel = useServerBans({
    bans: async () => ({ bans: [] }),
    addBan: async (_id, body) => { payload = body },
    removeBan: async () => {},
  })
  await panel.load(9)
  await panel.add(9, { uid: 7, reason: 'Example reason', expiresAt: '2027-01-01T00:00:00Z' })
  assert.deepEqual(payload, { uid: 7, reason: 'Example reason', expiresAt: '2027-01-01T00:00:00Z' })

  const failed = useServerBans({
    bans: async () => { throw new Error('quota') },
    addBan: async () => {}, removeBan: async () => {},
  })
  await failed.load(9)
  assert.equal(failed.error.value, 'quota')
})

test('event cursor appends distinct rows and cannot request the same page twice in flight', async () => {
  const cursors = []
  let release
  const panel = useServerEvents({
    events: async (_id, options) => {
      cursors.push(options.before || 0)
      if (cursors.length === 2) await new Promise((resolve) => { release = resolve })
      if (cursors.length === 1) return { events: [{ id: 5 }, { id: 4 }], nextBefore: 4, hasMore: true }
      return { events: [{ id: 3 }], nextBefore: 0, hasMore: false }
    },
  })
  await panel.load(9)
  const pending = panel.more(9)
  const duplicate = panel.more(9)
  assert.deepEqual(cursors, [0, 4])
  release()
  await pending
  assert.equal(await duplicate, false)
  await panel.more(9)
  assert.deepEqual(cursors, [0, 4])
  assert.deepEqual(panel.rows.value.map((row) => row.id), [5, 4, 3])
  assert.equal(panel.hasMore.value, false)
})

test('platform view loads quotas and audit and applies emergency changes', async () => {
  const writes = []
  const platform = useServerAccessPlatform({
    platformTenants: async () => ({ tenants: [tenant], total: 1, page: 1, size: 20 }),
    platformServers: async () => ({ servers: [server], total: 1, page: 1, size: 20 }),
    platformFallback: async () => ({ enabled: true }),
    platformAudit: async () => ({ audit: [{ id: 1, action: 'serveraccess_server_status_update' }], total: 1, page: 1, size: 20 }),
    updatePlatformTenant: async (_id, body) => { writes.push(['tenant', body]); return { id: 1 } },
    updatePlatformServer: async (_id, body) => { writes.push(['server', body]); return { id: 9 } },
    setPlatformFallback: async (enabled) => { writes.push(['fallback', enabled]); return { enabled } },
    revokePlatformCredential: async (_id, _cid) => { writes.push(['revoke']); return { revoked: true } },
  })
  await platform.load()
  assert.equal(platform.tenants.value[0].serverLimit, 5)
  assert.equal(platform.fallback.value, true)
  assert.equal(platform.audit.value[0].action, 'serveraccess_server_status_update')
  await platform.updateTenant(1, { status: 'suspended' })
  await platform.updateServer(9, { status: 'active' })
  await platform.setFallback(false)
  await platform.revokeCredential(9, 4)
  assert.deepEqual(writes, [
    ['tenant', { status: 'suspended' }], ['server', { status: 'active' }], ['fallback', false], ['revoke'],
  ])
})

test('platform credential revocation refreshes the server summaries used by selection', async () => {
  let serverReads = 0
  let auditReads = 0
  let rows = [{ ...server, credentials: [{ id: 4, mode: 'header', status: 'active', lastUsedAt: null, expiresAt: null }] }]
  const platform = useServerAccessPlatform({
    platformTenants: async () => ({ tenants: [], total: 0 }),
    platformServers: async () => { serverReads++; return { servers: rows, total: rows.length } },
    platformFallback: async () => ({ enabled: true }),
    platformAudit: async () => { auditReads++; return { audit: [], total: 0 } },
    revokePlatformCredential: async (_id, credentialId) => {
      rows = rows.map((row) => ({ ...row, credentials: row.credentials.map((item) => item.id === credentialId ? { ...item, status: 'revoked' } : item) }))
      return { revoked: true }
    },
  })
  await platform.load()
  assert.equal(await platform.revokeCredential(9, 4), true)
  assert.equal(serverReads, 2)
  assert.equal(auditReads, 2)
  assert.equal(platform.servers.value[0].credentials[0].status, 'revoked')
})

test('server quota patch sends only entered positive integer fields', () => {
  assert.deepEqual(buildServerQuotaPatch({ verifyRateLimit: '12', maxConcurrent: '', eventRetentionDays: 30 }), {
    patch: { verifyRateLimit: 12, eventRetentionDays: 30 }, valid: true,
  })
  assert.deepEqual(buildServerQuotaPatch({}), { patch: {}, valid: true })
  assert.deepEqual(buildServerQuotaPatch({ verifyBurst: 0 }), { patch: {}, valid: false })
  assert.deepEqual(buildServerQuotaPatch({ whitelistLimit: 1.5 }), { patch: {}, valid: false })
  assert.deepEqual(buildServerQuotaPatch({ maxConcurrent: 1000001 }), { patch: {}, valid: false })
})

test('discarding a secret while credential creation is pending prevents late publication', async () => {
  let release
  const panel = useServerCredentials({
    credentials: async () => ({ credentials: [] }),
    createCredential: async () => new Promise((resolve) => { release = resolve }),
  })
  const pending = panel.create(9, 'url_secret')
  panel.clearSecret()
  release({ secret: 'late-secret' })
  assert.equal(await pending, null)
  assert.equal(panel.secret.value, '')
})

test('clearing the credential panel drops stale metadata and late secrets', async () => {
  let release
  const panel = useServerCredentials({
    credentials: async () => ({ credentials: [{ id: 1, status: 'active' }] }),
    createCredential: async () => new Promise((resolve) => { release = resolve }),
  })
  await panel.load(9)
  const pending = panel.create(9, 'url_secret')
  panel.clear()
  assert.deepEqual(panel.credentials.value, [])
  release({ secret: 'discarded-secret' })
  assert.equal(await pending, null)
  assert.equal(panel.secret.value, '')
})

test('discarding a secret during the metadata refresh keeps it hidden', async () => {
  let release
  const panel = useServerCredentials({
    credentials: async () => new Promise((resolve) => { release = resolve }),
    createCredential: async () => ({ secret: 'refresh-secret' }),
  })
  const pending = panel.create(9, 'url_secret')
  await new Promise((resolve) => setTimeout(resolve, 0))
  panel.clearSecret()
  release({ credentials: [] })
  assert.equal(await pending, null)
  assert.equal(panel.secret.value, '')
})

test('a late revoke cannot replace the newly selected server credential list', async () => {
  let releaseRevoke
  const credentialReads = []
  const panel = useServerCredentials({
    credentials: async (id) => {
      credentialReads.push(id)
      return { credentials: [{ id, status: 'active' }] }
    },
    revokeCredential: async () => new Promise((resolve) => { releaseRevoke = resolve }),
  })
  await panel.load(9)
  const revoking = panel.revoke(9, 1)
  await panel.load(10)
  releaseRevoke({ revoked: true })
  assert.equal(await revoking, false)
  assert.deepEqual(credentialReads, [9, 10])
  assert.deepEqual(panel.credentials.value.map((row) => row.id), [10])
})

test('credential secret is available once and refreshes or revoke mask it', async () => {
  let secret = 'first-secret'
  const panel = useServerCredentials({
    credentials: async () => ({ credentials: [{ id: 1, status: 'active' }] }),
    createCredential: async () => ({ credential: { id: 2, status: 'active' }, secret }),
    rotateCredential: async () => {
      secret = 'rotated-secret'
      return { credential: { id: 3, status: 'active' }, secret }
    },
    revokeCredential: async () => ({ revoked: true }),
  })
  await panel.load(9)
  assert.equal(panel.secret.value, '')
  await panel.create(9, 'url_secret')
  assert.equal(panel.secret.value, 'first-secret')
  panel.clearSecret()
  assert.equal(panel.secret.value, '')
  await panel.rotate(9, 2, 'url_secret')
  assert.equal(panel.secret.value, 'rotated-secret')
  await panel.revoke(9, 3)
  assert.equal(panel.secret.value, '')
  await panel.load(9)
  assert.equal(panel.secret.value, '')
})
