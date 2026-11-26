<script setup>
import { watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { useServerEvents } from '../../composables/useServerEvents.js'
import EmptyState from '../EmptyState.vue'

const props = defineProps({
  serverId: { type: Number, default: null },
  api: { type: Object, required: true },
})
const { t } = useI18n()
const panel = useServerEvents(props.api)
watch(() => props.serverId, (id) => { if (id) panel.load(id) }, { immediate: true })

function label(group, value) {
  const key = `serverAccess.${group}.${value}`
  const translated = t(key)
  return translated === key ? value : translated
}
</script>

<template>
  <section class="card events-panel">
    <h2>{{ t('serverAccess.events') }}</h2>
    <div v-if="panel.error.value" class="error" role="alert">{{ panel.error.value }}</div>
    <p v-if="panel.loading.value" class="muted" role="status">{{ t('common.loading') }}</p>
    <EmptyState v-else-if="!panel.rows.value.length" :text="t('serverAccess.noEvents')" />
    <div v-else class="table-scroll">
      <table>
        <caption class="sr-only">{{ t('serverAccess.events') }}</caption>
        <thead><tr><th scope="col">{{ t('serverAccess.eventTime') }}</th><th scope="col">{{ t('serverAccess.result') }}</th><th scope="col">{{ t('serverAccess.reason') }}</th><th scope="col">{{ t('serverAccess.duration') }}</th><th scope="col">{{ t('serverAccess.source') }}</th></tr></thead>
        <tbody><tr v-for="row in panel.rows.value" :key="row.id">
          <td>{{ row.createdAt }}</td>
          <td><span class="badge" :class="{ danger: row.outcome !== 'allow' }">{{ label('outcome', row.outcome) }}</span></td>
          <td>{{ label('reasonCode', row.reasonCode) }}</td>
          <td>{{ row.durationMs }} ms</td>
          <td>{{ label('sourceMode', row.sourceMode) }}</td>
        </tr></tbody>
      </table>
    </div>
    <button v-if="panel.hasMore.value" class="secondary" :disabled="panel.loading.value" @click="panel.more(props.serverId)">{{ t('serverAccess.loadMore') }}</button>
    <p v-else-if="panel.rows.value.length" class="muted">{{ t('serverAccess.noMore') }}</p>
  </section>
</template>

<style scoped>
.events-panel { display: grid; gap: var(--sp-3); }
.events-panel h2 { margin: 0; font-size: var(--fs-m); }
.events-panel table { min-width: 760px; }
.events-panel button { justify-self: start; }
</style>
