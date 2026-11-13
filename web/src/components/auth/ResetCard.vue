<script setup>
// ResetCard.vue — the new-password half of the mail flow. The token arrives
// on the URL (/reset-password?token=…), so it is shell state; the two fields
// and the client-side match check live here.
import { inject, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { api } from '../../api.js'

const { t } = useI18n()
const auth = inject('authState')
const goto = inject('gotoView')
const { busy: authBusy, err: authErr, mailEnabled, forgotSent, resetToken } = auth

const resetPw1 = ref('')
const resetPw2 = ref('')
const resetDone = ref(false)

async function doReset() {
  authErr.value = ''
  if (authBusy.value) return
  if (resetPw1.value !== resetPw2.value) { authErr.value = t('auth.resetMismatch'); return }
  authBusy.value = true
  try {
    await api.resetPassword(resetToken.value, resetPw1.value)
    resetDone.value = true
  } catch (e) { authErr.value = e.message } finally { authBusy.value = false }
}
</script>

<template>
  <div class="card">
    <h2>{{ t('auth.resetTitle') }}</h2>
    <template v-if="resetDone">
      <p class="muted">{{ t('auth.resetDone') }}</p>
      <div class="row" style="margin-top: 16px;">
        <button @click="goto('login')">{{ t('common.login') }}</button>
      </div>
    </template>
    <template v-else-if="resetToken">
      <label for="f-reset1">{{ t('auth.resetNew') }}</label>
      <input id="f-reset1" v-model="resetPw1" type="password" autocomplete="new-password" />
      <label for="f-reset2">{{ t('auth.resetConfirm') }}</label>
      <input id="f-reset2" v-model="resetPw2" type="password" autocomplete="new-password" @keyup.enter="doReset" />
      <div class="error" role="alert" v-if="authErr">{{ authErr }}</div>
      <div class="row" style="margin-top: 16px;">
        <button :disabled="authBusy" @click="doReset">{{ authBusy ? t('common.pleaseWait') : t('auth.resetSubmit') }}</button>
        <button class="secondary" @click="goto('login')">{{ t('auth.toLogin') }}</button>
      </div>
    </template>
    <template v-else>
      <p class="error" role="alert">{{ t('auth.resetInvalid') }}</p>
      <div class="row" style="margin-top: 16px;">
        <button v-if="mailEnabled" @click="forgotSent = false; goto('forgot')">{{ t('auth.forgotSubmit') }}</button>
        <button class="secondary" @click="goto('login')">{{ t('auth.toLogin') }}</button>
      </div>
    </template>
  </div>
</template>
