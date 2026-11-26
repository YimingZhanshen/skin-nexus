<script setup>
import { computed, shallowRef, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import DialogPanel from '../DialogPanel.vue'

const props = defineProps({
  tenant: { type: Object, required: true },
  api: { type: Object, required: true },
  canExport: Boolean,
  canDelete: Boolean,
  busy: Boolean,
})
const emit = defineEmits(['changed'])
const { t } = useI18n()
const exporting = shallowRef(false)
const requesting = shallowRef(false)
const dialogOpen = shallowRef(false)
const confirmation = shallowRef('')
const deletion = shallowRef(null)
const error = shallowRef('')
const exported = shallowRef(false)
const actionBusy = computed(() => props.busy || exporting.value || requesting.value)
const deleting = computed(() => (deletion.value?.status || props.tenant.status) === 'deleting')
const deletionRequestedAt = computed(() => deletion.value?.deletionRequestedAt || props.tenant.deletionRequestedAt)
const deletionDueAt = computed(() => deletion.value?.deletionDueAt || props.tenant.deletionDueAt)
const confirmationMatches = computed(() => confirmation.value === props.tenant.name)

watch(() => props.tenant.id, () => {
  deletion.value = null
  dialogOpen.value = false
  confirmation.value = ''
  error.value = ''
  exported.value = false
})

function openDeletion() {
  if (!props.canDelete || actionBusy.value || deleting.value) return
  confirmation.value = ''
  error.value = ''
  dialogOpen.value = true
}
function closeDeletion() {
  dialogOpen.value = false
  confirmation.value = ''
}
async function requestDeletion() {
  if (!props.canDelete || actionBusy.value || deleting.value || !confirmationMatches.value) return
  requesting.value = true
  error.value = ''
  try {
    const result = await props.api.requestTenantDeletion(confirmation.value)
    deletion.value = result
    closeDeletion()
    emit('changed', result)
  } catch (cause) {
    error.value = cause.message
  } finally {
    requesting.value = false
  }
}

async function exportTenant() {
  if (!props.canExport || actionBusy.value) return
  exporting.value = true
  error.value = ''
  exported.value = false
  let url
  try {
    const blob = await props.api.exportTenant()
    url = URL.createObjectURL(blob)
    const link = document.createElement('a')
    link.href = url
    link.download = 'serveraccess-tenant.json'
    link.click()
    link.remove()
    exported.value = true
  } catch (cause) {
    error.value = cause.message
  } finally {
    if (url) URL.revokeObjectURL(url)
    exporting.value = false
  }
}
</script>

<template>
  <section class="card lifecycle-panel">
    <h2>{{ t('serverAccess.dataLifecycle') }}</h2>
    <p class="muted">{{ t('serverAccess.exportHint') }}</p>
    <div class="row lifecycle-actions">
      <button type="button" class="secondary" :disabled="!canExport || actionBusy" @click="exportTenant">{{ t('serverAccess.exportData') }}</button>
      <button v-if="canDelete && !deleting" type="button" class="secondary danger-outline" :disabled="actionBusy" @click="openDeletion">{{ t('serverAccess.requestDeletion') }}</button>
      <span v-if="exported" class="muted" role="status">{{ t('serverAccess.exportSuccess') }}</span>
    </div>
    <div v-if="deleting" class="deletion-state" role="status">
      <strong>{{ t('serverAccess.deletionRequested') }}</strong>
      <span>{{ t('serverAccess.deletionRequestedAt') }}: <time v-if="deletionRequestedAt" :datetime="deletionRequestedAt">{{ deletionRequestedAt }}</time></span>
      <span>{{ t('serverAccess.deletionDueAt') }}: <time v-if="deletionDueAt" :datetime="deletionDueAt">{{ deletionDueAt }}</time></span>
      <p class="muted">{{ t('serverAccess.deletionPending') }}</p>
    </div>
    <div v-if="error && !dialogOpen" class="error" role="alert">{{ error }}</div>
    <DialogPanel :open="dialogOpen" :title="t('serverAccess.deleteTenantTitle')" :ok-text="t('serverAccess.confirmDeletion')" danger @ok="requestDeletion" @cancel="closeDeletion">
      <div class="delete-body">
        <p>{{ t('serverAccess.deleteTenantHint', { name: tenant.name }) }}</p>
        <div v-if="error" class="error" role="alert">{{ error }}</div>
        <label for="sa-delete-confirmation">{{ t('serverAccess.deleteConfirmationHint', { name: tenant.name }) }}</label>
        <input id="sa-delete-confirmation" v-model="confirmation" autocomplete="off" :aria-describedby="'sa-delete-confirmation-hint'" />
        <p id="sa-delete-confirmation-hint" class="muted">{{ t('serverAccess.deleteConfirmationHint', { name: tenant.name }) }}</p>
      </div>
    </DialogPanel>
  </section>
</template>

<style scoped>
.lifecycle-panel { display: grid; gap: var(--sp-2); }
.lifecycle-panel h2, .lifecycle-panel p { margin: 0; }
.lifecycle-actions { margin: 0; flex-wrap: wrap; align-items: center; }
.deletion-state { display: grid; gap: var(--sp-1); }
.deletion-state p { margin-top: var(--sp-1); }
.delete-body { display: grid; gap: var(--sp-2); }
.delete-body p { margin: 0; }
.delete-body input { width: 100%; }
</style>
