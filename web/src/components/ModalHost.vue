<script setup>
// ModalHost renders the one dialog the whole app shares (see dialog.js). It
// lives next to ToastHub in the shell so any page — and the plugin bridge's
// proxied confirmations — can ask a question without each caller re-implementing
// a focus trap. <dialog> gives that for free: showModal() puts it in the top
// layer, makes the rest of the page inert, keeps Tab inside it, restores focus
// to the trigger on close and fires `cancel` on Escape.
import { computed, nextTick, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { dialogState, resolveDialog } from '../dialog.js'

const { t } = useI18n()
const dlg = ref(null)
const field = ref(null)

const isPrompt = computed(() => dialogState.kind === 'prompt')
const isAlert = computed(() => dialogState.kind === 'alert')
const okLabel = computed(() => (isAlert.value ? t('common.close') : t('common.confirm')))

function dismiss() { resolveDialog(isPrompt.value ? null : false) }
function submit(e) {
  // method="dialog" would close the element itself; the value is read here so
  // the promise resolves before the browser's default close fires `close`.
  e.preventDefault()
  resolveDialog(isPrompt.value ? String(field.value ? field.value.value : '') : true)
}

watch(() => dialogState.open, async (on) => {
  if (!on) { if (dlg.value && dlg.value.open) dlg.value.close(); return }
  await nextTick()
  if (!dlg.value || dlg.value.open) return
  dlg.value.showModal()
  if (isPrompt.value && field.value) { field.value.focus(); field.value.select() }
})

// A click that lands on the dialog element itself came from the backdrop.
function onBackdropClick(e) { if (e.target === dlg.value) dismiss() }
</script>

<template>
  <dialog
    ref="dlg"
    class="app-dialog"
    :aria-label="dialogState.message"
    @cancel.prevent="dismiss"
    @click="onBackdropClick"
  >
    <div class="dialog-inner">
      <h2 v-if="dialogState.title">{{ dialogState.title }}</h2>
      <p class="dialog-message">{{ dialogState.message }}</p>
      <input
        v-if="isPrompt"
        ref="field"
        class="dialog-field"
        :value="dialogState.value"
        @keyup.enter="submit"
      />
      <div class="dialog-actions">
        <button v-if="!isAlert" type="button" class="secondary" @click="dismiss">{{ t('common.cancel') }}</button>
        <button type="button" :class="{ 'danger-outline': dialogState.danger }" @click="submit">{{ okLabel }}</button>
      </div>
    </div>
  </dialog>
</template>

<style scoped>
/* Same token shape as the server-rendered detail page's dialog, so the two
   surfaces read as one product: .report-dialog in server/cmd/skin-nexus/texturepage.go */
.app-dialog {
  border: 1px solid var(--border);
  border-radius: var(--r-m);
  background: var(--panel);
  color: var(--text);
  max-width: 420px;
  width: calc(100% - 32px);
  padding: 18px;
}
.app-dialog::backdrop { background: rgba(0, 0, 0, 0.55); }
.dialog-inner h2 { font-size: var(--fs-m); margin: 0 0 8px; }
.dialog-message { margin: 0; font-size: var(--fs-s); line-height: 1.6; }
.dialog-field { width: 100%; margin-top: 12px; }
.dialog-actions { display: flex; gap: 8px; justify-content: flex-end; margin-top: 14px; }
</style>
