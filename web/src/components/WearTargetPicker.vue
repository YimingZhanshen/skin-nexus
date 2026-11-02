<script setup>
// WearTargetPicker answers the question the closet and the skin library both
// raise and neither used to be able to finish: "I have this texture — now put it
// on my character". Before this, the only way to wear something was to find the
// players page and pick from two long dropdowns, which is a different mental
// step from the one the player is standing in.
//
// Each row shows the player's current head (a server-rendered avatar, so no
// extra WebGL context per row) and marks the one already wearing this texture by
// content address — not by URL, which any plugin URL filter may have rewritten.
import { computed, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import DialogPanel from './DialogPanel.vue'
import { api } from '../api.js'
import { guard401 } from '../session.js'

const props = defineProps({
  open: { type: Boolean, default: false },
  type: { type: String, default: 'skin' }, // 'skin' | 'cape'
  textureId: { type: [Number, String], default: 0 },
  textureName: { type: String, default: '' },
})
const emit = defineEmits(['close', 'worn', 'need-player'])

const { t } = useI18n()
const players = ref([])
const loading = ref(false)
const err = ref('')
const busyId = ref(0)

const isCape = computed(() => props.type === 'cape')

function tid(p, kind) {
  return Number((kind === 'cape' ? (p.CapeTID ?? p.capeTID) : (p.SkinTID ?? p.skinTID)) || 0)
}
function worn(p) { return tid(p, props.type) === Number(props.textureId) }
function avatarOf(p) {
  const name = p.Name || p.name || ''
  return '/avatar/player/' + encodeURIComponent(name) + '?size=48'
}

async function load() {
  err.value = ''
  loading.value = true
  try {
    const r = await api.players()
    players.value = r.players || []
  } catch (e) { if (!guard401(e)) err.value = e.message } finally { loading.value = false }
}

watch(() => props.open, (on) => { if (on) load() })

async function wear(p) {
  const pid = p.ID ?? p.id
  if (!pid || busyId.value) return
  err.value = ''
  busyId.value = pid
  try {
    if (isCape.value) await api.setCape(pid, props.textureId)
    else await api.setSkin(pid, props.textureId)
    emit('worn', { name: p.Name || p.name, type: props.type, textureName: props.textureName })
    emit('close')
  } catch (e) {
    if (!guard401(e)) err.value = e.message
  } finally { busyId.value = 0 }
}
</script>

<template>
  <DialogPanel
    :open="open"
    :title="t('wear.title')"
    :label="t('wear.title')"
    :ok-text="t('common.close')"
    :show-cancel="false"
    @ok="emit('close')"
    @cancel="emit('close')"
  >
    <p class="muted wear-target-hint">{{ isCape ? t('wear.capeHint') : t('wear.skinHint', { name: textureName }) }}</p>
    <div class="error" role="alert" v-if="err">{{ err }}</div>
    <p class="muted" v-if="loading">{{ t('common.loading') }}</p>
    <template v-else-if="players.length">
      <button
        v-for="p in players"
        :key="p.ID ?? p.id"
        class="wear-target"
        :class="{ current: worn(p) }"
        :disabled="worn(p) || busyId === (p.ID ?? p.id)"
        @click="wear(p)"
      >
        <img class="wt-avatar" :src="avatarOf(p)" width="32" height="32" :alt="t('wear.avatarAlt')" loading="lazy" decoding="async" />
        <span class="wt-name">{{ p.Name || p.name }}</span>
        <span class="muted wt-state" v-if="worn(p)">{{ t('wear.already') }}</span>
        <span class="muted wt-state" v-else>{{ t('wear.pick') }}</span>
      </button>
    </template>
    <div v-else class="wear-empty">
      <p class="muted">{{ t('wear.noPlayers') }}</p>
      <button class="secondary" @click="emit('close'); emit('need-player')">{{ t('wear.goPlayers') }}</button>
    </div>
  </DialogPanel>
</template>

<style scoped>
.wear-target-hint { margin: 0 0 8px; font-size: var(--fs-s); }
.wear-target {
  display: flex; align-items: center; gap: 10px; width: 100%;
  margin: 6px 0; text-align: left; min-height: 44px;
}
.wear-target.current { opacity: 0.55; }
.wt-avatar { image-rendering: pixelated; border-radius: 4px; }
.wt-name { flex: 1; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
.wt-state { font-size: var(--fs-xs); }
.wear-empty { display: flex; flex-direction: column; gap: 8px; align-items: flex-start; }
</style>
