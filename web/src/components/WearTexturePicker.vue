<script setup>
// WearTexturePicker answers the other half of 「穿什么」: WearTargetPicker picks
// the character, this picks the material for a character that is already on
// screen. It replaces two <select> dropdowns that listed the whole closet as
// plain text — with a year of collecting behind it that is a hundred-option
// list where the only clue is a hash, and no picture at all.
import { computed, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import DialogPanel from './DialogPanel.vue'
import Pager from './Pager.vue'
import { api } from '../api.js'
import { guard401 } from '../session.js'

const props = defineProps({
  open: { type: Boolean, default: false },
  type: { type: String, default: 'skin' }, // 'skin' | 'cape'
  textureId: { type: [Number, String], default: 0 },
  // The content address of what is already mounted. /me only carries the avatar
  // hash (no tid), and a hash identifies the bytes regardless of which closet
  // entry points at them — so a caller that has either can ask for the mark.
  textureHash: { type: String, default: '' },
})
const emit = defineEmits(['close', 'pick'])

const { t } = useI18n()
const isCape = computed(() => props.type === 'cape')
const rows = ref([])
const total = ref(0)
const page = ref(1)
const pageSize = 12
const q = ref('')
const loading = ref(false)
const err = ref('')
let seq = 0

const pages = computed(() => Math.max(1, Math.ceil(total.value / pageSize)))
const shortHash = (h) => (h ? String(h).slice(0, 12) + '…' : '—')
const current = (r) =>
  (Number(props.textureId) && Number(r.id) === Number(props.textureId)) ||
  (!!props.textureHash && String(r.hash) === String(props.textureHash))
const label = (r) => r.label || shortHash(r.hash)

async function load() {
  const mine = ++seq
  loading.value = true
  err.value = ''
  try {
    const d = await api.wearable(props.type, q.value.trim(), page.value, pageSize)
    if (mine !== seq) return
    rows.value = d.textures || []
    total.value = d.total || 0
  } catch (e) {
    if (mine !== seq) return
    if (!guard401(e)) err.value = e.message
  } finally {
    if (mine === seq) loading.value = false
  }
}

let timer = null
watch(q, () => {
  clearTimeout(timer)
  timer = setTimeout(() => { page.value = 1; load() }, 300)
})
function goPage(p) {
  if (p < 1 || p > pages.value || p === page.value) return
  page.value = p
  load()
}
// Opening the dialog while it still holds last session's rows would flash the
// wrong list for a frame, and a re-open after a change elsewhere must not read
// a stale page.
watch(() => props.open, (on) => { if (on) { page.value = 1; load() } })
</script>

<template>
  <DialogPanel
    :open="open"
    :title="isCape ? t('picker.titleCape') : t('picker.titleSkin')"
    :label="isCape ? t('picker.titleCape') : t('picker.titleSkin')"
    :ok-text="t('common.close')"
    :show-cancel="false"
    @ok="emit('close')"
    @cancel="emit('close')"
  >
    <div class="row picker-bar">
      <input v-model="q" :placeholder="t('picker.search')" :aria-label="t('picker.search')" />
      <span class="muted">{{ t('common.total', { n: total }) }}</span>
    </div>
    <div class="error" role="alert" v-if="err">{{ err }}</div>
    <p class="muted" v-if="loading">{{ t('common.loading') }}</p>
    <div class="picker-grid" v-else-if="rows.length">
      <button
        v-for="r in rows"
        :key="r.id"
        class="picker-cell"
        :class="{ current: current(r) }"
        :disabled="current(r)"
        :title="r.hash"
        @click="emit('pick', Number(r.id))"
      >
        <img v-if="r.previewURL || r.url" :src="r.previewURL || r.url" alt="" loading="lazy" decoding="async" />
        <span class="pc-label">{{ label(r) }}</span>
        <span class="muted pc-hash">{{ shortHash(r.hash) }}</span>
        <span class="muted pc-state" v-if="current(r)">{{ t('picker.current') }}</span>
      </button>
    </div>
    <div v-else class="picker-empty">
      <p class="muted">{{ q ? t('picker.emptyFiltered') : t('picker.empty') }}</p>
      <p class="muted">{{ t('picker.hint') }}</p>
    </div>
    <Pager :page="page" :pages="pages" :total="total" @update:page="goPage" />
    <div class="row picker-foot">
      <button class="secondary danger-outline" :disabled="!textureId && !textureHash" @click="emit('pick', 0)">
        {{ isCape ? t('dashboard.noCape') : t('dashboard.noSkin') }}
      </button>
    </div>
  </DialogPanel>
</template>

<style scoped>
.picker-bar { gap: 8px; align-items: center; flex-wrap: wrap; }
.picker-bar input { flex: 1; min-width: 160px; max-width: 280px; }
.picker-grid {
  display: grid; gap: 8px; margin-top: 8px;
  grid-template-columns: repeat(auto-fill, minmax(96px, 1fr));
}
.picker-cell {
  display: flex; flex-direction: column; align-items: center; gap: 2px;
  border: 1px solid var(--border); border-radius: var(--r-m); background: var(--panel);
  padding: 6px; cursor: pointer; min-height: 88px; font-size: var(--fs-xs);
}
.picker-cell img { height: 56px; image-rendering: pixelated; border-radius: 4px; }
.picker-cell.current { border-color: var(--accent); }
.pc-label {
  max-width: 100%; overflow: hidden; text-overflow: ellipsis; white-space: nowrap;
  font-weight: 600;
}
.pc-hash { font-family: var(--mono, ui-monospace, monospace); }
.pc-state { color: var(--accent); }
.picker-empty { display: flex; flex-direction: column; gap: 4px; }
.picker-foot { margin-top: 10px; justify-content: flex-start; }
</style>
