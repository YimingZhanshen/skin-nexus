<script setup>
// DialogPanel is the one place this app puts something in front of the page.
// It wraps a native <dialog>, which is what gives the hard parts for free:
// showModal() renders in the top layer (so no overflow or stacking context can
// clip it), makes the rest of the page inert (Tab cannot leave the dialog), and
// returns focus to the trigger when it closes. Two consumers share it — the
// app-wide prompt/confirm/alert host and the wear-a-player picker — so there is
// one focus story and one visual language.
import { nextTick, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'

const props = defineProps({
  open: { type: Boolean, default: false },
  title: { type: String, default: '' },
  // Accessible name for the case with no visible heading: a confirmation whose
  // message *is* the question still has to name itself to a screen reader.
  label: { type: String, default: '' },
  okText: { type: String, required: true },
  cancelText: { type: String, default: '' },
  showCancel: { type: Boolean, default: true },
  danger: { type: Boolean, default: false },
})
const emit = defineEmits(['ok', 'cancel', 'opened'])

const { t } = useI18n()
const dlg = ref(null)

watch(() => props.open, async (on) => {
  if (!on) { if (dlg.value && dlg.value.open) dlg.value.close(); return }
  await nextTick()
  if (!dlg.value || dlg.value.open) return
  dlg.value.showModal()
  emit('opened')
})

// A click that lands on the dialog element itself came from the backdrop.
function onClick(e) { if (e.target === dlg.value) emit('cancel') }
</script>

<template>
  <dialog
    ref="dlg"
    class="dialog-panel"
    :aria-label="label || title"
    @cancel.prevent="emit('cancel')"
    @click="onClick"
  >
    <div class="panel-inner">
      <h2 v-if="title">{{ title }}</h2>
      <slot />
      <div class="panel-actions">
        <button v-if="showCancel" type="button" class="secondary" @click="emit('cancel')">
          {{ cancelText || t('common.cancel') }}
        </button>
        <button type="button" :class="{ 'danger-outline': danger }" @click="emit('ok')">{{ okText }}</button>
      </div>
    </div>
  </dialog>
</template>

<style scoped>
/* Same token shape as the server-rendered detail page's dialog
   (.report-dialog in server/cmd/skin-nexus/texturepage.go): two surfaces, one product. */
.dialog-panel {
  border: 1px solid var(--border);
  border-radius: var(--r-m);
  background: var(--panel);
  color: var(--text);
  max-width: 420px;
  width: calc(100% - 32px);
  padding: 18px;
}
.dialog-panel::backdrop { background: rgba(0, 0, 0, 0.55); }
.panel-inner h2 { font-size: var(--fs-m); margin: 0 0 8px; }
.panel-actions { display: flex; gap: 8px; justify-content: flex-end; margin-top: 14px; }
</style>
