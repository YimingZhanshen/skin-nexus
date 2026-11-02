<script setup>
// PlayersPage (A 级批次): 用户侧角色管理 —— 卡片网格（大预览 + 披风同框）
// + 改名 + 删除（双确认）+ 皮肤/披风更换。UUID 零漂移由后端保证（改名不碰 uuid_map.uuid）。
import { ref, onMounted } from 'vue'
import { useI18n } from 'vue-i18n'
import { api } from '../api.js'
import { guard401 } from '../session.js'
import { confirmDialog, promptDialog } from '../dialog.js'
import SkinPreview from '../components/SkinPreview.vue'
import PluginSlot from '../components/plugin/PluginSlot.vue'

const { t } = useI18n()

const players = ref([])
// The picker reads the wearable set — the closet, which is exactly what the
// server will accept — split by type. Own uploads reach it because upload
// collects them (and migration 0022 collected the history).
const skins = ref([])
const capes = ref([])
const newName = ref('')
const err = ref('')
const ok = ref('')
// Cards hold a dropdown each: without one-in-flight and a response stamp, a
// double-click sent two texture changes and a slow /me list could overwrite a
// fresher one.
const creating = ref(false)
const busyId = ref(0)
let reqSeq = 0

async function load() {
  const seq = ++reqSeq
  err.value = ''
  try {
    const [p, s, c] = await Promise.all([api.players(), api.wearable('skin'), api.wearable('cape')])
    if (seq !== reqSeq) return
    players.value = p.players || []
    skins.value = s.textures || []
    capes.value = c.textures || []
  } catch (e) { if (seq === reqSeq && !guard401(e)) err.value = e.message }
}

async function createPlayer() {
  if (creating.value) return
  err.value = ''; ok.value = ''
  creating.value = true
  try {
    await api.createPlayer(newName.value)
    newName.value = ''
    ok.value = t('players.created')
    await load()
  } catch (e) { if (!guard401(e)) err.value = e.message } finally { creating.value = false }
}

function pid(p) { return p.ID ?? p.id }
function pname(p) { return p.Name || p.name }
function puuid(p) { return p.UUID || p.uuid || '' }

async function renamePlayer(p) {
  if (busyId.value) return
  const next = await promptDialog(t('players.renamePrompt', { name: pname(p) }), pname(p))
  if (next === null || next.trim() === '' || next.trim() === pname(p)) return
  err.value = ''; ok.value = ''
  busyId.value = pid(p)
  try {
    await api.renamePlayer(pid(p), next.trim())
    ok.value = t('players.renamed')
    await load()
  } catch (e) { if (!guard401(e)) err.value = e.message } finally { busyId.value = 0 }
}

// Double-confirm (blessing parity for irreversible actions): identity echo
// on the second prompt.
async function deletePlayer(p) {
  if (busyId.value) return
  if (!(await confirmDialog(t('players.deleteConfirm', { name: pname(p), id: pid(p) }), { danger: true }))) return
  if (!(await confirmDialog(t('players.deleteDouble', { name: pname(p) }), { danger: true }))) return
  err.value = ''; ok.value = ''
  busyId.value = pid(p)
  try {
    await api.deletePlayer(pid(p))
    ok.value = t('players.deleted')
    await load()
  } catch (e) { if (!guard401(e)) err.value = e.message } finally { busyId.value = 0 }
}

// currentTid mirrors the player's mounted texture in the <select> (review
// B P2: without it the picker always showed 无皮肤). The payload now carries the
// mounted texture's own id, so the match is exact — the earlier URL-suffix
// compare broke silently whenever a plugin URL filter rewrote the address.
function currentTid(p, kind) {
  const raw = kind === 'cape' ? (p.CapeTID ?? p.capeTID) : (p.SkinTID ?? p.skinTID)
  const tid = Number(raw || 0)
  if (!tid) return ''
  const list = kind === 'skin' ? skins.value : capes.value
  return list.some(x => Number(x.id) === tid) ? String(tid) : ''
}

// The preview must agree with the mounted skin: an Alex (slim) skin rendered on
// the Steve model looks wrong and used to be indistinguishable from a broken
// upload. /me/players carries SkinModel; the texture lookup is the fallback for
// older payloads.
function currentModel(p) {
  const fromPlayer = p.SkinModel || p.skinModel || ''
  if (fromPlayer) return fromPlayer === 'slim' ? 'slim' : 'default'
  const tid = currentTid(p, 'skin')
  const tx = skins.value.find(x => String(x.id) === tid)
  return tx && tx.model === 'slim' ? 'slim' : 'default'
}

async function swapTexture(p, kind, e) {
  const v = parseInt(e.target.value || '0', 10)
  if (busyId.value) { e.target.value = currentTid(p, kind); return }
  err.value = ''; ok.value = ''
  busyId.value = pid(p)
  try {
    if (kind === 'skin') await api.setSkin(pid(p), v)
    else await api.setCape(pid(p), v)
    ok.value = t('players.textureSet')
  } catch (ex) { if (!guard401(ex)) err.value = ex.message }
  finally { busyId.value = 0; try { await load() } catch (ex) { if (!guard401(ex)) err.value = ex.message } }
}

onMounted(load)
</script>

<template>
  <div class="card">
    <h2 style="margin-top: 0;">{{ t('players.title', { count: players.length }) }}</h2>
    <p class="muted">{{ t('players.hint') }}</p>
    <div class="row">
      <input v-model="newName" :placeholder="t('dashboard.newPlayerName')" style="max-width: 220px;" @keyup.enter="createPlayer" />
      <button :disabled="creating" @click="createPlayer">{{ creating ? t('common.pleaseWait') : t('dashboard.createPlayer') }}</button>
    </div>
    <span class="field-hint">{{ t('players.nameRule') }}</span>
    <div class="error" role="alert" v-if="err">{{ err }}</div>
    <div class="success" role="status" v-if="ok">{{ ok }}</div>
    <div class="player-grid" v-if="players.length">
      <article class="player-card" v-for="p in players" :key="pid(p)">
        <div class="pc-preview">
          <SkinPreview :url="p.SkinURL || p.skinurl" :cape-url="p.CapeURL || p.capeurl"
                       :model-type="currentModel(p)" :width="160" :height="240" />
        </div>
        <div class="pc-body">
          <h3 class="pc-name" :title="pname(p)">{{ pname(p) }}</h3>
          <div class="pc-meta">
            <span class="muted">PID {{ pid(p) }}</span>
            <code class="uuid" :title="puuid(p)">{{ puuid(p) }}</code>
          </div>
          <label class="pc-field">
            <span>{{ t('dashboard.colSkin') }}</span>
            <select :disabled="busyId === pid(p)" :value="currentTid(p, 'skin')" @change="swapTexture(p, 'skin', $event)">
              <option value="">{{ t('dashboard.noSkin') }}</option>
              <option v-for="tx in skins" :key="tx.id" :value="String(tx.id)" :title="tx.hash">{{ tx.label }}</option>
            </select>
          </label>
          <label class="pc-field">
            <span>{{ t('dashboard.colCape') }}</span>
            <select :disabled="busyId === pid(p)" :value="currentTid(p, 'cape')" @change="swapTexture(p, 'cape', $event)">
              <option value="">{{ t('dashboard.noCape') }}</option>
              <option v-for="tx in capes" :key="tx.id" :value="String(tx.id)" :title="tx.hash">{{ tx.label }}</option>
            </select>
          </label>
          <div class="pc-actions">
            <button class="secondary" :disabled="busyId === pid(p)" @click="renamePlayer(p)">{{ t('players.rename') }}</button>
            <button class="secondary danger-outline" :disabled="busyId === pid(p)" @click="deletePlayer(p)">{{ t('common.delete') }}</button>
          </div>
        </div>
      </article>
    </div>
    <p class="muted" v-else>{{ t('players.empty') }}</p>
    <PluginSlot slot="player.card.footer" />
  </div>
</template>

<style scoped>
.player-grid {
  display: grid; gap: 12px; margin-top: 12px;
  grid-template-columns: repeat(auto-fill, minmax(240px, 1fr));
}
.player-card {
  border: 1px solid var(--border); border-radius: var(--r-m);
  background: var(--panel); overflow: hidden; display: flex; flex-direction: column;
}
.pc-preview {
  display: flex; justify-content: center; align-items: center;
  background: var(--bg); padding: 8px 0;
}
/* 160×240 is what SkinPreview is told to render; pinning the display size keeps
   the backing store and the box equal, so the skin is not resampled. */
.pc-preview :deep(canvas) { width: 160px; height: 240px; display: block; }
.pc-body { padding: 10px 12px 12px; display: flex; flex-direction: column; gap: 8px; }
.pc-name { margin: 0; font-size: var(--fs-m); font-weight: 600; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
.pc-meta { display: flex; flex-direction: column; gap: 2px; font-size: var(--fs-xs); }
/* A 32-char UUID has no break points; side by side with PID it wrapped into
   "PID 1 / 2". Stacked plus break-all keeps both readable. */
.uuid { font-size: var(--fs-xs); word-break: break-all; line-height: 1.35; }
.pc-field { display: flex; flex-direction: column; gap: 4px; margin: 0; font-size: var(--fs-xs); color: var(--muted); }
.pc-field select { width: 100%; padding: 4px 6px; font-size: var(--fs-s); }
.pc-actions { display: flex; gap: 8px; margin-top: 2px; }
.pc-actions button { flex: 1; }
</style>
