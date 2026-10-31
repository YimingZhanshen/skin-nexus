

<script setup>
// SkinNexus · Copyright (C) 2026 SkinNexus contributors · AGPL-3.0
// Sandboxed plugin UI. The frame never receives cookies or the admin token;
// every API call goes through the host bridge, which applies the plugin route
// tier, CSRF and rate limits.
import { inject, onBeforeUnmount, onMounted, ref } from 'vue'
import { assetURL } from '../../pluginUI.js'

const props = defineProps({
  plugin: { type: String, required: true },
  src: { type: String, required: true },
  title: { type: String, default: '' },
  height: { type: Number, default: 180 },
})
const frame = ref(null)
const height = ref(props.height)
const frameKey = ref(0)
const state = ref('loading') // loading | ready | error
const nonce = Math.random().toString(36).slice(2) + Date.now().toString(36)
const sessionUser = inject('sessionUser', null)
const csrfCache = new Map()
let csrf = ''
let user = null
let ready = false
let readyTimer = null

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
    link: cssVar('--jade', '#67b7a0'),
    danger: cssVar('--danger', '#d9564c'),
    ok: cssVar('--ok', '#6fbf7d'),
    dark: isDarkColor(bg),
  }
}

// Vue refs expose reactive proxies; structured clone rejects them, so the
// user snapshot must be converted to a plain object before postMessage.
function plain(value) {
  try { return value == null ? value : JSON.parse(JSON.stringify(value)) } catch { return null }
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
    locale: document.documentElement.lang || 'zh-CN', user: plain(user), csrf, theme: themePayload(),
  })
}

async function loadContext() {
  if (csrf) return
  try {
    const cached = csrfCache.get(props.plugin)
    if (cached) {
      csrf = cached
    } else {
      const r = await fetch(`/api/plugins/${encodeURIComponent(props.plugin)}/_csrf`, { credentials: 'same-origin' })
      if (r.ok) {
        csrf = (await r.json()).token || ''
        csrfCache.set(props.plugin, csrf)
      }
    }
  } catch { /* public page or plugin disabled */ }
  // App boot already resolved the session; only fall back to /me when this
  // frame is mounted outside the SPA shell (defensive).
  if (sessionUser && sessionUser.value) {
    user = sessionUser.value
    return
  }
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
  if (msg.type === 'ready') {
    ready = true
    state.value = 'ready'
    if (readyTimer) { clearTimeout(readyTimer); readyTimer = null }
    init()
    return
  }
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

function armReadyTimeout() {
  if (readyTimer) clearTimeout(readyTimer)
  readyTimer = setTimeout(() => {
    if (!ready) state.value = 'error'
  }, 8000)
}

function retry() {
  ready = false
  state.value = 'loading'
  frameKey.value++
  armReadyTimeout()
}

onMounted(() => {
  window.addEventListener('message', onMessage)
  armReadyTimeout()
})
onBeforeUnmount(() => {
  window.removeEventListener('message', onMessage)
  if (readyTimer) clearTimeout(readyTimer)
})
</script>

<template>
  <div class="plugin-frame" data-skin-plugin-frame :data-plugin="plugin">
    <div v-if="state === 'loading'" class="plugin-loading" role="status" aria-live="polite">
      <span class="spinner" aria-hidden="true"></span>
      <span class="muted">加载插件界面…</span>
    </div>
    <div v-else-if="state === 'error'" class="plugin-loading">
      <span class="muted">插件界面加载失败</span>
      <button class="secondary" @click="retry">重试</button>
    </div>
    <iframe :key="frameKey" ref="frame" :src="assetURL(plugin, src)" :title="title || plugin"
            sandbox="allow-scripts allow-forms"
            :style="{ width: '100%', height: height + 'px', border: 0 }"
            @load="init" />
  </div>
</template>

<style scoped>
.plugin-frame { position: relative; }
.plugin-loading {
  position: absolute; inset: 0; z-index: 1; display: flex;
  align-items: center; justify-content: center; gap: 10px;
  background: var(--bg, #101825); color: var(--text, #e9edf1);
}
.plugin-loading .spinner {
  width: 18px; height: 18px; border-radius: 50%;
  border: 2px solid var(--border, rgba(169,138,95,.3));
  border-top-color: var(--accent, #b0453a);
  animation: plugin-spin .8s linear infinite;
}
@keyframes plugin-spin { to { transform: rotate(360deg); } }
@media (prefers-reduced-motion: reduce) { .plugin-loading .spinner { animation-duration: 2.4s; } }
</style>
