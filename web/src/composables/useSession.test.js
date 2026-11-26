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
      if (source === './errors.js') return '\0session-composable-test-errors'
    },
    load(id) {
      if (id === '\0session-composable-test-api') {
        return 'export const api = new Proxy({}, { get: (_target, key) => (...args) => globalThis.__sessionApi[key](...args) })'
      }
      if (id === '\0session-composable-test-errors') {
        return 'export function describe(data,status){return data.message || data.code || `HTTP ${status}`}; export function transportError(){return "network"}'
      }
    },
  }],
})
const { useSession } = await vite.ssrLoadModule('/src/composables/useSession.js')
const { fetchJSON } = await vite.ssrLoadModule('/src/adminApi.js')
const { captureSessionVersion, setUnauthorizedHandler } = await vite.ssrLoadModule('/src/session.js')
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

test('setSessionUser advances the transport epoch only when authentication identity changes', async () => {
  storedToken('')
  const { app, session } = await mountSession()
  try {
    const initial = captureSessionVersion()
    session.setSessionUser({ id: 1, nickname: 'First', permission: 1 })
    const loggedIn = captureSessionVersion()
    assert.ok(loggedIn > initial)

    session.setSessionUser({ id: 1, nickname: 'Updated', permission: 0, unreadNotifications: 0 })
    assert.equal(captureSessionVersion(), loggedIn)
    assert.equal(session.user.value.nickname, 'Updated')
    assert.equal(session.isAdmin.value, false)

    session.setSessionUser({ id: 2, nickname: 'Second', permission: 1 })
    const replaced = captureSessionVersion()
    assert.ok(replaced > loggedIn)
    session.setSessionUser(null)
    const loggedOut = captureSessionVersion()
    assert.ok(loggedOut > replaced)
    session.setSessionUser(null)
    assert.equal(captureSessionVersion(), loggedOut)
    session.setSessionUser({ id: 1, nickname: 'First again', permission: 1 })
    assert.ok(captureSessionVersion() > loggedOut)
  } finally {
    app.unmount()
  }
})

test('a same-account local update keeps an earlier transport 401 current', async () => {
  storedToken('')
  const pending = deferred()
  const previousFetch = globalThis.fetch
  const notifications = []
  let session
  globalThis.fetch = () => pending.promise
  setUnauthorizedHandler(() => {
    notifications.push('notified')
    session.setSessionUser(null)
  })
  const mounted = await mountSession()
  session = mounted.session
  try {
    session.setSessionUser({ id: 1, nickname: 'First', permission: 1, unreadNotifications: 3 })
    const request = fetchJSON('/api/admin/v1/notifications/read')
    session.onInboxRead()
    assert.equal(session.user.value.unreadNotifications, 0)
    pending.resolve(new Response(JSON.stringify({ code: '', message: 'session lost' }), {
      status: 401, headers: { 'Content-Type': 'application/json' },
    }))
    await assert.rejects(request, (error) => error.status === 401)
    assert.deepEqual(notifications, ['notified'])
    assert.equal(session.user.value, null)
  } finally {
    setUnauthorizedHandler(null)
    globalThis.fetch = previousFetch
    mounted.app.unmount()
  }
})

test('account replacement and logout invalidate earlier transport 401s', async () => {
  const previousFetch = globalThis.fetch
  try {
    for (const transition of ['replace', 'logout']) {
      storedToken('')
      const pending = deferred()
      const notifications = []
      let session
      globalThis.fetch = () => pending.promise
      setUnauthorizedHandler(() => notifications.push('notified'))
      const mounted = await mountSession()
      session = mounted.session
      try {
        session.setSessionUser({ id: 1, nickname: 'First', permission: 1 })
        const request = fetchJSON('/api/admin/v1/test')
        if (transition === 'replace') session.setSessionUser({ id: 2, nickname: 'Second', permission: 1 })
        else session.setSessionUser(null)
        pending.resolve(new Response(JSON.stringify({ code: '', message: 'old session lost' }), {
          status: 401, headers: { 'Content-Type': 'application/json' },
        }))
        await assert.rejects(request, (error) => error.status === 401)
        assert.deepEqual(notifications, [])
        assert.equal(session.user.value?.id ?? null, transition === 'replace' ? 2 : null)
      } finally {
        mounted.app.unmount()
      }
    }
  } finally {
    setUnauthorizedHandler(null)
    globalThis.fetch = previousFetch
  }
})

test('a same-account local update still invalidates earlier /me work', async () => {
  storedToken('')
  const pending = deferred()
  globalThis.__sessionApi = { me: () => pending.promise }
  const { app, session } = await mountSession()
  try {
    session.setSessionUser({ id: 1, nickname: 'Current', permission: 1, unreadNotifications: 2 })
    const refresh = session.refreshMe()
    session.onInboxRead()
    pending.resolve({ id: 1, nickname: 'Stale refresh', permission: 1, unreadNotifications: 2 })
    assert.equal(await refresh, false)
    assert.equal(session.user.value.nickname, 'Current')
    assert.equal(session.user.value.unreadNotifications, 0)
  } finally {
    app.unmount()
    delete globalThis.__sessionApi
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

test('refreshMe preserves an account selected after readMe validates but before commit', async () => {
  storedToken('')
  const pending = deferred()
  globalThis.__sessionApi = { me: () => pending.promise }
  const { app, session } = await mountSession()
  try {
    session.setSessionUser({ id: 1, nickname: 'First', permission: 0 })
    const refresh = session.refreshMe()
    pending.resolve({ id: 1, nickname: 'Refreshed first', permission: 1 })
    // Resolving /me queues readMe's continuation first. This continuation then
    // selects B after readMe validates A but before refreshMe resumes.
    queueMicrotask(() => {
      session.setSessionUser({ id: 2, nickname: 'Second', permission: 2 })
    })
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

test('readMe returns a current account value without replacing the shell user', async () => {
  storedToken('')
  globalThis.__sessionApi = { me: async () => ({ id: 1, nickname: 'Updated shell name', permission: 0 }) }
  const { app, session } = await mountSession()
  try {
    session.setSessionUser({ id: 1, nickname: 'Shell name', permission: 1 })
    const value = await session.readMe()
    assert.equal(value.id, 1)
    assert.equal(value.nickname, 'Updated shell name')
    assert.equal(session.user.value.nickname, 'Shell name')
    assert.equal(session.isAdmin.value, true)
  } finally {
    app.unmount()
    delete globalThis.__sessionApi
  }
})

test('readMe ignores a delayed success from an account that was replaced', async () => {
  storedToken('')
  const pending = deferred()
  globalThis.__sessionApi = { me: () => pending.promise }
  const { app, session } = await mountSession()
  try {
    const read = session.readMe()
    session.setSessionUser({ id: 2, nickname: 'Second', permission: 1 })
    pending.resolve({ id: 1, nickname: 'Stale first', permission: 0 })
    assert.equal(await read, null)
    assert.equal(session.user.value.id, 2)
  } finally {
    app.unmount()
    delete globalThis.__sessionApi
  }
})

test('readMe ignores a delayed 401 from an account that was replaced', async () => {
  storedToken('')
  const pending = deferred()
  const notifications = []
  setUnauthorizedHandler(() => notifications.push('notified'))
  globalThis.__sessionApi = { me: () => pending.promise }
  const { app, session } = await mountSession()
  try {
    const read = session.readMe()
    session.setSessionUser({ id: 2, nickname: 'Second', permission: 1 })
    pending.reject(responseError(401))
    assert.equal(await read, null)
    assert.equal(session.user.value.id, 2)
    assert.deepEqual(notifications, [])
  } finally {
    setUnauthorizedHandler(null)
    app.unmount()
    delete globalThis.__sessionApi
  }
})

test('readMe clears and notifies for a current 401 but preserves network and 5xx errors', async () => {
  storedToken('')
  const notifications = []
  setUnauthorizedHandler(() => notifications.push('notified'))
  const { app, session } = await mountSession()
  try {
    globalThis.__sessionApi = { me: async () => { throw responseError(401) } }
    session.setSessionUser({ id: 1, nickname: 'First', permission: 1 })
    assert.equal(await session.readMe(), null)
    assert.equal(session.user.value, null)
    assert.deepEqual(notifications, ['notified'])

    for (const cause of [new Error('offline'), responseError(500), responseError(503)]) {
      globalThis.__sessionApi = { me: async () => { throw cause } }
      session.setSessionUser({ id: 7, nickname: 'Still signed in', permission: 1 })
      await assert.rejects(session.readMe(), (error) => error === cause)
      assert.equal(session.user.value.id, 7)
    }
    assert.deepEqual(notifications, ['notified'])
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
