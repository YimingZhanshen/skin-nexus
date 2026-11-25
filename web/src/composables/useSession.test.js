import test from 'node:test'
import assert from 'node:assert/strict'
import { fileURLToPath } from 'node:url'
import { createRenderer, nextTick } from 'vue'
import { createI18n } from 'vue-i18n'
import { createServer } from 'vite'
import en from '../locales/en.json' with { type: 'json' }

const root = fileURLToPath(new URL('../../', import.meta.url))
const vite = await createServer({
  root,
  configFile: false,
  server: { middlewareMode: true, hmr: false, ws: false },
  appType: 'custom',
  plugins: [{
    name: 'session-composable-test-api',
    enforce: 'pre',
    resolveId(source) {
      if (source === '../api.js') return '\0session-composable-test-api'
    },
    load(id) {
      if (id === '\0session-composable-test-api') {
        return 'export const api = new Proxy({}, { get: (_target, key) => (...args) => globalThis.__sessionApi[key](...args) })'
      }
    },
  }],
})
const { useSession } = await vite.ssrLoadModule('/src/composables/useSession.js')
const { setUnauthorizedHandler } = await vite.ssrLoadModule('/src/session.js')
await vite.close()

function hostNode(type, text = '') {
  return { type, text, props: {}, children: [], parent: null }
}
const renderer = createRenderer({
  createElement: (type) => hostNode(type),
  createText: (text) => hostNode('#text', text),
  createComment: (text) => hostNode('#comment', text),
  setText: (node, text) => { node.text = text },
  setElementText: (node, text) => { node.text = text; node.children = [] },
  parentNode: (node) => node.parent,
  nextSibling: () => null,
  insert: (child, parent) => { child.parent = parent; parent.children.push(child) },
  remove: (child) => {
    if (!child.parent) return
    child.parent.children.splice(child.parent.children.indexOf(child), 1)
    child.parent = null
  },
  patchProp: (node, key, _previous, value) => { node.props[key] = value },
})

function deferred() {
  let resolve
  let reject
  const promise = new Promise((res, rej) => { resolve = res; reject = rej })
  return { promise, resolve, reject }
}

function responseError(status, code) {
  return Object.assign(new Error(code || `HTTP ${status}`), { status, data: code ? { code } : {} })
}

async function mountSession() {
  let session
  const app = renderer.createApp({
    setup() {
      session = useSession()
      return () => null
    },
  })
  app.use(createI18n({ legacy: false, locale: 'en', messages: { en }, warnHtmlMessage: false }))
  const container = hostNode('root')
  app.mount(container)
  await nextTick()
  return { app, session }
}

function storedToken(value = 'legacy-machine-token') {
  const values = new Map(value ? [['admin_token', value]] : [])
  globalThis.localStorage = {
    getItem: (key) => values.get(key) || null,
    setItem: (key, next) => values.set(key, next),
    removeItem: (key) => values.delete(key),
  }
  return values
}

test('setSessionUser replaces the user, updates admin readiness, and removes legacy tokens', async () => {
  const values = storedToken()
  const { app, session } = await mountSession()
  try {
    session.setSessionUser({ id: 1, nickname: 'Admin', permission: 1 })
    assert.equal(session.user.value.id, 1)
    assert.equal(session.isAdmin.value, true)
    assert.equal(values.has('admin_token'), false)

    session.setSessionUser({ id: 1, nickname: 'Admin', permission: 0 })
    assert.equal(session.isAdmin.value, false)
    values.set('admin_token', 'legacy-machine-token')
    session.setSessionUser(null)
    assert.equal(session.user.value, null)
    assert.equal(values.has('admin_token'), false)
  } finally {
    app.unmount()
  }
})

test('a delayed /me response cannot replace a user selected later', async () => {
  storedToken('')
  const pending = deferred()
  globalThis.__sessionApi = { me: () => pending.promise }
  const { app, session } = await mountSession()
  try {
    session.setSessionUser({ id: 1, nickname: 'First', permission: 0 })
    const refresh = session.refreshMe()
    session.setSessionUser({ id: 2, nickname: 'Second', permission: 2 })
    pending.resolve({ id: 1, nickname: 'Stale first', permission: 0 })
    await refresh
    assert.equal(session.user.value.id, 2)
    assert.equal(session.user.value.nickname, 'Second')
    assert.equal(session.isAdmin.value, true)
  } finally {
    app.unmount()
    delete globalThis.__sessionApi
  }
})

test('refreshMe commits only the most recently started request', async () => {
  storedToken('')
  const first = deferred()
  const second = deferred()
  let calls = 0
  globalThis.__sessionApi = { me: () => (++calls === 1 ? first.promise : second.promise) }
  const { app, session } = await mountSession()
  try {
    const oldRefresh = session.refreshMe()
    const newRefresh = session.refreshMe()
    second.resolve({ id: 2, nickname: 'Newest', permission: 1 })
    await newRefresh
    first.resolve({ id: 1, nickname: 'Older', permission: 0 })
    await oldRefresh
    assert.equal(session.user.value.id, 2)
    assert.equal(session.user.value.nickname, 'Newest')
  } finally {
    app.unmount()
    delete globalThis.__sessionApi
  }
})

test('a current /me 401 clears the user and notifies the shell', async () => {
  storedToken('legacy-machine-token')
  const notifications = []
  setUnauthorizedHandler(() => notifications.push('notified'))
  globalThis.__sessionApi = { me: async () => { throw responseError(401) } }
  const { app, session } = await mountSession()
  try {
    session.setSessionUser({ id: 1, nickname: 'First', permission: 1 })
    await session.refreshMe()
    assert.equal(session.user.value, null)
    assert.equal(session.isAdmin.value, false)
    assert.deepEqual(notifications, ['notified'])
  } finally {
    setUnauthorizedHandler(null)
    app.unmount()
    delete globalThis.__sessionApi
  }
})

test('a stale /me 401 cannot clear a newly selected user', async () => {
  storedToken('')
  const pending = deferred()
  const notifications = []
  setUnauthorizedHandler(() => notifications.push('notified'))
  globalThis.__sessionApi = { me: () => pending.promise }
  const { app, session } = await mountSession()
  try {
    const refresh = session.refreshMe()
    session.setSessionUser({ id: 2, nickname: 'Second', permission: 1 })
    pending.reject(responseError(401))
    await refresh
    assert.equal(session.user.value.id, 2)
    assert.deepEqual(notifications, [])
  } finally {
    setUnauthorizedHandler(null)
    app.unmount()
    delete globalThis.__sessionApi
  }
})

test('network and 5xx /me failures preserve the current session without notifying', async () => {
  storedToken('')
  const notifications = []
  setUnauthorizedHandler(() => notifications.push('notified'))
  const { app, session } = await mountSession()
  try {
    for (const cause of [new Error('offline'), responseError(500), responseError(503)]) {
      globalThis.__sessionApi = { me: async () => { throw cause } }
      session.setSessionUser({ id: 7, nickname: 'Still signed in', permission: 1 })
      await session.refreshMe()
      assert.equal(session.user.value.id, 7)
      assert.equal(session.isAdmin.value, true)
    }
    assert.deepEqual(notifications, [])
  } finally {
    setUnauthorizedHandler(null)
    app.unmount()
    delete globalThis.__sessionApi
  }
})
