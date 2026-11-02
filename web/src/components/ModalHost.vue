<script setup>
// ModalHost is the one dialog the whole app shares (see dialog.js). It lives
// beside ToastHub in the shell so any page — including the plugin bridge's
// proxied confirmations — can ask a question without re-implementing a focus
// trap. DialogPanel owns the mechanics; this file owns the three shapes.
import { computed, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import DialogPanel from './DialogPanel.vue'
import { dialogState, resolveDialog } from '../dialog.js'

const { t } = useI18n()
const field = ref(null)

const isPrompt = computed(() => dialogState.kind === 'prompt')
const isAlert = computed(() => dialogState.kind === 'alert')
const okText = computed(() => (isAlert.value ? t('common.close') : t('common.confirm')))

function dismiss() { resolveDialog(isPrompt.value ? null : false) }
function accept() {
  resolveDialog(isPrompt.value ? String(field.value ? field.value.value : '') : true)
}
function onOpened() {
  if (isPrompt.value && field.value) { field.value.focus(); field.value.select() }
}
</script>

<template>
  <DialogPanel
    :open="dialogState.open"
    :title="dialogState.title"
    :label="dialogState.message"
    :ok-text="okText"
    :show-cancel="!isAlert"
    :danger="dialogState.danger"
    @ok="accept"
    @cancel="dismiss"
    @opened="onOpened"
  >
    <p class="dialog-message">{{ dialogState.message }}</p>
    <input
      v-if="isPrompt"
      ref="field"
      class="dialog-field"
      :value="dialogState.value"
      @keyup.enter="accept"
    />
  </DialogPanel>
</template>

<style scoped>
.dialog-message { margin: 0; font-size: var(--fs-s); line-height: 1.6; }
.dialog-field { width: 100%; margin-top: 12px; }
</style>
