<script setup>
import { shallowRef, computed } from 'vue'
import { useI18n } from 'vue-i18n'
import EmptyState from '../EmptyState.vue'

const props = defineProps({
  servers: { type: Array, default: () => [] },
  selectedId: { type: Number, default: null },
  used: { type: Number, default: 0 },
  limit: { type: Number, default: 0 },
  loading: Boolean,
  busy: Boolean,
  canWrite: Boolean,
  error: String,
})
const emit = defineEmits(['select', 'create', 'update', 'retry'])
const { t } = useI18n()
const newName = shallowRef('')
const atQuota = computed(() => props.limit > 0 && props.used >= props.limit)
const statusLabel = (status) => t(`serverAccess.statusMap.${status}`)

function create() {
  const name = newName.value.trim()
  if (!name || !props.canWrite || props.busy || atQuota.value) return
  emit('create', name)
  newName.value = ''
}
</script>

<template>
  <section class="card server-list">
    <div class="section-head">
      <h2>{{ t('serverAccess.title') }}</h2>
      <span class="muted">{{ t('serverAccess.serverCount', { used, limit }) }}</span>
    </div>
    <div v-if="error" class="error" role="alert">{{ error }} <button class="secondary small" @click="emit('retry')">{{ t('common.retry') }}</button></div>
    <p v-if="loading" class="muted" role="status">{{ t('common.loading') }}</p>
    <EmptyState v-else-if="!servers.length" :text="t('serverAccess.noServers')" />
    <div v-else class="table-scroll">
      <table>
        <caption class="sr-only">{{ t('serverAccess.title') }}</caption>
        <thead><tr><th scope="col">{{ t('serverAccess.serverName') }}</th><th scope="col">{{ t('serverAccess.publicId') }}</th><th scope="col">{{ t('serverAccess.status') }}</th><th scope="col">{{ t('serverAccess.actions') }}</th></tr></thead>
        <tbody>
          <tr v-for="server in servers" :key="server.id" :class="{ selected: selectedId === server.id }">
            <td><button class="secondary server-pick" :aria-pressed="selectedId === server.id ? 'true' : 'false'" @click="emit('select', server.id)">{{ server.name }}</button></td>
            <td><code>{{ server.publicId }}</code></td>
            <td><span class="badge" :class="{ danger: server.status === 'suspended' }">{{ statusLabel(server.status) }}</span></td>
            <td>
              <button v-if="canWrite && server.status !== 'suspended'" class="secondary small" :disabled="busy" @click="emit('update', server.id, { status: server.status === 'active' ? 'disabled' : 'active' })">
                {{ server.status === 'active' ? t('serverAccess.disable') : t('serverAccess.enable') }}
              </button>
            </td>
          </tr>
        </tbody>
      </table>
    </div>
    <form v-if="canWrite" class="row create-row" @submit.prevent="create">
      <label class="sr-only" for="sa-new-server">{{ t('serverAccess.serverName') }}</label>
      <input id="sa-new-server" v-model="newName" maxlength="191" :placeholder="t('serverAccess.serverName')" required />
      <button :disabled="busy || !newName.trim() || atQuota">{{ busy ? t('serverAccess.creating') : t('serverAccess.create') }}</button>
    </form>
    <p v-if="atQuota" class="muted">{{ t('serverAccess.serverCount', { used, limit }) }}</p>
  </section>
</template>

<style scoped>
.server-list { display: grid; gap: var(--sp-3); }
.section-head { display: flex; justify-content: space-between; align-items: baseline; gap: var(--sp-3); flex-wrap: wrap; }
.section-head h2 { margin: 0; font-size: var(--fs-l); }
.server-list table { min-width: 680px; }
.server-pick { max-width: 100%; overflow-wrap: anywhere; text-align: start; }
.selected { background: var(--accent-wash); }
.create-row { margin: 0; }
.create-row input { flex: 1; min-width: 0; }
</style>
