<script setup>
// UnionBindPage: MUA Union member-side profile management — server list,
// bind (get 2-min token) / bindto / unbind / UUID remap.
// All endpoints exist only when SKIN_UNION_ENABLED; otherwise the API 404s
// and this page shows a friendly notice (zero surface for non-alliance sites).
import { ref, onMounted, computed, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { api, union } from '../api.js'
import { guard401 } from '../session.js'

const { t } = useI18n()

const players = ref([])
const servers = ref([])
const serverVersion = ref('')
const error = ref('')
const success = ref('')
const loading = ref(false)

// bind token workflow
const bindToken = ref('')
const bindTokenFor = ref('')
const bindTokenExpire = ref(0)
const bindtoToken = ref('')
const selectedPID = ref(null)

// remap workflow
const remapTarget = ref('')
// combobox suggestions for the remap target (review P0-1): own local players
// plus the union-mapped uuid and same-name candidates from the hub.
const remapSuggestions = ref([]) // [{ value, label }]

const bindTokenAlive = computed(() =>
  bindToken.value && Date.now() < bindTokenExpire.value)

const selectedPlayer = computed(() =>
  players.value.find(p => playerUUID(p) === selectedPID.value) || null)

async function load() {
  error.value = ''; success.value = ''
  try {
    players.value = (await api.players()).players || []
  } catch (e) { if (!guard401(e)) error.value = e.message; return }
  try {
    const d = await union.servers()
    servers.value = d.servers || []
    serverVersion.value = d.version || ''
    fillServerNames(servers.value) // async, best-effort (old plugin servers.twig:20-31)
  } catch (e) {
    // disabled module -> friendly notice, not an error storm
    servers.value = []
    error.value = e.message.includes('404') ? t('union.disabled') : e.message
  }
  loadRemapSuggestions() // the remap card is open now (review P0-1)
}

// The hub ships `name` empty on member entries; the old plugin fills each
// row's display name client-side by fetching that member's
// {bs_root}/api/yggdrasil and reading meta.serverName (servers.twig:20-31).
// Mirrored here — failures just leave the fallback chain in place.
const serverNames = ref({})
function fillServerNames(list) {
  list.forEach(async (s) => {
    const root = String(s.bs_root || '').replace(/\/+$/, '')
    if (!root) return
    try {
      const r = await fetch(root + '/api/yggdrasil')
      const d = await r.json()
      const n = d && d.meta && d.meta.serverName
      if (n) serverNames.value = { ...serverNames.value, [s.code]: n }
    } catch { /* keep fallback */ }
  })
}
function serverDisplayName(s) {
  return serverNames.value[s.code] || s.name || String(s.bs_root || '').replace(/^https?:\/\//, '') || '—'
}

function playerUUID(p) { return p.UUID || p.uuid || '' }
function playerName(p) { return p.Name || p.name || '' }

// The backend stores the target as trimmed lowercase hex without dashes
// (webapi handleUnionRemapUUID): normalize the same way on every path.
function normUUID(v) { return String(v || '').trim().toLowerCase().replaceAll('-', '') }
function isHex32(v) { return /^[0-9a-f]{32}$/.test(v) }

// Lazy target suggestions: opened card / changed source profile. Any failure
// degrades to free-text-only input (review P0-1).
let sugSeq = 0
async function loadRemapSuggestions() {
  const token = ++sugSeq
  const p = selectedPlayer.value
  const out = new Map() // normalized uuid -> display label
  const self = p ? normUUID(playerUUID(p)) : ''
  if (p) {
    for (const q of players.value) {
      const u = normUUID(playerUUID(q))
      if (u && u !== self) out.set(u, playerName(q))
    }
    const [detail, unmapped] = await Promise.allSettled([
      union.detail(playerUUID(p)),
      union.unmapped(playerName(p)),
    ])
    if (detail.status === 'fulfilled' && detail.value && detail.value.mapped_uuid) {
      const u = normUUID(detail.value.mapped_uuid)
      if (u && u !== self) out.set(u, detail.value.bind_mapped_name || '')
    }
    if (unmapped.status === 'fulfilled') {
      for (const pr of (unmapped.value && unmapped.value.profiles) || []) {
        const u = normUUID(pr.uuid)
        if (u && u !== self) out.set(u, pr.name || '')
      }
    }
  }
  if (token !== sugSeq) return // a newer selection already answered
  remapSuggestions.value = [...out].map(([value, label]) => ({ value, label }))
}

async function doBind() {
  const p = selectedPlayer.value
  if (!p) return
  loading.value = true; error.value = ''; success.value = ''
  try {
    const d = await union.bind(playerUUID(p))
    if (d.error) { error.value = d.error; return }
    bindToken.value = d.token
    bindTokenFor.value = playerName(p)
    bindTokenExpire.value = Date.now() + 2 * 60 * 1000 // hub TTL 2min
    success.value = t('union.bindCodeSuccess')
  } catch (e) { if (!guard401(e)) error.value = e.message } finally { loading.value = false }
}

async function doBindTo() {
  const p = selectedPlayer.value
  if (!p || !bindtoToken.value.trim()) { error.value = t('union.bindTokenMissing'); return }
  loading.value = true; error.value = ''; success.value = ''
  try {
    const d = await union.bindto(playerUUID(p), bindtoToken.value.trim())
    if (d.error) { error.value = d.error; return }
    success.value = t('union.bindSuccess')
    bindtoToken.value = ''
  } catch (e) { if (!guard401(e)) error.value = e.message } finally { loading.value = false }
}

async function doUnbind() {
  const p = selectedPlayer.value
  if (!p) return
  if (!confirm(t('union.unbindConfirm', { name: playerName(p) }))) return
  loading.value = true; error.value = ''; success.value = ''
  try {
    const d = await union.unbind(playerUUID(p))
    if (d.error) { error.value = d.error; return }
    success.value = t('union.unbindDone')
  } catch (e) { if (!guard401(e)) error.value = e.message } finally { loading.value = false }
}

async function doRemap() {
  const p = selectedPlayer.value
  const target = normUUID(remapTarget.value) // one shape on every submit path
  if (!p || !target) { error.value = t('union.remapMissing'); return }
  if (target === normUUID(playerUUID(p))) { error.value = t('union.remapSelf'); return }
  if (!isHex32(target)) { error.value = t('union.remapTargetInvalid'); return }
  if (!confirm(t('union.remapConfirm', { name: playerName(p), target: target.slice(0, 16) + '…' }))) return
  loading.value = true; error.value = ''; success.value = ''
  try {
    const d = await union.remapuuid(playerUUID(p), target)
    if (d.error) { error.value = d.error; return }
    success.value = t('union.remapDone')
    remapTarget.value = ''
  } catch (e) { if (!guard401(e)) error.value = e.message } finally { loading.value = false }
}

watch(selectedPID, loadRemapSuggestions) // reload suggestions per source profile
onMounted(load)
</script>

<template>
  <div>
    <div class="card">
      <h2>{{ t('union.title') }}</h2>
      <p class="muted">
        {{ t('union.intro') }}
        <template v-if="serverVersion">{{ t('union.serverListVersion', { version: serverVersion }) }}</template>
      </p>
      <div class="error" v-if="error">{{ error }}</div>
      <div class="success" v-if="success">{{ success }}</div>

      <template v-if="players.length">
        <label>{{ t('union.selectProfile') }}</label>
        <select v-model="selectedPID" style="max-width: 320px;">
          <option v-for="p in players" :key="p.id" :value="playerUUID(p)">
            {{ playerName(p) }}（{{ playerUUID(p).slice(0, 16) }}…）
          </option>
        </select>
        <div class="row" style="margin-top: 12px;">
          <button :disabled="loading || !selectedPID" @click="doBind">{{ t('union.genBindCode') }}</button>
          <input v-model="bindtoToken" :placeholder="t('union.bindTokenPlaceholder')" style="max-width: 240px;" />
          <button class="secondary" :disabled="loading || !selectedPID" @click="doBindTo">{{ t('union.bindWithToken') }}</button>
          <button class="secondary" :disabled="loading || !selectedPID" @click="doUnbind">{{ t('union.unbind') }}</button>
        </div>
        <div v-if="bindTokenAlive" class="success" style="margin-top: 8px;">
          {{ t('union.bindCodeTitle', { name: bindTokenFor }) }}：<code>{{ bindToken }}</code>{{ t('union.bindCodeHint') }}
        </div>
      </template>
      <div v-else class="muted">{{ t('union.needProfile') }}</div>
    </div>

    <div class="card" v-if="players.length >= 1">
      <h2>{{ t('union.remapTitle') }}</h2>
      <p class="muted">{{ t('union.remapIntro') }}</p>
      <div class="row">
        <select v-model="selectedPID" style="max-width: 280px;">
          <option v-for="p in players" :key="p.id" :value="playerUUID(p)">
            {{ t('union.remapCurrent', { name: playerName(p) }) }}
          </option>
        </select>
        <!-- combobox: free-text 32-hex uuid + lazily loaded suggestions -->
        <input v-model="remapTarget" list="remap-target-suggestions"
               :placeholder="t('union.remapTarget')" style="max-width: 280px;" />
        <datalist id="remap-target-suggestions">
          <option v-for="s in remapSuggestions" :key="s.value" :value="s.value">{{ s.label }}</option>
        </datalist>
        <button class="secondary" :disabled="loading || !remapTarget" @click="doRemap">{{ t('union.remapSubmit') }}</button>
      </div>
    </div>

    <div class="card" v-if="servers.length">
      <h2>{{ t('union.serversTitle', { count: servers.length }) }}</h2>
      <div class="table-scroll">
      <table>
        <tr><th>{{ t('union.colCode') }}</th><th>{{ t('union.colName') }}</th></tr>
        <tr v-for="s in servers" :key="s.code">
          <td><code>{{ s.code }}</code></td>
          <td>{{ serverDisplayName(s) }}</td>
        </tr>
      </table>
      </div>
    </div>
  </div>
</template>
