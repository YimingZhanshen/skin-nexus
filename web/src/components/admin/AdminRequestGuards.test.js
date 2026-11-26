import test from 'node:test'
import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import { fileURLToPath, pathToFileURL } from 'node:url'
import { compileScript, compileTemplate, parse } from '@vue/compiler-sfc'
import { createRenderer, nextTick } from 'vue'
import { createI18n } from 'vue-i18n'
import { createServer } from 'vite'
import en from '../../locales/en.json' with { type: 'json' }
import { useServerAccessPlatform } from '../../composables/useServerAccessPlatform.js'

const root = fileURLToPath(new URL('../../../', import.meta.url))
const fixtures = [
  { name: 'UsersView', file: 'UsersView.vue', list: 'users', error: 'error', response: (id) => ({ users: [{ id }], total: 101 }) },
  { name: 'PlayersView', file: 'PlayersView.vue', list: 'players', error: 'error', response: (id) => ({ players: [{ id, name: `Player ${id}` }], total: 101 }) },
  { name: 'TexturesView', file: 'TexturesView.vue', list: 'rows', error: 'error', response: (id) => ({ textures: [{ id, name: `Texture ${id}`, hash: `hash-${id}` }], total: 101 }) },
  { name: 'AccessLogsView', file: 'AccessLogsView.vue', list: 'logs', error: 'error', response: (id) => ({ logs: [{ id, kind: 'web_login', outcome: 'ok' }], total: 101 }) },
  { name: 'ReportsView', file: 'ReportsView.vue', list: 'reports', error: 'error', response: (id) => ({ reports: [{ id, textureId: id, reason: 'other', status: 'open' }], total: 101 }) },
  { name: 'AuditView', file: 'AuditView.vue', list: 'rows', error: 'error', response: (id) => ({ audit: [{ id, action: 'test.action' }], total: 101, actions: [] }) },
  { name: 'ServerAccessAdminView', file: 'ServerAccessAdminView.vue', platform: true },
  { name: 'OidcClientsCard', file: 'OidcClientsCard.vue', standalone: true },
]
const modules = new Map()
const virtualModules = new Map([
  ['\0admin-request-test-api', 'export const fetchJSON = (...args) => globalThis.__adminRequestFetch(...args)'],
  ['\0admin-request-test-me', 'export const api = { me: async () => ({ permission: 2 }) }; export const serverAccessApi = new Proxy({}, { get: (_target, key) => globalThis.__adminRequestFetch[key] })'],
  ['\0admin-request-test-dialog', 'export const confirmDialog = async () => true; export const promptDialog = async () => null; export const alertDialog = async () => {}'],
  ['\0admin-request-test-pager', 'import { h } from "vue"; export default { props: ["page", "pages", "total"], emits: ["update:page"], setup: (props, { emit }) => () => h("div", { class: "test-pager", page: props.page, pages: props.pages, total: props.total, onUpdatePage: (page) => emit("update:page", page) }) }'],
  ['\0admin-request-test-empty', 'import { h } from "vue"; export default { props: ["text"], setup: (props) => () => h("div", { class: "test-empty" }, props.text) }'],
])

for (const fixture of fixtures) {
  const filename = fileURLToPath(new URL(`./${fixture.file}`, import.meta.url))
  const { descriptor } = parse(readFileSync(filename, 'utf8'), { filename })
  const script = compileScript(descriptor, { id: `admin-request-${fixture.name}` })
  const template = compileTemplate({
    source: descriptor.template.content,
    filename,
    id: `admin-request-${fixture.name}`,
    compilerOptions: { bindingMetadata: script.bindings, mode: 'module' },
  })
  assert.deepEqual(template.errors, [])
  const url = `/src/components/admin/${fixture.name}.request-test-compiled.js`
  modules.set(url, `${script.content.replace('export default', 'const component =')}\n${template.code}\ncomponent.render = render\nexport default component\n`)
  fixture.url = url
}

const vite = await createServer({
  root,
  configFile: false,
  server: { middlewareMode: true, hmr: false, ws: false },
  appType: 'custom',
  optimizeDeps: { noDiscovery: true, include: [] },
  plugins: [{
    name: 'admin-request-guards',
    enforce: 'pre',
    resolveId(source) {
      if (modules.has(source)) return source
      if (source.endsWith('/adminApi.js')) return '\0admin-request-test-api'
      if (source.endsWith('/api.js') || source.endsWith('/serverAccessApi.js')) return '\0admin-request-test-me'
      if (source.endsWith('/dialog.js')) return '\0admin-request-test-dialog'
      if (source === '../Pager.vue') return '\0admin-request-test-pager'
      if (source === '../EmptyState.vue') return '\0admin-request-test-empty'
      if (source.endsWith('/useServerAccessPlatform.js')) return fileURLToPath(new URL(source, pathToFileURL(fileURLToPath(new URL('./ServerAccessAdminView.vue', import.meta.url)))))
    },
    load(id) {
      if (modules.has(id)) return modules.get(id)
      if (virtualModules.has(id)) return virtualModules.get(id)
    },
  }],
})
for (const fixture of fixtures) fixture.component = (await vite.ssrLoadModule(fixture.url)).default
await vite.close()

function hostNode(type, text = '') {
  return {
    type, text, props: {}, children: [], parent: null,
    get options() { return this.children.filter((child) => child.type === 'option') },
    addEventListener() {}, removeEventListener() {},
  }
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
  patchProp: (node, key, _previous, value) => {
    node.props[key] = value
    if (key === 'value' || key === 'checked') node[key] = value
  },
})
function descendants(node) { return [node, ...node.children.flatMap(descendants)] }
function find(node, predicate) { return descendants(node).find(predicate) }
function text(node) { return node.text + node.children.map(text).join('') }
function button(node, label) { return find(node, (child) => child.type === 'button' && text(child) === label) }
function pager(container) { return find(container, (node) => node.props.class === 'test-pager') }
function rowId(state, fixture) { return state[fixture.list]?.[0]?.id ?? null }
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
async function mount(fixture, fetchHandler) {
  globalThis.__adminRequestFetch = fetchHandler
  const app = renderer.createApp(fixture.component)
  const errors = []
  app.config.errorHandler = (error) => errors.push(error)
  app.use(createI18n({ legacy: false, locale: 'en', messages: { en }, warnHtmlMessage: false }))
  const container = hostNode('root')
  app.mount(container)
  await settle()
  if (errors.length) throw errors[0]
  return { app, container, state: app._instance.setupState, errors }
}
function cleanup(app) {
  app.unmount()
  delete globalThis.__adminRequestFetch
}
function responseFor(fixture, path, id) {
  if (path === '/api/admin/v1/access-log/detail-mode') return { enabled: false }
  return fixture.response(id)
}
function startPageOne(fixture, container) {
  if (fixture.name === 'ReportsView') button(container, 'All').props.onClick()
  else if (fixture.name === 'TexturesView' || fixture.name === 'AuditView') button(container, 'Reload').props.onClick()
  else button(container, 'Search').props.onClick()
}
function setSearch(fixture, container, value) {
  if (fixture.name === 'ReportsView') {
    button(container, value).props.onClick()
    return
  }
  const input = find(container, (node) => node.type === 'input' && (
    node.props.onInput || node.props.onKeyup || (node.props['onUpdate:modelValue'] && !node.props.type)
  ))
  assert.ok(input, `${fixture.name} search input`)
  if (input.props.onInput) input.props.onInput(value)
  else if (input.props['onUpdate:modelValue']) input.props['onUpdate:modelValue'](value)
  if (input.props.onKeyup) input.props.onKeyup({ key: 'Enter' })
  else if (!input.props.onInput) button(container, 'Search').props.onClick()
}

for (const fixture of fixtures.filter((item) => !item.platform && !item.standalone)) {
  test(`${fixture.name}: a late page 1 response cannot replace page 2`, async () => {
    const page2 = deferred()
    const latePage1 = deferred()
    const calls = []
    const { app, container, state } = await mount(fixture, (path) => {
      if (path === '/api/admin/v1/access-log/detail-mode') return { enabled: false }
      const page = new URL(path, 'https://example.test').searchParams.get('page')
      calls.push([path, page])
      if (page === '2') return page2.promise
      if (calls.filter(([, value]) => value === '1').length === 2) return latePage1.promise
      return responseFor(fixture, path, 1)
    })
    try {
      assert.equal(rowId(state, fixture), 1)
      startPageOne(fixture, container)
      await settle()
      pager(container).props.onUpdatePage(2)
      await settle()
      page2.resolve(responseFor(fixture, '', 2))
      await settle()
      assert.equal(rowId(state, fixture), 2)
      latePage1.resolve(responseFor(fixture, '', 1))
      await settle()
      assert.equal(rowId(state, fixture), 2)
      assert.equal(pager(container).props.page, 2)
    } finally { cleanup(app) }
  })

  test(`${fixture.name}: a new search or filter cannot be overwritten by the old one`, async (context) => {
    if (fixture.name === 'TexturesView' || fixture.name === 'AuditView') context.mock.timers.enable({ apis: ['setTimeout'] })
    const oldSearch = deferred()
    const newSearch = deferred()
    const calls = []
    const { app, container, state } = await mount(fixture, (path) => {
      if (path === '/api/admin/v1/access-log/detail-mode') return { enabled: false }
      calls.push(path)
      if (calls.length === 2) return oldSearch.promise
      if (calls.length === 3) return newSearch.promise
      return responseFor(fixture, path, 1)
    })
    try {
      setSearch(fixture, container, fixture.name === 'ReportsView' ? 'All' : 'old')
      if (fixture.name === 'TexturesView' || fixture.name === 'AuditView') context.mock.timers.tick(300)
      await settle()
      setSearch(fixture, container, fixture.name === 'ReportsView' ? 'Open' : 'new')
      if (fixture.name === 'TexturesView' || fixture.name === 'AuditView') context.mock.timers.tick(300)
      await settle()
      newSearch.resolve(responseFor(fixture, '', 2))
      await settle()
      assert.equal(rowId(state, fixture), 2)
      oldSearch.resolve(responseFor(fixture, '', 1))
      await settle()
      assert.equal(rowId(state, fixture), 2)
    } finally { cleanup(app) }
  })

  test(`${fixture.name}: unmount prevents late success and rejection from publishing state`, async () => {
    for (const outcome of ['success', 'rejection']) {
      const pending = deferred()
      const { app, state } = await mount(fixture, (path) => {
        if (path === '/api/admin/v1/access-log/detail-mode') return { enabled: false }
        return pending.promise
      })
      const before = {
        list: state[fixture.list],
        error: state[fixture.error],
        loading: state.loading,
        total: state.total,
      }
      app.unmount()
      if (outcome === 'success') pending.resolve(responseFor(fixture, '', 99))
      else pending.reject(new Error('late failure'))
      await settle()
      assert.deepEqual({ list: state[fixture.list], error: state[fixture.error], loading: state.loading, total: state.total }, before,
        `${outcome} after unmount must not publish`)
      delete globalThis.__adminRequestFetch
    }
  })
}

for (const name of ['TexturesView', 'AuditView']) {
  test(`${name}: unmount clears the owned search debounce timer`, async (context) => {
    context.mock.timers.enable({ apis: ['setTimeout'] })
    const fixture = fixtures.find((item) => item.name === name)
    const calls = []
    const { app, container } = await mount(fixture, (path) => { calls.push(path); return fixture.response(1) })
    try {
      const input = find(container, (node) => node.type === 'input' && node.props.onInput)
      input.props.onInput('pending search')
      assert.equal(calls.length, 1)
      app.unmount()
      context.mock.timers.tick(300)
      assert.equal(calls.length, 1, 'a debounced load must not start after unmount')
    } finally { cleanup(app) }
  })
}

for (const trigger of ['page', 'filter']) {
  test(`AccessLogsView: old rows cannot start details while a new ${trigger} is pending`, async () => {
    const fixture = fixtures.find((item) => item.name === 'AccessLogsView')
    const pendingPage = deferred()
    const detailUrls = []
    let listCalls = 0
    const { app, container, state } = await mount(fixture, (path) => {
      if (path === '/api/admin/v1/access-log/detail-mode') return { enabled: false }
      if (path.startsWith('/api/admin/v1/access-logs?')) {
        listCalls++
        if (listCalls === 2) return pendingPage.promise
        return fixture.response(listCalls === 1 ? 11 : 22)
      }
      if (path.startsWith('/api/admin/v1/access-log/details?')) {
        detailUrls.push(path)
        return { rows: [{ id: 22, status_code: 201 }], limited: false }
      }
      throw new Error(`unexpected request: ${path}`)
    })
    try {
      assert.equal(rowId(state, fixture), 11)
      if (trigger === 'page') pager(container).props.onUpdatePage(2)
      else setSearch(fixture, container, 'new filter')
      await settle()
      assert.equal(rowId(state, fixture), 11, 'the old rows remain visible while loading')

      button(container, 'Show detail').props.onClick()
      await settle()
      assert.deepEqual(detailUrls, [], 'details must not start against the old visible rows')
      assert.deepEqual(state.expanded, {}, 'old rows must not expand while their page is pending')

      pendingPage.resolve(fixture.response(22))
      await settle()
      button(container, 'Show detail').props.onClick()
      await settle()
      assert.equal(detailUrls.length, 1)
      assert.equal(new URL(detailUrls[0], 'https://example.test').searchParams.get('ids'), '22')
      assert.equal(state.details[22].status_code, 201)
    } finally { cleanup(app) }
  })
}

function userResponse(id, nickname = `User ${id}`, emailVerified = 0) {
  return { users: [{ id, nickname, email: `${id}@example.test`, emailVerified, permission: 0 }], total: 101 }
}

function setModelValue(node, value) {
  assert.ok(node, 'model input')
  if (node.props['onUpdate:modelValue']) node.props['onUpdate:modelValue'](value)
  else node.props.onInput({ target: { value } })
}

test('UsersView: a successful inline PUT invalidates an older list snapshot and reloads canonical state', async () => {
  const fixture = fixtures.find((item) => item.name === 'UsersView')
  const staleList = deferred()
  const canonicalList = deferred()
  const pendingPut = deferred()
  const puts = []
  let listCalls = 0
  const { app, container, state } = await mount(fixture, (path, options) => {
    if (path.startsWith('/api/admin/v1/users?')) {
      listCalls++
      if (listCalls === 1) return userResponse(1, 'Before')
      if (listCalls === 2) return staleList.promise
      return canonicalList.promise
    }
    if (path === '/api/admin/v1/users/1/nickname' && options?.method === 'PUT') {
      puts.push(JSON.parse(options.body))
      return pendingPut.promise
    }
    throw new Error(`unexpected request: ${path}`)
  })
  try {
    button(container, 'Search').props.onClick()
    await settle()
    assert.equal(listCalls, 2)
    const nickname = find(container, (node) => node.type === 'input' && node.props.class === 'cell-input')
    setModelValue(nickname, 'Draft')
    await nextTick()
    nickname.props.onBlur()
    await settle()
    assert.deepEqual(puts, [{ nickname: 'Draft' }])

    pendingPut.resolve({})
    await settle()
    assert.equal(listCalls, 3, 'mutation success must start a newer canonical list generation')

    staleList.resolve(userResponse(1, 'Stale snapshot'))
    await settle()
    assert.equal(state.users[0].nickname, 'Draft', 'the old snapshot may keep the pre-reload row but cannot publish stale state')
    canonicalList.resolve(userResponse(1, 'Draft'))
    await settle()
    assert.equal(state.users[0].nickname, 'Draft')
  } finally { cleanup(app) }
})

test('UsersView: a PUT finishing after a page change never patches the stale row and reloads the current page', async () => {
  const fixture = fixtures.find((item) => item.name === 'UsersView')
  const pendingPut = deferred()
  const pageTwo = deferred()
  const currentReload = deferred()
  const listUrls = []
  let pageTwoCalls = 0
  const { app, container, state } = await mount(fixture, (path, options) => {
    if (path.startsWith('/api/admin/v1/users?')) {
      listUrls.push(path)
      if (listUrls.length === 1) return userResponse(1)
      pageTwoCalls++
      return pageTwoCalls === 1 ? pageTwo.promise : currentReload.promise
    }
    if (path === '/api/admin/v1/users/1/verification' && options?.method === 'PUT') return pendingPut.promise
    throw new Error(`unexpected request: ${path}`)
  })
  try {
    const staleRow = state.users[0]
    find(container, (node) => node.type === 'button' && node.props.class === 'veri-btn').props.onClick()
    await settle()
    pager(container).props.onUpdatePage(2)
    await settle()
    pageTwo.resolve(userResponse(2))
    await settle()
    assert.equal(state.users[0].id, 2)

    pendingPut.resolve({})
    await settle()
    assert.equal(staleRow.emailVerified, 0, 'a detached row must not be patched')
    assert.equal(listUrls.length, 3, 'the current page must be refreshed after the stale-page mutation')
    assert.equal(new URL(listUrls[2], 'https://example.test').searchParams.get('page'), '2')

    currentReload.resolve(userResponse(2))
    await settle()
    assert.equal(state.users[0].id, 2)
  } finally { cleanup(app) }
})

test('OidcClientsCard: a successful DELETE reloads past a list request started while it was pending', async () => {
  const fixture = fixtures.find((item) => item.name === 'OidcClientsCard')
  const pendingDelete = deferred()
  const staleList = deferred()
  const canonicalList = deferred()
  let listCalls = 0
  const { app, container, state } = await mount(fixture, (path, options) => {
    if (path === '/api/admin/v1/oidc/clients') {
      if (options?.method === 'POST') return { secret: '' }
      listCalls++
      if (listCalls === 1) return { clients: [{ client_id: 'old', name: 'Old', public: true }] }
      if (listCalls === 2) return staleList.promise
      return canonicalList.promise
    }
    if (path === '/api/admin/v1/oidc/clients/old' && options?.method === 'DELETE') return pendingDelete.promise
    throw new Error(`unexpected request: ${path}`)
  })
  try {
    button(container, 'Delete').props.onClick()
    await settle()
    const clientId = find(container, (node) => node.type === 'input' && node.props.placeholder === 'client_id')
    setModelValue(clientId, 'new')
    await nextTick()
    button(container, 'Create').props.onClick()
    await settle()
    assert.equal(listCalls, 2)

    pendingDelete.resolve({})
    await settle()
    assert.equal(listCalls, 3, 'DELETE success must start a newer canonical list generation')

    staleList.resolve({ clients: [{ client_id: 'old', name: 'Old', public: true }] })
    await settle()
    canonicalList.resolve({ clients: [{ client_id: 'new', name: 'new', public: true }] })
    await settle()
    assert.deepEqual(state.oidcClients.map((client) => client.client_id), ['new'])
  } finally { cleanup(app) }
})

test('AccessLogsView: page details inherit the page epoch and cannot overwrite a newer selection', async () => {
  const fixture = fixtures.find((item) => item.name === 'AccessLogsView')
  const oldPage = deferred()
  const oldDetails = deferred()
  const newDetails = deferred()
  let listCalls = 0
  let detailCalls = 0
  const { app, container, state } = await mount(fixture, (path) => {
    if (path === '/api/admin/v1/access-log/detail-mode') return { enabled: false }
    if (path.startsWith('/api/admin/v1/access-logs?')) {
      listCalls++
      if (listCalls === 2) return oldPage.promise
      return fixture.response(listCalls === 1 ? 11 : 22)
    }
    if (path.startsWith('/api/admin/v1/access-log/details?')) {
      detailCalls++
      return detailCalls === 1 ? oldDetails.promise : newDetails.promise
    }
    throw new Error(`unexpected request: ${path}`)
  })
  try {
    button(container, 'Show detail').props.onClick()
    await settle()
    setSearch(fixture, container, 'old selection')
    await settle()
    setSearch(fixture, container, 'new selection')
    await settle()
    assert.equal(rowId(state, fixture), 22)
    button(container, 'Show detail').props.onClick()
    await settle()
    newDetails.resolve({ rows: [{ id: 22, status_code: 201 }], limited: false })
    await settle()
    assert.match(text(container), /201/)
    oldPage.resolve(fixture.response(11))
    oldDetails.resolve({ rows: [{ id: 11, status_code: 503 }], limited: true })
    await settle()
    assert.equal(rowId(state, fixture), 22)
    assert.match(text(container), /201/)
    assert.doesNotMatch(text(container), /503/)
  } finally { cleanup(app) }
})

test('AccessLogsView: a stale detail rejection cannot replace current detail state', async () => {
  const fixture = fixtures.find((item) => item.name === 'AccessLogsView')
  const oldDetails = deferred()
  let detailCalls = 0
  const { app, container, state } = await mount(fixture, (path) => {
    if (path === '/api/admin/v1/access-log/detail-mode') return { enabled: false }
    if (path.startsWith('/api/admin/v1/access-logs?')) return fixture.response(1)
    if (path.startsWith('/api/admin/v1/access-log/details?')) return ++detailCalls === 1 ? oldDetails.promise : Promise.resolve({ rows: [{ id: 1, status_code: 204 }] })
    throw new Error(`unexpected request: ${path}`)
  })
  try {
    button(container, 'Show detail').props.onClick()
    await settle()
    setSearch(fixture, container, 'current')
    await settle()
    button(container, 'Show detail').props.onClick()
    await settle()
    assert.equal(state.details[1].status_code, 204)
    oldDetails.reject(new Error('stale detail failure'))
    await settle()
    assert.equal(state.detailsError, '')
    assert.equal(state.detailsBusy, false)
    assert.equal(state.detailsLoaded, true)
    assert.equal(state.details[1].status_code, 204)
  } finally { cleanup(app) }
})

function platformApi(overrides = {}) {
  return {
    platformTenants: async () => ({ tenants: [], total: 0 }),
    platformServers: async () => ({ servers: [], total: 0 }),
    platformAudit: async () => ({ audit: [], total: 0 }),
    platformFallback: async () => ({ enabled: false }),
    ...overrides,
  }
}

const platformFamilies = [
  { name: 'tenants', key: 'tenants', search: 'searchTenants', api: 'platformTenants', field: 'tenants', marker: (id) => ({ id, name: `Tenant ${id}` }) },
  { name: 'servers', key: 'servers', search: 'searchServers', api: 'platformServers', field: 'servers', marker: (id) => ({ id, name: `Server ${id}` }) },
  { name: 'audit', key: 'audit', search: 'searchAudit', api: 'platformAudit', field: 'audit', marker: (id) => ({ id, action: `audit.${id}` }) },
]
for (const family of platformFamilies) {
  test(`useServerAccessPlatform: ${family.name} search keeps the newest generation`, async () => {
    const oldSearch = deferred()
    const newSearch = deferred()
    let calls = 0
    const platform = useServerAccessPlatform(platformApi({ [family.api]: async () => ++calls === 1 ? oldSearch.promise : newSearch.promise }))
    const oldLoad = platform[family.search]('old')
    const newLoad = platform[family.search]('new')
    newSearch.resolve({ [family.field]: [family.marker(2)], total: 1 })
    assert.equal(await newLoad, true)
    oldSearch.resolve({ [family.field]: [family.marker(1)], total: 1 })
    assert.equal(await oldLoad, false)
    assert.deepEqual(platform[family.key].value, [family.marker(2)])
  })
}

test('useServerAccessPlatform: fallback uses its own request generation', async () => {
  const first = deferred()
  const second = deferred()
  let calls = 0
  const platform = useServerAccessPlatform(platformApi({ platformFallback: async () => ++calls === 1 ? first.promise : second.promise }))
  const firstLoad = platform.load()
  const secondLoad = platform.load()
  second.resolve({ enabled: false })
  await secondLoad
  first.resolve({ enabled: true })
  await firstLoad
  assert.equal(platform.fallback.value, false)
})

test('useServerAccessPlatform: a fallback mutation reloads past a newer in-flight fallback snapshot', async () => {
  const pendingMutation = deferred()
  const staleFallback = deferred()
  const canonicalFallback = deferred()
  let fallbackCalls = 0
  let auditCalls = 0
  const platform = useServerAccessPlatform(platformApi({
    platformFallback: async () => {
      fallbackCalls++
      if (fallbackCalls === 1) return { enabled: false }
      if (fallbackCalls === 2) return staleFallback.promise
      return canonicalFallback.promise
    },
    platformAudit: async () => { auditCalls++; return { audit: [], total: 0 } },
    setPlatformFallback: async () => pendingMutation.promise,
  }))
  try {
    await platform.load()
    const mutation = platform.setFallback(true)
    const concurrentLoad = platform.load()
    assert.equal(fallbackCalls, 2)

    pendingMutation.resolve({ enabled: true })
    await settle()
    assert.equal(fallbackCalls, 3, 'mutation success must invalidate and reload the fallback family')
    assert.equal(auditCalls, 3, 'audit refresh must still run alongside fallback refresh')

    staleFallback.resolve({ enabled: false })
    await concurrentLoad
    canonicalFallback.resolve({ enabled: true })
    assert.equal(await mutation, true)
    assert.equal(platform.fallback.value, true)
  } finally {
    platform.dispose()
  }
})

test('useServerAccessPlatform: dispose invalidates all four families and their loading/error writes', async () => {
  const requests = Object.fromEntries(['platformTenants', 'platformServers', 'platformAudit', 'platformFallback'].map((name) => [name, deferred()]))
  const platform = useServerAccessPlatform(platformApi(Object.fromEntries(Object.entries(requests).map(([name, request]) => [name, () => request.promise]))))
  const pending = platform.load()
  assert.equal(platform.loading.value, true)
  const before = {
    tenants: platform.tenants.value, servers: platform.servers.value, audit: platform.audit.value,
    tenantTotal: platform.tenantTotal.value, serverTotal: platform.serverTotal.value, auditTotal: platform.auditTotal.value,
    fallback: platform.fallback.value, loading: platform.loading.value, error: platform.error.value,
  }
  platform.dispose()
  requests.platformTenants.reject(new Error('late tenants failure'))
  requests.platformServers.resolve({ servers: [{ id: 99 }], total: 99 })
  requests.platformAudit.resolve({ audit: [{ id: 99 }], total: 99 })
  requests.platformFallback.resolve({ enabled: true })
  await pending
  assert.deepEqual({
    tenants: platform.tenants.value, servers: platform.servers.value, audit: platform.audit.value,
    tenantTotal: platform.tenantTotal.value, serverTotal: platform.serverTotal.value, auditTotal: platform.auditTotal.value,
    fallback: platform.fallback.value, loading: platform.loading.value, error: platform.error.value,
  }, before)
})

test('ServerAccessAdminView: view unmount disposes all platform request families', async () => {
  const fixture = fixtures.find((item) => item.platform)
  const requests = Object.fromEntries(['platformTenants', 'platformServers', 'platformAudit', 'platformFallback'].map((name) => [name, deferred()]))
  const api = platformApi(Object.fromEntries(Object.entries(requests).map(([name, request]) => [name, () => request.promise])))
  globalThis.__adminRequestFetch = api
  const app = renderer.createApp(fixture.component)
  app.use(createI18n({ legacy: false, locale: 'en', messages: { en }, warnHtmlMessage: false }))
  const container = hostNode('root')
  app.mount(container)
  await settle()
  const state = app._instance.setupState
  const before = {
    tenants: state.platform.tenants.value, servers: state.platform.servers.value, audit: state.platform.audit.value,
    fallback: state.platform.fallback.value, loading: state.platform.loading.value, error: state.platform.error.value,
  }
  app.unmount()
  requests.platformTenants.resolve({ tenants: [{ id: 1 }], total: 1 })
  requests.platformServers.resolve({ servers: [{ id: 2 }], total: 1 })
  requests.platformAudit.resolve({ audit: [{ id: 3 }], total: 1 })
  requests.platformFallback.resolve({ enabled: true })
  await settle()
  assert.deepEqual({
    tenants: state.platform.tenants.value, servers: state.platform.servers.value, audit: state.platform.audit.value,
    fallback: state.platform.fallback.value, loading: state.platform.loading.value, error: state.platform.error.value,
  }, before)
  delete globalThis.__adminRequestFetch
})
