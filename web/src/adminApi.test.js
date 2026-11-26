import test from 'node:test'
import assert from 'node:assert/strict'
import { fileURLToPath } from 'node:url'
import { createServer } from 'vite'

const root = fileURLToPath(new URL('../', import.meta.url))
const stored = new Map([['admin_token', 'legacy-machine-token'], ['skin_lang', 'en']])
const removed = []
globalThis.localStorage = {
  getItem: (key) => stored.get(key) || null,
  setItem: (key, value) => stored.set(key, value),
  removeItem: (key) => { removed.push(key); stored.delete(key) },
}

const vite = await createServer({
  root,
  configFile: false,
  server: { middlewareMode: true, hmr: false, ws: false },
  appType: 'custom',
  plugins: [{
    name: 'admin-api-test-errors',
    enforce: 'pre',
    resolveId(source) {
      if (source.endsWith('/errors.js') || source === './errors.js') return '\0admin-api-test-errors'
    },
    load(id) {
      if (id === '\0admin-api-test-errors') {
        return 'export function describe(data,status){return data.message || data.code || `HTTP ${status}`}; export function transportError(){return "network"}'
      }
    },
  }],
})
const { fetchJSON } = await vite.ssrLoadModule('/src/adminApi.js')
const { bumpSessionVersion, setUnauthorizedHandler } = await vite.ssrLoadModule('/src/session.js')
await vite.close()

function jsonResponse(status, body) {
  return new Response(JSON.stringify(body), { status, headers: { 'Content-Type': 'application/json' } })
}

test('loading the browser admin transport removes the legacy stored token', () => {
  assert.equal(stored.has('admin_token'), false)
  assert.ok(removed.includes('admin_token'))
})

test('admin requests use same-origin cookies and never send an admin token header', async () => {
  const previousFetch = globalThis.fetch
  stored.set('admin_token', 'must-not-be-sent')
  globalThis.fetch = async (path, options) => {
    assert.equal(path, '/api/admin/v1/overview')
    assert.equal(options.credentials, 'same-origin')
    assert.deepEqual(options.headers, { 'X-Custom': 'kept' })
    return jsonResponse(200, { users: 3 })
  }
  try {
    assert.deepEqual(await fetchJSON('/api/admin/v1/overview', {
      headers: { 'X-Custom': 'kept', 'X-Admin-Token': 'must-not-be-sent', 'x-admin-token': 'also-forbidden' },
    }), { users: 3 })
    assert.equal(stored.has('admin_token'), false)
  } finally {
    globalThis.fetch = previousFetch
  }
})

test('admin transport keeps the 20s default and honors an explicit timeout', async () => {
  const previousFetch = globalThis.fetch
  const previousSetTimeout = globalThis.setTimeout
  const timeouts = []
  globalThis.fetch = async () => jsonResponse(200, {})
  globalThis.setTimeout = (callback, delay, ...args) => {
    timeouts.push(delay)
    return previousSetTimeout(callback, delay, ...args)
  }
  try {
    await fetchJSON('/api/admin/v1/default')
    await fetchJSON('/api/admin/v1/upload', { method: 'POST', body: new FormData() }, 120000)
    assert.deepEqual(timeouts, [20000, 120000])
  } finally {
    globalThis.fetch = previousFetch
    globalThis.setTimeout = previousSetTimeout
  }
})

test('admin transport notifies before throwing only for session-loss responses', async () => {
  const previousFetch = globalThis.fetch
  const cases = [
    { status: 401, code: '', notify: true },
    { status: 403, code: 'admin.token_invalid', notify: true },
    { status: 403, code: 'admin.super_admin_required', notify: false },
    { status: 403, code: 'admin.web_session_required', notify: false },
    { status: 403, code: '', notify: false },
    { status: 404, code: 'admin.not_found', notify: false },
    { status: 500, code: '', notify: false },
  ]
  try {
    for (const item of cases) {
      const events = []
      setUnauthorizedHandler(() => events.push('notified'))
      globalThis.fetch = async () => jsonResponse(item.status, { code: item.code, message: item.code || 'failed' })
      await assert.rejects(fetchJSON('/api/admin/v1/test'), (error) => {
        assert.equal(error.status, item.status)
        assert.equal(error.data.code, item.code)
        assert.deepEqual(events, item.notify ? ['notified'] : [])
        return true
      })
    }
  } finally {
    setUnauthorizedHandler(null)
    globalThis.fetch = previousFetch
  }
})

test('admin transport ignores a delayed A-request 401 after switching to B', async () => {
  const previousFetch = globalThis.fetch
  let resolveRequest
  const notifications = []
  setUnauthorizedHandler(() => notifications.push('notified'))
  globalThis.fetch = async () => new Promise((resolve) => { resolveRequest = resolve })
  try {
    const request = fetchJSON('/api/admin/v1/test')
    bumpSessionVersion()
    resolveRequest(jsonResponse(401, { code: '', message: 'old session lost' }))
    await assert.rejects(request, (error) => error.status === 401)
    assert.deepEqual(notifications, [])
  } finally {
    setUnauthorizedHandler(null)
    globalThis.fetch = previousFetch
  }
})

test('admin transport preserves network failures without notifying session loss', async () => {
  const previousFetch = globalThis.fetch
  const notifications = []
  setUnauthorizedHandler(() => notifications.push('notified'))
  globalThis.fetch = async () => { throw new Error('offline') }
  try {
    await assert.rejects(fetchJSON('/api/admin/v1/test'), /offline/)
    assert.deepEqual(notifications, [])
  } finally {
    setUnauthorizedHandler(null)
    globalThis.fetch = previousFetch
  }
})
