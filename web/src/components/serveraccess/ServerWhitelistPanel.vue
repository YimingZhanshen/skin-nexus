<script setup>
import { ref, shallowRef, computed, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { confirmDialog } from '../../dialog.js'
import { useServerWhitelist } from '../../composables/useServerWhitelist.js'
import EmptyState from '../EmptyState.vue'

const props = defineProps({
  serverId: { type: Number, default: null },
  api: { type: Object, required: true },
  canWrite: Boolean,
})
const { t } = useI18n()
const panel = useServerWhitelist(props.api)
const query = shallowRef('')
const note = shallowRef('')
const names = ref({})
const canAdd = computed(() => props.canWrite && panel.resolved.value && query.value.trim() && !panel.busy.value)

watch(() => props.serverId, (id) => {
  query.value = ''
  note.value = ''
  names.value = {}
  panel.clearResolution()
  if (id) panel.load(id)
}, { immediate: true })

async function lookup() {
  const player = await panel.lookup(query.value)
  if (player) names.value = { ...names.value, [player.uid]: player.name }
}
async function add() {
  const player = panel.resolved.value
  if (!player || !(await panel.add(props.serverId, player, note.value.trim()))) return
  names.value = { ...names.value, [player.uid]: player.name }
  query.value = ''
  note.value = ''
}
async function remove(row) {
  if (!props.canWrite || !(await confirmDialog(t('serverAccess.confirmRemove', { uid: row.uid }), { danger: true }))) return
  await panel.remove(props.serverId, row.uid)
}
</script>

<template>
  <section class="card access-panel">
    <h2>{{ t('serverAccess.whitelist') }}</h2>
    <div v-if="panel.error.value" class="error" role="alert">{{ panel.error.value }}</div>
    <form v-if="canWrite" class="row lookup-row" @submit.prevent="lookup">
      <label class="sr-only" for="sa-whitelist-query">{{ t('serverAccess.lookupPlaceholder') }}</label>
      <input id="sa-whitelist-query" v-model="query" :placeholder="t('serverAccess.lookupPlaceholder')" required />
      <button class="secondary" :disabled="panel.busy.value || !query.trim()">{{ t('serverAccess.lookup') }}</button>
    </form>
    <p v-if="panel.resolved.value" class="success" role="status">{{ t('serverAccess.resolved', panel.resolved.value) }}</p>
    <form v-if="canWrite && panel.resolved.value" class="row lookup-row" @submit.prevent="add">
      <label class="sr-only" for="sa-whitelist-note">{{ t('serverAccess.note') }}</label>
      <input id="sa-whitelist-note" v-model="note" maxlength="255" :placeholder="t('serverAccess.note')" />
      <button :disabled="!canAdd">{{ t('serverAccess.add') }}</button>
    </form>
    <p v-if="panel.loading.value" class="muted" role="status">{{ t('common.loading') }}</p>
    <EmptyState v-else-if="!panel.rows.value.length" :text="t('serverAccess.empty')" />
    <div v-else class="table-scroll">
      <table>
        <caption class="sr-only">{{ t('serverAccess.whitelist') }}</caption>
        <thead><tr><th scope="col">{{ t('serverAccess.lookupPlaceholder') }}</th><th scope="col">UID</th><th scope="col">{{ t('serverAccess.note') }}</th><th scope="col">{{ t('serverAccess.createdAt') }}</th><th scope="col">{{ t('serverAccess.actions') }}</th></tr></thead>
        <tbody><tr v-for="row in panel.rows.value" :key="row.id">
          <td>{{ names[row.uid] || '—' }}</td><td>{{ row.uid }}</td><td>{{ row.note || '—' }}</td><td>{{ row.createdAt }}</td>
          <td><button v-if="canWrite" class="secondary small danger-outline" :disabled="panel.busy.value" @click="remove(row)">{{ t('serverAccess.remove') }}</button></td>
        </tr></tbody>
      </table>
    </div>
  </section>
</template>

<style scoped>
.access-panel { display: grid; gap: var(--sp-3); }
.access-panel h2 { margin: 0; font-size: var(--fs-m); }
.access-panel table { min-width: 640px; }
.lookup-row { margin: 0; }
.lookup-row input { flex: 1; min-width: 0; }
</style>
