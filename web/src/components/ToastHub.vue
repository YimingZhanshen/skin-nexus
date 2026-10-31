<script setup>
// SkinNexus · Copyright (C) 2026 SkinNexus contributors · AGPL-3.0
// ToastHub — global consumer of the 'skin-toast' window event (plugins'
// ui.toast bridge and any host code). Announced politely, auto-dismissed.
import { ref, onMounted, onBeforeUnmount } from 'vue'
import { useI18n } from 'vue-i18n'

const { t } = useI18n()
const toasts = ref([])
let seq = 0
const timers = []

function push(detail) {
  const message = String((detail && detail.message) || '')
  if (!message) return
  const raw = String((detail && detail.level) || 'info')
  const level = raw === 'ok' || raw === 'success' ? 'success' : raw === 'error' ? 'error' : 'info'
  const id = ++seq
  toasts.value = [...toasts.value.slice(-3), { id, message, level }]
  timers.push(setTimeout(() => {
    toasts.value = toasts.value.filter((x) => x.id !== id)
  }, 4200))
}

function dismiss(id) {
  toasts.value = toasts.value.filter((x) => x.id !== id)
}

function onToast(e) { push(e.detail) }

onMounted(() => window.addEventListener('skin-toast', onToast))
onBeforeUnmount(() => {
  window.removeEventListener('skin-toast', onToast)
  timers.forEach(clearTimeout)
})
</script>

<template>
  <div class="toast-stack" role="status" aria-live="polite">
    <div v-for="x in toasts" :key="x.id" class="toast" :class="x.level">
      <span>{{ x.message }}</span>
      <button class="toast-x" :aria-label="t('common.close')" @click="dismiss(x.id)">×</button>
    </div>
  </div>
</template>

<style scoped>
.toast-stack {
  position: fixed; right: 20px; bottom: 20px; z-index: var(--z-toast, 70);
  display: flex; flex-direction: column; gap: 8px; max-width: min(360px, calc(100vw - 40px));
  pointer-events: none;
}
.toast {
  display: flex; align-items: flex-start; gap: 10px;
  background: var(--panel); color: var(--text);
  border: 1px solid var(--line-strong); border-left: 3px solid var(--jade);
  border-radius: var(--r-m); padding: 10px 12px;
  font-size: var(--fs-s); line-height: 1.5;
  box-shadow: var(--shadow-lift);
  pointer-events: auto;
  animation: toast-in var(--t-mid) var(--ease) both;
}
.toast.success { border-left-color: var(--ok); }
.toast.error { border-left-color: var(--danger); }
.toast .toast-x {
  background: none; border: none; color: var(--muted); padding: 0 2px;
  min-height: 0; font-size: var(--fs-m); line-height: 1;
}
.toast .toast-x:hover { color: var(--text); background: none; box-shadow: none; transform: none; }
@keyframes toast-in { from { opacity: 0; transform: translateY(8px); } to { opacity: 1; transform: none; } }
@media (prefers-reduced-motion: reduce) {
  .toast { animation: none; }
}
</style>
