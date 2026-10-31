<script setup>
// AuthorizedAppsPage: lists the caller's live OIDC grants (jti ledger) and
// lets them revoke one grant or sign out everywhere.
// API: GET /api/v1/oidc/tokens, POST /api/v1/oidc/tokens/revoke {jti|all}.
import { ref, onMounted } from 'vue'
import { useI18n } from 'vue-i18n'
import { oidc } from '../api.js'
import { guard401 } from '../session.js'
import EmptyState from '../components/EmptyState.vue'

const { t } = useI18n()

const tokens = ref([])
const error = ref('')
const success = ref('')
const loading = ref(false)

async function load() {
  error.value = ''
  try {
    const d = await oidc.tokens()
    tokens.value = d.tokens || []
  } catch (e) { if (!guard401(e)) error.value = e.message }
}

async function revoke(jti) {
  if (!confirm(t('apps.revokeConfirm'))) return
  loading.value = true; error.value = ''; success.value = ''
  try {
    const d = await oidc.revokeToken({ jti })
    if (d.error) { error.value = d.error; return }
    success.value = t('apps.revokeDone')
    await load()
  } catch (e) { if (!guard401(e)) error.value = e.message } finally { loading.value = false }
}

async function revokeAll() {
  if (!confirm(t('apps.revokeAllConfirm'))) return
  loading.value = true; error.value = ''; success.value = ''
  try {
    const d = await oidc.revokeToken({ all: true })
    if (d.error) { error.value = d.error; return }
    success.value = t('apps.revokeAllDone')
    await load()
  } catch (e) { if (!guard401(e)) error.value = e.message } finally { loading.value = false }
}

function fmt(tm) {
  return new Date(tm).toLocaleString()
}

onMounted(load)
</script>

<template>
  <div class="card">
    <div class="row" style="justify-content: space-between;">
      <h2>{{ t('apps.title', { count: tokens.length }) }}</h2>
      <button class="secondary" :disabled="loading || !tokens.length" @click="revokeAll">{{ t('apps.revokeAll') }}</button>
    </div>
    <div class="error" v-if="error">{{ error }}</div>
    <div class="success" v-if="success">{{ success }}</div>
    <div class="table-scroll" v-if="tokens.length">
    <table>
      <tr><th>{{ t('apps.colApp') }}</th><th>{{ t('apps.colScopes') }}</th><th>{{ t('apps.colProfile') }}</th><th>{{ t('apps.colCreated') }}</th><th>{{ t('apps.colExpires') }}</th><th></th></tr>
      <tr v-for="tk in tokens" :key="tk.jti">
        <td><code style="font-size: 12px;">{{ tk.client_id }}</code></td>
        <td class="muted" style="font-size: 12px;">{{ (tk.scopes || []).join(', ') }}</td>
        <td>{{ tk.selected_pid ? '#' + tk.selected_pid : '—' }}</td>
        <td>{{ fmt(tk.created_at) }}</td>
        <td>{{ fmt(tk.expires_at) }}</td>
        <td><button class="secondary" :disabled="loading" @click="revoke(tk.jti)">{{ t('apps.revoke') }}</button></td>
      </tr>
    </table>
    </div>
    <EmptyState v-else :text="t('apps.empty')" />
  </div>
</template>
