<script setup>
// Sandboxed plugin UI. The frame never receives cookies or the admin token;
// every API call goes through the host bridge, which applies the plugin route
// tier, CSRF and rate limits.
import { onBeforeUnmount, onMounted, ref } from 'vue'
import { assetURL } from '../../pluginUI.js'

const props = defineProps({
  plugin: { type: String, required: true },
  src: { type: String, required: true },
  title: { type: String, default: '' },
  height: { type: Number, default: 180 },
})
const frame = ref(null)
const height = ref(props.height)
const nonce = Math.random().toString(36).slice(2) + Date.now().toString(36)
let csrf = ''
let user = null
let ready = false

function cssVar(name, fallback) {
  const v = getComputedStyle(document.documentElement).getPropertyValue(name).trim()
  return v || fallback
}

function isDarkColor(value) {
  const s = String(value || '').trim()
  let m = /^#([0-9a-f]{3})$/i.exec(s)
  if (m) {
    const h = m[1]
    return (parseInt(h[0] + h[0], 16) * 299 + parseInt(h[1] + h[1], 16) * 587 + parseInt(h[2] + h[2], 16) * 114) / 1000 < 128
  }
  m = /^#([0-9a-f]{6})$/i.exec(s)
  if (m) {
    const h = m[1]
    return (parseInt(h.slice(0, 2), 16) * 299 + parseInt(h.slice(2, 4), 16) * 587 + parseInt(h.slice(4, 6), 16) * 114) / 1000 < 128
  }
  m = /^rgba?\(\s*(\d+)[,\s]+(\d+)[,\s]+(\d+)/i.exec(s)
  if (m) return (Number(m[1]) * 299 + Number(m[2]) * 587 + Number(m[3]) * 114) / 1000 < 128
  return true
}

function themePayload() {
  const bg = cssVar('--bg', '#101825')
  return {
    bg,
    panel: cssVar('--panel', '#16202e'),
    border: cssVar('--border', 'rgba(169,138,95,.24)'),
    text: cssVar('--text', '#e9edf1'),
    muted: cssVar('--muted', '#a8b4c3'),
    accent: cssVar('--accent', '#b0453a'),
    dark: isDarkColor(bg),
  }
}

function send(type, payload, id) {
  const win = frame.value && frame.value.contentWindow
  if (!win) return
  win.postMessage({ v: 1, nonce, id, type, payload }, '*')
}

async function init() {
  if (!frame.value) return
  await loadContext()
  send('init', {
    plugin: props.plugin, src: props.src, origin: location.origin,
    path: location.pathname + location.search,
    locale: document.documentElement.lang || 'zh-CN', user, csrf, theme: themePayload(),
  })
}

async function loadContext() {
  if (csrf) return
  try {
    const r = await fetch(`/api/plugins/${encodeURIComponent(props.plugin)}/_csrf`, { credentials: 'same-origin' })
    if (r.ok) csrf = (await r.json()).token || ''
  } catch { /* public page or plugin disabled */ }
  try {
    const r = await fetch('/api/v1/me', { credentials: 'same-origin' })
    if (r.ok) user = await r.json()
  } catch { /* anonymous */ }
}

async function request(payload, id) {
  const path = '/' + String(payload.path || '').replace(/^\/+/, '')
  const method = String(payload.method || 'GET').toUpperCase()
  const headers = { 'Content-Type': 'application/json' }
  if (csrf && method !== 'GET' && method !== 'HEAD') headers['X-CSRF-Token'] = csrf
  let body
  if (payload.body !== undefined && method !== 'GET' && method !== 'HEAD') body = JSON.stringify(payload.body)
  try {
    const r = await fetch(`/api/plugins/${encodeURIComponent(props.plugin)}${path}`, {
      method, headers, body, credentials: 'same-origin',
    })
    let out = null
    try { out = await r.json() } catch { /* non-JSON */ }
    send('response', { status: r.status, body: out }, id)
  } catch (e) {
    send('response', { status: 0, error: e.message }, id)
  }
}

function onMessage(ev) {
  if (!frame.value || ev.source !== frame.value.contentWindow) return
  if (ev.origin !== location.origin && ev.origin !== 'null') return
  const msg = ev.data
  if (!msg || msg.v !== 1) return
  if (msg.type === 'ready') { ready = true; init(); return }
  if (!ready || msg.nonce !== nonce) return
  const p = msg.payload || {}
  switch (msg.type) {
    case 'resize': {
      const h = Math.max(80, Math.min(2000, Number(p.height) || 120))
      height.value = h
      break
    }
    case 'request': request(p, msg.id); break
    case 'navigate':
      if (typeof p.path === 'string' && p.path.startsWith('/') && !p.path.startsWith('//')) location.assign(p.path)
      break
    case 'confirm':
      send('confirm-result', { ok: window.confirm(String(p.message || '')) }, msg.id)
      break
    case 'toast':
      window.dispatchEvent(new CustomEvent('skin-toast', { detail: { message: String(p.message || ''), level: p.level || 'info' } }))
      break
  }
}

onMounted(() => {
  window.addEventListener('message', onMessage)
})
onBeforeUnmount(() => {
  window.removeEventListener('message', onMessage)
})
</script>

<template>
  <div class="plugin-frame" data-skin-plugin-frame :data-plugin="plugin">
    <iframe ref="frame" :src="assetURL(plugin, src)" :title="title || plugin"
            sandbox="allow-scripts allow-forms"
            :style="{ width: '100%', height: height + 'px', border: 0 }"
            @load="init" />
  </div>
</template>
