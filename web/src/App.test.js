import test, { after } from 'node:test'
import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import { fileURLToPath, pathToFileURL } from 'node:url'
import { compileScript, compileTemplate, parse } from '@vue/compiler-sfc'
import { createRenderer, getCurrentInstance, h, nextTick, onBeforeUnmount, ref } from 'vue'
import { createI18n } from 'vue-i18n'
import { createServer } from 'vite'
import en from './locales/en.json' with { type: 'json' }

const root = fileURLToPath(new URL('../', import.meta.url))
const stored = new Map()
globalThis.localStorage = {
  getItem: (key) => stored.get(key) || null,
  setItem: (key, value) => stored.set(key, value),
  removeItem: (key) => stored.delete(key),
}
globalThis.location = { pathname: '/admin/users', search: '', origin: 'http://localhost', href: 'http://localhost/' }
globalThis.history = {
  pushState(_state, _title, path) { globalThis.location.pathname = path },
  replaceState(_state, _title, path) { globalThis.location.pathname = path },
}
globalThis.window = { addEventListener() {}, removeEventListener() {} }
globalThis.__appMe = async () => ({ id: 1, nickname: 'Administrator', permission: 1 })
globalThis.__appLogout = async () => ({ ok: true })
globalThis.fetch = async () => new Response('{}', { status: 200, headers: { 'Content-Type': 'application/json' } })

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
const appCode = compile('./App.vue', 'app-session-cleanup-test')
  .replace(/const (\w+) = asyncPage\(\(\) => import\((['"])[^'"]+\2\)\)/g, 'const $1 = globalThis.__appPages.$1')
assert.doesNotMatch(appCode, /asyncPage\(\(\) => import/)
const realAdminAppCode = appCode.replace('const AdminPanel = globalThis.__appPages.AdminPanel', 'const AdminPanel = globalThis.__appRealAdminPanel')
const adminCode = compile('./components/AdminPanel.vue', 'admin-session-only-test')
const compiled = new Map([
  ['/src/App.test-compiled.js', appCode],
  ['/src/App.real-admin-test-compiled.js', realAdminAppCode],
  ['/src/components/AdminPanel.test-compiled.js', adminCode],
])
const stubs = new Map()

const vite = await createServer({
  root,
  configFile: false,
  server: { middlewareMode: true, hmr: false, ws: false },
  appType: 'custom',
  optimizeDeps: { noDiscovery: true, include: [] },
  plugins: [{
    name: 'app-session-cleanup-test',
    enforce: 'pre',
    resolveId(source, importer) {
      if (compiled.has(source)) return source
      if (source === './App.vue') return '/src/App.test-compiled.js'
      if (source === './AdminPanel.vue') return '/src/components/AdminPanel.test-compiled.js'
      if (source === './composables/useSession.js') return '\0app-use-session'
      if (source === './composables/useGoto.js') return '\0app-use-goto'
      if (source === './composables/useSiteConfig.js') return '\0app-use-site-config'
      if (source === './api.js' || source === '../api.js') return '\0app-api'
      if (source === './errors.js' || source === '../errors.js') return '\0app-errors'
      if (source === './pluginUI.js' || source === '../pluginUI.js') return '\0app-plugin-ui'
      if (source === './i18n.js') return '\0app-i18n'
      if (source === './routes.js') return fileURLToPath(new URL('./src/routes.js', pathToFileURL(root)))
      if (source === './session.js' || source === '../session.js') return fileURLToPath(new URL('./src/session.js', pathToFileURL(root)))
      if (source === '../adminApi.js') return fileURLToPath(new URL('./src/adminApi.js', pathToFileURL(root)))
      if (source.endsWith('.vue')) {
        const id = `\0app-stub-${source}`
        stubs.set(id, source)
        return id
      }
      if (source.endsWith('/useSession.js')) return fileURLToPath(new URL('./src/composables/useSession.js', pathToFileURL(root)))
      if (source.endsWith('/useGoto.js')) return fileURLToPath(new URL('./src/composables/useGoto.js', pathToFileURL(root)))
    },
    load(id) {
      if (compiled.has(id)) return compiled.get(id)
      if (id === '\0app-use-session') return `import { useSession as base } from ${JSON.stringify(pathToFileURL(fileURLToPath(new URL('./src/composables/useSession.js', pathToFileURL(root)))).href)}; export function useSession(){ const session = base(); globalThis.__appSession = session; return session }`
      if (id === '\0app-use-goto') return `import { useGoto as base } from ${JSON.stringify(pathToFileURL(fileURLToPath(new URL('./src/composables/useGoto.js', pathToFileURL(root)))).href)}; export function useGoto(args){ const goto = base(args); globalThis.__appGoto = goto; return goto }`
      if (id === '\0app-use-site-config') return `import { ref, computed } from 'vue'; export function useSiteConfig(){ const siteConf=ref({serveraccess_enabled:true}); return { siteConf, confLoaded:ref(true), normalizeFooter:(v)=>v, siteFooter:computed(()=>''), siteName:computed(()=>'SkinNexus'), inviteFieldShown:ref(false), inviteIsRequired:ref(false), mailEnabled:ref(true), verifyGateEnabled:ref(false), emailFieldShown:ref(true), unionEnabled:ref(false), oidcEnabled:ref(false), serverAccessEnabled:computed(()=>siteConf.value.serveraccess_enabled) } }`
      if (id === '\0app-api') return 'export const api = new Proxy({}, { get: (_target,key) => key === "me" ? () => globalThis.__appMe() : key === "logout" ? () => globalThis.__appLogout() : async () => ({}) }); export const gateWatch = {}'
      if (id === '\0app-errors') return 'export function transportError(){return "network"}; export function describe(data,status){return data.message || `HTTP ${status}`}'
      if (id === '\0app-plugin-ui') return `export async function loadPluginUI(){}; export function navPages(){return []}; export function pageByPath(){return null}; export function pluginLabel(_plugin,_key,fallback){return fallback || 'Plugin'}`
      if (id === '\0app-i18n') return 'export function setLocale(){}'
      if (stubs.has(id)) {
        const source = stubs.get(id)
        if (source.includes('TopBar.vue')) return 'import { h } from "vue"; export default { emits:["logout"], setup(_p,{emit}){return ()=>h("button",{class:"test-logout",onClick:()=>emit("logout")},"Log out")} }'
        if (source.includes('AdminPanel.vue')) return 'import { h } from "vue"; export default { props:["user"], setup(props){return ()=>h("section",{class:"admin-surface",permission:props.user?.permission},"Admin controls")} }'
        if (source.includes('LoginCard.vue')) return 'import { h } from "vue"; export default { setup(){return ()=>h("section",{class:"login-surface"},"Login")} }'
        if (source.includes('DashboardView.vue')) return 'import { h } from "vue"; export default { setup(){return ()=>h("div",{class:"admin-dashboard"},"Admin dashboard")} }'
        return 'import { h } from "vue"; export default { setup(){return ()=>h("div",{class:"stub-view"},"Stub")} }'
      }
    },
  }],
})
const { default: AdminPanel } = await vite.ssrLoadModule('/src/components/AdminPanel.test-compiled.js')
globalThis.__appRealAdminPanel = AdminPanel
const pageNames = ['AdminPanel', 'DevicePage', 'AuthorizedAppsPage', 'NotificationsPage', 'MyReportsPage', 'UnionBindPage', 'ServersPage', 'SetupPage', 'DashboardPage', 'ClosetPage', 'SkinlibPage', 'PlayersPage', 'ProfilePage', 'HelpPage', 'PluginPage']
const ServerPageStub = {
  setup() {
    const lifecycle = globalThis.__appServerLifecycle = { disposed: false, actions: 0 }
    globalThis.__appServerInstance = getCurrentInstance()
    onBeforeUnmount(() => { lifecycle.disposed = true })
    return () => h('section', { class: 'tenant-surface' }, [
      h('span', {}, 'Tenant controls'),
      h('button', { class: 'tenant-action', onClick: () => { if (!lifecycle.disposed) lifecycle.actions++ } }, 'Create server'),
    ])
  },
}
globalThis.__appPages = Object.fromEntries(pageNames.map((name) => [name, name === 'AdminPanel' ? {
  props: ['user'],
  setup: (props) => () => h('section', { class: 'admin-surface', permission: props.user?.permission }, 'Admin controls'),
} : name === 'ServersPage' ? ServerPageStub : {
  setup: () => () => h('div', { class: 'stub-view' }, 'Stub'),
}]))
const { default: App } = await vite.ssrLoadModule('/src/App.test-compiled.js')
const { default: RealAdminApp } = await vite.ssrLoadModule('/src/App.real-admin-test-compiled.js')
const sessionURL = pathToFileURL(fileURLToPath(new URL('./session.js', import.meta.url))).href
const { notifyUnauthorized, setUnauthorizedHandler } = await vite.ssrLoadModule(sessionURL)
after(() => vite.close())

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
  nextSibling: (node) => node.parent?.children[node.parent.children.indexOf(node) + 1] || null,
  insert: (child, parent, anchor = null) => {
    if (child.parent) child.parent.children.splice(child.parent.children.indexOf(child), 1)
    const index = anchor ? parent.children.indexOf(anchor) : parent.children.length
    parent.children.splice(index < 0 ? parent.children.length : index, 0, child)
    child.parent = parent
  },
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
async function mountApp(me = async () => ({ id: 1, nickname: 'Administrator', permission: 1 }), path = '/admin/users', { realAdminPanel = false } = {}) {
  globalThis.__appMe = me
  globalThis.location.pathname = path
  globalThis.location.search = ''
  stored.set('admin_token', 'legacy-machine-token')
  const app = renderer.createApp(realAdminPanel ? RealAdminApp : App)
  const errors = []
  app.config.errorHandler = (error) => errors.push(error)
  app.use(createI18n({ legacy: false, locale: 'en', messages: { en }, warnHtmlMessage: false }))
  const container = hostNode('root')
  app.mount(container)
  await settle()
  const expectedClass = globalThis.__appSession?.user.value ? 'admin-surface' : 'login-surface'
  for (let i = 0; i < 20 && !find(container, (node) => node.props.class === expectedClass); i++) await settle()
  if (errors.length) throw errors[0]
  return { app, container, errors }
}

// The synthetic renderer cannot unmount Vue's optimized fragment blocks after a
// branch swap. Stop the scope and run before-unmount hooks directly so shell
// lifecycle assertions do not depend on that unrelated renderer limitation.
function disposeApp(app) {
  const instance = app._instance
  if (!instance) return
  for (const hook of instance.bum || []) hook()
  instance.scope.stop()
  instance.isUnmounted = true
  app._instance = null
}

test('App boot removes legacy admin tokens and disposes tenant controls on session loss', async () => {
  const { app, container } = await mountApp(undefined, '/servers')
  try {
    assert.equal(stored.has('admin_token'), false)
    const session = globalThis.__appSession
    const goto = globalThis.__appGoto
    session.setSessionUser({ id: 2, nickname: 'Other', permission: 1 })
    await settle()
    assert.ok(find(container, (node) => node.props.class === 'tenant-surface'), text(container))
    assert.equal(globalThis.__appServerLifecycle.disposed, false)
    const tenantAction = find(container, (node) => node.props.class === 'tenant-action')
    goto.extPath.value = '/ext/example'
    goto.pendingExt.value = '/ext/pending'
    goto.pendingView.value = 'admin'
    goto.menuOpen.value = true
    session.pendingBusy.value = true
    session.pendingNote.value = 'sent'
    session.pendingChangeOpen.value = true
    session.pendingEmail.value = 'old@example.test'
    session.verifyMailTo.value = 'old@example.test'
    session.verifyMailBusy.value = true
    session.verifyMailNote.value = 'sent'
    session.authErr.value = 'old error'
    session.authBusy.value = true
    notifyUnauthorized()
    await settle()
    assert.equal(session.user.value, null)
    assert.equal(goto.view.value, 'login')
    assert.equal(goto.extPath.value, '')
    assert.equal(goto.pendingExt.value, '')
    assert.equal(goto.pendingView.value, '')
    assert.equal(goto.menuOpen.value, false)
    assert.equal(session.pendingBusy.value, false)
    assert.equal(session.pendingNote.value, '')
    assert.equal(session.pendingChangeOpen.value, false)
    assert.equal(session.pendingEmail.value, '')
    assert.equal(session.verifyMailTo.value, '')
    assert.equal(session.verifyMailBusy.value, false)
    assert.equal(session.verifyMailNote.value, '')
    assert.equal(session.authErr.value, '')
    assert.equal(session.authBusy.value, false)
    assert.equal(globalThis.location.pathname, '/')
    assert.ok(find(container, (node) => node.props.class === 'login-surface'))
    assert.equal(globalThis.__appServerLifecycle.disposed, true)
    tenantAction.props.onClick()
    assert.equal(globalThis.__appServerLifecycle.actions, 0)
  } finally {
    disposeApp(app)
  }
})

test('App logout clears the user, route residue, and legacy token even if logout fails', async () => {
  const { app, container, errors } = await mountApp()
  globalThis.__appLogout = async () => { throw new Error('offline') }
  stored.set('admin_token', 'legacy-machine-token')
  try {
    globalThis.__appGoto.extPath.value = '/ext/example'
    globalThis.__appGoto.pendingView.value = 'admin'
    globalThis.__appGoto.menuOpen.value = true
    const logout = find(container, (node) => node.props.class === 'test-logout')
    await logout.props.onClick()
    await settle()
    if (errors.length) throw errors[0]
    assert.equal(globalThis.__appSession.user.value, null)
    assert.equal(globalThis.__appGoto.view.value, 'login')
    assert.equal(globalThis.__appGoto.extPath.value, '')
    assert.equal(globalThis.__appGoto.pendingView.value, '')
    assert.equal(globalThis.__appGoto.menuOpen.value, false)
    assert.equal(stored.has('admin_token'), false)
    assert.equal(globalThis.location.pathname, '/')
  } finally {
    globalThis.__appLogout = async () => ({ ok: true })
    disposeApp(app)
  }
})

test('App unregisters its unauthorized handler on unmount', async () => {
  const calls = []
  setUnauthorizedHandler(() => calls.push('external'))
  const { app } = await mountApp()
  try {
    disposeApp(app)
    notifyUnauthorized()
    assert.deepEqual(calls, ['external'])
  } finally {
    setUnauthorizedHandler(null)
    if (app._instance) disposeApp(app)
  }
})

test('non-admin users on a non-admin route do not mount admin controls', async () => {
  const { app, container } = await mountApp(async () => ({ id: 2, nickname: 'Member', permission: 0 }), '/')
  try {
    assert.equal(find(container, (node) => node.props.class === 'admin-surface'), undefined)
    assert.ok(find(container, (node) => node.props.class === 'app-shell'))
  } finally {
    disposeApp(app)
  }
})

test('App keeps the AdminPanel forbidden surface when admin permission is lost', async () => {
  const { app, container, errors } = await mountApp(undefined, '/admin', { realAdminPanel: true })
  try {
    assert.equal(globalThis.__appSession.user.value?.permission, 1)
    assert.equal(globalThis.__appGoto.view.value, 'admin')
    assert.ok(find(container, (node) => node.props.class === 'admin-shell'), text(container))
    assert.ok(find(container, (node) => node.props.class === 'admin-dashboard'), text(container))
    globalThis.__appSession.setSessionUser({ id: 1, nickname: 'Former admin', permission: 0 })
    await settle()
    assert.equal(globalThis.__appGoto.view.value, 'admin')
    assert.ok(find(container, (node) => node.props.class === 'admin-shell'))
    assert.equal(find(container, (node) => node.props.class === 'admin-dashboard'), undefined)
    const main = find(container, (node) => node.props.class === 'admin-main')
    assert.ok(text(main).includes(en.errors.common.forbidden))
    assert.deepEqual(errors, [])
  } finally {
    disposeApp(app)
  }
})

test('AdminPanel has no token input and its ready surface follows current permission', async () => {
  globalThis.location.pathname = '/admin'
  stored.set('admin_token', 'legacy-machine-token')
  const user = ref({ id: 1, nickname: 'Administrator', permission: 1 })
  const errors = []
  const container = hostNode('root')
  // Mount through a reactive host so prop changes exercise AdminPanel's computed readiness.
  const PanelHost = { setup: () => () => h(AdminPanel, { user: user.value, siteConf: {}, confLoaded: true }) }
  const panel = renderer.createApp(PanelHost)
  panel.config.errorHandler = (error) => errors.push(error)
  panel.use(createI18n({ legacy: false, locale: 'en', messages: { en }, warnHtmlMessage: false }))
  panel.mount(container)
  await settle()
  try {
    assert.equal(find(container, (node) => node.type === 'input' && node.props.type === 'password'), undefined)
    assert.ok(find(container, (node) => node.props.class === 'admin-dashboard'))
    assert.equal(stored.has('admin_token'), false)
    user.value = { ...user.value, permission: 0 }
    await settle()
    assert.equal(find(container, (node) => node.props.class === 'admin-dashboard'), undefined)
    user.value = { ...user.value, permission: 2 }
    await settle()
    assert.ok(find(container, (node) => node.props.class === 'admin-dashboard'))
  } finally {
    panel.unmount()
  }
  assert.deepEqual(errors, [])
})
