import test from 'node:test'
import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import { fileURLToPath } from 'node:url'
import { compileScript, compileTemplate, parse } from '@vue/compiler-sfc'
import { createRenderer, nextTick } from 'vue'
import { createI18n } from 'vue-i18n'
import { createServer } from 'vite'
import en from '../../locales/en.json' with { type: 'json' }

const root = fileURLToPath(new URL('../../../', import.meta.url))
const filename = fileURLToPath(new URL('./PluginsView.vue', import.meta.url))
const { descriptor } = parse(readFileSync(filename, 'utf8'), { filename })
const script = compileScript(descriptor, { id: 'plugins-view-test' })
const template = compileTemplate({
  source: descriptor.template.content,
  filename,
  id: 'plugins-view-test',
  compilerOptions: { bindingMetadata: script.bindings, mode: 'module' },
})
assert.deepEqual(template.errors, [])
const componentURL = '/src/components/admin/PluginsView.test-compiled.js'
const componentCode = `${script.content.replace('export default', 'const component =')}\n${template.code}\ncomponent.render = render\nexport default component\n`
const vite = await createServer({
  root,
  configFile: false,
  server: { middlewareMode: true, hmr: false, ws: false },
  appType: 'custom',
  optimizeDeps: { noDiscovery: true, include: [] },
  plugins: [{
    name: 'plugins-view-test',
    enforce: 'pre',
    resolveId(source) {
      if (source === componentURL) return componentURL
      if (source.endsWith('/adminApi.js')) return '\0plugins-view-test-admin-api'
      if (source.endsWith('/api.js')) return '\0plugins-view-test-api'
      if (source.endsWith('/dialog.js')) return '\0plugins-view-test-dialog'
      if (source.endsWith('/pluginUI.js')) return '\0plugins-view-test-plugin-ui'
      if (source === '../SchemaForm.vue') return '\0plugins-view-test-schema-form'
    },
    load(id) {
      if (id === componentURL) return componentCode
      if (id === '\0plugins-view-test-admin-api') {
        return 'export const needToken = { value: false }; export const fetchJSON = (...args) => globalThis.__pluginsFetchJSON(...args)'
      }
      if (id === '\0plugins-view-test-api') return 'export const api = { me: async () => ({ permission: 2 }) }'
      if (id === '\0plugins-view-test-dialog') return 'export const confirmDialog = async () => true'
      if (id === '\0plugins-view-test-plugin-ui') return 'export const loadPluginUI = async () => {}'
      if (id === '\0plugins-view-test-schema-form') {
        return 'import { h } from "vue"; export default { props: ["schema", "modelValue", "disabled"], emits: ["update:modelValue"], setup(props, { emit }) { return () => h("input", { class: "test-schema-value", "data-schema": props.schema?.title, value: props.modelValue?.message || "", disabled: props.disabled, onInput: (event) => emit("update:modelValue", { message: event.target.value }) }) } }'
      }
    },
  }],
})
const { default: PluginsView } = await vite.ssrLoadModule(componentURL)
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

function button(rootNode, label) {
  return find(rootNode, (node) => node.type === 'button' && text(node) === label)
}

function configButton(container, title) {
  const row = find(container, (node) => node.props.class === 'plugin-row'
    && descendants(node).some((child) => child.type === 'strong' && text(child) === title))
  assert.ok(row, `missing plugin row for ${title}`)
  return button(row, 'Config')
}

function schemaInput(container) {
  return find(container, (node) => node.props.class === 'test-schema-value')
}

function alertText(container) {
  return descendants(container).filter((node) => node.props.role === 'alert').map(text).join('')
}

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

const plugins = [
  {
    name: 'alpha', title: 'Alpha', version: '1.0.0', description: 'Alpha plugin',
    trust: 'verified', source: 'manual', state: 'loaded', health: 'ok', enabled: true,
    routes: 1, uiPages: 0, permissions: [], author: 'Example',
  },
  {
    name: 'beta', title: 'Beta', version: '2.0.0', description: 'Beta plugin',
    trust: 'verified', source: 'manual', state: 'loaded', health: 'ok', enabled: true,
    routes: 2, uiPages: 0, permissions: [], author: 'Example',
  },
]

function configResponse(name, value) {
  return {
    schema: { title: `${name} schema`, type: 'object', properties: { message: { type: 'string' } } },
    values: { message: value },
  }
}

async function mountView(configRequest) {
  globalThis.__pluginsFetchJSON = async (path, options) => {
    if (path === '/api/admin/v1/plugins') return { plugins, guardsBypassed: false }
    return configRequest(path, options)
  }
  const app = renderer.createApp(PluginsView)
  const errors = []
  app.config.errorHandler = (error) => errors.push(error)
  app.use(createI18n({ legacy: false, locale: 'en', messages: { en }, warnHtmlMessage: false }))
  const container = hostNode('root')
  app.mount(container)
  await settle()
  if (errors.length) throw errors[0]
  return { app, container, errors }
}

function cleanup(app) {
  app.unmount()
  delete globalThis.__pluginsFetchJSON
}

test('a late Alpha GET does not replace the visible Beta config after switching targets', async () => {
  const alphaGet = deferred()
  const { app, container } = await mountView((path) => {
    if (path === '/api/admin/v1/plugins/alpha/config') return alphaGet.promise
    if (path === '/api/admin/v1/plugins/beta/config') return Promise.resolve(configResponse('Beta', 'beta value'))
    throw new Error(`unexpected request: ${path}`)
  })
  try {
    configButton(container, 'Alpha').props.onClick()
    await settle()
    configButton(container, 'Beta').props.onClick()
    await settle()
    assert.equal(schemaInput(container).props['data-schema'], 'Beta schema')
    assert.equal(schemaInput(container).props.value, 'beta value')

    alphaGet.resolve(configResponse('Alpha', 'late alpha value'))
    await settle()

    assert.equal(schemaInput(container).props['data-schema'], 'Beta schema')
    assert.equal(schemaInput(container).props.value, 'beta value')
    assert.match(text(container), /Plugin config: Beta/)
  } finally {
    cleanup(app)
  }
})

test('a GET from before close cannot overwrite the same plugin config after reopening', async () => {
  const firstGet = deferred()
  const secondGet = deferred()
  let alphaGets = 0
  const { app, container } = await mountView((path) => {
    assert.equal(path, '/api/admin/v1/plugins/alpha/config')
    alphaGets++
    return alphaGets === 1 ? firstGet.promise : secondGet.promise
  })
  try {
    configButton(container, 'Alpha').props.onClick()
    await settle()
    button(container, 'Close').props.onClick()
    await nextTick()
    assert.equal(schemaInput(container), undefined)
    assert.doesNotMatch(text(container), /Plugin config: Alpha/)

    configButton(container, 'Alpha').props.onClick()
    await settle()
    secondGet.resolve(configResponse('Alpha', 'reopened alpha value'))
    await settle()
    assert.equal(schemaInput(container).props.value, 'reopened alpha value')

    firstGet.resolve(configResponse('Alpha', 'stale alpha value'))
    await settle()

    assert.equal(schemaInput(container).props.value, 'reopened alpha value')
  } finally {
    cleanup(app)
  }
})

test('a late Alpha PUT response does not pollute the visible Beta config', async () => {
  const alphaPut = deferred()
  const puts = []
  const { app, container } = await mountView((path, options) => {
    if (options?.method === 'PUT') {
      puts.push([path, JSON.parse(options.body)])
      return alphaPut.promise
    }
    if (path === '/api/admin/v1/plugins/alpha/config') return Promise.resolve(configResponse('Alpha', 'alpha value'))
    if (path === '/api/admin/v1/plugins/beta/config') return Promise.resolve(configResponse('Beta', 'beta value'))
    throw new Error(`unexpected request: ${path}`)
  })
  try {
    configButton(container, 'Alpha').props.onClick()
    await settle()
    schemaInput(container).props.onInput({ target: { value: 'alpha draft' } })
    await nextTick()
    button(container, 'Save config').props.onClick()
    await settle()
    assert.deepEqual(puts, [['/api/admin/v1/plugins/alpha/config', { values: { message: 'alpha draft' } }]])

    configButton(container, 'Beta').props.onClick()
    await settle()
    assert.equal(schemaInput(container).props.value, 'beta value')
    alphaPut.resolve(configResponse('Alpha', 'saved alpha value'))
    await settle()

    assert.equal(schemaInput(container).props['data-schema'], 'Beta schema')
    assert.equal(schemaInput(container).props.value, 'beta value')
    assert.doesNotMatch(text(container), /Config saved/)
    assert.match(text(container), /Plugin config: Beta/)
  } finally {
    cleanup(app)
  }
})

test('save and schema editing stay disabled while a config PUT is pending', async () => {
  const alphaPut = deferred()
  const { app, container } = await mountView((path, options) => {
    if (options?.method === 'PUT') return alphaPut.promise
    if (path === '/api/admin/v1/plugins/alpha/config') return Promise.resolve(configResponse('Alpha', 'alpha value'))
    throw new Error(`unexpected request: ${path}`)
  })
  try {
    configButton(container, 'Alpha').props.onClick()
    await settle()
    const save = button(container, 'Save config')
    assert.equal(save.props.disabled, false)
    save.props.onClick()
    await nextTick()
    assert.equal(save.props.disabled, true)
    assert.equal(schemaInput(container).props.disabled, true)

    alphaPut.resolve(configResponse('Alpha', 'saved alpha value'))
    await settle()
    assert.equal(save.props.disabled, false)
    assert.equal(schemaInput(container).props.disabled, false)
    assert.match(text(container), /Config saved/)
  } finally {
    cleanup(app)
  }
})

test('a late Alpha GET rejection does not show an error on the Beta config', async () => {
  const alphaGet = deferred()
  const { app, container } = await mountView((path) => {
    if (path === '/api/admin/v1/plugins/alpha/config') return alphaGet.promise
    if (path === '/api/admin/v1/plugins/beta/config') return Promise.resolve(configResponse('Beta', 'beta value'))
    throw new Error(`unexpected request: ${path}`)
  })
  try {
    configButton(container, 'Alpha').props.onClick()
    await settle()
    configButton(container, 'Beta').props.onClick()
    await settle()

    alphaGet.reject(new Error('Alpha config failed'))
    await settle()

    assert.equal(alertText(container), '')
    assert.equal(schemaInput(container).props.value, 'beta value')
  } finally {
    cleanup(app)
  }
})

test('a late Alpha PUT rejection does not show an error on the Beta config', async () => {
  const alphaPut = deferred()
  const { app, container } = await mountView((path, options) => {
    if (options?.method === 'PUT') return alphaPut.promise
    if (path === '/api/admin/v1/plugins/alpha/config') return Promise.resolve(configResponse('Alpha', 'alpha value'))
    if (path === '/api/admin/v1/plugins/beta/config') return Promise.resolve(configResponse('Beta', 'beta value'))
    throw new Error(`unexpected request: ${path}`)
  })
  try {
    configButton(container, 'Alpha').props.onClick()
    await settle()
    button(container, 'Save config').props.onClick()
    await settle()
    configButton(container, 'Beta').props.onClick()
    await settle()

    alphaPut.reject(new Error('Alpha save failed'))
    await settle()

    assert.equal(alertText(container), '')
    assert.equal(schemaInput(container).props['data-schema'], 'Beta schema')
    assert.equal(schemaInput(container).props.value, 'beta value')
    assert.equal(button(container, 'Save config').props.disabled, false)
  } finally {
    cleanup(app)
  }
})
