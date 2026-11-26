import test, { afterEach } from 'node:test'
import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import { fileURLToPath } from 'node:url'
import { compileScript, compileTemplate, parse } from '@vue/compiler-sfc'
import { createRenderer, nextTick } from 'vue'
import { createI18n } from 'vue-i18n'
import { createServer } from 'vite'
import en from '../locales/en.json' with { type: 'json' }

const root = fileURLToPath(new URL('../../', import.meta.url))
const dialogPath = fileURLToPath(new URL('../dialog.js', import.meta.url))

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
  ModalHost: '/src/components/ModalHost.test-compiled.js',
  DialogPanel: '/src/components/DialogPanel.test-compiled.js',
}
const compiled = new Map([
  [componentURLs.ModalHost, compile('./ModalHost.vue', 'modal-host-test')],
  [componentURLs.DialogPanel, compile('./DialogPanel.vue', 'dialog-panel-test')],
])
const vite = await createServer({
  root,
  configFile: false,
  server: { middlewareMode: true, hmr: false, ws: false },
  appType: 'custom',
  optimizeDeps: { noDiscovery: true, include: [] },
  plugins: [{
    name: 'modal-host-test',
    enforce: 'pre',
    resolveId(source) {
      if (compiled.has(source)) return source
      for (const [name, url] of Object.entries(componentURLs)) {
        if (source.endsWith(`/${name}.vue`)) return url
      }
      if (source === '../dialog.js') return dialogPath
    },
    load(id) {
      if (compiled.has(id)) return compiled.get(id)
    },
  }],
})
const { default: ModalHost } = await vite.ssrLoadModule(componentURLs.ModalHost)
const { promptDialog, resolveDialog } = await vite.ssrLoadModule(dialogPath)
await vite.close()

const events = []
function hostNode(type, text = '') {
  const node = { type, text, props: {}, children: [], parent: null, value: '', open: false, addEventListener() {}, removeEventListener() {} }
  if (type === 'dialog') {
    node.showModal = function () {
      assert.equal(this.open, false, 'showModal must only run for a closed native dialog')
      this.open = true
      events.push(`show:${this.props['aria-label']}`)
    }
    node.close = function () {
      assert.equal(this.open, true, 'close must run for the visible native dialog')
      this.open = false
      events.push(`close:${this.props['aria-label']}`)
    }
  }
  if (type === 'input') {
    node.focus = () => events.push(`focus:${node.props['aria-label']}`)
    node.select = () => events.push(`select:${node.props['aria-label']}`)
  }
  return node
}
const renderer = createRenderer({
  createElement: (type) => hostNode(type),
  createText: (text) => hostNode('#text', text),
  createComment: (text) => hostNode('#comment', text),
  setText: (node, value) => { node.text = value },
  setElementText: (node, value) => { node.text = value; node.children = [] },
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
    if (key === 'value') node.value = value
  },
})
function descendants(node) { return [node, ...node.children.flatMap(descendants)] }
function find(node, predicate) { return descendants(node).find(predicate) }
function text(node) { return node.text + node.children.map(text).join('') }
function button(container, label) { return find(container, (node) => node.type === 'button' && text(node).trim() === label) }
async function settle() {
  await new Promise((resolve) => setImmediate(resolve))
  await nextTick()
  await new Promise((resolve) => setImmediate(resolve))
  await nextTick()
}

afterEach(async () => {
  for (let i = 0; i < 4; i++) {
    resolveDialog(null)
    await nextTick()
  }
  events.length = 0
})

test('native prompt handoff closes before reopening and ignores duplicate close events until opened', async () => {
  const app = renderer.createApp(ModalHost)
  app.use(createI18n({ legacy: false, locale: 'en', messages: { en }, warnHtmlMessage: false }))
  const container = hostNode('root')
  app.mount(container)
  try {
    const first = promptDialog('First prompt', 'first draft')
    let secondSettled = false
    const second = promptDialog('Second prompt', 'second draft').then((value) => {
      secondSettled = true
      return value
    })
    await settle()
    const dialog = find(container, (node) => node.type === 'dialog')
    assert.ok(dialog)
    assert.equal(dialog.open, true)
    assert.deepEqual(events, [
      'show:First prompt',
      'focus:First prompt',
      'select:First prompt',
    ])

    const accept = button(container, en.common.confirm)
    const cancel = button(container, en.common.cancel)
    accept.props.onClick()
    accept.props.onClick()
    cancel.props.onClick()
    dialog.props.onCancel({ preventDefault() {} })
    assert.equal(secondSettled, false)

    await settle()
    assert.deepEqual(events, [
      'show:First prompt',
      'focus:First prompt',
      'select:First prompt',
      'close:First prompt',
      'show:Second prompt',
      'focus:Second prompt',
      'select:Second prompt',
    ])
    assert.equal(dialog.open, true, 'the next prompt must reopen the native dialog')
    assert.equal(secondSettled, false, 'duplicate close events must not settle the next prompt')

    const nextCancel = button(container, en.common.cancel)
    nextCancel.props.onClick()
    nextCancel.props.onClick()
    dialog.props.onCancel({ preventDefault() {} })
    await settle()
    assert.equal(await first, 'first draft')
    assert.equal(await second, null)
    assert.deepEqual(events, [
      'show:First prompt',
      'focus:First prompt',
      'select:First prompt',
      'close:First prompt',
      'show:Second prompt',
      'focus:Second prompt',
      'select:Second prompt',
      'close:Second prompt',
    ])
    assert.equal(dialog.open, false)
  } finally {
    app.unmount()
  }
})
