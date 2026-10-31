<script setup>
// PlayersPage (A 级批次): 用户侧角色管理 —— PID 列 + 改名 + 删除（双确认）
// + 皮肤/披风更换。UUID 零漂移由后端保证（改名不碰 uuid_map.uuid）。
import { ref, onMounted } from 'vue'
import { useI18n } from 'vue-i18n'
import { api } from '../api.js'
import { guard401 } from '../session.js'
import SkinPreview from '../components/SkinPreview.vue'
import PluginSlot from '../components/plugin/PluginSlot.vue'

const { t } = useI18n()

const players = ref([])
const textures = ref([])
const newName = ref('')
const err = ref('')
const ok = ref('')

async function load() {
  err.value = ''
  try {
    const [p, tx] = await Promise.all([api.players(), api.textures()])
    players.value = p.players || []
    textures.value = tx.textures || []
  } catch (e) { if (!guard401(e)) err.value = e.message }
}

function pid(p) { return p.ID ?? p.id }
function pname(p) { return p.Name || p.name }
function puuid(p) { return p.UUID || p.uuid || '' }

async function createPlayer() {
  err.value = ''; ok.value = ''
  try {
    await api.createPlayer(newName.value)
    newName.value = ''
    ok.value = t('players.created')
    await load()
  } catch (e) { if (!guard401(e)) err.value = e.message }
}

async function renamePlayer(p) {
  const next = prompt(t('players.renamePrompt', { name: pname(p) }), pname(p))
  if (next === null || next.trim() === '' || next.trim() === pname(p)) return
  err.value = ''; ok.value = ''
  try {
    await api.renamePlayer(pid(p), next.trim())
    ok.value = t('players.renamed')
    await load()
  } catch (e) { if (!guard401(e)) err.value = e.message }
}

// Double-confirm (blessing parity for irreversible actions): identity echo
// on the second prompt.
async function deletePlayer(p) {
  if (!confirm(t('players.deleteConfirm', { name: pname(p), id: pid(p) }))) return
  if (!confirm(t('players.deleteDouble', { name: pname(p) }))) return
  err.value = ''; ok.value = ''
  try {
    await api.deletePlayer(pid(p))
    ok.value = t('players.deleted')
    await load()
  } catch (e) { if (!guard401(e)) err.value = e.message }
}

// currentTid mirrors the player's mounted texture in the <select> (review
// B P2: without it the picker always showed 无皮肤).
function currentTid(p, kind) {
  const url = (kind === 'skin' ? (p.SkinURL || p.skinurl) : (p.CapeURL || p.capeurl)) || ''
  if (!url) return ''
  const tx = textures.value.find(x => url.endsWith(x.hash))
  return tx ? String(tx.id) : ''
}

async function swapTexture(p, kind, e) {
  const v = parseInt(e.target.value || '0', 10)
  err.value = ''; ok.value = ''
  try {
    if (kind === 'skin') await api.setSkin(pid(p), v)
    else await api.setCape(pid(p), v)
    ok.value = t('players.textureSet')
  } catch (ex) { if (!guard401(ex)) err.value = ex.message }
  finally {
    try { await load() } catch (ex) { if (!guard401(ex)) err.value = ex.message }
  }
}

onMounted(load)
</script>

<template>
  <div class="card">
    <h2 style="margin-top: 0;">{{ t('players.title', { count: players.length }) }}</h2>
    <p class="muted">{{ t('players.hint') }}</p>
    <div class="row">
      <input v-model="newName" :placeholder="t('dashboard.newPlayerName')" style="max-width: 220px;" @keyup.enter="createPlayer" />
      <button @click="createPlayer">{{ t('dashboard.createPlayer') }}</button>
    </div>
    <div class="error" v-if="err">{{ err }}</div>
    <div class="success" v-if="ok">{{ ok }}</div>
    <div class="table-scroll" v-if="players.length">
    <table class="p-table" style="margin-top: 12px;">
      <tr>
        <th>PID</th>
        <th>{{ t('players.colPreview') }}</th>
        <th>{{ t('players.colName') }}</th>
        <th>UUID</th>
        <th>{{ t('dashboard.colSkin') }}</th>
        <th>{{ t('dashboard.colCape') }}</th>
        <th></th>
      </tr>
      <tr v-for="p in players" :key="pid(p)">
        <td class="muted">{{ pid(p) }}</td>
        <td class="prev-cell"><SkinPreview :url="p.SkinURL || p.skinurl" /></td>
        <td class="pname">{{ pname(p) }}</td>
        <td><code class="uuid">{{ puuid(p).slice(0, 18) }}…</code></td>
        <td>
          <select class="cell-sel" :value="currentTid(p, 'skin')" @change="swapTexture(p, 'skin', $event)">
            <option value="">{{ t('dashboard.noSkin') }}</option>
            <option v-for="tx in textures.filter(x => x.type === 'skin')" :key="tx.id" :value="String(tx.id)">{{ tx.hash.slice(0, 10) }}…</option>
          </select>
        </td>
        <td>
          <select class="cell-sel" :value="currentTid(p, 'cape')" @change="swapTexture(p, 'cape', $event)">
            <option value="">{{ t('dashboard.noCape') }}</option>
            <option v-for="tx in textures.filter(x => x.type === 'cape')" :key="tx.id" :value="String(tx.id)">{{ tx.hash.slice(0, 10) }}…</option>
          </select>
        </td>
        <td class="nowrap">
          <button class="secondary" @click="renamePlayer(p)">{{ t('players.rename') }}</button>
          <button class="secondary danger-outline" style="margin-left: 6px;" @click="deletePlayer(p)">{{ t('common.delete') }}</button>
        </td>
      </tr>
    </table>
    </div>
    <p class="muted" v-else>{{ t('players.empty') }}</p>
    <PluginSlot slot="player.card.footer" />
  </div>
</template>

<style scoped>
/* narrow viewports scroll via the global .table-scroll wrapper */
.p-table { min-width: 860px; }
.p-table th:nth-child(1), .p-table td:nth-child(1) { width: 48px; }
.p-table th:nth-child(2), .p-table td:nth-child(2) { width: 72px; text-align: center; }
.p-table th:nth-child(3), .p-table td:nth-child(3) { width: 130px; }
.p-table th:nth-child(5), .p-table td:nth-child(5) { width: 160px; }
.p-table th:nth-child(6), .p-table td:nth-child(6) { width: 160px; }
/* SkinPreview renders a 128×192 canvas — scale it down to row height or
   it stretches the table far past the card (screenshot bug) */
.prev-cell :deep(canvas) { width: 48px !important; height: 72px !important; }
.pname { font-weight: 600; }
.uuid { font-size: var(--fs-xs); }
.cell-sel { width: 100%; padding: 4px 6px; font-size: var(--fs-xs); }
.nowrap { white-space: nowrap; }
</style>
