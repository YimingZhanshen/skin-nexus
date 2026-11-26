import test, { after } from 'node:test'
import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import { fileURLToPath, pathToFileURL } from 'node:url'
import { compileScript, compileTemplate, parse } from '@vue/compiler-sfc'
import { createRenderer, h, nextTick } from 'vue'
import { createI18n } from 'vue-i18n'
import { createServer } from 'vite'
import en from '../locales/en.json' with { type: 'json' }

const root = fileURLToPath(new URL('../../', import.meta.url))
const stored = new Map()
globalThis.localStorage = {
  getItem: (key) => stored.get(key) || null,
  setItem: (key, value) => stored.set(key, value),
  removeItem: (key) => stored.delete(key),
}
globalThis.location = { pathname: '/', search: '', origin: 'http://localhost', href: 'http://localhost/' }
globalThis.Document = class Document {}
globalThis.ShadowRoot = class ShadowRoot {}

function compile(filename, id) {
  const source = readFileSync(fileURLToPath(new URL(filename, import.meta.url)), 'utf8')
  const { descriptor } = parse(source, { filename })
  const script = compileScript(descriptor, { id })
  const template = compileTemplate({
    source: descriptor.template.content, filename, id,
    compilerOptions: { bindingMetadata: script.bindings, mode: 'module', hoistStatic: false },
  })
  assert.deepEqual(template.errors, [])
  return `${script.content.replace('export default', 'const component =')}\n${template.code}\ncomponent.render = render\nexport default component\n`
}

const compiled = new Map([
  ['/src/pages/DashboardPage.test-compiled.js', compile('./DashboardPage.vue', 'dashboard-session-read-test')],
  ['/src/pages/ProfilePage.test-compiled.js', compile('./ProfilePage.vue', 'profile-session-read-test')],
])
const stubs = new Map()
const sessionPath = fileURLToPath(new URL('./src/session.js', pathToFileURL(root)))
const vite = await createServer({
  root,
  configFile: false,
  server: { middlewareMode: true, hmr: false, ws: false },
  appType: 'custom',
  optimizeDeps: { noDiscovery: true, include: [] },
  plugins: [{
    name: 'page-session-read-test',
    enforce: 'pre',
    resolveId(source) {
      if (compiled.has(source)) return source
      if (source === '../api.js') return '\0page-session-api'
      if (source === '../session.js') return sessionPath
      if (source === '../dialog.js') return '\0page-session-dialog'
      if (source === '../skinModel.js') return '\0page-session-skin-model'
      if (source === '../textureLabel.js') return '\0page-session-texture-label'
      if (source === '../format.js') return '\0page-session-format'
      if (source.endsWith('.vue')) {
        const id = `\0page-session-stub-${source}`
        stubs.set(id, source)
        return id
      }
    },
    load(id) {
      if (compiled.has(id)) return compiled.get(id)
      if (id === '\0page-session-api') {
        return 'export const api = new Proxy({}, { get: (_target, key) => (...args) => globalThis.__pageApi[key](...args) }); export const uploadTexture = (...args) => globalThis.__pageApi.uploadTexture(...args)'
      }
      if (id === '\0page-session-dialog') return 'export async function confirmDialog(){ return true }'
      if (id === '\0page-session-skin-model') return 'export async function detectSkinModel(){ return "default" }'
      if (id === '\0page-session-texture-label') return 'export function nameFromFile(name){ return name }'
      if (id === '\0page-session-format') return 'export function formatBytes(){ return "0 B" }'
      if (stubs.get(id)?.includes('WearTexturePicker.vue')) return 'import { h } from "vue"; export default { setup(_props,{emit}){ return () => h("button", { class: "avatar-pick-action", onClick: () => emit("pick", 42) }, "Pick avatar") } }'
      if (stubs.has(id)) return 'import { h } from "vue"; export default { setup(){ return () => h("div", { class: "page-stub" }, "Stub") } }'
    },
  }],
})
const { default: DashboardPage } = await vite.ssrLoadModule('/src/pages/DashboardPage.test-compiled.js')
const { default: ProfilePage } = await vite.ssrLoadModule('/src/pages/ProfilePage.test-compiled.js')
const { useSession } = await vite.ssrLoadModule('/src/composables/useSession.js')
const { setUnauthorizedHandler } = await vite.ssrLoadModule(pathToFileURL(sessionPath).href)
after(() => vite.close())

function hostNode(type, text = '') {
  return { type, text, props: {}, children: [], parent: null, addEventListener() {}, removeEventListener() {}, getRootNode() { return this } }
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
function descendants(node) { return [node, ...node.children.flatMap(descendants)] }
function find(node, predicate) { return descendants(node).find(predicate) }
function text(node) { return node.text + node.children.map(text).join('') }
async function settle() {
  await new Promise((resolve) => setImmediate(resolve))
  await nextTick()
  await new Promise((resolve) => setImmediate(resolve))
  await nextTick()
}
function deferred() {
  let resolve
  let reject
  const promise = new Promise((res, rej) => { resolve = res; reject = rej })
  return { promise, resolve, reject }
}
function responseError(status) {
  return Object.assign(new Error(`HTTP ${status}`), { status, data: {} })
}
function disposeApp(app) {
  const instance = app._instance
  if (!instance) return
  for (const hook of instance.bum || []) hook()
  instance.scope.stop()
  instance.isUnmounted = true
  app._instance = null
}
async function mountPage(Page, initialUser = { id: 1, nickname: 'First', permission: 0 }) {
  let session
  let unauthorizedCalls = 0
  const app = renderer.createApp({
    setup() {
      session = useSession()
      session.setSessionUser(initialUser)
      setUnauthorizedHandler(() => {
        unauthorizedCalls += 1
        session.setSessionUser(null)
      })
      const pageProps = Page === DashboardPage ? { readMe: session.readMe } : { readMe: session.readMe, mailEnabled: true }
      return () => h('div', [
        h(Page, pageProps),
        h('output', { class: 'current-user' }, session.user.value?.nickname || ''),
      ])
    },
  })
  app.use(createI18n({ legacy: false, locale: 'en', messages: { en }, warnHtmlMessage: false }))
  const container = hostNode('root')
  app.mount(container)
  await settle()
  return { app, container, session, unauthorizedCalls: () => unauthorizedCalls }
}

for (const [name, Page] of [['DashboardPage', DashboardPage], ['ProfilePage', ProfilePage]]) {
  test(`${name} ignores a delayed /me 401 after account replacement`, async () => {
    const pending = deferred()
    globalThis.__pageApi = { me: () => pending.promise }
    const mounted = await mountPage(Page)
    try {
      mounted.session.setSessionUser({ id: 2, nickname: 'Second', permission: 1 })
      pending.reject(responseError(401))
      await settle()
      assert.equal(mounted.unauthorizedCalls(), 0)
      assert.equal(mounted.session.user.value.id, 2)
      assert.equal(text(find(mounted.container, (node) => node.props.class === 'current-user')), 'Second')
    } finally {
      setUnauthorizedHandler(null)
      disposeApp(mounted.app)
      delete globalThis.__pageApi
    }
  })
}

test('ProfilePage ignores a delayed /me 401 after saving the old account profile', async () => {
  const pending = deferred()
  let meCalls = 0
  globalThis.__pageApi = {
    me: () => (++meCalls === 1 ? Promise.resolve({ id: 1, nickname: 'First', permission: 0 }) : pending.promise),
    updateProfile: async () => ({ ok: true }),
  }
  const mounted = await mountPage(ProfilePage)
  try {
    const save = find(mounted.container, (node) => node.type === 'button' && text(node).trim() === en.common.save)
    assert.ok(save)
    const saving = save.props.onClick()
    await settle()
    mounted.session.setSessionUser({ id: 2, nickname: 'Second', permission: 1 })
    pending.reject(responseError(401))
    await saving
    await settle()
    assert.equal(mounted.unauthorizedCalls(), 0)
    assert.equal(mounted.session.user.value.id, 2)
    assert.equal(text(find(mounted.container, (node) => node.props.class === 'current-user')), 'Second')
  } finally {
    setUnauthorizedHandler(null)
    disposeApp(mounted.app)
    delete globalThis.__pageApi
  }
})

for (const [name, method, selector] of [
  ['setting an avatar', 'setAvatar', (node) => node.props.class === 'avatar-pick-action'],
  ['resetting an avatar', 'resetAvatar', (node) => node.type === 'button' && text(node).trim() === en.profile.avatarResetBtn],
]) {
  test(`ProfilePage ignores a delayed /me 401 after ${name} for the old account`, async () => {
    const pending = deferred()
    let meCalls = 0
    globalThis.__pageApi = {
      me: () => (++meCalls === 1 ? Promise.resolve({ id: 1, nickname: 'First', permission: 0, avatarHash: 'old-avatar' }) : pending.promise),
      [method]: async () => ({ ok: true }),
    }
    const mounted = await mountPage(ProfilePage)
    try {
      const action = find(mounted.container, selector)
      assert.ok(action)
      const changing = action.props.onClick()
      await settle()
      mounted.session.setSessionUser({ id: 2, nickname: 'Second', permission: 1 })
      pending.reject(responseError(401))
      await changing
      await settle()
      assert.equal(mounted.unauthorizedCalls(), 0)
      assert.equal(mounted.session.user.value.id, 2)
      assert.equal(text(find(mounted.container, (node) => node.props.class === 'current-user')), 'Second')
    } finally {
      setUnauthorizedHandler(null)
      disposeApp(mounted.app)
      delete globalThis.__pageApi
    }
  })
}
