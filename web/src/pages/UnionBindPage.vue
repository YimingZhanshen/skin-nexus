<script setup>
// UnionBindPage: MUA Union member-side profile management — server list,
// bind (get 2-min token) / bindto / unbind / UUID remap.
// All endpoints exist only when SKIN_UNION_ENABLED; otherwise the API 404s
// and this page shows a friendly notice (zero surface for non-alliance sites).
import { ref, onMounted, computed, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { api, union } from '../api.js'
import { guard401 } from '../session.js'
import { confirmDialog } from '../dialog.js'

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

// The hub's rating of this station. `null` means "the hub did not say" and is
// rendered as such: SL0 is a real verdict (this station is not on the union
// blacklist), so a read that failed must never fall through to it.
const level = ref(null)
const levelState = ref('loading') // loading | ok | unknown — the badge has three looks

async function loadLevel() {
  try {
    const d = await union.securityLevel()
    const n = Number(d && d.level)
    if (Number.isInteger(n) && n >= 0) {
      level.value = n
      levelState.value = 'ok'
    } else {
      level.value = null
      levelState.value = 'unknown'
    }
  } catch (e) {
    // A station that is not in the union at all, and a hub that is down, both
    // land here. Neither is the player's mistake, so it stays a quiet fact
    // instead of an error banner: the bind cards below still work.
    level.value = null
    levelState.value = 'unknown'
  }
}

const levelClass = computed(() => {
  if (levelState.value !== 'ok') return ''
  return level.value === 3 ? 'ok' : 'gold'
})

const levelText = computed(() => {
  if (levelState.value === 'loading') return t('union.levelLoading')
  if (levelState.value !== 'ok') return t('union.levelUnknown')
  if (level.value === 3) return t('union.level3')
  if (level.value === 1) return t('union.level1')
  if (level.value === 0) return t('union.level0')
  return t('union.levelOther', { level: level.value })
})

const levelBadge = computed(() => (levelState.value === 'ok' ? 'SL' + level.value : 'SL —'))

async function load() {
  error.value = ''; success.value = ''
  try {
    players.value = (await api.players()).players || []
  } catch (e) { if (!guard401(e)) error.value = e.message; return }
  try {
    const d = await union.servers()
    servers.value = d.servers || []
    serverVersion.value = d.version || ''
  } catch (e) {
    // disabled module -> friendly notice, not an error storm
    servers.value = []
    error.value = e.message.includes('404') ? t('union.disabled') : e.message
  }
  loadLevel() // the rating line fills itself in; it must not delay the cards
  loadRemapSuggestions() // the remap card is open now (review P0-1)
}

// The hub list carries {code,bs_root} and nothing else: `name` is empty on all
// 53 entries, so the address is the only identifier the hub gives us, and it is
// shown on every row.
//
// The name on top of it comes from the peer itself, read by this station:
// GET {bs_root}/api/yggdrasil -> meta.serverName, which is what the old plugin
// asked the visitor's browser to do (servers.twig:22-33). A browser cannot do
// that here — our CSP sets connect-src 'self', so every cross-origin read died
// before it left, which is why this page used to show addresses under a header
// that said "Name". Server-side it works, and it goes through the guarded
// outbound client in the background (union.MemberNames): the request that opens
// this page never waits on fifty peers, so a first visit after a restart may
// still show bare addresses and the next one shows names.
function serverAddress(s) {
  return String(s.bs_root || '').replace(/^https?:\/\//, '') || '—'
}
// The hub list can repeat a code with a different address (live: YITUEM ships
// twice), so a code is not a row identity — keying on it collides.
function serverKey(s) { return s.code + '|' + s.bs_root }

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
  if (!(await confirmDialog(t('union.unbindConfirm', { name: playerName(p) }), { danger: true }))) return
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
  if (!(await confirmDialog(t('union.remapConfirm', { name: playerName(p), target: target.slice(0, 16) + '…' }), { danger: true }))) return
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
      <div class="error" role="alert" v-if="error">{{ error }}</div>
      <div class="success" role="status" v-if="success">{{ success }}</div>

      <!-- The hub's rating of this station (yggdrasil-connect union.twig:26-43).
           Occupied from the first paint, so the card does not jump when it lands. -->
      <p class="union-level">
        <span class="muted">{{ t('union.levelLabel') }}</span>
        <span class="badge" :class="levelClass">{{ levelBadge }}</span>
        <span>{{ levelText }}</span>
      </p>

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
      <p class="muted">{{ t('union.serversNote') }}</p>
      <div class="table-scroll">
      <table>
        <caption class="sr-only">{{ t('union.serversTitle', { count: servers.length }) }}</caption>
        <tr><th scope="col">{{ t('union.colCode') }}</th><th scope="col">{{ t('union.colSite') }}</th></tr>
        <tr v-for="s in servers" :key="serverKey(s)">
          <td><code>{{ s.code }}</code></td>
          <td>
            <div v-if="s.name">{{ s.name }}</div>
            <code class="muted">{{ serverAddress(s) }}</code>
          </td>
        </tr>
      </table>
      </div>
    </div>
  </div>
</template>
