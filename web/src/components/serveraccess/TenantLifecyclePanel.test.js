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
const filename = fileURLToPath(new URL('./TenantLifecyclePanel.vue', import.meta.url))
const { descriptor } = parse(readFileSync(filename, 'utf8'), { filename })
const script = compileScript(descriptor, { id: 'tenant-lifecycle-panel-test' })
const template = compileTemplate({
  source: descriptor.template.content,
  filename,
  id: 'tenant-lifecycle-panel-test',
  compilerOptions: { bindingMetadata: script.bindings, mode: 'module' },
})
assert.deepEqual(template.errors, [])
const componentURL = '/src/components/serveraccess/TenantLifecyclePanel.test-compiled.js'
const componentCode = `${script.content.replace('export default', 'const component =')}\n${template.code}\ncomponent.render = render\nexport default component\n`
const vite = await createServer({
  root,
  configFile: false,
  server: { middlewareMode: true, hmr: false, ws: false },
  appType: 'custom',
  optimizeDeps: { noDiscovery: true, include: [] },
  plugins: [{
    name: 'tenant-lifecycle-panel-test',
    enforce: 'pre',
    resolveId(source) {
      if (source === componentURL) return componentURL
      if (source === '../DialogPanel.vue') return '\0tenant-lifecycle-test-dialog'
    },
    load(id) {
      if (id === componentURL) return componentCode
      if (id === '\0tenant-lifecycle-test-dialog') {
        return 'import { h } from "vue"; export default { props: ["open", "title", "okText", "danger", "showCancel"], emits: ["ok", "cancel"], setup(props, { slots, emit }) { return () => props.open ? h("section", { class: "dialog-panel" }, [slots.default?.(), h("button", { class: "dialog-ok", onClick: () => emit("ok") }, props.okText)]) : null } }'
      }
    },
  }],
})
const { default: TenantLifecyclePanel } = await vite.ssrLoadModule(componentURL)
await vite.close()

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
  patchProp: (node, key, _previous, value) => { node.props[key] = value },
})
function descendants(node) { return [node, ...node.children.flatMap(descendants)] }
function text(node) { return node.text + node.children.map(text).join('') }
function find(node, predicate) { return descendants(node).find(predicate) }
function button(rootNode, label) { return find(rootNode, (node) => node.type === 'button' && text(node) === label) }
async function settle() {
  await new Promise((resolve) => setImmediate(resolve))
  await nextTick()
  await new Promise((resolve) => setImmediate(resolve))
  await nextTick()
}
const tenant = { id: 1, name: 'Example tenant', status: 'active', serverLimit: 5 }

async function mountPanel(props = {}) {
  const app = renderer.createApp(TenantLifecyclePanel, { tenant, canExport: true, canDelete: true, busy: false, ...props })
  const errors = []
  app.config.errorHandler = (error) => errors.push(error)
  app.use(createI18n({ legacy: false, locale: 'en', messages: { en } }))
  const container = hostNode('root')
  app.mount(container)
  await settle()
  if (errors.length) throw errors[0]
  return { app, container }
}

test('read-only members cannot delete and export failures stay visible with the control re-enabled', async () => {
  const readOnly = await mountPanel({ canExport: false, canDelete: false, api: {} })
  try {
    assert.equal(button(readOnly.container, 'Export tenant data').props.disabled, true)
    assert.equal(button(readOnly.container, 'Request tenant deletion'), undefined)
  } finally {
    readOnly.app.unmount()
  }
  const { app, container } = await mountPanel({
    api: {
      exportTenant: async () => { throw Object.assign(new Error('events exceeds export limit of 100000'), { status: 413 }) },
      requestTenantDeletion: async () => { throw new Error('must not be called') },
    },
  })
  try {
    const exportButton = button(container, 'Export tenant data')
    exportButton.props.onClick()
    await settle()
    assert.match(text(container), /events exceeds export limit of 100000/)
    assert.equal(exportButton.props.disabled, false)
  } finally {
    app.unmount()
  }
})

test('tenant deletion requires the exact tenant name and then shows the server-assigned grace deadline', async () => {
  const confirmations = []
  const changes = []
  const due = '2027-02-01T03:04:05Z'
  const { app, container } = await mountPanel({
    api: {
      exportTenant: async () => new Blob(),
      requestTenantDeletion: async (confirmation) => {
        confirmations.push(confirmation)
        return { status: 'deleting', deletionRequestedAt: '2027-01-25T03:04:05Z', deletionDueAt: due }
      },
    },
    onChanged: (value) => changes.push(value),
  })
  try {
    const requestButton = button(container, 'Request tenant deletion')
    assert.ok(requestButton)
    requestButton.props.onClick()
    await nextTick()
    const input = find(container, (node) => node.props.id === 'sa-delete-confirmation')
    const confirm = find(container, (node) => node.type === 'button' && node.props.class === 'dialog-ok')
    assert.ok(input, 'missing typed confirmation input')
    assert.match(text(input.parent), /Type Example tenant to confirm/)

    input.props['onUpdate:modelValue']('Example')
    await nextTick()
    confirm.props.onClick()
    await settle()
    assert.deepEqual(confirmations, [], 'partial name must not request deletion')

    input.props['onUpdate:modelValue']('Example tenant')
    await nextTick()
    confirm.props.onClick()
    await settle()
    assert.deepEqual(confirmations, ['Example tenant'])
    assert.match(text(container), /Deletion requested/)
    assert.match(text(container), /2027-02-01T03:04:05Z/)
    assert.equal(requestButton.props.disabled, true)
    assert.deepEqual(changes, [{ status: 'deleting', deletionRequestedAt: '2027-01-25T03:04:05Z', deletionDueAt: due }])
  } finally {
    app.unmount()
  }
})

test('owner can download the real tenant JSON attachment and the control is busy while pending', async () => {
  const previousDocument = globalThis.document
  const previousCreateURL = globalThis.URL.createObjectURL
  const previousRevokeURL = globalThis.URL.revokeObjectURL
  const downloads = []
  const revoked = []
  let release
  let calls = 0
  globalThis.document = {
    createElement: (tag) => {
      assert.equal(tag, 'a')
      return { click() { downloads.push(this.download) }, remove() {} }
    },
  }
  globalThis.URL.createObjectURL = (blob) => { assert.equal(blob.type, 'application/json'); return 'blob:tenant-export' }
  globalThis.URL.revokeObjectURL = (url) => revoked.push(url)
  const { app, container } = await mountPanel({
    api: { exportTenant: () => { calls++; return new Promise((resolve) => { release = resolve }) } },
  })
  try {
    const exportButton = button(container, 'Export tenant data')
    assert.ok(exportButton, 'missing export control')
    exportButton.props.onClick()
    await nextTick()
    assert.equal(calls, 1)
    assert.equal(exportButton.props.disabled, true)
    release(new Blob(['{"schemaVersion":1}'], { type: 'application/json' }))
    await settle()
    assert.deepEqual(downloads, ['serveraccess-tenant.json'])
    assert.deepEqual(revoked, ['blob:tenant-export'])
    assert.equal(exportButton.props.disabled, false)
    assert.match(text(container), /Export ready/)
  } finally {
    app.unmount()
    globalThis.document = previousDocument
    globalThis.URL.createObjectURL = previousCreateURL
    globalThis.URL.revokeObjectURL = previousRevokeURL
  }
})
