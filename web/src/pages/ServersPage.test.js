import test, { afterEach } from 'node:test'
import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import { fileURLToPath } from 'node:url'
import { compileScript, compileTemplate, parse } from '@vue/compiler-sfc'
import { createRenderer, h, nextTick } from 'vue'
import { createI18n } from 'vue-i18n'
import { createServer } from 'vite'
import en from '../locales/en.json' with { type: 'json' }

const root = fileURLToPath(new URL('../../', import.meta.url))

function compile(relativePath, id) {
  const filename = fileURLToPath(new URL(relativePath, import.meta.url))
  const { descriptor } = parse(readFileSync(filename, 'utf8'), { filename })
  const script = compileScript(descriptor, { id })
  const template = compileTemplate({
    source: descriptor.template.content, filename, id,
    compilerOptions: { bindingMetadata: script.bindings, mode: 'module', hoistStatic: false },
  })
  assert.deepEqual(template.errors, [])
  return `${script.content.replace('export default', 'const component =')}\n${template.code}\ncomponent.render = render\nexport default component\n`
}

const componentURLs = {
  ServersPage: '/src/pages/ServersPage.test-compiled.js',
  ServerListPanel: '/src/components/serveraccess/ServerListPanel.test-compiled.js',
  ServerDetailPanel: '/src/components/serveraccess/ServerDetailPanel.test-compiled.js',
}
const dialogPath = fileURLToPath(new URL('../dialog.js', import.meta.url))
const catalogPath = fileURLToPath(new URL('../composables/useServerCatalog.js', import.meta.url))
const compiled = new Map([
  [componentURLs.ServersPage, compile('./ServersPage.vue', 'servers-page-test')],
  [componentURLs.ServerListPanel, compile('../components/serveraccess/ServerListPanel.vue', 'server-list-panel-test')],
  [componentURLs.ServerDetailPanel, compile('../components/serveraccess/ServerDetailPanel.vue', 'server-detail-panel-test')],
])
const vite = await createServer({
  root,
  configFile: false,
  server: { middlewareMode: true, hmr: false, ws: false },
  appType: 'custom',
  optimizeDeps: { noDiscovery: true, include: [] },
  plugins: [{
    name: 'servers-page-test',
    enforce: 'pre',
    resolveId(source) {
      if (compiled.has(source)) return source
      for (const [name, url] of Object.entries(componentURLs)) {
        if (source.endsWith(`/${name}.vue`)) return url
      }
      if (source.endsWith('/dialog.js')) return dialogPath
      if (source.endsWith('/useServerCatalog.js')) return catalogPath
      if (source.endsWith('/serverAccessApi.js')) return '\0servers-page-test-api'
      if (source.endsWith('/useServerTenant.js')) return '\0servers-page-test-portal'
      if (source.endsWith('.vue')) return `\0servers-page-test-stub-${source}`
    },
    load(id) {
      if (compiled.has(id)) return compiled.get(id)
      if (id === '\0servers-page-test-api') return 'export const serverAccessApi = new Proxy({}, { get: (_target, key) => (...args) => globalThis.__serversPageApi[key](...args) })'
      if (id === '\0servers-page-test-portal') {
        return `import { ref } from 'vue'; export function useServerTenant() { return {
          tenant: ref({ id: 1, name: 'Example tenant', serverLimit: 5 }), role: ref('owner'),
          loading: ref(false), busy: ref(false), error: ref(''), forbidden: ref(false), unavailable: ref(false),
          members: ref([]), invites: ref([]), page: ref(1), size: ref(20), total: ref(0),
          load: async () => true, invite: async () => null, acceptInvite: async () => null,
          removeMember: async () => null, revokeInvite: async () => null, pageInvites: async () => true,
        } }`
      }
      if (id.startsWith('\0servers-page-test-stub-')) return 'import { h } from "vue"; export default { props: ["text"], setup: (props) => () => h("div", { class: "page-stub" }, props.text || "Stub") }'
    },
  }],
})
const { default: ServersPage } = await vite.ssrLoadModule(componentURLs.ServersPage)
const { dialogState, resolveDialog } = await vite.ssrLoadModule(dialogPath)
await vite.close()
afterEach(() => resolveDialog(null))

function hostNode(type, text = '') {
  return { type, text, props: {}, children: [], parent: null, addEventListener() {}, removeEventListener() {} }
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
  patchProp: (node, key, _previous, value) => { node.props[key] = value; if (key === 'value') node.value = value },
})
function descendants(node) { return [node, ...node.children.flatMap(descendants)] }
function find(node, predicate) { return descendants(node).find(predicate) }
function text(node) { return node.text + node.children.map(text).join('') }
function button(rootNode, label) { return find(rootNode, (node) => node.type === 'button' && text(node).trim() === label) }
async function settle() {
  await new Promise((resolve) => setImmediate(resolve))
  await nextTick()
  await new Promise((resolve) => setImmediate(resolve))
  await nextTick()
}

async function mountPage(status = 'active') {
  const server = { id: 9, publicId: 'srv_example', name: 'Example server', status, createdAt: '2027-01-01T00:00:00Z' }
  const detail = { server, policy: { whitelistEnabled: true } }
  const writes = []
  globalThis.__serversPageApi = {
    servers: async () => ({ servers: [server], total: 1, quotaUsed: 1 }),
    server: async () => detail,
    updateServer: async (id, body) => {
      writes.push([id, body])
      Object.assign(server, body)
      return { id, ...body }
    },
  }
  const app = renderer.createApp(ServersPage, { enabled: true })
  const errors = []
  app.config.errorHandler = (error) => errors.push(error)
  app.use(createI18n({ legacy: false, locale: 'en', messages: { en }, warnHtmlMessage: false }))
  const container = hostNode('root')
  app.mount(container)
  await settle()
  if (errors.length) throw errors[0]
  return { app, container, writes, server }
}

test('disabling a server waits for confirmation and cancel sends no PATCH', async () => {
  const { app, container, writes } = await mountPage()
  try {
    const disabling = button(container, en.serverAccess.disable).props.onClick()
    await settle()
    assert.equal(dialogState.open, true)
    assert.equal(dialogState.kind, 'confirm')
    assert.match(dialogState.message, /Example server/)
    assert.deepEqual(writes, [], 'catalog must not change before confirmation')

    resolveDialog(false)
    await disabling
    await settle()
    assert.deepEqual(writes, [])
    assert.equal(dialogState.open, false)
    assert.ok(button(container, en.serverAccess.disable))
  } finally {
    app.unmount()
    delete globalThis.__serversPageApi
  }
})

test('accepting disable sends exactly one status patch and other server updates stay unconfirmed', async () => {
  const { app, container, writes, server } = await mountPage()
  try {
    const disabling = button(container, en.serverAccess.disable).props.onClick()
    await settle()
    assert.equal(dialogState.open, true, 'disable must wait for confirmation')
    assert.deepEqual(writes, [])
    resolveDialog(true)
    await disabling
    await settle()
    assert.deepEqual(writes, [[9, { status: 'disabled' }]])
    assert.equal(server.status, 'disabled')
    assert.ok(button(container, en.serverAccess.enable))

    const name = find(container, (node) => node.props.id === 'sa-server-name')
    name.props['onUpdate:modelValue']('Renamed server')
    await nextTick()
    await button(container, en.serverAccess.save).props.onClick()
    await settle()
    assert.equal(dialogState.open, false)
    assert.deepEqual(writes, [[9, { status: 'disabled' }], [9, { name: 'Renamed server' }]])
    assert.equal(server.status, 'disabled', 'a name update must not silently re-enable the server')

    await button(container, en.serverAccess.enable).props.onClick()
    await settle()
    assert.equal(dialogState.open, false)
    assert.deepEqual(writes, [[9, { status: 'disabled' }], [9, { name: 'Renamed server' }], [9, { status: 'active' }]])
    assert.equal(server.status, 'active')
  } finally {
    app.unmount()
    delete globalThis.__serversPageApi
  }
})
