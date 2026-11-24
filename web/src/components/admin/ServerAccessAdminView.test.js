import test from 'node:test'
import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import { fileURLToPath, pathToFileURL } from 'node:url'
import { compileScript, compileTemplate, parse } from '@vue/compiler-sfc'
import { createRenderer, h, nextTick } from 'vue'
import { createI18n } from 'vue-i18n'
import { createServer } from 'vite'
import en from '../../locales/en.json' with { type: 'json' }

const root = fileURLToPath(new URL('../../../', import.meta.url))
const filename = fileURLToPath(new URL('./ServerAccessAdminView.vue', import.meta.url))
const { descriptor } = parse(readFileSync(filename, 'utf8'), { filename })
const script = compileScript(descriptor, { id: 'server-access-admin-view-test' })
const template = compileTemplate({
  source: descriptor.template.content,
  filename,
  id: 'server-access-admin-view-test',
  compilerOptions: { bindingMetadata: script.bindings, mode: 'module' },
})
assert.deepEqual(template.errors, [])
const componentURL = '/src/components/admin/ServerAccessAdminView.test-compiled.js'
const componentCode = `${script.content.replace('export default', 'const component =')}\n${template.code}\ncomponent.render = render\nexport default component\n`
const vite = await createServer({
  root,
  configFile: false,
  server: { middlewareMode: true, hmr: false, ws: false },
  appType: 'custom',
  optimizeDeps: { noDiscovery: true, include: [] },
  plugins: [{
    name: 'server-access-admin-view-test',
    enforce: 'pre',
    resolveId(source) {
      if (source === componentURL) return componentURL
      if (source.endsWith('/dialog.js')) return '\0server-access-test-dialog'
      if (source.endsWith('/serverAccessApi.js')) return '\0server-access-test-api'
      if (source === '../EmptyState.vue') return '\0server-access-test-empty'
      if (source === '../Pager.vue') return '\0server-access-test-pager'
      if (source === '../../composables/useServerAccessPlatform.js') return fileURLToPath(new URL(source, pathToFileURL(filename)))
    },
    load(id) {
      if (id === componentURL) return componentCode
      if (id === '\0server-access-test-dialog') return 'export const confirmDialog = (...args) => globalThis.__serverAccessConfirm(...args)'
      if (id === '\0server-access-test-api') return 'export const serverAccessApi = new Proxy({}, { get: (_target, key) => globalThis.__serverAccessApi[key] })'
      if (id === '\0server-access-test-empty') return 'import { h } from "vue"; export default { props: ["text"], setup: (props) => () => h("div", { class: "empty-state" }, props.text) }'
      if (id === '\0server-access-test-pager') return 'export default { render: () => null }'
    },
  }],
})
const { default: ServerAccessAdminView } = await vite.ssrLoadModule(componentURL)
await vite.close()

function hostNode(type, text = '') {
  return {
    type, text, props: {}, children: [], parent: null,
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

function descendants(node) {
  return [node, ...node.children.flatMap(descendants)]
}

function text(node) {
  return node.text + node.children.map(text).join('')
}

function find(node, predicate) {
  return descendants(node).find(predicate)
}

function byId(rootNode, id) {
  return find(rootNode, (node) => node.props.id === id)
}
function button(rootNode, label) {
  return find(rootNode, (node) => node.type === 'button' && text(node) === label)
}

async function settle() {
  await new Promise((resolve) => setImmediate(resolve))
  await nextTick()
  await new Promise((resolve) => setImmediate(resolve))
  await nextTick()
}

const policy = {
  whitelistEnabled: true,
  premiumFallbackEnabled: false,
  verifyRateLimit: 41,
  verifyBurst: 7,
  maxConcurrent: 3,
  whitelistLimit: 19,
  eventRetentionDays: 14,
}
const credentials = [
  { id: 5, mode: 'url_secret', status: 'revoked', lastUsedAt: null, expiresAt: '2027-02-01T00:00:00Z' },
  { id: 4, mode: 'header', status: 'active', lastUsedAt: '2027-01-02T03:04:05Z', expiresAt: null },
]
const servers = [
  { id: 9, publicId: 'srv_example', name: 'Example server', status: 'active', policy, credentials },
  { id: 10, publicId: 'srv_other', name: 'Other server', status: 'active', policy: { ...policy, verifyRateLimit: 88 }, credentials: [] },
]

async function mountView(overrides = {}) {
  const state = { servers: structuredClone(servers), writes: [], confirmations: [] }
  const api = {
    platformTenants: async () => ({ tenants: [], total: 0 }),
    platformServers: async () => ({ servers: state.servers, total: state.servers.length }),
    platformFallback: async () => ({ enabled: true }),
    platformAudit: async () => ({ audit: [], total: 0 }),
    updatePlatformTenant: async () => ({ id: 1 }),
    updatePlatformServer: async (id, body) => { state.writes.push(['server', id, body]); return { id } },
    setPlatformFallback: async (enabled) => ({ enabled }),
    revokePlatformCredential: async (serverId, credentialId) => {
      state.writes.push(['revoke', serverId, credentialId])
      const row = state.servers.find((item) => item.id === serverId)
      row.credentials = row.credentials.map((item) => item.id === credentialId ? { ...item, status: 'revoked' } : item)
      return { revoked: true }
    },
    ...overrides,
  }
  globalThis.__serverAccessApi = api
  globalThis.__serverAccessConfirm = async (message, options) => { state.confirmations.push([message, options]); return true }
  const app = renderer.createApp(ServerAccessAdminView)
  const errors = []
  app.config.errorHandler = (error) => errors.push(error)
  app.use(createI18n({ legacy: false, locale: 'en', messages: { en } }))
  const container = hostNode('root')
  app.mount(container)
  await settle()
  if (errors.length) throw errors[0]
  return { app, container, state, errors }
}

test('platform tenant creation sends numeric fields, refreshes tenants and audit, and clears the form', async () => {
  const state = { creates: [], tenantLoads: 0, auditLoads: 0 }
  const { app, container } = await mountView({
    platformTenants: async () => { state.tenantLoads++; return { tenants: [], total: 0 } },
    platformAudit: async () => { state.auditLoads++; return { audit: [], total: 0 } },
    createPlatformTenant: async (body) => { state.creates.push(body); return { tenant: { id: 3 }, owner: { uid: 42 } } },
  })
  try {
    byId(container, 'sa-create-tenant-name').props['onUpdate:modelValue']('New tenant')
    byId(container, 'sa-create-tenant-limit').props['onUpdate:modelValue']('5')
    byId(container, 'sa-create-tenant-owner').props['onUpdate:modelValue']('42')
    await nextTick()
    const name = byId(container, 'sa-create-tenant-name')
    const form = find(container, (node) => node.type === 'form' && descendants(node).includes(name))
    form.props.onSubmit({ preventDefault() {} })
    await settle()
    assert.deepEqual(state.creates, [{ name: 'New tenant', serverLimit: 5, ownerUid: 42 }])
    assert.equal(state.tenantLoads, 2)
    assert.equal(state.auditLoads, 2)
    assert.equal(byId(container, 'sa-create-tenant-name').value, '')
    assert.equal(byId(container, 'sa-create-tenant-limit').value, '')
    assert.equal(byId(container, 'sa-create-tenant-owner').value, '')
    assert.ok(find(container, (node) => node.props.role === 'status' && /created successfully/i.test(text(node))))
    assert.equal(find(container, (node) => node.props.role === 'alert'), undefined)
  } finally {
    app.unmount()
    delete globalThis.__serverAccessApi
    delete globalThis.__serverAccessConfirm
  }
})

test('platform tenant creation keeps input and shows an accessible error when rejected', async () => {
  const state = { creates: [], tenantLoads: 0, auditLoads: 0 }
  const { app, container } = await mountView({
    platformTenants: async () => { state.tenantLoads++; return { tenants: [], total: 0 } },
    platformAudit: async () => { state.auditLoads++; return { audit: [], total: 0 } },
    createPlatformTenant: async (body) => { state.creates.push(body); throw new Error('Owner already belongs to an active tenant') },
  })
  try {
    byId(container, 'sa-create-tenant-name').props['onUpdate:modelValue']('Rejected tenant')
    byId(container, 'sa-create-tenant-limit').props['onUpdate:modelValue']('8')
    byId(container, 'sa-create-tenant-owner').props['onUpdate:modelValue']('42')
    await nextTick()
    const name = byId(container, 'sa-create-tenant-name')
    find(container, (node) => node.type === 'form' && descendants(node).includes(name)).props.onSubmit({ preventDefault() {} })
    await settle()
    assert.equal(state.creates.length, 1)
    assert.equal(state.tenantLoads, 1)
    assert.equal(state.auditLoads, 1)
    find(container, (node) => node.type === 'form' && descendants(node).includes(byId(container, 'sa-create-tenant-name'))).props.onSubmit({ preventDefault() {} })
    await settle()
    assert.deepEqual(state.creates, [
      { name: 'Rejected tenant', serverLimit: 8, ownerUid: 42 },
      { name: 'Rejected tenant', serverLimit: 8, ownerUid: 42 },
    ], 'failed creation must retain all draft values')
    assert.match(text(find(container, (node) => node.props.role === 'alert')), /Owner already belongs/)
    assert.equal(find(container, (node) => node.props.role === 'status' && /created successfully/i.test(text(node))), undefined)
  } finally {
    app.unmount()
    delete globalThis.__serverAccessApi
    delete globalThis.__serverAccessConfirm
  }
})

test('platform server view shows current policy and quota beside explicit sparse patch fields', async () => {
  const { app, container, state } = await mountView()
  try {
    const expected = { verifyRateLimit: 41, verifyBurst: 7, maxConcurrent: 3, whitelistLimit: 19, eventRetentionDays: 14 }
    for (const [field, value] of Object.entries(expected)) {
      const label = find(container, (node) => node.type === 'label' && node.props.for === `sa-quota-9-${field}`)
      assert.ok(label, `missing explicit ${field} label`)
      assert.match(text(label), new RegExp(`\\b${value}\\b`))
    }
    assert.match(text(container), /Whitelist[\s\S]*Enabled/)
    assert.match(text(container), /Premium fallback[\s\S]*Disabled/)

    const input = byId(container, 'sa-quota-9-verifyRateLimit')
    input.props['onUpdate:modelValue']('55')
    await nextTick()
    const form = find(container, (node) => node.type === 'form' && node.props.onSubmit && descendants(node).includes(input))
    form.props.onSubmit({ preventDefault() {} })
    await settle()
    assert.deepEqual(state.writes, [['server', 9, { verifyRateLimit: 55 }]])
  } finally {
    app.unmount()
    delete globalThis.__serverAccessApi
    delete globalThis.__serverAccessConfirm
  }
})

test('platform deletion displays both timestamps and enables deletion only for a due deleting tenant', async () => {
  const tenants = [
    { id: 1, name: 'Due tenant', status: 'deleting', serverLimit: 5, deletionRequestedAt: '2000-01-01T00:00:00Z', deletionDueAt: '2000-01-08T00:00:00Z' },
    { id: 2, name: 'Future tenant', status: 'deleting', serverLimit: 5, deletionRequestedAt: '2999-01-01T00:00:00Z', deletionDueAt: '2999-01-08T00:00:00Z' },
    { id: 3, name: 'Active tenant', status: 'active', serverLimit: 5, deletionRequestedAt: null, deletionDueAt: '2000-01-08T00:00:00Z' },
  ]
  const state = { tenants: structuredClone(tenants), deletes: [] }
  const { app, container } = await mountView({
    platformTenants: async () => ({ tenants: state.tenants, total: state.tenants.length }),
    deletePlatformTenant: async (id) => {
      state.deletes.push(id)
      state.tenants = state.tenants.filter((row) => row.id !== id)
      return { deleted: true }
    },
  })
  try {
    assert.match(text(container), /2000-01-01T00:00:00Z/)
    assert.match(text(container), /2999-01-08T00:00:00Z/)
    const rows = descendants(container).filter((node) => node.type === 'tr')
    const row = (id) => rows.find((node) => descendants(node).some((cell) => cell.type === 'td' && text(cell) === String(id)))
    assert.equal(button(row(1), 'Delete tenant').props.disabled, false)
    assert.equal(button(row(2), 'Delete tenant').props.disabled, true)
    assert.equal(button(row(3), 'Delete tenant'), undefined, 'active tenant must not expose physical deletion')

    button(row(2), 'Delete tenant').props.onClick()
    await settle()
    assert.deepEqual(state.deletes, [])
    button(row(1), 'Delete tenant').props.onClick()
    await settle()
    assert.deepEqual(state.deletes, [1])
  } finally {
    app.unmount()
  }
})

test('emergency revocation selects a listed credential and displays its current summary', async () => {
  const { app, container, state } = await mountView()
  try {
    const serverSelect = byId(container, 'sa-revoke-server')
    const credentialSelect = byId(container, 'sa-revoke-credential')
    assert.equal(serverSelect?.type, 'select')
    assert.equal(credentialSelect?.type, 'select')
    assert.equal(find(container, (node) => node.type === 'input' && node.props.id === 'sa-revoke-credential'), undefined)
    assert.deepEqual(descendants(serverSelect).filter((node) => node.type === 'option').map((node) => node.props.value), ['', '9', '10'])

    serverSelect.props.onChange({ target: { value: '9' } })
    await nextTick()
    const options = descendants(credentialSelect).filter((node) => node.type === 'option')
    assert.deepEqual(options.map((node) => node.props.value), ['', '5', '4'])
    assert.equal(options.find((node) => node.props.value === '5').props.disabled, true)
    assert.match(text(container), /2027-01-02T03:04:05Z/)
    assert.match(text(container), /2027-02-01T00:00:00Z/)

    credentialSelect.props.onChange({ target: { value: '4' } })
    await nextTick()
    const form = find(container, (node) => node.type === 'form' && descendants(node).includes(credentialSelect))
    form.props.onSubmit({ preventDefault() {} })
    await settle()
    assert.deepEqual(state.writes, [['revoke', 9, 4]])
    assert.equal(state.confirmations.length, 1)
    assert.equal(credentialSelect.props.value, '')

    credentialSelect.props.onChange({ target: { value: '5' } })
    await nextTick()
    serverSelect.props.onChange({ target: { value: '10' } })
    await nextTick()
    assert.equal(credentialSelect.props.value, '')
    assert.equal(credentialSelect.props.disabled, true)
  } finally {
    app.unmount()
    delete globalThis.__serverAccessApi
    delete globalThis.__serverAccessConfirm
  }
})
