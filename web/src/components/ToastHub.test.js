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
const filename = fileURLToPath(new URL('./ToastHub.vue', import.meta.url))
const { descriptor } = parse(readFileSync(filename, 'utf8'), { filename })
const script = compileScript(descriptor, { id: 'toast-hub-test' })
const template = compileTemplate({
  source: descriptor.template.content, filename, id: 'toast-hub-test',
  compilerOptions: { bindingMetadata: script.bindings, mode: 'module', hoistStatic: false },
})
assert.deepEqual(template.errors, [])
const componentURL = '/src/components/ToastHub.test-compiled.js'
const componentCode = `${script.content.replace('export default', 'const component =')}\n${template.code}\ncomponent.render = render\nexport default component\n`
const vite = await createServer({
  root, configFile: false, server: { middlewareMode: true, hmr: false, ws: false }, appType: 'custom',
  optimizeDeps: { noDiscovery: true, include: [] },
  plugins: [{
    name: 'toast-hub-test',
    enforce: 'pre',
    resolveId(source) { if (source === componentURL) return componentURL },
    load(id) { if (id === componentURL) return componentCode },
  }],
})
const { default: ToastHub } = await vite.ssrLoadModule(componentURL)
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
function toasts(container) { return descendants(container).filter((node) => String(node.props.class || '').split(/\s+/).includes('toast')) }
async function settle() {
  await new Promise((resolve) => setImmediate(resolve))
  await nextTick()
  await new Promise((resolve) => setImmediate(resolve))
  await nextTick()
}

function fakeClock(t) {
  let time = 0
  let sequence = 0
  const pending = new Map()
  const cleared = []
  t.mock.method(Date, 'now', () => time)
  t.mock.method(globalThis, 'setTimeout', (callback, delay = 0) => {
    const id = ++sequence
    pending.set(id, { callback, due: time + delay })
    return id
  })
  t.mock.method(globalThis, 'clearTimeout', (id) => {
    cleared.push(id)
    pending.delete(id)
  })
  function advance(ms) {
    time += ms
    for (;;) {
      const next = [...pending.entries()].filter(([, timer]) => timer.due <= time).sort((a, b) => a[1].due - b[1].due)[0]
      if (!next) return
      pending.delete(next[0])
      next[1].callback()
    }
  }
  return { pending, cleared, advance }
}

async function mountHub(t) {
  const clock = fakeClock(t)
  const listeners = new Map()
  const previousWindow = globalThis.window
  globalThis.window = {
    addEventListener: (name, listener) => listeners.set(name, listener),
    removeEventListener: (name, listener) => { if (listeners.get(name) === listener) listeners.delete(name) },
  }
  const app = renderer.createApp(ToastHub)
  const errors = []
  app.config.errorHandler = (error) => errors.push(error)
  app.use(createI18n({ legacy: false, locale: 'en', messages: { en }, warnHtmlMessage: false }))
  const container = hostNode('root')
  app.mount(container)
  await settle()
  t.after(() => { globalThis.window = previousWindow })
  return {
    app, container, clock, errors, listeners,
    push: (message, level = 'info') => listeners.get('skin-toast')({ detail: { message, level } }),
  }
}

test('manual toast dismissal clears its timer and automatic expiry removes its timer entry', async (t) => {
  const hub = await mountHub(t)
  try {
    hub.push('Manual toast', 'success')
    await settle()
    assert.equal(toasts(hub.container).length, 1)
    const timer = [...hub.clock.pending.keys()][0]
    toasts(hub.container)[0].children.find((node) => node.type === 'button').props.onClick()
    await settle()
    assert.equal(toasts(hub.container).length, 0)
    assert.ok(hub.clock.cleared.includes(timer))
    assert.equal(hub.clock.pending.size, 0)

    hub.push('Automatic toast', 'error')
    await settle()
    const automaticTimer = [...hub.clock.pending.keys()][0]
    hub.clock.advance(4_199)
    await settle()
    assert.match(text(hub.container), /Automatic toast/)
    hub.clock.advance(1)
    await settle()
    assert.doesNotMatch(text(hub.container), /Automatic toast/)
    assert.ok(hub.clock.cleared.includes(automaticTimer))
    assert.equal(hub.clock.pending.size, 0)
    assert.deepEqual(hub.errors, [])
  } finally {
    hub.app.unmount()
  }
})

test('toast eviction keeps the newest four and clears every evicted timer', async (t) => {
  const hub = await mountHub(t)
  try {
    for (let i = 1; i <= 5; i++) hub.push(`Toast ${i}`)
    await settle()
    assert.deepEqual(toasts(hub.container).map((node) => text(node).replace(/×$/, '')), ['Toast 2', 'Toast 3', 'Toast 4', 'Toast 5'])
    assert.equal(hub.clock.pending.size, 4, 'evicted toasts must not retain timers')
    assert.deepEqual(hub.clock.cleared, [1])

    hub.push('')
    hub.push('')
    await settle()
    assert.equal(toasts(hub.container).length, 4)
    assert.equal(hub.clock.pending.size, 4)
  } finally {
    hub.app.unmount()
  }
})

test('unmount clears all toast timers and removes the event listener', async (t) => {
  const hub = await mountHub(t)
  for (let i = 1; i <= 4; i++) hub.push(`Toast ${i}`)
  await settle()
  const timerIds = [...hub.clock.pending.keys()]
  assert.equal(timerIds.length, 4)
  hub.app.unmount()
  assert.equal(hub.clock.pending.size, 0)
  assert.deepEqual(hub.clock.cleared, timerIds)
  assert.equal(hub.listeners.has('skin-toast'), false)
})
