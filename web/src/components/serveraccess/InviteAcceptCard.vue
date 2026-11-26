<script setup>
import { shallowRef } from 'vue'
import { useI18n } from 'vue-i18n'

const props = defineProps({
  busy: Boolean,
  error: String,
})
const emit = defineEmits(['accept'])
const { t } = useI18n()
const token = shallowRef('')
const attempted = shallowRef(false)

function submit() {
  if (props.busy || !token.value.trim()) return
  attempted.value = true
  emit('accept', token.value.trim())
  token.value = ''
}
</script>

<template>
  <section class="card invite-accept-card" aria-labelledby="sa-accept-invite-title">
    <h2 id="sa-accept-invite-title" class="invite-accept-title">{{ t('serverAccess.acceptInviteTitle') }}</h2>
    <p class="muted invite-accept-hint">{{ t('serverAccess.acceptInviteBody') }}</p>
    <form class="row invite-accept-form" @submit.prevent="submit">
      <label class="sr-only" for="sa-accept-invite-token">{{ t('serverAccess.acceptInviteToken') }}</label>
      <input
        id="sa-accept-invite-token"
        v-model="token"
        type="password"
        autocomplete="off"
        autocapitalize="none"
        spellcheck="false"
        :placeholder="t('serverAccess.acceptInviteToken')"
        required
        :disabled="busy"
        :aria-invalid="attempted && error ? 'true' : undefined"
        :aria-describedby="attempted && error ? 'sa-accept-invite-error' : undefined"
      />
      <button type="submit" :disabled="busy || !token.trim()">{{ busy ? t('serverAccess.acceptInviteBusy') : t('serverAccess.acceptInviteSubmit') }}</button>
    </form>
    <p v-if="busy" class="muted invite-accept-status" role="status">{{ t('serverAccess.acceptInviteBusy') }}</p>
    <p v-if="attempted && error" id="sa-accept-invite-error" class="error invite-accept-error" role="alert">{{ error }}</p>
  </section>
</template>

<style scoped>
.invite-accept-card { display: grid; gap: var(--sp-2); }
.invite-accept-title, .invite-accept-hint, .invite-accept-status, .invite-accept-error { margin: 0; }
.invite-accept-form { margin: 0; flex-wrap: wrap; }
.invite-accept-form input { flex: 1; min-width: 0; }
</style>
