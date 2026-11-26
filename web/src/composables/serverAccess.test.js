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

function deferred() {
  let resolve
  let reject
  const promise = new Promise((res, rej) => { resolve = res; reject = rej })
  return { promise, resolve, reject }
}

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

test('accepting an invitation exposes busy state and reloads the newly joined tenant', async () => {
  let joined = false
  let releaseAccept
  let tenantReads = 0
  let memberReads = 0
  const acceptedTokens = []
  const portal = useServerTenant({
    tenantMe: async () => {
      tenantReads++
      if (!joined) throw Object.assign(new Error('forbidden'), { status: 403 })
      return { tenant, role: 'viewer' }
    },
    members: async () => { memberReads++; return { members: [{ uid: 7, role: 'viewer' }] } },
    invites: async () => { throw new Error('viewer must not load owner invitations') },
    acceptInvite: (token) => {
      acceptedTokens.push(token)
      return new Promise((resolve) => { releaseAccept = resolve })
    },
  })
  assert.equal(await portal.load(), false)
  assert.equal(portal.forbidden.value, true)

  const pending = portal.acceptInvite('pasted-invite-token')
  assert.equal(portal.busy.value, true)
  assert.equal(portal.error.value, '')
  joined = true
  releaseAccept({ tenantId: 1 })
  assert.deepEqual(await pending, { tenantId: 1 })
  assert.deepEqual(acceptedTokens, ['pasted-invite-token'])
  assert.equal(tenantReads, 2)
  assert.equal(memberReads, 1)
  assert.equal(portal.tenant.value.id, 1)
  assert.equal(portal.role.value, 'viewer')
  assert.equal(portal.forbidden.value, false)
  assert.equal(portal.busy.value, false)
  assert.equal(portal.error.value, '')
})

test('invitation acceptance failures stay visible without reloading or joining a tenant', async () => {
  for (const cause of [
    Object.assign(new Error('The invitation is invalid, expired, or already used'), { status: 404 }),
    Object.assign(new Error('Verify this email address first'), { status: 403 }),
    Object.assign(new Error('The invitation is invalid, expired, or already used'), { status: 404 }),
  ]) {
    let tenantReads = 0
    const portal = useServerTenant({
      tenantMe: async () => { tenantReads++; throw Object.assign(new Error('forbidden'), { status: 403 }) },
      members: async () => ({ members: [] }),
      acceptInvite: async () => { throw cause },
    })
    assert.equal(await portal.load(), false)
    assert.equal(await portal.acceptInvite('pasted-invite-token'), null)
    assert.equal(tenantReads, 1)
    assert.equal(portal.error.value, cause.message)
    assert.equal(portal.forbidden.value, true)
    assert.equal(portal.tenant.value, null)
    assert.equal(portal.busy.value, false)
  }
})

test('invitation acceptance does not report success when its tenant reload loses the session', async () => {
  const portal = useServerTenant({
    tenantMe: async () => { throw Object.assign(new Error('session lost'), { status: 401 }) },
    members: async () => ({ members: [] }),
    acceptInvite: async () => ({ tenantId: 1 }),
  })
  assert.equal(await portal.acceptInvite('pasted-invite-token'), null)
  assert.equal(portal.tenant.value, null)
  assert.equal(portal.error.value, '')
  assert.equal(portal.busy.value, false)
})

test('tenant 401 clears loaded state and marks the load unsuccessful', async () => {
  let cause
  const portal = useServerTenant({
    tenantMe: async () => {
      if (!cause) return { tenant, role: 'owner' }
      throw cause
    },
    members: async () => ({ members: [{ uid: 7 }] }),
    invites: async () => ({ invites: [{ id: 1 }], total: 1 }),
  })
  await portal.load()
  portal.pageInvites(3)
  cause = Object.assign(new Error('session lost'), { status: 401 })
  assert.equal(await portal.load(), false)
  assert.equal(portal.tenant.value, null)
  assert.equal(portal.role.value, '')
  assert.deepEqual(portal.members.value, [])
  assert.deepEqual(portal.invites.value, [])
  assert.equal(portal.total.value, 0)
  assert.equal(portal.page.value, 1)
  assert.equal(portal.loading.value, false)
  assert.equal(portal.error.value, '')
  assert.equal(portal.unavailable.value, false)
  assert.equal(portal.forbidden.value, false)
})

test('tenant mutation 401 clears state and invalidates an in-flight load', async () => {
  let hold = false
  let release
  let failMutation = false
  const portal = useServerTenant({
    tenantMe: async () => {
      if (hold) return new Promise((resolve) => { release = resolve })
      return { tenant, role: 'owner' }
    },
    members: async () => ({ members: [{ uid: 7 }] }),
    invites: async () => ({ invites: [{ id: 1 }], total: 1 }),
    removeMember: async () => {
      if (!failMutation) return { removed: true }
      throw Object.assign(new Error('session lost'), { status: 401 })
    },
  })
  await portal.load()
  hold = true
  const pending = portal.load()
  failMutation = true
  assert.equal(await portal.removeMember(7), null)
  assert.equal(portal.tenant.value, null)
  assert.deepEqual(portal.members.value, [])
  assert.equal(portal.busy.value, false)
  release({ tenant: { ...tenant, name: 'Stale tenant' }, role: 'owner' })
  assert.equal(await pending, false)
  assert.equal(portal.tenant.value, null)
})

test('tenant session loss invalidates an in-flight mutation without reloading', async () => {
  let releaseMutation
  let failLoad = false
  let tenantCalls = 0
  const portal = useServerTenant({
    tenantMe: async () => {
      tenantCalls += 1
      if (failLoad) throw Object.assign(new Error('session lost'), { status: 401 })
      return { tenant, role: 'owner' }
    },
    members: async () => ({ members: [{ uid: 7 }] }),
    invites: async () => ({ invites: [{ id: 1 }], total: 1 }),
    removeMember: async () => new Promise((resolve) => { releaseMutation = resolve }),
  })
  await portal.load()
  const pendingMutation = portal.removeMember(7)
  failLoad = true
  assert.equal(await portal.load(), false)
  const callsAfterLoss = tenantCalls
  releaseMutation({ removed: true })
  assert.equal(await pendingMutation, null)
  assert.equal(tenantCalls, callsAfterLoss)
  assert.equal(portal.tenant.value, null)
  assert.deepEqual(portal.members.value, [])
  assert.equal(portal.busy.value, false)
})

test('a stale mutation cannot clear the busy state of a post-session-loss mutation', async () => {
  let failLoad = false
  let releaseOld
  let releaseNew
  let removeCalls = 0
  const portal = useServerTenant({
    tenantMe: async () => {
      if (failLoad) throw Object.assign(new Error('session lost'), { status: 401 })
      return { tenant, role: 'owner' }
    },
    members: async () => ({ members: [{ uid: 7 }] }),
    invites: async () => ({ invites: [], total: 0 }),
    removeMember: async () => {
      removeCalls += 1
      return new Promise((resolve) => { if (removeCalls === 1) releaseOld = resolve; else releaseNew = resolve })
    },
  })
  await portal.load()
  const oldMutation = portal.removeMember(7)
  failLoad = true
  assert.equal(await portal.load(), false)
  failLoad = false
  const newMutation = portal.removeMember(7)
  assert.equal(portal.busy.value, true)
  releaseOld({ removed: true })
  assert.equal(await oldMutation, null)
  assert.equal(portal.busy.value, true)
  releaseNew({ removed: true })
  assert.deepEqual(await newMutation, { removed: true })
  assert.equal(portal.busy.value, false)
})

test('tenant 403, 404, network, and 5xx failures preserve loaded tenant data', async () => {
  for (const cause of [
    Object.assign(new Error('forbidden'), { status: 403 }),
    Object.assign(new Error('missing'), { status: 404 }),
    new Error('offline'),
    Object.assign(new Error('server error'), { status: 500 }),
  ]) {
    let fail = false
    const portal = useServerTenant({
      tenantMe: async () => {
        if (fail) throw cause
        return { tenant, role: 'viewer' }
      },
      members: async () => ({ members: [{ uid: 7 }] }),
      invites: async () => ({ invites: [], total: 0 }),
    })
    await portal.load()
    fail = true
    assert.equal(await portal.load(), false)
    assert.equal(portal.tenant.value.id, 1)
    assert.equal(portal.role.value, 'viewer')
    assert.equal(portal.members.value.length, 1)
    assert.equal(portal.error.value, cause.message)
    assert.equal(portal.forbidden.value, cause.status === 403)
    assert.equal(portal.unavailable.value, cause.status === 404)
  }
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
  let createdServer = null
  const catalog = useServerCatalog({
    servers: async () => ({ servers: createdServer ? [server, createdServer] : [server], total: createdServer ? 2 : 1 }),
    server: async (id) => { calls.push(id); return { server: { ...server, name: 'Loaded' }, policy: { whitelistEnabled: true } } },
    createServer: async (name) => (createdServer = { ...server, id: 10, name }),
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

test('server catalog keeps pagination metadata and quota separate from the visible page', async () => {
  const calls = []
  const catalog = useServerCatalog({
    servers: async (...args) => {
      calls.push(args)
      return { servers: [server], total: 51, quotaUsed: 7, page: args[0], size: args[1] }
    },
  })
  await catalog.load()
  assert.deepEqual(calls, [[1, 50, '']])
  assert.equal(catalog.page.value, 1)
  assert.equal(catalog.size.value, 50)
  assert.equal(catalog.total.value, 51)
  assert.equal(catalog.quotaUsed.value, 7)
  await catalog.goPage(2)
  assert.deepEqual(calls[1], [2, 50, ''])
  await catalog.filter('disabled')
  assert.deepEqual(calls[2], [1, 50, 'disabled'])
  assert.equal(catalog.page.value, 1)
})

test('server catalog ignores an older page response', async () => {
  let releaseOld
  const catalog = useServerCatalog({
    servers: async (page) => {
      if (page === 2) return new Promise((resolve) => { releaseOld = resolve })
      return { servers: [{ ...server, id: 10 }], total: 51, quotaUsed: 7, page: 1, size: 50 }
    },
  })
  const oldPage = catalog.goPage(2)
  await catalog.goPage(1)
  releaseOld({ servers: [server], total: 51, quotaUsed: 7, page: 2, size: 50 })
  assert.equal(await oldPage, false)
  assert.deepEqual(catalog.servers.value.map((row) => row.id), [10])
  assert.equal(catalog.page.value, 1)
})

test('server catalog keeps the selected server when another page is viewed', async () => {
  const catalog = useServerCatalog({
    servers: async (page) => ({ servers: [{ ...server, id: page }], total: 51, quotaUsed: 7, page, size: 50 }),
    server: async (id) => ({ server: { ...server, id }, policy: {} }),
  })
  await catalog.load()
  await catalog.select(1)
  await catalog.goPage(2)
  assert.equal(catalog.selected.value.server.id, 1)
})

test('server catalog create invalidates an older load and adopts canonical quota without double counting', async () => {
  const staleLoad = deferred()
  const canonicalLoad = deferred()
  const pendingCreate = deferred()
  const calls = []
  const created = { ...server, id: 10, name: 'Created' }
  const catalog = useServerCatalog({
    servers: async (...args) => {
      calls.push(args)
      if (calls.length === 1) return { servers: [{ ...server, id: 1 }], total: 1, quotaUsed: 1, page: 1, size: 50 }
      if (calls.length === 2) return staleLoad.promise
      return canonicalLoad.promise
    },
    createServer: async () => pendingCreate.promise,
  })
  await catalog.load()
  const olderLoad = catalog.load()
  const creation = catalog.create('Created')
  pendingCreate.resolve(created)
  await new Promise((resolve) => setImmediate(resolve))
  assert.equal(calls.length, 3, 'create success must invalidate and reload the current list')
  assert.equal(catalog.quotaUsed.value, 1, 'the mutation response must not pre-increment canonical quota')
  assert.deepEqual(catalog.servers.value.map((row) => row.id), [1], 'the mutation response must not patch the list snapshot')

  staleLoad.resolve({ servers: [{ ...server, id: 99 }], total: 99, quotaUsed: 99, page: 1, size: 50 })
  assert.equal(await olderLoad, false)
  assert.deepEqual(catalog.servers.value.map((row) => row.id), [1])
  canonicalLoad.resolve({ servers: [{ ...server, id: 1 }, created], total: 2, quotaUsed: 2, page: 1, size: 50 })
  assert.equal(await creation, created)
  assert.deepEqual(catalog.servers.value.map((row) => row.id), [1, 10])
  assert.equal(catalog.total.value, 2)
  assert.equal(catalog.quotaUsed.value, 2)
})

test('server catalog update invalidates an older load and preserves selected detail semantics', async () => {
  const staleLoad = deferred()
  const canonicalLoad = deferred()
  const pendingUpdate = deferred()
  const calls = []
  const updated = { ...server, name: 'Renamed', status: 'disabled' }
  const catalog = useServerCatalog({
    servers: async (...args) => {
      calls.push(args)
      if (calls.length === 1) return { servers: [server], total: 51, quotaUsed: 7, page: 2, size: 25 }
      if (calls.length === 2) return staleLoad.promise
      return canonicalLoad.promise
    },
    server: async () => ({ server, policy: { whitelistEnabled: true } }),
    updateServer: async () => pendingUpdate.promise,
  })
  await catalog.load(2, 25, 'disabled')
  await catalog.select(9)
  const olderLoad = catalog.load()
  const mutation = catalog.update(9, { name: 'Renamed', status: 'disabled', policy: { whitelistEnabled: false } })
  pendingUpdate.resolve(updated)
  await new Promise((resolve) => setImmediate(resolve))
  assert.equal(calls.length, 3)
  assert.deepEqual(calls[2], [2, 25, 'disabled'], 'update must reload the current page, size and filter')
  assert.equal(catalog.selected.value.server.name, 'Renamed')
  assert.equal(catalog.policy.value.whitelistEnabled, false)

  staleLoad.resolve({ servers: [{ ...server, id: 99 }], total: 99, quotaUsed: 99, page: 2, size: 25 })
  assert.equal(await olderLoad, false)
  assert.deepEqual(catalog.servers.value.map((row) => row.id), [9])
  canonicalLoad.resolve({ servers: [updated], total: 51, quotaUsed: 7, page: 2, size: 25 })
  assert.equal(await mutation, updated)
  assert.deepEqual(catalog.servers.value.map((row) => row.id), [9])
  assert.equal(catalog.selected.value.server.name, 'Renamed')
  assert.equal(catalog.policy.value.whitelistEnabled, false)
})

for (const kind of ['create', 'update']) {
  test(`server catalog ${kind} reloads the current page, size and status after success`, async () => {
    const calls = []
    const pendingMutation = deferred()
    const changed = { ...server, id: kind === 'create' ? 10 : 9, status: 'disabled' }
    const catalog = useServerCatalog({
      servers: async (...args) => {
        calls.push(args)
        return { servers: kind === 'update' && calls.length > 1 ? [changed] : [server], total: 51, quotaUsed: calls.length > 1 ? 8 : 7, page: args[0], size: args[1] }
      },
      createServer: async () => pendingMutation.promise,
      updateServer: async () => pendingMutation.promise,
    })
    await catalog.load(2, 25, 'disabled')
    const mutation = catalog[kind](kind === 'create' ? 'Created' : 9, { status: 'disabled' })
    pendingMutation.resolve(changed)
    await mutation
    assert.deepEqual(calls, [[2, 25, 'disabled'], [2, 25, 'disabled']])
    assert.deepEqual([catalog.page.value, catalog.size.value, catalog.status.value], [2, 25, 'disabled'])
    assert.equal(catalog.quotaUsed.value, 8)
  })

  test(`server catalog ${kind} finishing after a filter change reloads past the stale filter response`, async () => {
    const staleFilter = deferred()
    const canonicalLoad = deferred()
    const pendingMutation = deferred()
    const changed = { ...server, id: kind === 'create' ? 10 : 9, name: 'Changed', status: 'disabled' }
    let listCalls = 0
    const catalog = useServerCatalog({
      servers: async () => {
        listCalls++
        if (listCalls === 1) return { servers: [server], total: 1, quotaUsed: 1, page: 1, size: 25 }
        if (listCalls === 2) return staleFilter.promise
        return canonicalLoad.promise
      },
      createServer: async () => pendingMutation.promise,
      updateServer: async () => pendingMutation.promise,
    })
    await catalog.load(1, 25, '')
    const mutation = catalog[kind](kind === 'create' ? 'Changed' : 9, { status: 'disabled' })
    const filtered = catalog.filter('disabled')
    assert.equal(listCalls, 2)
    pendingMutation.resolve(changed)
    await new Promise((resolve) => setImmediate(resolve))
    assert.equal(listCalls, 3, `${kind} success must invalidate the in-flight filter load`)

    staleFilter.resolve({ servers: [{ ...server, id: 99 }], total: 99, quotaUsed: 99, page: 1, size: 25 })
    assert.equal(await filtered, false)
    assert.deepEqual(catalog.servers.value.map((row) => row.id), [9])
    canonicalLoad.resolve({ servers: [changed], total: 1, quotaUsed: 2, page: 1, size: 25 })
    await mutation
    assert.deepEqual(catalog.servers.value.map((row) => row.id), [kind === 'create' ? 10 : 9])
    assert.equal(catalog.status.value, 'disabled')
    assert.equal(catalog.quotaUsed.value, 2)
  })
}

test('owner invitations paginate independently and retain their page on refresh', async () => {
  const calls = []
  const portal = useServerTenant({
    tenantMe: async () => ({ tenant, role: 'owner' }),
    members: async () => ({ members: [] }),
    invites: async (...args) => {
      calls.push(args)
      return { invites: [{ id: args[0] }], total: 101, page: args[0], size: args[1] }
    },
  })
  await portal.load()
  assert.deepEqual(calls, [[1, 50]])
  assert.equal(portal.page.value, 1)
  assert.equal(portal.size.value, 50)
  assert.equal(portal.total.value, 101)
  await portal.pageInvites(3)
  assert.deepEqual(calls[1], [3, 50])
  await portal.load()
  assert.deepEqual(calls[2], [3, 50])
})

test('server access lists adopt server-normalized page and size metadata', async () => {
  const metadata = { total: 101, page: 2, size: 25 }
  const catalog = useServerCatalog({ servers: async () => ({ servers: [], ...metadata }) })
  await catalog.load(7, 100)
  assert.deepEqual([catalog.page.value, catalog.size.value, catalog.total.value], [2, 25, 101])

  const portal = useServerTenant({
    tenantMe: async () => ({ tenant, role: 'owner' }),
    members: async () => ({ members: [] }),
    invites: async () => ({ invites: [], ...metadata }),
  })
  await portal.pageInvites(7)
  assert.deepEqual([portal.page.value, portal.size.value, portal.total.value], [2, 25, 101])

  const whitelist = useServerWhitelist({ whitelist: async () => ({ whitelist: [], ...metadata }) })
  await whitelist.load(9, 7, 100)
  assert.deepEqual([whitelist.page.value, whitelist.size.value, whitelist.total.value], [2, 25, 101])

  const bans = useServerBans({ bans: async () => ({ bans: [], ...metadata }) })
  await bans.load(9, 7, 100)
  assert.deepEqual([bans.page.value, bans.size.value, bans.total.value], [2, 25, 101])

  const platform = useServerAccessPlatform({
    platformTenants: async () => ({ tenants: [], ...metadata }),
    platformServers: async () => ({ servers: [], ...metadata }),
    platformAudit: async () => ({ audit: [], ...metadata }),
    platformFallback: async () => ({ enabled: false }),
  })
  await platform.load()
  for (const state of [
    [platform.tenantPage.value, platform.tenantSize.value, platform.tenantTotal.value],
    [platform.serverPage.value, platform.serverSize.value, platform.serverTotal.value],
    [platform.auditPage.value, platform.auditSize.value, platform.auditTotal.value],
  ]) assert.deepEqual(state, [2, 25, 101])
})

test('whitelist paginates, resets on server changes, and ignores an older page response', async () => {
  let releaseOld
  const calls = []
  const panel = useServerWhitelist({
    whitelist: async (id, page, size) => {
      calls.push([id, page, size])
      if (id === 9 && page === 2) return new Promise((resolve) => { releaseOld = resolve })
      return { whitelist: [{ id, page }], total: 101, page, size }
    },
  })
  await panel.load(9)
  assert.deepEqual(calls[0], [9, 1, 50])
  assert.equal(panel.page.value, 1)
  assert.equal(panel.size.value, 50)
  assert.equal(panel.total.value, 101)
  const oldPage = panel.goPage(9, 2)
  await panel.goPage(9, 1)
  releaseOld({ whitelist: [{ id: 99 }], total: 101, page: 2, size: 50 })
  assert.equal(await oldPage, false)
  assert.deepEqual(panel.rows.value.map((row) => row.id), [9])
  await panel.load(10)
  assert.deepEqual(calls.at(-1), [10, 1, 50])
  assert.equal(panel.page.value, 1)
})

test('whitelist clamps to the last populated page after deleting its final row', async () => {
  const calls = []
  let rows = 51
  const panel = useServerWhitelist({
    whitelist: async (_id, page, size) => {
      calls.push([page, size])
      const start = (page - 1) * size
      return {
        whitelist: Array.from({ length: Math.max(0, Math.min(size, rows - start)) }, (_, index) => ({ id: start + index + 1 })),
        total: rows, page, size,
      }
    },
    removeWhitelist: async () => { rows-- },
  })
  await panel.load(9)
  await panel.goPage(9, 2)
  assert.equal(panel.rows.value.length, 1)
  await panel.remove(9, 51)
  assert.deepEqual(calls, [[1, 50], [2, 50], [2, 50], [1, 50]])
  assert.equal(panel.page.value, 1)
  assert.equal(panel.rows.value.length, 50)
  assert.equal(panel.total.value, 50)
})

test('ban pagination resets on server changes and clamps an empty page after unbanning', async () => {
  const calls = []
  let rows = 2
  const panel = useServerBans({
    bans: async (id, page, size) => {
      calls.push([id, page, size])
      return { bans: rows ? [{ id: rows }] : [], total: rows, page, size }
    },
    removeBan: async () => { rows = 0 },
  })
  await panel.load(9, 2, 1)
  assert.equal(panel.page.value, 2)
  assert.equal(panel.size.value, 1)
  assert.equal(panel.total.value, 2)
  await panel.remove(9, 2)
  assert.equal(panel.page.value, 1)
  assert.equal(panel.total.value, 0)
  await panel.load(10)
  assert.deepEqual(calls.at(-1), [10, 1, 1])
  assert.equal(panel.page.value, 1)
})

test('ban list ignores a late response from another server', async () => {
  let releaseOld
  const panel = useServerBans({
    bans: async (id) => {
      if (id === 9) return new Promise((resolve) => { releaseOld = resolve })
      return { bans: [{ id: 10 }], total: 1, page: 1, size: 50 }
    },
  })
  const oldLoad = panel.load(9)
  await panel.load(10)
  releaseOld({ bans: [{ id: 9 }], total: 1, page: 1, size: 50 })
  assert.equal(await oldLoad, false)
  assert.deepEqual(panel.rows.value.map((row) => row.id), [10])
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

test('platform pagination keeps the backend default page size of 20', async () => {
  const calls = []
  const platform = useServerAccessPlatform({
    platformTenants: async (...args) => { calls.push(['tenants', ...args]); return { tenants: [], total: 21, page: 1, size: 20 } },
    platformServers: async (...args) => { calls.push(['servers', ...args]); return { servers: [], total: 41, page: 1, size: 20 } },
    platformAudit: async (...args) => { calls.push(['audit', ...args]); return { audit: [], total: 61, page: 1, size: 20 } },
    platformFallback: async () => ({ enabled: false }),
  })
  await platform.load()
  assert.equal(platform.tenantSize.value, 20)
  assert.equal(platform.serverSize.value, 20)
  assert.equal(platform.auditSize.value, 20)
  assert.deepEqual(calls, [['tenants', 1, ''], ['servers', 1, ''], ['audit', 1, '']])
})

test('platform tenant deletion forwards exact confirmation and clamps an empty last page', async () => {
  const calls = []
  const deletes = []
  let rows = Array.from({ length: 21 }, (_, index) => ({ ...tenant, id: index + 1 }))
  const platform = useServerAccessPlatform({
    platformTenants: async (page) => {
      calls.push(page)
      return { tenants: rows.slice((page - 1) * 20, page * 20), total: rows.length, page, size: 20 }
    },
    platformServers: async () => ({ servers: [], total: 0 }),
    platformAudit: async () => ({ audit: [], total: 0 }),
    platformFallback: async () => ({ enabled: false }),
    deletePlatformTenant: async (id, confirmation) => { deletes.push([id, confirmation]); rows = rows.filter((row) => row.id !== id) },
  })
  await platform.load()
  await platform.pageTenants(2)
  await platform.deleteTenant(21, 'Tenant 21')
  assert.deepEqual(deletes, [[21, 'Tenant 21']])
  assert.deepEqual(calls, [1, 2, 2, 1])
  assert.equal(platform.tenantPage.value, 1)
  assert.equal(platform.tenants.value.length, 20)
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
