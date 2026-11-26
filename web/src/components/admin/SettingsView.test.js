import test from 'node:test'
import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import { fileURLToPath, pathToFileURL } from 'node:url'
import { compileScript, compileTemplate, parse } from '@vue/compiler-sfc'
import { createRenderer, nextTick } from 'vue'
import { createI18n } from 'vue-i18n'
import { createServer } from 'vite'
import en from '../../locales/en.json' with { type: 'json' }

const root = fileURLToPath(new URL('../../../', import.meta.url))
function compile(file, id) {
  const filename = fileURLToPath(new URL(file, import.meta.url))
  const { descriptor } = parse(readFileSync(filename, 'utf8'), { filename })
  const script = compileScript(descriptor, { id })
  const template = compileTemplate({
    source: descriptor.template.content,
    filename,
    id,
    compilerOptions: { bindingMetadata: script.bindings, mode: 'module' },
  })
  assert.deepEqual(template.errors, [])
  return `${script.content.replace('export default', 'const component =')}\n${template.code}\ncomponent.render = render\nexport default component\n`
}
const modules = new Map([
  ['/src/components/admin/SettingsView.test-compiled.js', compile('./SettingsView.vue', 'settings-view-test')],
  ['/src/components/admin/SeoSection.test-compiled.js', compile('./SeoSection.vue', 'seo-section-test')],
])
const stubs = new Map([
  ['\0settings-test-seo', 'import { h } from "vue"; export default { setup: () => () => h("div") }'],
  ['\0settings-test-theme', 'import { h } from "vue"; export default { setup: () => () => h("div") }'],
  ['\0settings-test-oidc', 'import { h } from "vue"; export default { setup: () => () => h("div") }'],
  ['\0settings-test-dialog', 'export async function confirmDialog(){return true}'],
  ['\0settings-test-errors', 'export function describe(data,status){return data.message || `HTTP ${status}`}; export function transportError(){return "network"}'],
])
const vite = await createServer({
  root,
  configFile: false,
  server: { middlewareMode: true, hmr: false, ws: false },
  appType: 'custom',
  optimizeDeps: { noDiscovery: true, include: [] },
  plugins: [{
    name: 'settings-view-test',
    enforce: 'pre',
    resolveId(source) {
      if (modules.has(source)) return source
      if (source === '../../adminApi.js') return pathToFileURL(fileURLToPath(new URL('../../adminApi.js', import.meta.url))).href
      if (source === './SeoSection.vue') return '\0settings-test-seo'
      if (source === './ThemeMarketSection.vue') return '\0settings-test-theme'
      if (source === './OidcClientsCard.vue') return '\0settings-test-oidc'
      if (source === '../../dialog.js') return '\0settings-test-dialog'
      if (source.endsWith('/errors.js') || source === './errors.js') return '\0settings-test-errors'
    },
    load(id) {
      if (modules.has(id)) return modules.get(id)
      if (stubs.has(id)) return stubs.get(id)
    },
  }],
})
const { default: SettingsView } = await vite.ssrLoadModule('/src/components/admin/SettingsView.test-compiled.js')
const { default: SeoSection } = await vite.ssrLoadModule('/src/components/admin/SeoSection.test-compiled.js')
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
function jsonResponse(body) {
  return new Response(JSON.stringify(body), { status: 200, headers: { 'Content-Type': 'application/json' } })
}
async function mount(component, fetchHandler) {
  const previousFetch = globalThis.fetch
  globalThis.fetch = fetchHandler
  const app = renderer.createApp(component)
  const errors = []
  app.config.errorHandler = (error) => errors.push(error)
  app.use(createI18n({ legacy: false, locale: 'en', messages: { en }, warnHtmlMessage: false }))
  const container = hostNode('root')
  app.mount(container)
  await settle()
  if (errors.length) throw errors[0]
  return {
    app,
    state: app._instance.setupState,
    previousFetch,
    cleanup() {
      app.unmount()
      globalThis.fetch = previousFetch
    },
  }
}
function appearance(footerText) {
  return { footer_text: footerText, footer_links: [], custom_css: '', custom_js: '', active_theme: '' }
}

for (const outcome of ['success', 'rejection']) {
  test(`SettingsView appearSave invalidates a delayed loader ${outcome} and reloads canonical appearance`, async () => {
    const oldLoader = deferred()
    const canonicalLoader = deferred()
    const pendingSave = deferred()
    let appearanceCalls = 0
    const mounted = await mount(SettingsView, async (path, options) => {
      if (path === '/api/admin/v1/settings') return jsonResponse({ settings: [] })
      if (path === '/api/admin/v1/themes') return jsonResponse({ themes: [] })
      if (path === '/api/admin/v1/appearance' && options?.method === 'POST') return pendingSave.promise
      if (path === '/api/admin/v1/appearance') {
        appearanceCalls++
        return appearanceCalls === 1 ? oldLoader.promise : canonicalLoader.promise
      }
      throw new Error(`unexpected request: ${path}`)
    })
    try {
      assert.equal(appearanceCalls, 1)
      mounted.state.appearance = appearance('Draft')
      const saving = mounted.state.appearSave()
      if (outcome === 'success') oldLoader.resolve(jsonResponse(appearance('Stale loader')))
      else oldLoader.reject(new Error('stale loader failure'))
      await settle()
      assert.equal(mounted.state.appearance.footer_text, 'Draft', 'the invalidated loader cannot replace the draft')
      assert.equal(mounted.state.appearError, '')

      pendingSave.resolve(jsonResponse({}))
      await settle()
      assert.equal(appearanceCalls, 2, 'save success must reload canonical appearance and themes')
      assert.equal(mounted.state.appearOk, en.common.saved)
      canonicalLoader.resolve(jsonResponse(appearance('Canonical')))
      await saving
      assert.equal(mounted.state.appearance.footer_text, 'Canonical')
    } finally {
      mounted.cleanup()
    }
  })
}

for (const outcome of ['success', 'rejection']) {
  test(`SettingsView appearSave does not publish ${outcome} after unmount`, async () => {
    const pendingSave = deferred()
    const mounted = await mount(SettingsView, async (path, options) => {
      if (path === '/api/admin/v1/settings') return jsonResponse({ settings: [] })
      if (path === '/api/admin/v1/themes') return jsonResponse({ themes: [] })
      if (path === '/api/admin/v1/appearance' && options?.method === 'POST') return pendingSave.promise
      if (path === '/api/admin/v1/appearance') return jsonResponse(appearance('Loaded'))
      throw new Error(`unexpected request: ${path}`)
    })
    const saving = mounted.state.appearSave()
    const before = {
      appearance: mounted.state.appearance,
      themes: mounted.state.themes,
      error: mounted.state.appearError,
      ok: mounted.state.appearOk,
    }
    mounted.app.unmount()
    if (outcome === 'success') pendingSave.resolve(jsonResponse({}))
    else pendingSave.reject(new Error('late save failure'))
    await saving
    await settle()
    assert.deepEqual({
      appearance: mounted.state.appearance,
      themes: mounted.state.themes,
      error: mounted.state.appearError,
      ok: mounted.state.appearOk,
    }, before)
    globalThis.fetch = mounted.previousFetch
  })
}

for (const outcome of ['success', 'rejection']) {
  test(`SeoSection seoSave invalidates a delayed loader ${outcome} and reloads canonical SEO`, async () => {
    const oldLoader = deferred()
    const canonicalLoader = deferred()
    const pendingSave = deferred()
    let seoCalls = 0
    const mounted = await mount(SeoSection, async (path, options) => {
      if (path === '/api/admin/v1/seo' && options?.method === 'POST') return pendingSave.promise
      if (path === '/api/admin/v1/seo') {
        seoCalls++
        return seoCalls === 1 ? oldLoader.promise : canonicalLoader.promise
      }
      throw new Error(`unexpected request: ${path}`)
    })
    try {
      assert.equal(seoCalls, 1)
      mounted.state.seo = { title: 'Draft', description: '', keywords: '', og_image: '', canonical: '', sitemap: true, noindex: false, robots: '' }
      const saving = mounted.state.seoSave()
      if (outcome === 'success') oldLoader.resolve(jsonResponse({ title: 'Stale loader' }))
      else oldLoader.reject(new Error('stale loader failure'))
      await settle()
      assert.equal(mounted.state.seo.title, 'Draft', 'the invalidated loader cannot replace the draft')
      assert.equal(mounted.state.seoError, '')

      pendingSave.resolve(jsonResponse({}))
      await settle()
      assert.equal(seoCalls, 2, 'save success must reload canonical SEO')
      assert.equal(mounted.state.seoOk, en.common.saved)
      canonicalLoader.resolve(jsonResponse({ title: 'Canonical' }))
      await saving
      assert.equal(mounted.state.seo.title, 'Canonical')
    } finally {
      mounted.cleanup()
    }
  })
}

for (const outcome of ['success', 'rejection']) {
  test(`SeoSection seoSave does not publish ${outcome} after unmount`, async () => {
    const pendingSave = deferred()
    const mounted = await mount(SeoSection, async (path, options) => {
      if (path === '/api/admin/v1/seo' && options?.method === 'POST') return pendingSave.promise
      if (path === '/api/admin/v1/seo') return jsonResponse({ title: 'Loaded' })
      throw new Error(`unexpected request: ${path}`)
    })
    const saving = mounted.state.seoSave()
    const before = { seo: mounted.state.seo, error: mounted.state.seoError, ok: mounted.state.seoOk }
    mounted.app.unmount()
    if (outcome === 'success') pendingSave.resolve(jsonResponse({}))
    else pendingSave.reject(new Error('late save failure'))
    await saving
    await settle()
    assert.deepEqual({ seo: mounted.state.seo, error: mounted.state.seoError, ok: mounted.state.seoOk }, before)
    globalThis.fetch = mounted.previousFetch
  })
}
