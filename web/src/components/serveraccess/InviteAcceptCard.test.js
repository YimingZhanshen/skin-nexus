import test from 'node:test'
import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import { fileURLToPath } from 'node:url'
import { compileScript, compileTemplate, parse } from '@vue/compiler-sfc'
import { createRenderer, h, nextTick, shallowRef } from 'vue'
import { createI18n } from 'vue-i18n'
import { createServer } from 'vite'
import en from '../../locales/en.json' with { type: 'json' }
import zhCN from '../../locales/zh-CN.json' with { type: 'json' }

const root = fileURLToPath(new URL('../../../', import.meta.url))
const filename = fileURLToPath(new URL('./InviteAcceptCard.vue', import.meta.url))
const { descriptor } = parse(readFileSync(filename, 'utf8'), { filename })
const script = compileScript(descriptor, { id: 'invite-accept-card-test' })
const template = compileTemplate({
  source: descriptor.template.content,
  filename,
  id: 'invite-accept-card-test',
  compilerOptions: { bindingMetadata: script.bindings, mode: 'module' },
})
assert.deepEqual(template.errors, [])
const componentURL = '/src/components/serveraccess/InviteAcceptCard.test-compiled.js'
const componentCode = `${script.content.replace('export default', 'const component =')}\n${template.code}\ncomponent.render = render\nexport default component\n`
const vite = await createServer({
  root,
  configFile: false,
  server: { middlewareMode: true, hmr: false, ws: false },
  appType: 'custom',
  optimizeDeps: { noDiscovery: true, include: [] },
  plugins: [{
    name: 'invite-accept-card-test',
    enforce: 'pre',
    resolveId(source) {
      if (source === componentURL) return componentURL
    },
    load(id) {
      if (id === componentURL) return componentCode
    },
  }],
})
const { default: InviteAcceptCard } = await vite.ssrLoadModule(componentURL)
await vite.close()

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
function text(node) { return node.text + node.children.map(text).join('') }
function find(node, predicate) { return descendants(node).find(predicate) }
function byId(rootNode, id) { return find(rootNode, (node) => node.props.id === id) }
function button(rootNode, label) { return find(rootNode, (node) => node.type === 'button' && text(node) === label) }
async function settle() {
  await new Promise((resolve) => setImmediate(resolve))
  await nextTick()
  await new Promise((resolve) => setImmediate(resolve))
  await nextTick()
}

async function mountCard(props = {}, locale = 'en') {
  const app = renderer.createApp(InviteAcceptCard, { busy: false, error: '', ...props })
  const errors = []
  app.config.errorHandler = (error) => errors.push(error)
  app.use(createI18n({ legacy: false, locale, messages: { en, 'zh-CN': zhCN } }))
  const container = hostNode('root')
  app.mount(container)
  await settle()
  if (errors.length) throw errors[0]
  return { app, container }
}

test('invalid, unverified, and replayed invitation errors are announced accessibly', async () => {
  for (const message of [
    en.errors.serveraccess.invite_invalid,
    en.errors.auth.email_verify_required,
    en.errors.serveraccess.invite_invalid,
  ]) {
    const { app, container } = await mountCard({ error: message, onAccept() {} })
    try {
      assert.equal(find(container, (node) => node.props.role === 'alert'), undefined)
      const input = byId(container, 'sa-accept-invite-token')
      input.props['onUpdate:modelValue']('pasted-invite-token')
      await nextTick()
      find(container, (node) => node.type === 'form').props.onSubmit({ preventDefault() {} })
      await settle()
      const alert = find(container, (node) => node.props.role === 'alert')
      assert.ok(alert)
      assert.equal(text(alert), message)
      assert.equal(input.props['aria-invalid'], 'true')
      assert.equal(input.props['aria-describedby'], 'sa-accept-invite-error')
    } finally {
      app.unmount()
    }
  }
})

test('a pending invitation acceptance disables duplicate input and announces busy state', async () => {
  const { app, container } = await mountCard({ busy: true, onAccept() {} })
  try {
    const input = byId(container, 'sa-accept-invite-token')
    const submit = button(container, 'Accepting…')
    assert.equal(input.props.disabled, true)
    assert.equal(submit.props.disabled, true)
    const status = find(container, (node) => node.props.role === 'status')
    assert.ok(status)
    assert.equal(text(status), 'Accepting…')
  } finally {
    app.unmount()
  }
})

test('the invitation card renders complete Chinese copy', async () => {
  const { app, container } = await mountCard({}, 'zh-CN')
  try {
    assert.match(text(container), /接受服务器接入邀请/)
    assert.match(text(container), /不会出现在 URL 中/)
    assert.ok(button(container, '接受邀请'))
    assert.equal(byId(container, 'sa-accept-invite-token').props.placeholder, '邀请令牌')
  } finally {
    app.unmount()
  }
})

test('the servers page offers acceptance before the forbidden state and refreshes the catalog after success', async () => {
  const pageFilename = fileURLToPath(new URL('../../pages/ServersPage.vue', import.meta.url))
  const pageSource = readFileSync(pageFilename, 'utf8')
  const { descriptor: pageDescriptor } = parse(pageSource, { filename: pageFilename })
  const pageScript = compileScript(pageDescriptor, { id: 'servers-page-invite-test' })
  const pageTemplate = compileTemplate({
    source: pageDescriptor.template.content,
    filename: pageFilename,
    id: 'servers-page-invite-test',
    compilerOptions: { bindingMetadata: pageScript.bindings, mode: 'module' },
  })
  assert.deepEqual(pageTemplate.errors, [])
  const pageURL = '/src/pages/ServersPage.test-compiled.js'
  const pageCode = `${pageScript.content.replace('export default', 'const component =')}\n${pageTemplate.code}\ncomponent.render = render\nexport default component\n`
  const pageVite = await createServer({
    root,
    configFile: false,
    server: { middlewareMode: true, hmr: false, ws: false },
    appType: 'custom',
    optimizeDeps: { noDiscovery: true, include: [] },
    plugins: [{
      name: 'servers-page-invite-test',
      enforce: 'pre',
      resolveId(source) {
        if (source === pageURL) return pageURL
        if (source.endsWith('/InviteAcceptCard.vue')) return componentURL
        if (source.endsWith('/serverAccessApi.js')) return '\0servers-page-invite-api'
        if (source.endsWith('/useServerTenant.js')) return '\0servers-page-invite-portal'
        if (source.endsWith('/useServerCatalog.js')) return '\0servers-page-invite-catalog'
        if (source.endsWith('/dialog.js')) return '\0servers-page-invite-dialog'
        if (source.endsWith('/EmptyState.vue')) return '\0servers-page-invite-empty'
        if (source.includes('/components/serveraccess/') && source.endsWith('.vue')) return '\0servers-page-invite-panel'
      },
      load(id) {
        if (id === pageURL) return pageCode
        if (id === componentURL) return componentCode
        if (id === '\0servers-page-invite-api') return 'export const serverAccessApi = {}'
        if (id === '\0servers-page-invite-portal') return 'export function useServerTenant(){ return globalThis.__serversPageInvitePortal }'
        if (id === '\0servers-page-invite-catalog') return 'export function useServerCatalog(){ return globalThis.__serversPageInviteCatalog }'
        if (id === '\0servers-page-invite-dialog') return 'export async function confirmDialog(){ return false }'
        if (id === '\0servers-page-invite-empty') return 'import { h } from "vue"; export default { props: ["text", "hint"], setup(props){ return () => h("div", { class: "empty-state" }, [props.text, props.hint]) } }'
        if (id === '\0servers-page-invite-panel') return 'import { h } from "vue"; export default { setup(){ return () => h("div", { class: "server-panel-stub" }) } }'
      },
    }],
  })
  const { default: ServersPage } = await pageVite.ssrLoadModule(pageURL)
  await pageVite.close()

  const acceptedTokens = []
  const selectedIds = []
  let catalogLoads = 0
  const server = { id: 9, name: 'Example server' }
  const portal = {
    tenant: shallowRef(null), role: shallowRef(''), members: shallowRef([]), invites: shallowRef([]),
    loading: shallowRef(false), busy: shallowRef(false), error: shallowRef(''),
    unavailable: shallowRef(false), forbidden: shallowRef(true), total: shallowRef(0), page: shallowRef(1), size: shallowRef(50),
    load: async () => false,
    acceptInvite: async (token) => {
      acceptedTokens.push(token)
      portal.forbidden.value = false
      portal.tenant.value = { id: 1, name: 'Example tenant', serverLimit: 5 }
      portal.role.value = 'viewer'
      return { tenantId: 1 }
    },
  }
  const catalog = {
    servers: shallowRef([server]), selected: shallowRef(null), quotaUsed: shallowRef(1),
    loading: shallowRef(false), busy: shallowRef(false), error: shallowRef(''), total: shallowRef(1), page: shallowRef(1), size: shallowRef(50),
    load: async () => { catalogLoads++; return true },
    select: async (id) => { selectedIds.push(id); catalog.selected.value = { server } },
  }
  globalThis.__serversPageInvitePortal = portal
  globalThis.__serversPageInviteCatalog = catalog
  const previousDocument = globalThis.document
  const previousDocumentConstructor = globalThis.Document
  const previousShadowRoot = globalThis.ShadowRoot
  globalThis.document = { activeElement: null }
  globalThis.Document = class Document {}
  globalThis.ShadowRoot = class ShadowRoot {}
  const app = renderer.createApp(ServersPage, { enabled: true })
  const errors = []
  app.config.errorHandler = (error) => errors.push(error)
  app.use(createI18n({ legacy: false, locale: 'en', messages: { en } }))
  const container = hostNode('root')
  try {
    app.mount(container)
    await settle()
    if (errors.length) throw errors[0]
    const input = byId(container, 'sa-accept-invite-token')
    const empty = find(container, (node) => node.props.class === 'empty-state')
    assert.ok(input, 'missing invitation acceptance card')
    assert.ok(empty)
    assert.match(text(empty), /No owner portal access/)
    assert.ok(descendants(container).indexOf(input) < descendants(container).indexOf(empty))

    input.props['onUpdate:modelValue']('pasted-invite-token')
    await nextTick()
    find(container, (node) => node.type === 'form').props.onSubmit({ preventDefault() {} })
    await settle()
    if (errors.length) throw errors[0]
    assert.deepEqual(acceptedTokens, ['pasted-invite-token'])
    assert.equal(catalogLoads, 1)
    assert.deepEqual(selectedIds, [9])
    assert.equal(byId(container, 'sa-accept-invite-token'), undefined)
    assert.match(text(container), /Example tenant/)
  } finally {
    app.unmount()
    delete globalThis.__serversPageInvitePortal
    delete globalThis.__serversPageInviteCatalog
    globalThis.document = previousDocument
    globalThis.Document = previousDocumentConstructor
    globalThis.ShadowRoot = previousShadowRoot
  }
})

test('a pasted invitation token stays private until the form is explicitly submitted', async () => {
  const accepted = []
  const { app, container } = await mountCard({ onAccept: (token) => accepted.push(token) })
  try {
    const input = byId(container, 'sa-accept-invite-token')
    const form = find(container, (node) => node.type === 'form')
    assert.ok(input)
    assert.ok(form)
    assert.equal(input.props.type, 'password')
    assert.equal(input.props.autocomplete, 'off')
    assert.deepEqual(accepted, [])

    input.props['onUpdate:modelValue']('  pasted-invite-token  ')
    await nextTick()
    assert.deepEqual(accepted, [])
    const submit = button(container, 'Accept invitation')
    assert.ok(submit)
    assert.equal(submit.props.type, 'submit')
    assert.equal(submit.props.disabled, false)

    form.props.onSubmit({ preventDefault() {} })
    await settle()
    assert.deepEqual(accepted, ['pasted-invite-token'])
    assert.equal(input.value, '')
  } finally {
    app.unmount()
  }
})
