<script setup>
import { ref, shallowRef, computed, onMounted, onBeforeUnmount, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { confirmDialog, promptDialog } from '../../dialog.js'
import { serverAccessApi } from '../../api/serverAccessApi.js'
import { buildServerQuotaPatch, isTenantDeletionDue, useServerAccessPlatform } from '../../composables/useServerAccessPlatform.js'
import EmptyState from '../EmptyState.vue'
import Pager from '../Pager.vue'

const { t } = useI18n()
const platform = useServerAccessPlatform(serverAccessApi)
const quotaDrafts = ref({})
const serverQuotaDrafts = ref({})
const quotaError = shallowRef('')
const quotaSaved = shallowRef('')
const quotaFields = [
  { key: 'verifyRateLimit', label: 'verifyRateLimit' },
  { key: 'verifyBurst', label: 'verifyBurst' },
  { key: 'maxConcurrent', label: 'maxConcurrent' },
  { key: 'whitelistLimit', label: 'whitelistLimit' },
  { key: 'eventRetentionDays', label: 'eventRetentionDays' },
]
const revokeServer = shallowRef('')
const revokeCredential = shallowRef('')
const selectedRevokeServer = computed(() => platform.servers.value.find((row) => String(row.id) === revokeServer.value) || null)
const selectedRevokeCredentials = computed(() => selectedRevokeServer.value?.credentials || [])
const hasActiveRevokeCredentials = computed(() => selectedRevokeCredentials.value.some((credential) => credential.status === 'active'))
const canRevokeSelected = computed(() => selectedRevokeCredentials.value.some((credential) => String(credential.id) === revokeCredential.value && credential.status === 'active'))
const tenantSearch = shallowRef('')
const tenantDraft = ref({ name: '', serverLimit: '', ownerUid: '' })
const tenantCreated = shallowRef(false)
const tenantValidationError = shallowRef('')
const serverSearch = shallowRef('')
const auditSearch = shallowRef('')
const now = shallowRef(Date.now())
let clock
const tenantPages = computed(() => Math.max(1, Math.ceil(platform.tenantTotal.value / platform.tenantSize.value)))
const serverPages = computed(() => Math.max(1, Math.ceil(platform.serverTotal.value / platform.serverSize.value)))
const auditPages = computed(() => Math.max(1, Math.ceil(platform.auditTotal.value / platform.auditSize.value)))
watch(() => platform.tenants.value, (rows) => {
  quotaDrafts.value = Object.fromEntries(rows.map((row) => [row.id, row.serverLimit]))
}, { immediate: true })
watch(() => platform.servers.value, (rows) => {
  for (const row of rows) if (!serverQuotaDrafts.value[row.id]) serverQuotaDrafts.value[row.id] = {}
}, { immediate: true })
watch(revokeServer, () => { revokeCredential.value = '' })
onMounted(() => {
  platform.load()
  clock = setInterval(() => { now.value = Date.now() }, 1000)
})
onBeforeUnmount(() => clearInterval(clock))

function statusLabel(status) {
  const key = `serverAccess.statusMap.${status}`
  const label = t(key)
  return label === key ? status : label
}
function policyState(enabled) {
  return statusLabel(enabled ? 'active' : 'disabled')
}
function modeLabel(mode) {
  const key = `serverAccess.sourceMode.${mode}`
  const label = t(key)
  return label === key ? mode : label
}
function auditLabel(action) {
  const key = `adminServerAccess.auditAction.${action}`
  const label = t(key)
  return label === key ? action : label
}
async function createTenant() {
  if (platform.busy.value) return
  tenantCreated.value = false
  tenantValidationError.value = ''
  const name = tenantDraft.value.name.trim()
  const serverLimit = Number(tenantDraft.value.serverLimit)
  const ownerUid = Number(tenantDraft.value.ownerUid)
  if (!name || Array.from(name).length > 190 || !Number.isSafeInteger(serverLimit) || serverLimit < 1 || serverLimit > 10000 || !Number.isSafeInteger(ownerUid) || ownerUid < 1) {
    tenantValidationError.value = t('adminServerAccess.createInvalid')
    return
  }
  if (await platform.createTenant({ name, serverLimit, ownerUid })) {
    tenantDraft.value = { name: '', serverLimit: '', ownerUid: '' }
    tenantSearch.value = ''
    auditSearch.value = ''
    tenantCreated.value = true
  }
}
async function toggleTenant(row) {
  if (row.status === 'deleting') return
  const suspending = row.status !== 'suspended'
  const message = suspending ? t('adminServerAccess.confirmSuspend', { id: row.id }) : t('adminServerAccess.confirmActivate', { id: row.id })
  if (!(await confirmDialog(message, { danger: suspending }))) return
  await platform.updateTenant(row.id, { status: suspending ? 'suspended' : 'active' })
}
async function deleteTenant(row) {
  if (!isTenantDeletionDue(row, now.value) || platform.busy.value) return
  const message = t('adminServerAccess.confirmDelete', { id: row.id, name: row.name, due: row.deletionDueAt })
  const confirmation = await promptDialog(message, '', { danger: true })
  if (confirmation === null) return
  await platform.deleteTenant(row.id, confirmation)
}
async function toggleServer(row) {
  const suspending = row.status !== 'suspended'
  const message = suspending ? t('adminServerAccess.confirmServerSuspend', { id: row.id }) : t('adminServerAccess.confirmServerActivate', { id: row.id })
  if (!(await confirmDialog(message, { danger: suspending }))) return
  await platform.updateServer(row.id, { status: suspending ? 'suspended' : 'active' })
}
async function saveServerQuota(row) {
  quotaError.value = ''
  quotaSaved.value = ''
  const { patch, valid } = buildServerQuotaPatch(serverQuotaDrafts.value[row.id])
  if (!valid) {
    quotaError.value = t('adminServerAccess.quotaInvalid')
    return
  }
  if (!Object.keys(patch).length) return
  if (await platform.updateServer(row.id, patch)) {
    serverQuotaDrafts.value[row.id] = {}
    quotaSaved.value = row.id
  }
}
async function toggleFallback(event) {
  const enabled = event.target.checked
  if (!(await confirmDialog(t('adminServerAccess.fallbackHint'), { danger: !enabled }))) {
    event.target.checked = platform.fallback.value
    return
  }
  const ok = await platform.setFallback(enabled)
  if (!ok) event.target.checked = platform.fallback.value
}
async function revoke() {
  const serverId = Number(revokeServer.value)
  const credentialId = Number(revokeCredential.value)
  if (!Number.isSafeInteger(serverId) || serverId <= 0 || !Number.isSafeInteger(credentialId) || credentialId <= 0) return
  if (!(await confirmDialog(t('serverAccess.confirmRevoke'), { danger: true }))) return
  if (await platform.revokeCredential(serverId, credentialId)) {
    revokeServer.value = ''
    revokeCredential.value = ''
  }
}
</script>

<template>
  <div class="platform-view">
    <h1>{{ t('adminServerAccess.title') }}</h1>
    <p class="muted">{{ t('adminServerAccess.hint') }}</p>
    <div v-if="platform.error.value" class="error" role="alert">{{ platform.error.value }}</div>
    <p v-if="platform.loading.value" class="muted" role="status">{{ t('common.loading') }}</p>

    <section class="card fallback-card">
      <label class="check-row"><input type="checkbox" :checked="platform.fallback.value" :disabled="platform.busy.value" @change="toggleFallback" /> {{ t('adminServerAccess.globalFallback') }}</label>
      <p class="muted">{{ t('adminServerAccess.fallbackHint') }}</p>
    </section>

    <section class="card">
      <h2>{{ t('adminServerAccess.tenants') }}</h2>
      <form class="create-tenant-form" @submit.prevent="createTenant">
        <h3>{{ t('adminServerAccess.createTenant') }}</h3>
        <p class="muted">{{ t('adminServerAccess.createTenantHint') }}</p>
        <div class="row">
          <label for="sa-create-tenant-name">{{ t('adminServerAccess.tenantName') }}</label>
          <input id="sa-create-tenant-name" v-model="tenantDraft.name" name="name" required maxlength="190" :disabled="platform.busy.value" />
        </div>
        <div class="row">
          <label for="sa-create-tenant-limit">{{ t('adminServerAccess.serverLimit') }}</label>
          <input id="sa-create-tenant-limit" v-model="tenantDraft.serverLimit" name="serverLimit" type="number" min="1" max="10000" step="1" required :disabled="platform.busy.value" />
        </div>
        <div class="row">
          <label for="sa-create-tenant-owner">{{ t('adminServerAccess.ownerUid') }}</label>
          <input id="sa-create-tenant-owner" v-model="tenantDraft.ownerUid" name="ownerUid" type="number" min="1" step="1" required :disabled="platform.busy.value" />
        </div>
        <p v-if="tenantValidationError" class="error" role="alert">{{ tenantValidationError }}</p>
        <p v-if="tenantCreated" class="muted" role="status">{{ t('adminServerAccess.tenantCreated') }}</p>
        <button :disabled="platform.busy.value">{{ t('adminServerAccess.create') }}</button>
      </form>
      <form class="row search-row" @submit.prevent="platform.searchTenants(tenantSearch)">
        <label class="sr-only" for="sa-platform-tenants">{{ t('adminServerAccess.search') }}</label>
        <input id="sa-platform-tenants" v-model="tenantSearch" :placeholder="t('adminServerAccess.search')" />
        <button class="secondary">{{ t('adminServerAccess.search') }}</button>
      </form>
      <EmptyState v-if="!platform.tenants.value.length" :text="t('adminServerAccess.empty')" />
      <div v-else class="table-scroll">
        <table>
          <caption class="sr-only">{{ t('adminServerAccess.tenants') }}</caption>
          <thead><tr><th scope="col">ID</th><th scope="col">{{ t('adminServerAccess.tenant') }}</th><th scope="col">{{ t('serverAccess.status') }}</th><th scope="col">{{ t('adminServerAccess.deletionRequestedAt') }}</th><th scope="col">{{ t('adminServerAccess.deletionDueAt') }}</th><th scope="col">{{ t('adminServerAccess.quota') }}</th><th scope="col">{{ t('serverAccess.actions') }}</th></tr></thead>
          <tbody><tr v-for="row in platform.tenants.value" :key="row.id">
            <td>{{ row.id }}</td><td>{{ row.name }}</td><td>{{ statusLabel(row.status) }}</td>
            <td><time v-if="row.deletionRequestedAt" :datetime="row.deletionRequestedAt">{{ row.deletionRequestedAt }}</time><span v-else>—</span></td>
            <td><time v-if="row.deletionDueAt" :datetime="row.deletionDueAt">{{ row.deletionDueAt }}</time><span v-else>—</span></td>
            <td><div class="row quota-row"><input v-model.number="quotaDrafts[row.id]" type="number" min="1" max="10000" :disabled="row.status === 'deleting'" :aria-label="t('adminServerAccess.quota')" /><button class="secondary small" :disabled="platform.busy.value || row.status === 'deleting'" @click="platform.updateTenant(row.id, { serverLimit: Number(quotaDrafts[row.id]) })">{{ t('adminServerAccess.saveQuota') }}</button></div></td>
            <td><button v-if="row.status === 'deleting'" class="secondary small danger-outline" :disabled="platform.busy.value || !isTenantDeletionDue(row, now)" :title="t('adminServerAccess.deleteDueHint')" @click="deleteTenant(row)">{{ t('adminServerAccess.deleteTenant') }}</button><button v-else class="secondary small" :class="{ 'danger-outline': row.status !== 'suspended' }" :disabled="platform.busy.value" @click="toggleTenant(row)">{{ row.status === 'suspended' ? t('adminServerAccess.activate') : t('adminServerAccess.suspend') }}</button></td>
          </tr></tbody>
        </table>
      </div>
      <Pager :page="platform.tenantPage.value" :pages="tenantPages" :total="platform.tenantTotal.value" @update:page="platform.pageTenants" />
    </section>

    <section class="card">
      <h2>{{ t('adminServerAccess.servers') }}</h2>
      <p class="muted">{{ t('adminServerAccess.serverQuotaHint') }}</p>
      <form class="row search-row" @submit.prevent="platform.searchServers(serverSearch)">
        <label class="sr-only" for="sa-platform-servers">{{ t('adminServerAccess.search') }}</label>
        <input id="sa-platform-servers" v-model="serverSearch" :placeholder="t('adminServerAccess.search')" />
        <button class="secondary">{{ t('adminServerAccess.search') }}</button>
      </form>
      <EmptyState v-if="!platform.servers.value.length" :text="t('adminServerAccess.empty')" />
      <div v-else class="table-scroll">
        <table>
          <caption class="sr-only">{{ t('adminServerAccess.servers') }}</caption>
          <thead><tr><th scope="col">ID</th><th scope="col">{{ t('adminServerAccess.server') }}</th><th scope="col">{{ t('serverAccess.publicId') }}</th><th scope="col">{{ t('serverAccess.status') }}</th><th scope="col">{{ t('serverAccess.actions') }}</th></tr></thead>
          <tbody><tr v-for="row in platform.servers.value" :key="row.id">
            <td>{{ row.id }}</td><td>{{ row.name }}</td><td><code>{{ row.publicId }}</code></td><td>{{ statusLabel(row.status) }}</td>
            <td>
              <div class="server-actions">
                <button class="secondary small" :class="{ 'danger-outline': row.status !== 'suspended' }" :disabled="platform.busy.value" @click="toggleServer(row)">{{ row.status === 'suspended' ? t('adminServerAccess.activate') : t('adminServerAccess.suspend') }}</button>
                <details class="quota-details">
                  <summary>{{ t('adminServerAccess.serverQuota') }}</summary>
                  <dl class="policy-summary">
                    <div><dt>{{ t('serverAccess.whitelist') }}</dt><dd>{{ policyState(row.policy?.whitelistEnabled) }}</dd></div>
                    <div><dt>{{ t('serverAccess.premiumFallback') }}</dt><dd>{{ policyState(row.policy?.premiumFallbackEnabled) }}</dd></div>
                  </dl>
                  <form class="quota-form" @submit.prevent="saveServerQuota(row)">
                    <label v-for="field in quotaFields" :key="field.key" :for="`sa-quota-${row.id}-${field.key}`">
                      <span>{{ t(`adminServerAccess.quotaFields.${field.label}`) }}</span>
                      <span class="quota-current">{{ t('adminServerAccess.currentValue', { value: row.policy?.[field.key] ?? '—' }) }}</span>
                      <input :id="`sa-quota-${row.id}-${field.key}`" v-model="serverQuotaDrafts[row.id][field.key]" type="number" min="1" max="1000000" step="1" :placeholder="t('adminServerAccess.unchanged')" />
                    </label>
                    <button class="secondary small" :disabled="platform.busy.value">{{ t('adminServerAccess.saveQuota') }}</button>
                    <span v-if="quotaSaved === row.id" class="muted" role="status">{{ t('common.saved') }}</span>
                  </form>
                </details>
              </div>
            </td>
          </tr></tbody>
        </table>
      </div>
      <p v-if="quotaError" class="error" role="alert">{{ quotaError }}</p>
      <Pager :page="platform.serverPage.value" :pages="serverPages" :total="platform.serverTotal.value" @update:page="platform.pageServers" />
    </section>

    <section class="card">
      <h2>{{ t('adminServerAccess.credentialRevoke') }}</h2>
      <p class="muted">{{ t('adminServerAccess.revokeHint') }}</p>
      <form class="row search-row" @submit.prevent="revoke">
        <label for="sa-revoke-server">{{ t('adminServerAccess.server') }}</label>
        <select id="sa-revoke-server" :value="revokeServer" :disabled="platform.busy.value" @change="revokeServer = $event.target.value">
          <option value="" disabled>{{ t('adminServerAccess.selectServer') }}</option>
          <option v-for="row in platform.servers.value" :key="row.id" :value="String(row.id)">#{{ row.id }} · {{ row.name }}</option>
        </select>
        <label for="sa-revoke-credential">{{ t('adminServerAccess.credential') }}</label>
        <select id="sa-revoke-credential" :value="revokeCredential" :disabled="platform.busy.value || !hasActiveRevokeCredentials" @change="revokeCredential = $event.target.value">
          <option value="">{{ t('adminServerAccess.selectCredential') }}</option>
          <option v-for="credential in selectedRevokeCredentials" :key="credential.id" :value="String(credential.id)" :disabled="credential.status !== 'active'">#{{ credential.id }} · {{ modeLabel(credential.mode) }} · {{ statusLabel(credential.status) }}</option>
        </select>
        <button class="danger-outline" :disabled="platform.busy.value || !canRevokeSelected">{{ t('adminServerAccess.revoke') }}</button>
      </form>
      <EmptyState v-if="selectedRevokeServer && !selectedRevokeCredentials.length" :text="t('adminServerAccess.noCredentials')" />
      <div v-else-if="selectedRevokeServer" class="table-scroll">
        <table>
          <caption class="sr-only">{{ t('adminServerAccess.credentialSummaries') }}</caption>
          <thead><tr><th scope="col">ID</th><th scope="col">{{ t('serverAccess.mode') }}</th><th scope="col">{{ t('serverAccess.status') }}</th><th scope="col">{{ t('serverAccess.lastUsedAt') }}</th><th scope="col">{{ t('serverAccess.expiresAt') }}</th></tr></thead>
          <tbody><tr v-for="credential in selectedRevokeCredentials" :key="credential.id">
            <td>{{ credential.id }}</td><td>{{ modeLabel(credential.mode) }}</td><td>{{ statusLabel(credential.status) }}</td>
            <td>{{ credential.lastUsedAt || t('serverAccess.never') }}</td><td>{{ credential.expiresAt || '—' }}</td>
          </tr></tbody>
        </table>
      </div>
    </section>

    <section class="card">
      <h2>{{ t('adminServerAccess.audit') }}</h2>
      <form class="row search-row" @submit.prevent="platform.searchAudit(auditSearch)">
        <label class="sr-only" for="sa-platform-audit">{{ t('adminServerAccess.search') }}</label>
        <input id="sa-platform-audit" v-model="auditSearch" :placeholder="t('adminServerAccess.search')" />
        <button class="secondary">{{ t('adminServerAccess.search') }}</button>
      </form>
      <EmptyState v-if="!platform.audit.value.length" :text="t('adminServerAccess.empty')" />
      <div v-else class="table-scroll">
        <table>
          <caption class="sr-only">{{ t('adminServerAccess.audit') }}</caption>
          <thead><tr><th scope="col">{{ t('adminServerAccess.time') }}</th><th scope="col">{{ t('adminServerAccess.actor') }}</th><th scope="col">{{ t('adminServerAccess.action') }}</th><th scope="col">{{ t('admin.colDetail') }}</th></tr></thead>
          <tbody><tr v-for="row in platform.audit.value" :key="row.id">
            <td>{{ row.createdAt }}</td><td>{{ row.actorName || '—' }} <span class="muted">#{{ row.actorUid }}</span></td><td>{{ auditLabel(row.action) }}</td><td><code class="audit-detail">{{ row.detail }}</code></td>
          </tr></tbody>
        </table>
      </div>
      <Pager :page="platform.auditPage.value" :pages="auditPages" :total="platform.auditTotal.value" @update:page="platform.pageAudit" />
    </section>
  </div>
</template>

<style scoped>
.platform-view { display: grid; gap: var(--sp-4); }
.platform-view h1 { margin: 0; font-size: var(--fs-xl); }
.platform-view h2 { margin: 0 0 var(--sp-3); font-size: var(--fs-l); }
.platform-view .card { display: grid; gap: var(--sp-3); }
.platform-view p { margin: 0; }
.platform-view table { min-width: 760px; }
.fallback-card { display: flex !important; flex-direction: column; }
.check-row { display: flex; align-items: center; gap: var(--sp-2); }
.search-row { margin: 0; flex-wrap: wrap; }
.create-tenant-form { display: grid; gap: var(--sp-2); padding: var(--sp-3); border: 1px solid var(--border); border-radius: var(--r-m); }
.create-tenant-form h3, .create-tenant-form p { margin: 0; font-size: var(--fs-s); }
.create-tenant-form label { min-width: var(--sp-8); }
.search-row input, .search-row select { flex: 1; min-width: 0; }
.quota-row { margin: 0; }
.quota-row input { width: var(--sp-8); }
.server-actions { display: grid; gap: var(--sp-2); }
.quota-details summary { cursor: pointer; font-size: var(--fs-s); }
.policy-summary { display: flex; flex-wrap: wrap; gap: var(--sp-3); margin: var(--sp-2) 0 0; }
.policy-summary div { display: grid; gap: var(--sp-1); }
.policy-summary dt, .quota-current { color: var(--muted); font-size: var(--fs-s); }
.policy-summary dd { margin: 0; }
.quota-form { display: grid; gap: var(--sp-2); margin-top: var(--sp-2); }
.quota-form label { display: grid; gap: var(--sp-1); font-size: var(--fs-s); }
.quota-form input { width: 100%; }
.audit-detail { overflow-wrap: anywhere; }
</style>
