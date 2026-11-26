import test from 'node:test'
import assert from 'node:assert/strict'
import { fileURLToPath } from 'node:url'
import { createServer } from 'vite'

const vite = await createServer({
  root: fileURLToPath(new URL('../../', import.meta.url)),
  configFile: false,
  server: { middlewareMode: true, hmr: false, ws: false },
  appType: 'custom',
  plugins: [{
    name: 'server-access-test-errors',
    enforce: 'pre',
    resolveId(source) {
      if (source.endsWith('/errors.js') || source === './errors.js') return '\0server-access-test-errors'
    },
    load(id) {
      if (id === '\0server-access-test-errors') {
        return 'export function describe(data,status){return data.message || `HTTP ${status}`}; export function transportError(){return "network"}'
      }
    },
  }],
})
const { serverAccessApi } = await vite.ssrLoadModule('/src/api/serverAccessApi.js')
const { setUnauthorizedHandler } = await vite.ssrLoadModule('/src/session.js')
await vite.close()

function jsonResponse(status, body) {
  return new Response(JSON.stringify(body), { status, headers: { 'Content-Type': 'application/json' } })
}

test('owner lookup encodes the query and keeps ordinary session semantics', async () => {
  const previousFetch = globalThis.fetch
  const previousStorage = globalThis.localStorage
  globalThis.localStorage = { getItem: () => 'must-not-be-sent' }
  globalThis.fetch = async (path, options) => {
    assert.equal(path, '/api/serveraccess/v1/players/lookup?q=Lookup%20Player')
    assert.equal(options.credentials, 'same-origin')
    assert.equal(options.headers['X-Admin-Token'], undefined)
    return jsonResponse(200, { uid: 7, name: 'Lookup Player', uuid: '0123456789abcdef0123456789abcdef' })
  }
  try {
    const player = await serverAccessApi.lookupPlayer('Lookup Player')
    assert.deepEqual(player, { uid: 7, name: 'Lookup Player', uuid: '0123456789abcdef0123456789abcdef' })
  } finally {
    globalThis.fetch = previousFetch
    globalThis.localStorage = previousStorage
  }
})

test('invite acceptance posts only the pasted token with ordinary session semantics', async () => {
  const previousFetch = globalThis.fetch
  const previousStorage = globalThis.localStorage
  globalThis.localStorage = { getItem: () => 'must-not-be-sent' }
  globalThis.fetch = async (path, options) => {
    assert.equal(path, '/api/serveraccess/v1/invites/accept')
    assert.equal(options.method, 'POST')
    assert.equal(options.credentials, 'same-origin')
    assert.deepEqual(options.headers, { 'Content-Type': 'application/json' })
    assert.equal(options.headers['X-Admin-Token'], undefined)
    assert.equal(options.body, '{"token":"pasted-invite-token"}')
    return jsonResponse(200, { tenantId: 12 })
  }
  try {
    assert.deepEqual(await serverAccessApi.acceptInvite('pasted-invite-token'), { tenantId: 12 })
  } finally {
    globalThis.fetch = previousFetch
    globalThis.localStorage = previousStorage
  }
})

test('owner lists send explicit page and size with a default page size of 50', async () => {
  const previousFetch = globalThis.fetch
  const calls = []
  globalThis.fetch = async (path) => {
    calls.push(path)
    return jsonResponse(200, {})
  }
  try {
    await serverAccessApi.servers()
    await serverAccessApi.servers(2, 25, 'disabled')
    await serverAccessApi.whitelist(9)
    await serverAccessApi.whitelist(9, 3, 100)
    await serverAccessApi.bans(9)
    await serverAccessApi.bans(9, 4, 75)
    await serverAccessApi.invites()
    await serverAccessApi.invites(5, 20)
    assert.deepEqual(calls, [
      '/api/serveraccess/v1/servers?page=1&size=50',
      '/api/serveraccess/v1/servers?page=2&size=25&status=disabled',
      '/api/serveraccess/v1/servers/9/whitelist?page=1&size=50',
      '/api/serveraccess/v1/servers/9/whitelist?page=3&size=100',
      '/api/serveraccess/v1/servers/9/bans?page=1&size=50',
      '/api/serveraccess/v1/servers/9/bans?page=4&size=75',
      '/api/serveraccess/v1/tenants/me/invites?page=1&size=50',
      '/api/serveraccess/v1/tenants/me/invites?page=5&size=20',
    ])
  } finally {
    globalThis.fetch = previousFetch
  }
})

test('event pagination sends the cursor and preserves API error status', async () => {
  const previousFetch = globalThis.fetch
  globalThis.fetch = async (path) => {
    assert.equal(path, '/api/serveraccess/v1/servers/9/events?before=31&size=25')
    return jsonResponse(404, { error: 'Not Found', code: 'serveraccess.not_found', message: 'Resource not found' })
  }
  try {
    await assert.rejects(serverAccessApi.events(9, { before: 31, size: 25 }), (error) => {
      assert.equal(error.status, 404)
      assert.equal(error.data.code, 'serveraccess.not_found')
      return true
    })
  } finally {
    globalThis.fetch = previousFetch
  }
})

test('tenant export downloads the JSON attachment without parsing it as an API envelope', async () => {
  const previousFetch = globalThis.fetch
  const previousStorage = globalThis.localStorage
  globalThis.localStorage = { getItem: () => 'must-not-be-sent' }
  globalThis.fetch = async (path, options) => {
    assert.equal(path, '/api/serveraccess/v1/tenants/me/export')
    assert.equal(options.method, 'GET')
    assert.equal(options.credentials, 'same-origin')
    assert.equal(options.headers['X-Admin-Token'], undefined)
    return new Response('{"schemaVersion":1,"servers":[]}\n', {
      status: 200,
      headers: { 'Content-Type': 'application/json', 'Content-Disposition': 'attachment; filename="serveraccess-tenant-1.json"' },
    })
  }
  try {
    const blob = await serverAccessApi.exportTenant()
    assert.equal(blob.type, 'application/json')
    assert.equal(await blob.text(), '{"schemaVersion":1,"servers":[]}\n')
  } finally {
    globalThis.fetch = previousFetch
    globalThis.localStorage = previousStorage
  }
})

test('tenant export preserves the explicit 413 error for the confirmation UI', async () => {
  const previousFetch = globalThis.fetch
  globalThis.fetch = async () => jsonResponse(413, { code: 'serveraccess.export_too_large', message: 'events exceeds export limit of 100000' })
  try {
    await assert.rejects(serverAccessApi.exportTenant(), (error) => {
      assert.equal(error.status, 413)
      assert.equal(error.data.code, 'serveraccess.export_too_large')
      return true
    })
  } finally {
    globalThis.fetch = previousFetch
  }
})

test('deletion requests use only the existing owner and platform lifecycle endpoints', async () => {
  const previousFetch = globalThis.fetch
  const calls = []
  globalThis.fetch = async (path, options) => {
    calls.push([path, options.method, options.body])
    return jsonResponse(200, path.includes('/api/admin/') ? { deleted: true } : { status: 'deleting', deletionDueAt: '2027-01-31T00:00:00Z' })
  }
  try {
    await serverAccessApi.requestTenantDeletion()
    await serverAccessApi.deletePlatformTenant(7)
    assert.deepEqual(calls, [
      ['/api/serveraccess/v1/tenants/me/delete-request', 'POST', '{}'],
      ['/api/admin/v1/serveraccess/tenants/7/delete', 'POST', '{}'],
    ])
  } finally {
    globalThis.fetch = previousFetch
  }
})

test('platform tenant creation sends only the explicit owner contract with session semantics', async () => {
  const previousFetch = globalThis.fetch
  const previousStorage = globalThis.localStorage
  globalThis.localStorage = { getItem: () => 'must-not-be-sent' }
  globalThis.fetch = async (path, options) => {
    assert.equal(path, '/api/admin/v1/serveraccess/tenants')
    assert.equal(options.method, 'POST')
    assert.equal(options.credentials, 'same-origin')
    assert.deepEqual(options.headers, { 'Content-Type': 'application/json' })
    assert.equal(options.headers['X-Admin-Token'], undefined)
    assert.equal(options.body, '{"name":"Tenant","serverLimit":5,"ownerUid":42}')
    return jsonResponse(201, { tenant: { id: 3 }, owner: { tenantId: 3, uid: 42, role: 'owner' } })
  }
  try {
    const result = await serverAccessApi.createPlatformTenant({ name: 'Tenant', serverLimit: 5, ownerUid: 42 })
    assert.deepEqual(result, { tenant: { id: 3 }, owner: { tenantId: 3, uid: 42, role: 'owner' } })
  } finally {
    globalThis.fetch = previousFetch
    globalThis.localStorage = previousStorage
  }
})

test('platform tenant creation preserves conflict status and response data', async () => {
  const previousFetch = globalThis.fetch
  globalThis.fetch = async () => jsonResponse(409, { code: 'serveraccess.duplicate', message: 'Duplicate' })
  try {
    await assert.rejects(serverAccessApi.createPlatformTenant({ name: 'Tenant', serverLimit: 5, ownerUid: 42 }), (error) => {
      assert.equal(error.status, 409)
      assert.equal(error.data.code, 'serveraccess.duplicate')
      return true
    })
  } finally {
    globalThis.fetch = previousFetch
  }
})

test('owner 401 and admin token_invalid notify the shell before rejection', async () => {
  const previousFetch = globalThis.fetch
  try {
    for (const item of [
      { call: () => serverAccessApi.servers(), status: 401, code: '' },
      { call: () => serverAccessApi.setPlatformFallback(true), status: 403, code: 'admin.token_invalid' },
    ]) {
      const events = []
      setUnauthorizedHandler(() => events.push('notified'))
      globalThis.fetch = async () => jsonResponse(item.status, { code: item.code, message: 'session lost' })
      await assert.rejects(item.call(), (error) => {
        assert.equal(error.status, item.status)
        assert.equal(error.data.code, item.code)
        assert.deepEqual(events, ['notified'])
        return true
      })
    }
  } finally {
    setUnauthorizedHandler(null)
    globalThis.fetch = previousFetch
  }
})

test('Server Access authorization, module, server, and network failures do not end the session', async () => {
  const previousFetch = globalThis.fetch
  const notifications = []
  setUnauthorizedHandler(() => notifications.push('notified'))
  try {
    for (const item of [
      { status: 403, code: 'serveraccess.forbidden' },
      { status: 403, code: 'admin.super_admin_required' },
      { status: 403, code: '' },
      { status: 404, code: 'serveraccess.not_found' },
      { status: 500, code: '' },
    ]) {
      globalThis.fetch = async () => jsonResponse(item.status, { code: item.code, message: item.code || 'failed' })
      await assert.rejects(serverAccessApi.servers(), (error) => {
        assert.equal(error.status, item.status)
        assert.equal(error.data.code, item.code)
        return true
      })
    }
    globalThis.fetch = async () => { throw new Error('offline') }
    await assert.rejects(serverAccessApi.servers(), /offline/)
    assert.deepEqual(notifications, [])
  } finally {
    setUnauthorizedHandler(null)
    globalThis.fetch = previousFetch
  }
})

test('platform fallback uses the same cookie client without an admin token', async () => {
  const previousFetch = globalThis.fetch
  const previousStorage = globalThis.localStorage
  globalThis.localStorage = { getItem: () => 'must-not-be-sent' }
  globalThis.fetch = async (path, options) => {
    assert.equal(path, '/api/admin/v1/serveraccess/fallback')
    assert.equal(options.method, 'PUT')
    assert.equal(options.headers['Content-Type'], 'application/json')
    assert.equal(options.headers['X-Admin-Token'], undefined)
    assert.equal(options.body, '{"enabled":false}')
    return jsonResponse(200, { enabled: false })
  }
  try {
    assert.deepEqual(await serverAccessApi.setPlatformFallback(false), { enabled: false })
  } finally {
    globalThis.fetch = previousFetch
    globalThis.localStorage = previousStorage
  }
})
