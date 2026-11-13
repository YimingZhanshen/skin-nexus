<script setup>
// ForgotCard.vue — the shareable "I forgot my password" form. mail
// unconfigured means the flow is honestly unavailable (direct navigation to
// /forgot-password lands here too), and the server is enumeration-safe, so
// the sent-state is the same for every address.
import { inject, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { api } from '../../api.js'

const { t } = useI18n()
const auth = inject('authState')
const goto = inject('gotoView')
const { busy: authBusy, err: authErr, siteConf, confLoaded, forgotSent } = auth

const forgotEmail = ref('')

async function doForgot() {
  authErr.value = ''
  if (authBusy.value) return
  authBusy.value = true
  try {
    await api.forgotPassword(forgotEmail.value.trim())
    forgotSent.value = true
  } catch (e) { authErr.value = e.message } finally { authBusy.value = false }
}
</script>

<template>
  <div class="card">
    <h2>{{ t('auth.forgotTitle') }}</h2>
    <!-- mail unconfigured → the flow is honestly unavailable (direct
         navigation to /forgot-password lands here too) -->
    <template v-if="confLoaded && !siteConf.mail_configured">
      <p class="muted">{{ t('auth.mailDisabled') }}</p>
      <div class="row" style="margin-top: 16px;">
        <button class="secondary" @click="goto('login')">{{ t('auth.toLogin') }}</button>
      </div>
    </template>
    <template v-else-if="!forgotSent">
      <label for="f-forgot-email">{{ t('auth.forgotEmail') }}</label>
      <input id="f-forgot-email" v-model="forgotEmail" type="email" autocomplete="email" @keyup.enter="doForgot" />
      <div class="error" role="alert" v-if="authErr">{{ authErr }}</div>
      <div class="row" style="margin-top: 16px;">
        <button :disabled="authBusy" @click="doForgot">{{ authBusy ? t('common.pleaseWait') : t('auth.forgotSubmit') }}</button>
        <button class="secondary" @click="goto('login')">{{ t('auth.toLogin') }}</button>
      </div>
    </template>
    <template v-else>
      <p class="muted">{{ t('auth.forgotSent') }}</p>
      <div class="row" style="margin-top: 16px;">
        <button class="secondary" @click="goto('login')">{{ t('auth.toLogin') }}</button>
      </div>
    </template>
  </div>
</template>
