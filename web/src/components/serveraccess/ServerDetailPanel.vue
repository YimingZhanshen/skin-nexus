<script setup>
import { shallowRef, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import ServerPolicyPanel from './ServerPolicyPanel.vue'

const props = defineProps({
  detail: { type: Object, required: true },
  canWrite: Boolean,
  busy: Boolean,
  error: String,
})
const emit = defineEmits(['update', 'credentials'])
const { t } = useI18n()
const name = shallowRef('')
watch(() => props.detail.server, (server) => { name.value = server.name }, { immediate: true })
function saveName() {
  const next = name.value.trim()
  if (props.canWrite && !props.busy && next && next !== props.detail.server.name) emit('update', { name: next })
}
function statusLabel(status) {
  const key = `serverAccess.statusMap.${status}`
  const label = t(key)
  return label === key ? status : label
}
</script>

<template>
  <section class="detail-column">
    <div class="card detail-head">
      <div v-if="error" class="error" role="alert">{{ error }}</div>
      <div class="row name-row">
        <label for="sa-server-name">{{ t('serverAccess.serverName') }}</label>
        <input id="sa-server-name" v-model="name" maxlength="191" :disabled="!canWrite || busy || detail.server.status === 'suspended'" @keyup.enter="saveName" />
        <button v-if="canWrite && detail.server.status !== 'suspended'" :disabled="busy || !name.trim() || name.trim() === detail.server.name" @click="saveName">{{ t('serverAccess.save') }}</button>
      </div>
      <dl class="meta-grid">
        <dt>{{ t('serverAccess.publicId') }}</dt><dd><code>{{ detail.server.publicId }}</code></dd>
        <dt>{{ t('serverAccess.status') }}</dt><dd>{{ statusLabel(detail.server.status) }}</dd>
        <dt>{{ t('serverAccess.createdAt') }}</dt><dd>{{ detail.server.createdAt }}</dd>
      </dl>
      <div class="row detail-actions">
        <button class="secondary" @click="emit('credentials')">{{ t('serverAccess.openCredentials') }}</button>
        <span class="muted">{{ t('serverAccess.exportUnavailable') }}</span>
      </div>
    </div>
    <ServerPolicyPanel :policy="detail.policy" :can-write="canWrite && detail.server.status !== 'suspended'" :busy="busy" @save="emit('update', $event)" />
  </section>
</template>

<style scoped>
.detail-column { display: grid; gap: var(--sp-4); align-content: start; }
.detail-head { display: grid; gap: var(--sp-3); }
.name-row { margin: 0; flex-wrap: wrap; }
.name-row input { flex: 1; min-width: 0; }
.meta-grid { display: grid; grid-template-columns: max-content minmax(0, 1fr); gap: var(--sp-2) var(--sp-3); margin: 0; }
.meta-grid dt { color: var(--muted); }
.meta-grid dd { margin: 0; overflow-wrap: anywhere; }
.detail-actions { margin: 0; flex-wrap: wrap; }
</style>
