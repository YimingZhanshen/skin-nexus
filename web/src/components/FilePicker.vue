<script setup>
// FilePicker: the styled equivalent of the browser's 「选择文件」 chip.
// The plugin/theme upload rows used to embed a raw <input type=file>, which
// sits in the page as a foreign object: it is the one control on the panel that
// no other button matches, and once a file is picked it shows only a path the
// operator cannot read at a glance. The material upload on the dashboard
// already solved this; this is that shape, reused.
// Labels come from the dashboard namespace on purpose: it already says 「选择文件」/
// 「清除已选文件」, and a second copy of the same words in a second namespace is
// how a panel ends up with three labels for one action. The byte size goes to
// format.js for the same reason: this component rounded its own way — 1500 B
// read「1 KB」here and「1.5 KB」in the lists around it, and a gigabyte-sized
// pick read「1024.0 MB」because the local copy had no GB step.
import { ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { formatBytes } from '../format.js'

const props = defineProps({
  file: { type: Object, default: null },
  accept: { type: String, default: '' },
  disabled: { type: Boolean, default: false },
  // Saying why the control is dead is the difference between「按不动」and
  // 「我知道差什么」— a greyed button alone teaches nothing.
  disabledReason: { type: String, default: '' },
  hint: { type: String, default: '' },
})
const emit = defineEmits(['update:file'])

const { t } = useI18n()
const input = ref(null)

function pick() {
  if (props.disabled) return
  if (input.value) input.value.click()
}
function onInput(e) {
  emit('update:file', (e.target.files && e.target.files[0]) || null)
}
function clear() {
  emit('update:file', null)
  // The native input keeps its own selection; without this, choosing the same
  // file again fires no change event and the row looks stuck.
  if (input.value) input.value.value = ''
}
</script>

<template>
  <div class="fp">
    <input ref="input" type="file" :accept="accept" class="fp-hidden" @change="onInput" />
    <div class="fp-row">
      <button type="button" class="secondary" :disabled="disabled" @click="pick">{{ t('dashboard.chooseFile') }}</button>
      <span class="fp-name muted" v-if="file" :title="file.name">
        {{ file.name }} <span class="muted" v-if="Number(file.size) > 0">· {{ formatBytes(file.size) }}</span>
      </span>
      <button type="button" class="secondary danger-outline" v-if="file" @click="clear">{{ t('dashboard.clearFile') }}</button>
    </div>
    <p class="fp-note muted" v-if="disabled && disabledReason">{{ disabledReason }}</p>
    <p class="fp-note muted" v-else-if="hint">{{ hint }}</p>
  </div>
</template>

<style scoped>
.fp { display: flex; flex-direction: column; gap: 4px; min-width: 0; }
.fp-hidden { position: absolute; width: 1px; height: 1px; opacity: 0; pointer-events: none; }
.fp-row { display: flex; align-items: center; gap: 8px; min-width: 0; }
.fp-name { flex: 1; min-width: 0; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; font-size: var(--fs-xs); }
.fp-note { margin: 0; font-size: var(--fs-xs); }
</style>
