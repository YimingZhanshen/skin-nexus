<script setup>
// WearTexturePicker answers the other half of 「穿什么」: WearTargetPicker picks
// the character, this picks the material for a character that is already on
// screen. It replaces two <select> dropdowns that listed the whole closet as
// plain text — with a year of collecting behind it that is a hundred-option
// list where the only clue is a hash, and no picture at all.
//
// A hash as the headline was still the wrong answer. A row now says what it is:
// the name the player gave it, 「未命名」 when nobody ever did, and the short
// fingerprint underneath either way — that is what tells two unnamed skins
// apart. Renaming happens here too, because this is the moment a person
// actually wants a name; the closet page has had a rename dialog all along, but
// using it meant leaving the picker.
import { computed, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import DialogPanel from './DialogPanel.vue'
import Pager from './Pager.vue'
import { api } from '../api.js'
import { guard401 } from '../session.js'
import { promptDialog } from '../dialog.js'
import { shortHash } from '../textureLabel.js'

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
// Twelve per page over a long-collected closet meant dozens of pages of three
// columns. The grid scrolls now, so a page is what fits on one screen to scan.
const pageSize = 24
const q = ref('')
const loading = ref(false)
const err = ref('')
const renaming = ref(0)
let seq = 0

const pages = computed(() => Math.max(1, Math.ceil(total.value / pageSize)))
const current = (r) =>
  (Number(props.textureId) && Number(r.id) === Number(props.textureId)) ||
  (!!props.textureHash && String(r.hash) === String(props.textureHash))
const what = () => (isCape.value ? t('picker.unnamedCape') : t('picker.unnamedSkin'))

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

// The alias lives on the closet entry, not on the texture, so a rename needs the
// entry id /me/wearable now returns. Zero means a row that is not a closet entry
// at all — better to say so than to fire a doomed PUT.
async function rename(r) {
  if (renaming.value) return
  if (!r.closetId) {
    err.value = t('picker.renameUnavailable')
    return
  }
  const next = await promptDialog(t('picker.renamePrompt'), r.named ? r.label : '',
    { title: t('picker.renameTitle') })
  if (next === null) return // cancelled
  renaming.value = r.closetId
  err.value = ''
  try {
    await api.closetRename(r.closetId, next.trim())
    await load()
  } catch (e) {
    if (!guard401(e)) err.value = e.message
  } finally {
    renaming.value = 0
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
      <div class="picker-cell" v-for="r in rows" :key="r.id">
        <button
          class="picker-pick"
          :class="{ current: current(r), unnamed: !r.named }"
          :disabled="current(r)"
          :title="r.hash"
          @click="emit('pick', Number(r.id))"
        >
          <img v-if="r.previewURL || r.url" :src="r.previewURL || r.url" alt="" loading="lazy" decoding="async" />
          <span class="pc-label">{{ r.named ? r.label : what() }}</span>
          <span class="muted pc-hash">{{ shortHash(r.hash) }}</span>
          <span class="muted pc-state" v-if="current(r)">{{ t('picker.current') }}</span>
        </button>
        <button class="pc-rename secondary" :disabled="renaming === r.closetId" @click="rename(r)">
          {{ t('picker.rename') }}
        </button>
      </div>
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
  grid-template-columns: repeat(auto-fill, minmax(104px, 1fr));
  /* DialogPanel bounds the width but not the height, and 24 rows would run
     straight off the bottom of the screen. */
  max-height: min(48vh, 420px);
  overflow-y: auto;
  align-content: start;
}
.picker-cell { display: flex; flex-direction: column; gap: 4px; }
.picker-pick {
  display: flex; flex-direction: column; align-items: center; gap: 2px;
  border: 1px solid var(--border); border-radius: var(--r-m); background: var(--panel);
  /* The global `button` rule pairs a solid background with light text
     (`--on-cinnabar`). This cell overrides the background to a light surface but
     is still a <button>, so without its own color the label renders light-on-
     light and vanishes — which is exactly how "the picker is all hashes" looked:
     the fingerprint line survives because `.muted` sets a color explicitly. */
  color: var(--text);
  padding: 6px; cursor: pointer; min-height: 88px; font-size: var(--fs-xs);
}
.picker-pick img { height: 56px; image-rendering: pixelated; border-radius: var(--r-s); }
.picker-pick.current { border-color: var(--accent); }
.pc-label {
  max-width: 100%; overflow: hidden; text-overflow: ellipsis; white-space: nowrap;
  font-weight: 500;
}
/* An unnamed entry IS its fingerprint, so there the hash carries the weight and
   the placeholder stays quiet — the reverse of a named one. */
.picker-pick.unnamed .pc-label { font-weight: 400; color: var(--muted); }
.picker-pick.unnamed .pc-hash { color: var(--text); }
.pc-hash { font-family: var(--mono, ui-monospace, monospace); }
.pc-state { color: var(--accent); }
.pc-rename { font-size: var(--fs-xs); padding: 4px 8px; min-height: 32px; border-radius: var(--r-s); }
.picker-empty { display: flex; flex-direction: column; gap: 4px; }
.picker-foot { margin-top: 10px; justify-content: flex-start; }
</style>
