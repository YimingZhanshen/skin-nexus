<script setup>
// UsersView: blessing 管理面板 用户管理 parity (B2) — table with search,
// pagination, and inline edits (email / nickname / verification /
// permission) on top of the P1 password-reset / delete actions.
//
// Permission guard semantics live server-side (adminapi.inlineGuard):
// strictly-lower targets only, no self-edit, ADMIN grant needs SUPER_ADMIN.
// The UI just renders the 403 message.
import { ref, onMounted } from 'vue'
import { useI18n } from 'vue-i18n'
import { fetchJSON } from '../../adminApi.js'
import Pager from '../Pager.vue'

const { t } = useI18n()

const users = ref([])
const total = ref(0)
const page = ref(1)
const pageSize = 50
const query = ref('')
const error = ref('')
const busy = ref(false) // in-flight guard: no double submit

async function load() {
  error.value = ''
  try {
    const qs = new URLSearchParams({ page: String(page.value) })
    if (query.value.trim()) qs.set('q', query.value.trim())
    const d = await fetchJSON(`/api/admin/v1/users?${qs}`)
    users.value = d.users || []
    total.value = d.total || 0
  } catch (e) { error.value = e.message }
}

function search() {
  page.value = 1
  load()
}

function goPage(p) {
  if (p < 1 || (p - 1) * pageSize >= total.value || p === page.value) return
  page.value = p
  load()
}

async function adminResetPassword(u) {
  const pw = prompt(t('admin.resetPwPrompt', { name: u.nickname, id: u.id }), '')
  if (!pw) return
  error.value = ''
  try {
    await fetchJSON(`/api/admin/v1/users/${u.id}/password`, {
      method: 'POST', headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ password: pw }),
    })
    alert(t('admin.resetPwDone'))
  } catch (e) { error.value = e.message }
}

async function adminDeleteUser(u) {
  if (!confirm(t('admin.deleteConfirm', { name: u.nickname, id: u.id }))) return
  error.value = ''
  try {
    await fetchJSON(`/api/admin/v1/users/${u.id}`, { method: 'DELETE' })
    await load()
  } catch (e) { error.value = e.message }
}

// ---- inline edits ----

async function putUser(id, path, body) {
  if (busy.value) return false
  busy.value = true
  error.value = ''
  try {
    await fetchJSON(`/api/admin/v1/users/${id}/${path}`, {
      method: 'PUT', headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify(body),
    })
    return true
  } catch (e) {
    error.value = e.message
    return false
  } finally {
    busy.value = false
  }
}

// Each edit keeps the old value until the PUT succeeds; on failure the
// table refetches so the UI never shows an unsaved phantom state.
async function saveEmail(u) {
  const prev = u.email
  if (await putUser(u.id, 'email', { email: u.email || '' })) {
    u.email = String(u.email || '').trim()
  } else {
    u.email = prev
    await load()
  }
}

async function saveNickname(u) {
  const prev = u.nickname
  if (await putUser(u.id, 'nickname', { nickname: u.nickname })) {
    u.nickname = String(u.nickname).trim()
  } else {
    u.nickname = prev
    await load()
  }
}

async function toggleVerified(u) {
  const next = Number(u.emailVerified) ? 0 : 1
  if (await putUser(u.id, 'verification', { verified: !!next })) {
    u.emailVerified = next
  } else {
    await load()
  }
}

// Permission select: -1 banned / 0 normal / 1 admin. SUPER_ADMIN (2) is
// never grantable from the UI (blessing parity: setup-created only).
async function changePermission(u, ev) {
  const next = Number(ev.target.value)
  const prev = Number(u.permission)
  if (next === prev) return
  const label = (p) => t(`admin.perm${p}`)
  if (!confirm(t('admin.permChangeConfirm', {
    name: u.nickname, id: u.id, from: label(prev), to: label(next),
  }))) {
    ev.target.value = String(prev)
    return
  }
  if (await putUser(u.id, 'permission', { permission: next })) {
    u.permission = next
  } else {
    ev.target.value = String(prev)
    await load()
  }
}

const pageMax = () => Math.max(1, Math.ceil(total.value / pageSize))

onMounted(load)
</script>

<template>
  <div class="card">
    <h2 style="margin-top: 0;">{{ t('admin.usersTitle') }}</h2>
    <div class="row">
      <input v-model="query" :placeholder="t('admin.userSearchPlaceholder')" style="max-width: 260px;"
             @keyup.enter="search" />
      <button class="secondary" @click="search">{{ t('admin.search') }}</button>
      <span class="muted">{{ t('admin.userTotal', { total }) }}</span>
    </div>
    <div class="error" v-if="error">{{ error }}</div>
    <p class="muted" v-if="!users.length && !error" style="margin-top: 12px;">{{ t('admin.usersEmpty') }}</p>
    <div class="table-scroll" v-if="users.length">
    <table class="users-table" style="margin-top: 12px;">
      <tr>
        <th>{{ t('admin.colId') }}</th><th>{{ t('admin.colNickname') }}</th><th>{{ t('admin.colEmail') }}</th>
        <th>{{ t('admin.colPermission') }}</th><th>{{ t('admin.colVerified') }}</th>
        <th>{{ t('admin.colAlgo') }}</th><th>{{ t('admin.colCreated') }}</th><th></th>
      </tr>
      <tr v-for="u in users" :key="u.id">
        <td class="muted">{{ u.id }}</td>
        <!-- inline edit: blur/enter commits, Esc reverts. Inputs render
             borderless until hover/focus (the dark filled boxes broke the
             row rhythm and clipped the email) -->
        <td><input class="cell-input" v-model="u.nickname" @blur="saveNickname(u)"
                   @keyup.escape="load()" /></td>
        <td><input class="cell-input cell-email" v-model="u.email" placeholder="—" @blur="saveEmail(u)"
                   @keyup.escape="load()" /></td>
        <td>
          <!-- SUPER_ADMIN (permission=2) has no select option on purpose
               (never grantable/demotable from the UI); render its label as a
               badge — an optionless select displayed as a blank dropdown. -->
          <span v-if="Number(u.permission) === 2" class="badge accent">{{ t('admin.perm2') }}</span>
          <select v-else class="cell-input cell-perm" :value="String(u.permission)" @change="changePermission(u, $event)"
                  :disabled="u.id === 1">
            <option value="-1">{{ t('admin.perm-1') }}</option>
            <option value="0">{{ t('admin.perm0') }}</option>
            <option value="1">{{ t('admin.perm1') }}</option>
          </select>
        </td>
        <td>
          <button class="veri-btn" :class="{ on: Number(u.emailVerified) }" @click="toggleVerified(u)">
            {{ Number(u.emailVerified) ? '✓' : '—' }}
          </button>
        </td>
        <td><span class="badge" :class="{ danger: (u.passwordAlgo || '').startsWith('legacy') }">{{ u.passwordAlgo }}</span></td>
        <td class="muted nowrap">{{ (u.createdAt || '').slice(0, 10) }}</td>
        <td class="nowrap">
          <button class="secondary" @click="adminResetPassword(u)">{{ t('admin.resetPw') }}</button>
          <button class="secondary danger-outline" v-if="u.id !== 1 && Number(u.permission) !== 2" @click="adminDeleteUser(u)" style="margin-left: 8px;">{{ t('admin.delete') }}</button>
        </td>
      </tr>
    </table>
    </div>
    <Pager :page="page" :pages="pageMax()" :total="total" @update:page="goPage" />
  </div>
</template>

<style scoped>
/* narrow viewports scroll via the global .table-scroll wrapper */
.users-table { min-width: 860px; }
.users-table th:nth-child(1), .users-table td:nth-child(1) { width: 56px; }
.users-table th:nth-child(2), .users-table td:nth-child(2) { width: 170px; }
.users-table th:nth-child(4), .users-table td:nth-child(4) { width: 130px; }
.users-table th:nth-child(5), .users-table td:nth-child(5) { width: 64px; text-align: center; }
.users-table th:nth-child(6), .users-table td:nth-child(6) { width: 110px; }
.users-table th:nth-child(7), .users-table td:nth-child(7) { width: 96px; }
.nowrap { white-space: nowrap; }
/* inline edit: borderless until hover/focus — no more dark clipped boxes */
.cell-input {
  width: 100%; padding: 4px 8px; font-size: var(--fs-xs);
  background: transparent; border: 1px solid transparent; border-radius: var(--r-m);
  color: var(--text);
}
.cell-input:hover { border-color: var(--border); }
.cell-input:focus { border-color: var(--accent); background: var(--bg); outline: none; }
.cell-input:disabled { opacity: .55; cursor: not-allowed; }
.cell-email { min-width: 200px; }
.cell-perm { min-width: 110px; }
.veri-btn {
  width: 36px; padding: 4px 0; font-size: var(--fs-xs);
  background: transparent; border: 1px solid var(--border); color: var(--muted);
}
.veri-btn.on { color: var(--ok); border-color: var(--ok)66; }
</style>
