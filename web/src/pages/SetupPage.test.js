import test from 'node:test'
import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import { fileURLToPath } from 'node:url'
import { compileScript, compileTemplate, parse } from '@vue/compiler-sfc'
import { createRenderer, h, nextTick } from 'vue'
import { createI18n } from 'vue-i18n'
import { createServer } from 'vite'
import en from '../locales/en.json' with { type: 'json' }

const root = fileURLToPath(new URL('../../', import.meta.url))
const filename = fileURLToPath(new URL('./SetupPage.vue', import.meta.url))
const { descriptor } = parse(readFileSync(filename, 'utf8'), { filename })
const script = compileScript(descriptor, { id: 'setup-page-test' })
const template = compileTemplate({
  source: descriptor.template.content, filename, id: 'setup-page-test',
  compilerOptions: { bindingMetadata: script.bindings, mode: 'module', hoistStatic: false },
})
assert.deepEqual(template.errors, [])
const componentURL = '/src/pages/SetupPage.test-compiled.js'
const componentCode = `${script.content.replace('export default', 'const component =')}\n${template.code}\ncomponent.render = render\nexport default component\n`
const vite = await createServer({
  root, configFile: false, server: { middlewareMode: true, hmr: false, ws: false }, appType: 'custom',
  optimizeDeps: { noDiscovery: true, include: [] },
  plugins: [{
    name: 'setup-page-test',
    enforce: 'pre',
    resolveId(source) {
      if (source === componentURL) return componentURL
      if (source === '../errors.js') return '\0setup-page-test-errors'
    },
    load(id) {
      if (id === componentURL) return componentCode
      if (id === '\0setup-page-test-errors') return 'export function describe(data, status) { return data.message || `HTTP ${status}` }'
    },
  }],
})
const { default: SetupPage } = await vite.ssrLoadModule(componentURL)
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

test('setup token is a masked new-password field and is submitted unchanged', async (t) => {
  const previousLocation = globalThis.location
  const previousDocument = globalThis.Document
  const previousShadowRoot = globalThis.ShadowRoot
  globalThis.Document = class Document {}
  globalThis.ShadowRoot = class ShadowRoot {}
  globalThis.location = { origin: 'https://skin.example.com', href: 'https://skin.example.com/setup' }
  t.after(() => {
    globalThis.location = previousLocation
    globalThis.Document = previousDocument
    globalThis.ShadowRoot = previousShadowRoot
  })
  const submissions = []
  t.mock.method(globalThis, 'fetch', async (url, options) => {
    if (url === '/api/v1/setup/status') return Response.json({ needs_setup: true, token_required: true })
    assert.equal(url, '/api/v1/setup')
    submissions.push(JSON.parse(options.body))
    return Response.json({ site_url: 'https://skin.example.com', env_base_url: '', mismatch: false })
  })
  const app = renderer.createApp(SetupPage)
  const errors = []
  app.config.errorHandler = (error) => errors.push(error)
  app.use(createI18n({ legacy: false, locale: 'en', messages: { en }, warnHtmlMessage: false }))
  const container = hostNode('root')
  app.mount(container)
  await settle()
  try {
    const token = find(container, (node) => node.props.id === 's-setup-token')
    assert.ok(token)
    assert.equal(token.props.type, 'password')
    assert.equal(token.props.autocomplete, 'new-password')
    token.props['onUpdate:modelValue']('  setup-secret  ')
    find(container, (node) => node.props.id === 's-site-name').props['onUpdate:modelValue']('Example site')
    await nextTick()
    await button(container, en.setup.next).props.onClick()

    find(container, (node) => node.props.id === 's-nickname').props['onUpdate:modelValue']('Owner')
    find(container, (node) => node.props.id === 's-email').props['onUpdate:modelValue']('owner@example.com')
    find(container, (node) => node.props.id === 's-password').props['onUpdate:modelValue']('long-password')
    find(container, (node) => node.props.id === 's-password2').props['onUpdate:modelValue']('long-password')
    await nextTick()
    await button(container, en.setup.next).props.onClick()
    await button(container, en.setup.finish).props.onClick()
    await settle()
    assert.equal(submissions.length, 1)
    assert.equal(submissions[0].setup_token, 'setup-secret')
    assert.equal(find(container, (node) => node.props.role === 'alert'), undefined)
    assert.deepEqual(errors, [])
  } finally {
    app.unmount()
  }
})
