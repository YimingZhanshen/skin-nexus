<script setup>
// OidcClientsCard.vue — OAuth client registration for the launcher/OIDC flow.
//
// The secret is shown exactly once (the server stores no readable copy), so
// the card keeps the last one on screen until the next successful create —
// never blanked by a second submit, never refetched.
import { ref, onMounted } from 'vue'
import { useI18n } from 'vue-i18n'
import { fetchJSON } from '../../adminApi.js'
import { confirmDialog } from '../../dialog.js'

const { t } = useI18n()

const oidcClients = ref([])
const oidcError = ref('')
const newClient = ref({ client_id: '', name: '', redirect_uris: '', public: true })
const lastSecret = ref('') // shown once after create
const oidcBusy = ref(false)

async function loadOIDC() {
  oidcError.value = ''
  try {
    const c = await fetchJSON('/api/admin/v1/oidc/clients')
    oidcClients.value = c.clients || []
  } catch (e) { oidcError.value = e.message }
}
async function createClient() {
  if (oidcBusy.value) return
  oidcError.value = ''
  const body = {
    client_id: newClient.value.client_id.trim(),
    name: newClient.value.name.trim() || newClient.value.client_id.trim(),
    redirect_uris: newClient.value.redirect_uris.split('\n').map(s => s.trim()).filter(Boolean),
    grant_types: ['authorization_code', 'refresh_token'],
    public: newClient.value.public,
  }
  if (!body.client_id) { oidcError.value = t('admin.oidcIdRequired'); return }
  oidcBusy.value = true
  try {
    const d = await fetchJSON('/api/admin/v1/oidc/clients', {
      method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify(body),
    })
    // The secret is handed over once and never stored in a readable form, so a
    // second submit must not blank what the first one just produced. It is
    // replaced only by the next successful create — a public client genuinely
    // has none, so clearing there is the server telling us so.
    lastSecret.value = d.secret || ''
    newClient.value = { client_id: '', name: '', redirect_uris: '', public: true }
    await loadOIDC()
  } catch (e) { oidcError.value = e.message }
  finally { oidcBusy.value = false }
}
async function deleteClient(id) {
  if (!(await confirmDialog(t('admin.oidcDeleteConfirm', { id }), { danger: true }))) return
  oidcError.value = ''
  try {
    await fetchJSON(`/api/admin/v1/oidc/clients/${encodeURIComponent(id)}`, { method: 'DELETE' })
    oidcClients.value = oidcClients.value.filter(c => c.client_id !== id)
  } catch (e) { oidcError.value = e.message }
}

onMounted(loadOIDC)
</script>

<template>
    <!-- OIDC clients -->
    <div class="card">
      <h2>{{ t('admin.oidcClientsTitle', { count: oidcClients.length }) }}</h2>
      <div class="row">
        <input v-model="newClient.client_id" :placeholder="t('admin.oidcClientId')" style="max-width: 180px;" />
        <input v-model="newClient.name" :placeholder="t('admin.oidcClientName')" style="max-width: 180px;" />
        <label style="display: flex; align-items: center; gap: 4px; white-space: nowrap;">
          <input type="checkbox" v-model="newClient.public" /> {{ t('admin.oidcPublicCheckbox') }}
        </label>
        <button :disabled="oidcBusy" @click="createClient">{{ oidcBusy ? t('admin.oidcCreating') : t('admin.oidcCreate') }}</button>
      </div>
      <textarea v-model="newClient.redirect_uris" :placeholder="t('admin.oidcRedirectPlaceholder')" rows="2"
                style="margin-top: 8px; width: 100%;"></textarea>
      <div class="success" role="status" v-if="lastSecret" style="margin-top: 8px;">
        {{ t('admin.oidcSecretOnce') }}<code>{{ lastSecret }}</code>
      </div>
      <div class="error" role="alert" v-if="oidcError">{{ oidcError }}</div>
      <div class="table-scroll" v-if="oidcClients.length" style="margin-top: 12px;">
      <table>
        <caption class="sr-only">{{ t('admin.oidcClientsTitle', { count: oidcClients.length }) }}</caption>
        <tr><th scope="col">{{ t('admin.oidcClientId') }}</th><th scope="col">{{ t('admin.oidcClientName') }}</th><th scope="col">{{ t('admin.oidcClientType') }}</th><th scope="col">{{ t('admin.oidcClientGrants') }}</th><th scope="col">{{ t('admin.oidcClientRedirects') }}</th><th scope="col"></th></tr>
        <tr v-for="c in oidcClients" :key="c.client_id">
          <td><code>{{ c.client_id }}</code></td>
          <td>{{ c.name }}</td>
          <td><span class="badge">{{ c.public ? t('admin.oidcPublic') : t('admin.oidcConfidential') }}</span></td>
          <td class="muted" style="font-size: var(--fs-xs);">{{ (c.grant_types || []).join(', ') }}</td>
          <td class="muted" style="font-size: var(--fs-xs);">{{ (c.redirect_uris || []).join(', ') || '—' }}</td>
          <td><button class="secondary" @click="deleteClient(c.client_id)">{{ t('admin.oidcDelete') }}</button></td>
        </tr>
      </table>
      </div>
    </div>
</template>
