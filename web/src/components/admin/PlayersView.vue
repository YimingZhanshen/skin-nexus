<script setup>
// PlayersView: blessing 玩家管理 parity (B3) — paged table with search,
// texture thumbnails, and admin ops: rename (UUID zero-drift guaranteed
// server-side), re-home, texture swap/unmount, delete (confirm-gated,
// audited, union hub announced best-effort).
import { ref, onMounted } from 'vue'
import { useI18n } from 'vue-i18n'
import { fetchJSON } from '../../adminApi.js'
import Pager from '../Pager.vue'

const { t } = useI18n()

const players = ref([])
const total = ref(0)
const page = ref(1)
const pageSize = 50
const query = ref('')
const error = ref('')
const busy = ref(false)

async function load() {
  error.value = ''
  try {
    const qs = new URLSearchParams({ page: String(page.value) })
    if (query.value.trim()) qs.set('q', query.value.trim())
    const d = await fetchJSON(`/api/admin/v1/players?${qs}`)
    players.value = d.players || []
    total.value = d.total || 0
  } catch (e) { error.value = e.message }
}

function search() { page.value = 1; load() }

function goPage(p) {
  if (p < 1 || (p - 1) * pageSize >= total.value || p === page.value) return
  page.value = p
  load()
}
const pageMax = () => Math.max(1, Math.ceil(total.value / pageSize))

async function put(path, body) {
  if (busy.value) return false
  busy.value = true
  error.value = ''
  try {
    await fetchJSON(path, { method: 'PUT', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify(body) })
    return true
  } catch (e) { error.value = e.message; return false }
  finally { busy.value = false }
}

async function renamePlayer(p) {
  const next = prompt(t('admin.renamePrompt', { name: p.name }), p.name)
  if (next === null || next.trim() === '' || next.trim() === p.name) return
  if (await put(`/api/admin/v1/players/${p.id}/name`, { name: next.trim() })) await load()
}

async function changeOwner(p) {
  const next = prompt(t('admin.ownerPrompt', { name: p.name, uid: p.uid }), String(p.uid))
  if (next === null) return
  const uid = Number(next)
  if (!Number.isInteger(uid) || uid <= 0) { error.value = t('admin.ownerInvalid'); return }
  if (uid === p.uid) return
  if (!confirm(t('admin.ownerConfirm', { name: p.name, from: p.uid, to: uid }))) return
  if (await put(`/api/admin/v1/players/${p.id}/owner`, { uid })) await load()
}

// Texture swap: prompt for the target tid — 0 clears the slot. The picker
// shows the row's current hashes; admins curate from the texture list.
async function swapTexture(p, kind) {
  const current = kind === 'skin' ? p.skinHash : p.capeHash
  const input = prompt(
    t('admin.texturePrompt', { name: p.name, kind: t(`admin.kind${kind === 'skin' ? 'Skin' : 'Cape'}`), current: current || '—' }),
    '',
  )
  if (input === null) return
  const tid = Number(input)
  if (!Number.isInteger(tid) || tid < 0) { error.value = t('admin.textureInvalid'); return }
  if (await put(`/api/admin/v1/players/${p.id}/textures`, { type: kind, textureId: tid })) await load()
}

async function deletePlayer(p) {
  if (!confirm(t('admin.playerDeleteConfirm', { name: p.name, id: p.id }))) return
  // second gate: identity check (blessing-style double confirm for the
  // irreversible action — uuid_map row + hub announcement go with it)
  if (!confirm(t('admin.playerDeleteDouble', { name: p.name }))) return
  if (busy.value) return
  busy.value = true
  error.value = ''
  try {
    await fetchJSON(`/api/admin/v1/players/${p.id}`, { method: 'DELETE' })
    await load()
  } catch (e) { error.value = e.message }
  finally { busy.value = false }
}

function texURL(hash) { return hash ? `/textures/${hash}` : '' }

onMounted(load)
</script>

<template>
  <div class="card">
    <h2 style="margin-top: 0;">{{ t('admin.playersTitle') }}</h2>
    <div class="row">
      <input v-model="query" :placeholder="t('admin.playerSearchPlaceholder')" style="max-width: 260px;"
             @keyup.enter="search" />
      <button class="secondary" @click="search">{{ t('admin.search') }}</button>
      <span class="muted">{{ t('admin.playerTotal', { total }) }}</span>
    </div>
    <div class="error" v-if="error">{{ error }}</div>
    <div class="table-scroll" v-if="players.length">
    <table class="players-table" style="margin-top: 10px;">
      <tr>
        <th>PID</th><th>{{ t('admin.colPreview') }}</th><th>{{ t('admin.colPlayerName') }}</th>
        <th>UUID</th><th>{{ t('admin.colOwner') }}</th><th>{{ t('admin.colSkin') }}</th>
        <th>{{ t('admin.colCape') }}</th><th>{{ t('admin.colUpdated') }}</th><th></th>
      </tr>
      <tr v-for="p in players" :key="p.id">
        <td class="muted">{{ p.id }}</td>
        <td>
          <img v-if="p.skinHash" class="thumb" :src="texURL(p.skinHash)" alt="" />
          <span v-else class="muted">—</span>
        </td>
        <td class="pname">{{ p.name }}</td>
        <td><code class="uuid">{{ p.uuid || '—' }}</code></td>
        <td class="nowrap">{{ p.ownerNickname || '—' }} <span class="muted">#{{ p.uid }}</span></td>
        <td>
          <button class="secondary cell-btn" @click="swapTexture(p, 'skin')">
            <img v-if="p.skinHash" class="thumb-sm" :src="texURL(p.skinHash)" alt="" />
            <span v-else>{{ t('admin.none') }}</span>
          </button>
        </td>
        <td>
          <button class="secondary cell-btn" @click="swapTexture(p, 'cape')">
            <img v-if="p.capeHash" class="thumb-sm" :src="texURL(p.capeHash)" alt="" />
            <span v-else>{{ t('admin.none') }}</span>
          </button>
        </td>
        <td class="muted nowrap">{{ (p.updatedAt || '').slice(0, 10) }}</td>
        <td class="nowrap">
          <button class="secondary" @click="renamePlayer(p)">{{ t('admin.rename') }}</button>
          <button class="secondary" style="margin-left: 6px;" @click="changeOwner(p)">{{ t('admin.changeOwner') }}</button>
          <button class="secondary danger-outline" style="margin-left: 6px;" @click="deletePlayer(p)">{{ t('admin.delete') }}</button>
        </td>
      </tr>
    </table>
    </div>
    <p class="muted" v-else>{{ t('admin.playerEmpty') }}</p>
    <Pager :page="page" :pages="pageMax()" :total="total" @update:page="goPage" />
  </div>
</template>

<style scoped>
/* narrow viewports scroll via the global .table-scroll wrapper */
.players-table { min-width: 920px; }
.players-table th:nth-child(1), .players-table td:nth-child(1) { width: 56px; }
.players-table th:nth-child(2), .players-table td:nth-child(2) { width: 64px; text-align: center; }
.players-table th:nth-child(3), .players-table td:nth-child(3) { width: 130px; }
.players-table th:nth-child(5), .players-table td:nth-child(5) { width: 130px; }
.players-table th:nth-child(6), .players-table td:nth-child(6) { width: 80px; text-align: center; }
.players-table th:nth-child(7), .players-table td:nth-child(7) { width: 80px; text-align: center; }
.players-table th:nth-child(8), .players-table td:nth-child(8) { width: 96px; }
.nowrap { white-space: nowrap; }
.pname { font-weight: 600; }
.thumb { width: 28px; height: auto; image-rendering: pixelated; vertical-align: middle; border-radius: 4px; }
.thumb-sm { width: 18px; height: auto; image-rendering: pixelated; vertical-align: middle; }
.cell-btn { padding: 3px 8px; min-width: 48px; display: inline-flex; align-items: center; justify-content: center; }
.uuid { font-size: 11px; letter-spacing: .2px; }
</style>
