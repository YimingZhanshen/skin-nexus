<script setup>
// AuthorizedAppsPage: lists the caller's live OIDC grants (jti ledger) and
// lets them revoke one grant or sign out everywhere.
// API: GET /api/v1/oidc/tokens, POST /api/v1/oidc/tokens/revoke {jti|all}.
import { ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { oidc } from '../api.js'
import { guard401 } from '../session.js'
import { confirmDialog } from '../dialog.js'
import { formatWhen } from '../format.js'
import EmptyState from '../components/EmptyState.vue'

const { t } = useI18n()

// The grants ledger lives behind /api/v1/oidc/*, which is only mounted when
// the provider exists. Without the capability this page must not fetch and
// must say why — a raw "HTTP 404" told the reader nothing (the bug this gate
// closes). Same rule as the nav entry in App.vue.
const props = defineProps({ oidcEnabled: { type: Boolean, default: false } })

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
  if (!(await confirmDialog(t('apps.revokeConfirm'), { danger: true }))) return
  loading.value = true; error.value = ''; success.value = ''
  try {
    const d = await oidc.revokeToken({ jti })
    if (d.error) { error.value = d.error; return }
    success.value = t('apps.revokeDone')
    await load()
  } catch (e) { if (!guard401(e)) error.value = e.message } finally { loading.value = false }
}

async function revokeAll() {
  if (!(await confirmDialog(t('apps.revokeAllConfirm'), { danger: true }))) return
  loading.value = true; error.value = ''; success.value = ''
  try {
    const d = await oidc.revokeToken({ all: true })
    if (d.error) { error.value = d.error; return }
    success.value = t('apps.revokeAllDone')
    await load()
  } catch (e) { if (!guard401(e)) error.value = e.message } finally { loading.value = false }
}

function fmt(tm) {
  return formatWhen(tm)
}

// Fetch only when the capability is (or becomes) present; site-config resolves
// after mount, so the gate is watched rather than read once.
watch(() => props.oidcEnabled, (on) => { if (on) load() }, { immediate: true })
</script>

<template>
  <div class="card">
    <template v-if="oidcEnabled">
    <div class="row" style="justify-content: space-between;">
      <h2>{{ t('apps.title', { count: tokens.length }) }}</h2>
      <button class="secondary" :disabled="loading || !tokens.length" @click="revokeAll">{{ t('apps.revokeAll') }}</button>
    </div>
    <div class="error" role="alert" v-if="error">{{ error }}</div>
    <div class="success" role="status" v-if="success">{{ success }}</div>
    <div class="table-scroll" v-if="tokens.length">
    <table>
      <caption class="sr-only">{{ t('apps.title', { count: tokens.length }) }}</caption>
      <tr><th scope="col">{{ t('apps.colApp') }}</th><th scope="col">{{ t('apps.colScopes') }}</th><th scope="col">{{ t('apps.colProfile') }}</th><th scope="col">{{ t('apps.colCreated') }}</th><th scope="col">{{ t('apps.colExpires') }}</th><th scope="col"></th></tr>
      <tr v-for="tk in tokens" :key="tk.jti">
        <!-- 名字在上、client_id 小字次行：技术串只作区分同名的辅助 -->
        <td>
          <div>{{ tk.client_name || tk.client_id }}</div>
          <code v-if="tk.client_name && tk.client_name !== tk.client_id" class="muted" style="font-size: var(--fs-xs);">{{ tk.client_id }}</code>
        </td>
        <td class="muted" style="font-size: var(--fs-xs);">{{ (tk.scopes || []).join(', ') }}</td>
        <td>{{ tk.selected_pid ? '#' + tk.selected_pid : '—' }}</td>
        <td>{{ fmt(tk.created_at) }}</td>
        <td>{{ fmt(tk.expires_at) }}</td>
        <td><button class="secondary" :disabled="loading" @click="revoke(tk.jti)">{{ t('apps.revoke') }}</button></td>
      </tr>
    </table>
    </div>
    <EmptyState v-else :text="t('apps.empty')" />
    </template>
    <EmptyState v-else :text="t('apps.notEnabled')" />
  </div>
</template>
