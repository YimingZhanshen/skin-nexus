import test from 'node:test'
import assert from 'node:assert/strict'
import { fileURLToPath } from 'node:url'
import { createServer } from 'vite'

const vite = await createServer({
  root: fileURLToPath(new URL('../../', import.meta.url)), configFile: false, server: { middlewareMode: true }, appType: 'custom',
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
