<script setup>
import { ref, shallowRef, computed, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { confirmDialog } from '../../dialog.js'
import { useServerBans } from '../../composables/useServerBans.js'
import EmptyState from '../EmptyState.vue'

const props = defineProps({
  serverId: { type: Number, default: null },
  api: { type: Object, required: true },
  canWrite: Boolean,
})
const { t } = useI18n()
const panel = useServerBans(props.api)
const query = shallowRef('')
const reason = shallowRef('')
const expiry = shallowRef('')
const permanent = shallowRef(true)
const validationError = shallowRef('')
const names = ref({})
const canAdd = computed(() => props.canWrite && panel.resolved.value && (permanent.value || expiry.value) && !panel.busy.value)

watch(() => props.serverId, (id) => {
  query.value = ''
  reason.value = ''
  expiry.value = ''
  permanent.value = true
  validationError.value = ''
  names.value = {}
  panel.clearResolution()
  if (id) panel.load(id)
}, { immediate: true })

async function lookup() {
  const player = await panel.lookup(query.value)
  if (player) names.value = { ...names.value, [player.uid]: player.name }
}
async function add() {
  if (!panel.resolved.value) return
  let expiresAt = ''
  if (!permanent.value) {
    const at = new Date(expiry.value)
    if (Number.isNaN(at.getTime()) || at.getTime() <= Date.now()) {
      validationError.value = t('serverAccess.invalidExpiry')
      return
    }
    expiresAt = at.toISOString()
  }
  const player = panel.resolved.value
  if (!(await panel.add(props.serverId, { uid: player.uid, reason: reason.value.trim(), expiresAt }))) return
  names.value = { ...names.value, [player.uid]: player.name }
  query.value = ''
  reason.value = ''
  expiry.value = ''
  permanent.value = true
}
async function unban(row) {
  if (!props.canWrite || !(await confirmDialog(t('serverAccess.confirmUnban', { uid: row.uid }), { danger: true }))) return
  await panel.remove(props.serverId, row.uid)
}
</script>

<template>
  <section class="card access-panel">
    <h2>{{ t('serverAccess.bans') }}</h2>
    <div v-if="panel.error.value || validationError" class="error" role="alert">{{ panel.error.value || validationError }}</div>
    <form v-if="canWrite" class="row lookup-row" @submit.prevent="lookup">
      <label class="sr-only" for="sa-ban-query">{{ t('serverAccess.lookupPlaceholder') }}</label>
      <input id="sa-ban-query" v-model="query" :placeholder="t('serverAccess.lookupPlaceholder')" required />
      <button class="secondary" :disabled="panel.busy.value || !query.trim()">{{ t('serverAccess.lookup') }}</button>
    </form>
    <p v-if="panel.resolved.value" class="success" role="status">{{ t('serverAccess.resolved', panel.resolved.value) }}</p>
    <form v-if="canWrite && panel.resolved.value" class="ban-form" @submit.prevent="add">
      <label>{{ t('serverAccess.banReason') }}<input v-model="reason" maxlength="255" /></label>
      <label class="check-row"><input type="checkbox" v-model="permanent" /> {{ t('serverAccess.permanent') }}</label>
      <label v-if="!permanent">{{ t('serverAccess.expiry') }}<input v-model="expiry" type="datetime-local" required /></label>
      <button :disabled="!canAdd">{{ t('serverAccess.addBan') }}</button>
    </form>
    <p v-if="panel.loading.value" class="muted" role="status">{{ t('common.loading') }}</p>
    <EmptyState v-else-if="!panel.rows.value.length" :text="t('serverAccess.empty')" />
    <div v-else class="table-scroll">
      <table>
        <caption class="sr-only">{{ t('serverAccess.bans') }}</caption>
        <thead><tr><th scope="col">{{ t('serverAccess.lookupPlaceholder') }}</th><th scope="col">UID</th><th scope="col">{{ t('serverAccess.banReason') }}</th><th scope="col">{{ t('serverAccess.expiry') }}</th><th scope="col">{{ t('serverAccess.createdAt') }}</th><th scope="col">{{ t('serverAccess.actions') }}</th></tr></thead>
        <tbody><tr v-for="row in panel.rows.value" :key="row.id">
          <td>{{ names[row.uid] || '—' }}</td><td>{{ row.uid }}</td><td>{{ row.reason || '—' }}</td>
          <td>{{ row.expiresAt ? row.expiresAt : t('serverAccess.permanent') }}</td><td>{{ row.createdAt }}</td>
          <td><button v-if="canWrite" class="secondary small danger-outline" :disabled="panel.busy.value" @click="unban(row)">{{ t('serverAccess.unban') }}</button></td>
        </tr></tbody>
      </table>
    </div>
  </section>
</template>

<style scoped>
.access-panel { display: grid; gap: var(--sp-3); }
.access-panel h2 { margin: 0; font-size: var(--fs-m); }
.access-panel table { min-width: 760px; }
.lookup-row { margin: 0; }
.lookup-row input { flex: 1; min-width: 0; }
.ban-form { display: flex; align-items: end; gap: var(--sp-3); flex-wrap: wrap; }
.ban-form label { display: grid; gap: var(--sp-1); }
.check-row { display: flex; align-items: center; gap: var(--sp-2); }
</style>
