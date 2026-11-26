import test from 'node:test'
import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import { fileURLToPath, pathToFileURL } from 'node:url'
import { compileScript, compileTemplate, parse } from '@vue/compiler-sfc'
import { createRenderer, nextTick, ref } from 'vue'
import { createI18n } from 'vue-i18n'
import { createServer } from 'vite'
import en from '../../locales/en.json' with { type: 'json' }

const root = fileURLToPath(new URL('../../../', import.meta.url))
const filename = fileURLToPath(new URL('./MarketView.vue', import.meta.url))
const { descriptor } = parse(readFileSync(filename, 'utf8'), { filename })
const script = compileScript(descriptor, { id: 'market-view-test' })
const template = compileTemplate({
  source: descriptor.template.content,
  filename,
  id: 'market-view-test',
  compilerOptions: { bindingMetadata: script.bindings, mode: 'module' },
})
assert.deepEqual(template.errors, [])
const compiled = `${script.content.replace('export default', 'const component =')}\n${template.code}\ncomponent.render = render\nexport default component\n`
const stubs = new Map([
  ['\0market-test-pager', 'import { h } from "vue"; export default { props: ["page", "pages"], setup: () => () => h("div") }'],
  ['\0market-test-file', 'import { h } from "vue"; export default { props: ["file"], emits: ["update:file"], setup: () => () => h("div") }'],
  ['\0market-test-empty', 'import { h } from "vue"; export default { props: ["text"], setup: () => () => h("div") }'],
  ['\0market-test-plugin-ui', 'export async function loadPluginUI(){}'],
  ['\0market-test-errors', 'export function describe(data,status){return data.message || `HTTP ${status}`}; export function transportError(){return "network"}'],
])
const vite = await createServer({
  root,
  configFile: false,
  server: { middlewareMode: true, hmr: false, ws: false },
  appType: 'custom',
  optimizeDeps: { noDiscovery: true, include: [] },
  plugins: [{
    name: 'market-view-test',
    enforce: 'pre',
    resolveId(source) {
      if (source === '/src/components/admin/MarketView.test-compiled.js') return source
      if (source === '../../adminApi.js') return pathToFileURL(fileURLToPath(new URL('../../adminApi.js', import.meta.url))).href
      if (source === '../../api.js') return pathToFileURL(fileURLToPath(new URL('../../api.js', import.meta.url))).href
      if (source === '../Pager.vue') return '\0market-test-pager'
      if (source === '../FilePicker.vue') return '\0market-test-file'
      if (source === '../EmptyState.vue') return '\0market-test-empty'
      if (source === '../../pluginUI.js') return '\0market-test-plugin-ui'
      if (source.endsWith('/errors.js') || source === './errors.js') return '\0market-test-errors'
    },
    load(id) {
      if (id === '/src/components/admin/MarketView.test-compiled.js') return compiled
      if (stubs.has(id)) return stubs.get(id)
    },
  }],
})
const { default: MarketView } = await vite.ssrLoadModule('/src/components/admin/MarketView.test-compiled.js')
const { setUnauthorizedHandler } = await vite.ssrLoadModule('/src/session.js')
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
  nextSibling: () => null,
  insert: (child, parent) => { child.parent = parent; parent.children.push(child) },
  remove: (child) => {
    if (!child.parent) return
    child.parent.children.splice(child.parent.children.indexOf(child), 1)
    child.parent = null
  },
  patchProp: (node, key, _previous, value) => { node.props[key] = value },
})
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
function jsonResponse(status, body) {
  return new Response(JSON.stringify(body), { status, headers: { 'Content-Type': 'application/json' } })
}
async function mountMarket(uploadResponse, catalogHandler = async () => jsonResponse(200, { packages: [] })) {
  const previousFetch = globalThis.fetch
  const previousSetTimeout = globalThis.setTimeout
  const shellUser = ref({ id: 1, permission: 2 })
  const notifications = []
  const uploads = []
  const timeouts = []
  globalThis.setTimeout = (callback, delay, ...args) => {
    timeouts.push(delay)
    return previousSetTimeout(callback, delay, ...args)
  }
  globalThis.fetch = async (path, options) => {
    if (path === '/api/v1/me') return jsonResponse(200, { id: 1, permission: 2 })
    if (path === '/api/admin/v1/market/catalog') return catalogHandler()
    if (path === '/api/admin/v1/market/upload') {
      uploads.push(options)
      return uploadResponse
    }
    throw new Error(`unexpected request: ${path}`)
  }
  setUnauthorizedHandler(() => {
    notifications.push('session lost')
    shellUser.value = null
  })
  const app = renderer.createApp(MarketView)
  const errors = []
  app.config.errorHandler = (error) => errors.push(error)
  app.use(createI18n({ legacy: false, locale: 'en', messages: { en }, warnHtmlMessage: false }))
  const container = hostNode('root')
  app.mount(container)
  await settle()
  if (errors.length) throw errors[0]
  function restore() {
    setUnauthorizedHandler(null)
    globalThis.fetch = previousFetch
    globalThis.setTimeout = previousSetTimeout
  }
  return {
    app,
    state: app._instance.setupState,
    shellUser,
    notifications,
    uploads,
    timeouts,
    cleanup() {
      app.unmount()
      restore()
    },
    restore,
  }
}

test('MarketView: a newer catalog loader wins over a delayed older response', async () => {
  const older = deferred()
  const newer = deferred()
  let calls = 0
  const mounted = await mountMarket(jsonResponse(200, {}), () => {
    calls++
    if (calls === 1) return jsonResponse(200, { packages: [{ name: 'old' }] })
    if (calls === 2) return older.promise
    return newer.promise
  })
  try {
    const oldLoad = mounted.state.load()
    const newLoad = mounted.state.load()
    newer.resolve(jsonResponse(200, { packages: [{ name: 'new' }] }))
    await newLoad
    older.resolve(jsonResponse(200, { packages: [{ name: 'old' }] }))
    await oldLoad
    await settle()
    assert.deepEqual(mounted.state.packages.map((item) => item.name), ['new'])
  } finally { mounted.cleanup() }
})

test('MarketView: unmount blocks late catalog success and rejection writes', async () => {
  for (const outcome of ['success', 'rejection']) {
    const pending = deferred()
    const mounted = await mountMarket(jsonResponse(200, {}), () => pending.promise)
    const before = { packages: mounted.state.packages, error: mounted.state.loadError, loading: mounted.state.loading }
    mounted.app.unmount()
    if (outcome === 'success') pending.resolve(jsonResponse(200, { packages: [{ name: 'late' }] }))
    else pending.reject(new Error('late catalog failure'))
    await settle()
    assert.deepEqual({ packages: mounted.state.packages, error: mounted.state.loadError, loading: mounted.state.loading }, before)
    mounted.restore()
  }
})

test('MarketView: inspected install reloads past an older catalog snapshot', async () => {
  const staleList = deferred()
  const canonicalList = deferred()
  let calls = 0
  const mounted = await mountMarket(jsonResponse(200, {}), () => {
    calls++
    if (calls === 1) return jsonResponse(200, { packages: [{ name: 'old' }] })
    if (calls === 2) return staleList.promise
    return canonicalList.promise
  })
  try {
    mounted.state.uploadFile = new File(['signed bundle'], 'bundle.zip', { type: 'application/zip' })
    mounted.state.inspection = { status: 'verified', zipSha: 'digest' }
    const olderLoad = mounted.state.load()
    const installation = mounted.state.installInspected()
    await settle()
    assert.equal(calls, 3, 'install success must invalidate and reload the catalog')
    staleList.resolve(jsonResponse(200, { packages: [{ name: 'stale' }] }))
    await olderLoad
    canonicalList.resolve(jsonResponse(200, { packages: [{ name: 'canonical' }] }))
    await installation
    assert.deepEqual(mounted.state.packages.map((item) => item.name), ['canonical'])
    assert.equal(mounted.state.inspection, null)
  } finally { mounted.cleanup() }
})

test('MarketView uses 120s for both FormData uploads while catalog requests keep the 20s default', async () => {
  const mounted = await mountMarket(jsonResponse(200, { status: 'verified', zipSha: 'digest' }))
  try {
    assert.deepEqual(mounted.timeouts, [15000, 20000])
    mounted.state.uploadFile = new File(['signed bundle'], 'bundle.zip', { type: 'application/zip' })
    await mounted.state.uploadBundle()
    assert.equal(mounted.state.inspection.zipSha, 'digest')
    await mounted.state.installInspected()
    assert.deepEqual(mounted.timeouts, [15000, 20000, 120000, 120000, 20000])
    assert.equal(mounted.uploads.length, 2)
    for (const upload of mounted.uploads) {
      assert.equal(upload.credentials, 'same-origin')
      assert.ok(upload.body instanceof FormData)
    }
  } finally { mounted.cleanup() }
})

for (const action of ['uploadBundle', 'installInspected']) {
  for (const response of [
    { status: 401, code: '' },
    { status: 403, code: 'admin.token_invalid' },
  ]) {
    test(`MarketView ${action}: ${response.status} ${response.code || 'unauthorized'} ends the shell session through fetchJSON`, async () => {
      const mounted = await mountMarket(jsonResponse(response.status, { code: response.code, message: 'session lost' }))
      try {
        mounted.state.uploadFile = new File(['signed bundle'], 'bundle.zip', { type: 'application/zip' })
        if (action === 'installInspected') mounted.state.inspection = { status: 'verified', zipSha: 'digest' }
        await mounted.state[action]()
        assert.equal(mounted.uploads.length, 1)
        assert.equal(mounted.uploads[0].credentials, 'same-origin')
        assert.ok(mounted.uploads[0].body instanceof FormData)
        assert.equal(mounted.uploads[0].headers?.['Content-Type'], undefined)
        assert.equal(mounted.uploads[0].body.get('file').name, 'bundle.zip')
        assert.equal(mounted.state.actionError, 'session lost')
        assert.deepEqual(mounted.notifications, ['session lost'])
        assert.equal(mounted.shellUser.value, null)
      } finally {
        mounted.cleanup()
      }
    })
  }
}
