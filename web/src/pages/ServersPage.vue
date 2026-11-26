<script setup>
import { computed, shallowRef, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { confirmDialog } from '../dialog.js'
import { serverAccessApi } from '../api/serverAccessApi.js'
import { useServerTenant } from '../composables/useServerTenant.js'
import { useServerCatalog } from '../composables/useServerCatalog.js'
import ServerListPanel from '../components/serveraccess/ServerListPanel.vue'
import ServerDetailPanel from '../components/serveraccess/ServerDetailPanel.vue'
import ServerMembersPanel from '../components/serveraccess/ServerMembersPanel.vue'
import ServerWhitelistPanel from '../components/serveraccess/ServerWhitelistPanel.vue'
import ServerBansPanel from '../components/serveraccess/ServerBansPanel.vue'
import ServerEventsPanel from '../components/serveraccess/ServerEventsPanel.vue'
import ServerCredentialDialog from '../components/serveraccess/ServerCredentialDialog.vue'
import InviteAcceptCard from '../components/serveraccess/InviteAcceptCard.vue'
import TenantLifecyclePanel from '../components/serveraccess/TenantLifecyclePanel.vue'
import EmptyState from '../components/EmptyState.vue'

const props = defineProps({ enabled: Boolean })
const { t } = useI18n()
const portal = useServerTenant(serverAccessApi)
const catalog = useServerCatalog(serverAccessApi)
const credentialOpen = shallowRef(false)
const inviteToken = shallowRef('')
const selectedId = computed(() => catalog.selected.value?.server.id || null)
const canWrite = computed(() => ['owner', 'admin'].includes(portal.role.value))
const canManageMembers = computed(() => portal.role.value === 'owner')
const loading = computed(() => portal.loading.value || catalog.loading.value)
const error = computed(() => portal.error.value || catalog.error.value)
const updatingServers = new Set()

async function load() {
  inviteToken.value = ''
  if (!(await portal.load())) return
  await catalog.load()
  if (!catalog.selected.value && catalog.servers.value.length) await catalog.select(catalog.servers.value[0].id)
}
watch(() => props.enabled, (enabled) => { if (enabled) load() }, { immediate: true })

async function create(name) {
  const created = await catalog.create(name)
  if (created) await catalog.select(created.id)
}
async function invite(email, role) {
  const result = await portal.invite(email, role)
  if (result) inviteToken.value = result.token
}
async function acceptInvite(token) {
  const result = await portal.acceptInvite(token)
  if (!result) return
  await catalog.load()
  if (!catalog.selected.value && catalog.servers.value.length) await catalog.select(catalog.servers.value[0].id)
}
async function updateServer(id, body) {
  if (updatingServers.has(id)) return
  updatingServers.add(id)
  try {
    if (body.status === 'disabled') {
      const current = catalog.servers.value.find((server) => server.id === id)
      const name = current?.name || catalog.selected.value?.server.name || ''
      if (!(await confirmDialog(t('serverAccess.confirmDisable', { name }), { danger: true }))) return
    }
    await catalog.update(id, body)
  } finally {
    updatingServers.delete(id)
  }
}
async function removeMember(uid) {
  if (!(await confirmDialog(t('serverAccess.confirmRemoveMember', { uid }), { danger: true }))) return
  await portal.removeMember(uid)
}
</script>

<template>
  <div class="server-portal">
    <EmptyState v-if="!enabled" :text="t('serverAccess.unavailableTitle')" :hint="t('serverAccess.unavailableBody')" />
    <template v-else>
      <InviteAcceptCard v-if="portal.forbidden.value" :busy="portal.busy.value" :error="portal.error.value" @accept="acceptInvite" />
      <EmptyState v-if="portal.forbidden.value" :text="t('serverAccess.forbiddenTitle')" :hint="t('serverAccess.forbiddenBody')" />
      <EmptyState v-else-if="portal.unavailable.value" :text="t('serverAccess.unavailableTitle')" :hint="t('serverAccess.unavailableBody')" />
      <template v-else>
        <header v-if="portal.tenant.value" class="portal-head">
          <div><h1>{{ t('serverAccess.title') }}</h1><p class="muted">{{ portal.tenant.value?.name }} · {{ t(`serverAccess.roleMap.${portal.role.value}`) }}</p></div>
          <span class="badge">{{ t('serverAccess.serverCount', { used: catalog.quotaUsed.value, limit: portal.tenant.value?.serverLimit || 0 }) }}</span>
        </header>
        <p v-if="loading" class="muted" role="status">{{ t('common.loading') }}</p>
        <div v-if="error" class="error" role="alert">{{ error }} <button class="secondary small" @click="load">{{ t('common.retry') }}</button></div>
        <TenantLifecyclePanel v-if="portal.tenant.value" :tenant="portal.tenant.value" :api="serverAccessApi" :can-export="canWrite" :can-delete="canManageMembers" :busy="portal.busy.value" @changed="load" />
        <ServerMembersPanel :members="portal.members.value" :invites="portal.invites.value" :page="portal.page.value" :size="portal.size.value" :total="portal.total.value" :busy="portal.busy.value" :can-manage-members="canManageMembers" :invite-token="inviteToken" @invite="invite" @remove-member="removeMember" @revoke-invite="portal.revokeInvite" @update:page="portal.pageInvites" @hide-invite="inviteToken = ''" />
        <div class="portal-grid">
          <ServerListPanel :servers="catalog.servers.value" :selected-id="selectedId" :used="catalog.quotaUsed.value" :limit="portal.tenant.value?.serverLimit || 0" :page="catalog.page.value" :size="catalog.size.value" :total="catalog.total.value" :loading="catalog.loading.value" :busy="catalog.busy.value" :can-write="canWrite" :error="catalog.error.value" @select="catalog.select" @create="create" @update="updateServer" @update:page="catalog.goPage" @retry="load" />
          <div v-if="catalog.selected.value" class="detail-column">
            <ServerDetailPanel :detail="catalog.selected.value" :can-write="canWrite" :busy="catalog.busy.value" :error="catalog.error.value" @update="updateServer(selectedId, $event)" @credentials="credentialOpen = true" />
            <ServerWhitelistPanel :server-id="selectedId" :api="serverAccessApi" :can-write="canWrite" />
            <ServerBansPanel :server-id="selectedId" :api="serverAccessApi" :can-write="canWrite" />
            <ServerEventsPanel :server-id="selectedId" :api="serverAccessApi" />
          </div>
        </div>
      </template>
    </template>
    <ServerCredentialDialog :open="credentialOpen" :server-id="selectedId" :api="serverAccessApi" :can-write="canWrite" @close="credentialOpen = false" />
  </div>
</template>

<style scoped>
.server-portal { display: grid; gap: var(--sp-4); }
.portal-head { display: flex; justify-content: space-between; align-items: start; gap: var(--sp-4); flex-wrap: wrap; }
.portal-head h1 { margin: 0 0 var(--sp-1); font-size: var(--fs-xl); }
.portal-head p { margin: 0; }
.portal-grid { display: grid; grid-template-columns: minmax(0, 1fr) minmax(0, 1.4fr); gap: var(--sp-4); align-items: start; }
.detail-column { display: grid; gap: var(--sp-4); min-width: 0; }
@media (max-width: 1100px) { .portal-grid { grid-template-columns: minmax(0, 1fr); } }
</style>
