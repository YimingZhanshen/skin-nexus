<script setup>
import { shallowRef, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { confirmDialog } from '../../dialog.js'
import { useServerCredentials } from '../../composables/useServerCredentials.js'
import DialogPanel from '../DialogPanel.vue'
import EmptyState from '../EmptyState.vue'

const props = defineProps({
  open: Boolean,
  serverId: { type: Number, default: null },
  api: { type: Object, required: true },
  canWrite: Boolean,
})
const emit = defineEmits(['close'])
const { t } = useI18n()
const panel = useServerCredentials(props.api)
const mode = shallowRef('url_secret')
const copyError = shallowRef('')
const copied = shallowRef(false)
const hidden = shallowRef(false)

watch(() => props.open, async (open) => {
  copyError.value = ''
  copied.value = false
  hidden.value = false
  mode.value = 'url_secret'
  if (open && props.serverId) await panel.load(props.serverId)
  else panel.clear()
})
watch(() => props.serverId, (id) => {
  panel.clear()
  copyError.value = ''
  copied.value = false
  if (props.open && id) panel.load(id)
})

function close() {
  panel.clear()
  emit('close')
}
async function create() {
  if (props.canWrite && props.serverId) {
    hidden.value = false
    copyError.value = ''
    copied.value = false
    await panel.create(props.serverId, mode.value)
  }
}
async function rotate(credential) {
  if (!props.canWrite || !(await confirmDialog(t('serverAccess.confirmRotate'), { danger: true }))) return
  hidden.value = false
  copyError.value = ''
  copied.value = false
  await panel.rotate(props.serverId, credential.id, credential.mode)
}
async function revoke(credential) {
  if (!props.canWrite || !(await confirmDialog(t('serverAccess.confirmRevoke'), { danger: true }))) return
  await panel.revoke(props.serverId, credential.id)
}
async function copy() {
  const secret = panel.secret.value
  copyError.value = ''
  copied.value = false
  try {
    await navigator.clipboard.writeText(secret)
    // Do not report success for a secret that was hidden, rotated or replaced
    // while the clipboard promise was pending.
    if (secret && panel.secret.value === secret) copied.value = true
  } catch {
    if (secret && panel.secret.value === secret) {
      copyError.value = t('common.dataLoadFailed', { message: t('errors.common.forbidden') })
    }
  }
}
function statusLabel(status) {
  const key = `serverAccess.statusMap.${status}`
  const label = t(key)
  return label === key ? status : label
}
</script>

<template>
  <DialogPanel :open="open" :title="t('serverAccess.credentials')" :ok-text="t('common.close')" :show-cancel="false" @ok="close" @cancel="close">
    <div class="credential-body">
      <div v-if="panel.error.value" class="error" role="alert">{{ panel.error.value }}</div>
      <p class="muted proxy-hint">{{ t('serverAccess.proxyHint') }}</p>
      <form v-if="canWrite" class="row create-row" @submit.prevent="create">
        <label for="sa-credential-mode">{{ t('serverAccess.mode') }}</label>
        <select id="sa-credential-mode" v-model="mode">
          <option value="url_secret">{{ t('serverAccess.sourceMode.url_secret') }}</option>
          <option value="header">{{ t('serverAccess.sourceMode.header') }}</option>
        </select>
        <button :disabled="panel.busy.value || !serverId">{{ t('serverAccess.createCredential') }}</button>
      </form>
      <section v-if="panel.secret.value" class="secret-box" aria-live="polite">
        <h3>{{ t('serverAccess.secretTitle') }}</h3>
        <p>{{ t('serverAccess.secretHint') }}</p>
        <label class="sr-only" for="sa-credential-secret">{{ t('serverAccess.secretTitle') }}</label>
        <input id="sa-credential-secret" readonly :value="panel.secret.value" autocomplete="off" />
        <div class="row secret-actions">
          <button type="button" class="secondary" @click="copy">{{ t('serverAccess.copy') }}</button>
          <button type="button" class="secondary" @click="panel.clearSecret(); hidden = true">{{ t('serverAccess.hide') }}</button>
        </div>
        <p v-if="copied" class="muted" role="status">{{ t('serverAccess.copied') }}</p>
        <div v-if="copyError" class="error" role="alert">{{ copyError }}</div>
      </section>
      <p v-else-if="hidden" class="muted" role="status">{{ t('serverAccess.secretHidden') }}</p>
      <EmptyState v-if="!panel.credentials.value.length" :text="t('serverAccess.empty')" />
      <div v-else class="table-scroll">
        <table>
          <caption class="sr-only">{{ t('serverAccess.credentials') }}</caption>
          <thead><tr><th scope="col">ID</th><th scope="col">{{ t('serverAccess.mode') }}</th><th scope="col">{{ t('serverAccess.status') }}</th><th scope="col">{{ t('serverAccess.expiresAt') }}</th><th scope="col">{{ t('serverAccess.lastUsedAt') }}</th><th scope="col">{{ t('serverAccess.actions') }}</th></tr></thead>
          <tbody><tr v-for="credential in panel.credentials.value" :key="credential.id">
            <td>{{ credential.id }}</td><td>{{ t(`serverAccess.sourceMode.${credential.mode}`) }}</td><td>{{ statusLabel(credential.status) }}</td>
            <td>{{ credential.expiresAt || '—' }}</td><td>{{ credential.lastUsedAt || t('serverAccess.never') }}</td>
            <td><div v-if="canWrite" class="row row-actions">
              <button type="button" class="secondary small" :disabled="panel.busy.value || credential.status !== 'active'" @click="rotate(credential)">{{ t('serverAccess.rotate') }}</button>
              <button type="button" class="secondary small danger-outline" :disabled="panel.busy.value || credential.status !== 'active'" @click="revoke(credential)">{{ t('serverAccess.revoke') }}</button>
            </div></td>
          </tr></tbody>
        </table>
      </div>
    </div>
  </DialogPanel>
</template>

<style scoped>
.credential-body { display: grid; gap: var(--sp-3); }
.proxy-hint { margin: 0; font-size: var(--fs-s); }
.create-row { margin: 0; flex-wrap: wrap; }
.secret-box { display: grid; gap: var(--sp-2); padding: var(--sp-3); border: 1px solid var(--border); border-radius: var(--r-m); background: var(--panel-2); }
.secret-box h3, .secret-box p { margin: 0; }
.secret-box input { width: 100%; font-family: var(--mono); }
.secret-actions { margin: 0; flex-wrap: wrap; }
.credential-body table { min-width: 720px; }
.row-actions { margin: 0; flex-wrap: wrap; }
</style>
