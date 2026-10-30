// Plugin UI registry: one fetch per page load, merged into vue-i18n under
// plugin.<name>.* so plugin pages/widgets never edit the static locale files.
import { reactive } from 'vue'
import { i18n } from './i18n.js'
import { fetchWithTimeout } from './net.js'

export const pluginUI = reactive({ plugins: [], capabilities: [], slots: [], hostVersion: '' })

export async function loadPluginUI() {
  try {
    // Boot awaits this; a stalled edge request must not hold the shell.
    const r = await fetchWithTimeout('/api/v1/plugins/ui', {}, 8000)
    if (!r.ok) return
    const d = await r.json()
    pluginUI.plugins = d.plugins || []
    pluginUI.capabilities = d.capabilities || []
    pluginUI.slots = d.slots || []
    pluginUI.hostVersion = d.hostVersion || ''
    for (const p of pluginUI.plugins) {
      for (const [locale, kv] of Object.entries(p.i18n || {})) {
        i18n.global.mergeLocaleMessage(locale, { plugin: { [p.plugin]: kv } })
      }
    }
  } catch { /* plugin UI is best-effort */ }
}

export function hostHasCapability(cap) {
  return pluginUI.capabilities.includes(cap)
}

export function pages(area) {
  const out = []
  for (const p of pluginUI.plugins) {
    for (const page of (p.pages || [])) {
      if (page.area === area) out.push({ ...page, plugin: p.plugin })
    }
  }
  return out
}

export function navPages(area) {
  return pages(area).filter((p) => p.nav && p.nav.label)
}

export function pageByPath(path) {
  const want = '/' + String(path || '').replace(/^\/+/, '')
  for (const p of pluginUI.plugins) {
    for (const page of (p.pages || [])) {
      if (page.path === want) return { ...page, plugin: p.plugin }
    }
  }
  return null
}

export function slotsFor(id) {
  const out = []
  for (const p of pluginUI.plugins) {
    for (const slot of (p.slots || [])) {
      if (slot.id === id) out.push({ ...slot, plugin: p.plugin })
    }
  }
  return out
}

export function assetURL(plugin, src) {
  const rel = String(src || '').replace(/^\/+/, '')
  const base = `/plugin-assets/${encodeURIComponent(plugin)}/${rel.split('/').map(encodeURIComponent).join('/')}`
  const p = pluginUI.plugins.find((x) => x.plugin === plugin)
  const v = p && p.version ? `?v=${encodeURIComponent(p.version)}` : ''
  return base + v
}

export function formsFor(formID) {
  const out = []
  for (const p of pluginUI.plugins) {
    const form = p.forms && p.forms[formID]
    if (form) out.push({ plugin: p.plugin, title: p.title || p.plugin, schema: form.schema || {} })
  }
  return out
}
