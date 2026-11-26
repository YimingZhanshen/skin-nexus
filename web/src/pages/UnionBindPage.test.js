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
const filename = fileURLToPath(new URL('./UnionBindPage.vue', import.meta.url))
const { descriptor } = parse(readFileSync(filename, 'utf8'), { filename })
const script = compileScript(descriptor, { id: 'union-bind-page-test' })
const template = compileTemplate({
  source: descriptor.template.content, filename, id: 'union-bind-page-test',
  compilerOptions: { bindingMetadata: script.bindings, mode: 'module', hoistStatic: false },
})
assert.deepEqual(template.errors, [])
const componentURL = '/src/pages/UnionBindPage.test-compiled.js'
const sessionPath = fileURLToPath(new URL('../session.js', import.meta.url))
const componentCode = `${script.content.replace('export default', 'const component =')}\n${template.code}\ncomponent.render = render\nexport default component\n`
const vite = await createServer({
  root,
  configFile: false,
  server: { middlewareMode: true, hmr: false, ws: false },
  appType: 'custom',
  optimizeDeps: { noDiscovery: true, include: [] },
  plugins: [{
    name: 'union-bind-page-test',
    enforce: 'pre',
    resolveId(source) {
      if (source === componentURL) return componentURL
      if (source === '../api.js') return '\0union-bind-test-api'
      if (source === '../session.js') return sessionPath
      if (source.endsWith('/dialog.js')) return '\0union-bind-test-dialog'
    },
    load(id) {
      if (id === componentURL) return componentCode
      if (id === '\0union-bind-test-api') {
        return `export const api = { players: async () => ({ players: [{ id: 1, UUID: '0123456789abcdef0123456789abcdef', Name: 'Example player' }] }) };
          export const union = new Proxy({}, { get: (_target, key) => key === 'bind' ? () => globalThis.__unionBind() : async () => key === 'securityLevel' ? { level: 3 } : {} });`
      }
      if (id === '\0union-bind-test-dialog') return 'export async function confirmDialog(){ return false }'
    },
  }],
})
const { default: UnionBindPage } = await vite.ssrLoadModule(componentURL)
const { setUnauthorizedHandler } = await vite.ssrLoadModule('/src/session.js')
await vite.close()

function hostNode(type, text = '') {
  return { type, text, props: {}, children: [], parent: null, options: [], selectedIndex: -1, addEventListener() {}, removeEventListener() {} }
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

function fakeClock(t, start) {
  let time = start
  let sequence = 0
  const pending = new Map()
  const scheduled = []
  const cleared = []
  const dateNow = t.mock.method(Date, 'now', () => time)
  t.mock.method(globalThis, 'setTimeout', (callback, delay = 0) => {
    const id = ++sequence
    scheduled.push({ id, delay, callback })
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
  return {
    pending, scheduled, cleared, advance,
    setDateNowSequence(values) {
      const sequence = [...values]
      dateNow.mock.mockImplementation(() => {
        const value = sequence.length ? sequence.shift() : time
        return value
      })
    },
  }
}

async function mountPage() {
  let tokenSequence = 0
  globalThis.__unionBind = async () => ({
    get token() {
      globalThis.__beforeUnionBind?.()
      return `token-${++tokenSequence}`
    },
  })
  const app = renderer.createApp(UnionBindPage)
  const errors = []
  app.config.errorHandler = (error) => errors.push(error)
  app.use(createI18n({ legacy: false, locale: 'en', messages: { en }, warnHtmlMessage: false }))
  const container = hostNode('root')
  app.mount(container)
  await settle()
  if (errors.length) throw errors[0]
  const profile = find(container, (node) => node.type === 'select')
  profile.props['onUpdate:modelValue']('0123456789abcdef0123456789abcdef')
  await settle()
  return { app, container, state: app._instance.setupState }
}

async function generate(container) {
  const action = find(container, (node) => node.type === 'button' && text(node).trim() === en.union.genBindCode)
  await action.props.onClick()
  await settle()
}

test('Union bind token expires on its own without an unrelated render', async (t) => {
  const clock = fakeClock(t, 1_800_000_000_000)
  const { app, container } = await mountPage()
  try {
    assert.equal(clock.pending.size, 0)
    await generate(container)
    assert.match(text(container), /token-1/)
    assert.equal(clock.pending.size, 1, 'one expiry timer must be scheduled')

    clock.advance(119_999)
    await settle()
    assert.match(text(container), /token-1/)
    clock.advance(1)
    await settle()
    assert.doesNotMatch(text(container), /token-1/)
    assert.equal(clock.pending.size, 0)
  } finally {
    app.unmount()
    delete globalThis.__unionBind
  }
})

test('a replacement Union token cancels the old timeout and starts one new deadline', async (t) => {
  const clock = fakeClock(t, 1_800_000_000_000)
  const { app, container } = await mountPage()
  try {
    await generate(container)
    const firstTimer = clock.scheduled[0]
    assert.ok(firstTimer)
    clock.advance(1_000)
    await generate(container)
    assert.equal(clock.pending.size, 1)
    assert.ok(clock.cleared.includes(firstTimer.id), 'replacement must cancel the previous timer')
    assert.match(text(container), /token-2/)
    assert.doesNotMatch(text(container), /token-1/)

    clock.advance(119_000)
    await settle()
    assert.match(text(container), /token-2/, 'old deadline must not remove the replacement')
    clock.advance(1_000)
    await settle()
    assert.doesNotMatch(text(container), /token-2/)
    assert.equal(clock.pending.size, 0)
  } finally {
    app.unmount()
    delete globalThis.__unionBind
  }
})

test('Union expiry reschedules deadlines beyond the browser timeout limit', async (t) => {
  const maxTimeout = 2_147_483_647
  const clock = fakeClock(t, 0)
  const { app, container } = await mountPage()
  try {
    // Simulate a wall-clock correction between reading the deadline and the
    // reactive clock, leaving more than the browser maximum to schedule.
    globalThis.__beforeUnionBind = () => clock.setDateNowSequence([maxTimeout, 0])
    await generate(container)
    assert.match(text(container), /token-1/)
    assert.equal(clock.pending.size, 1)
    assert.equal(clock.scheduled[0].delay, maxTimeout, 'a long deadline must be split at the browser limit')

    clock.advance(maxTimeout - 1)
    await settle()
    assert.match(text(container), /token-1/)
    clock.advance(1)
    assert.equal(clock.scheduled.at(-1).delay, 120_000)
    clock.advance(119_999)
    await settle()
    assert.match(text(container), /token-1/)
    clock.advance(1)
    await settle()
    assert.doesNotMatch(text(container), /token-1/)
    assert.equal(clock.pending.size, 0)
  } finally {
    app.unmount()
    delete globalThis.__unionBind
    delete globalThis.__beforeUnionBind
  }
})

test('a bind response cannot publish state or schedule a timer after unmount', async (t) => {
  const clock = fakeClock(t, 1_800_000_000_000)
  const { app, container, state } = await mountPage()
  let resolveBind
  globalThis.__unionBind = () => new Promise((resolve) => { resolveBind = resolve })
  try {
    const action = find(container, (node) => node.type === 'button' && text(node).trim() === en.union.genBindCode)
    const binding = action.props.onClick()
    await settle()
    assert.equal(state.loading, true)
    app.unmount()

    resolveBind({ token: 'late-token' })
    await binding
    await settle()
    assert.deepEqual(clock.scheduled, [], 'a disposed page must not create an expiry timer')
    assert.equal(state.bindToken, '')
    assert.equal(state.bindTokenFor, '')
    assert.equal(state.bindTokenExpire, 0)
    assert.equal(state.success, '')
    assert.equal(state.loading, true, 'finally must not publish after disposal')
  } finally {
    if (app._instance) app.unmount()
    delete globalThis.__unionBind
  }
})

test('a rejected bind cannot publish an error or finish loading after unmount', async (t) => {
  const clock = fakeClock(t, 1_800_000_000_000)
  const { app, container, state } = await mountPage()
  let rejectBind
  globalThis.__unionBind = () => new Promise((_resolve, reject) => { rejectBind = reject })
  try {
    const action = find(container, (node) => node.type === 'button' && text(node).trim() === en.union.genBindCode)
    const binding = action.props.onClick()
    await settle()
    app.unmount()

    rejectBind(new Error('late bind failure'))
    await binding
    await settle()
    assert.deepEqual(clock.scheduled, [])
    assert.equal(state.error, '')
    assert.equal(state.loading, true, 'finally must not publish after disposal')
  } finally {
    if (app._instance) app.unmount()
    delete globalThis.__unionBind
  }
})

test('a late bind 401 still notifies global session loss after unmount', async (t) => {
  const clock = fakeClock(t, 1_800_000_000_000)
  const { app, container, state } = await mountPage()
  let rejectBind
  globalThis.__unionBind = () => new Promise((_resolve, reject) => { rejectBind = reject })
  const notifications = []
  setUnauthorizedHandler(() => notifications.push('session lost'))
  try {
    const action = find(container, (node) => node.type === 'button' && text(node).trim() === en.union.genBindCode)
    const binding = action.props.onClick()
    await settle()
    app.unmount()

    rejectBind(Object.assign(new Error('unauthorized'), { status: 401, data: {} }))
    await binding
    await settle()
    assert.deepEqual(notifications, ['session lost'])
    assert.deepEqual(clock.scheduled, [])
    assert.equal(state.error, '')
    assert.equal(state.loading, true, 'finally must not publish after disposal')
  } finally {
    setUnauthorizedHandler(null)
    if (app._instance) app.unmount()
    delete globalThis.__unionBind
  }
})

test('a stale expiry callback cannot mutate state after unmount', async (t) => {
  const clock = fakeClock(t, 1_800_000_000_000)
  const { app, container, state } = await mountPage()
  try {
    await generate(container)
    const staleTimer = clock.scheduled[0]
    assert.ok(staleTimer)
    app.unmount()
    clock.advance(120_000)
    staleTimer.callback()
    await settle()
    assert.equal(state.now, 1_800_000_000_000)
    assert.equal(state.bindToken, 'token-1')
    assert.equal(state.bindTokenFor, 'Example player')
    assert.equal(clock.scheduled.length, 1, 'a stale callback must not reschedule')
  } finally {
    if (app._instance) app.unmount()
    delete globalThis.__unionBind
  }
})

test('Union expiry timeout is canceled when the page unmounts', async (t) => {
  const clock = fakeClock(t, 1_800_000_000_000)
  const { app, container } = await mountPage()
  try {
    await generate(container)
    const timer = clock.scheduled[0]
    assert.ok(timer)
    app.unmount()
    assert.deepEqual(clock.cleared, [timer.id])
    assert.equal(clock.pending.size, 0)
  } finally {
    if (app._instance) app.unmount()
    delete globalThis.__unionBind
  }
})
