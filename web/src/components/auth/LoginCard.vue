<script setup>
// LoginCard.vue — the sign-in card, and the two ways out of a failed attempt:
// reset the password by mail, or read why the account cannot get in. Both are
// reachable logged out.
//
// The shared sign-in machinery (the one in-flight guard, the 429 countdown,
// the post-auth resume) is injected from the shell: only one card is ever on
// screen, so they are one state, not five copies.
import { inject, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { api } from '../../api.js'
import PluginSlot from '../plugin/PluginSlot.vue'

const { t } = useI18n()
const auth = inject('authState')
const goto = inject('gotoView')
const {
  user, busy: authBusy, err: authErr, locked, waitText, startWait, finishAuth,
  mailEnabled, forgotSent,
} = auth

const identifier = ref('')
const password = ref('')

async function doLogin() {
  authErr.value = ''
  if (locked.value) return
  authBusy.value = true
  try {
    user.value = await api.login(identifier.value, password.value)
    // New backends return `permission` on login; fall back to /me only for
    // older servers (saves one RTT on every login) — and for a blocked
    // account, which needs the address the screen has to name. login answers
    // with id/nickname/permission only, so without this the gate's own page
    // would read "the link is on its way to" and then nothing.
    if (user.value && (user.value.permission === undefined || user.value.verifyRequired)) {
      try { user.value = await api.me() } catch { /* keep login payload */ }
    }
  } catch (e) {
    // A lockout answers with the seconds left in the body: without it the
    // player only learns "too many attempts" and keeps clicking, which is how
    // a typo turns into being shut out for fifteen minutes.
    if (e.status === 429 && e.data && e.data.retry_after_seconds > 0) {
      startWait(Number(e.data.retry_after_seconds))
    } else {
      authErr.value = e.message
    }
    return
  } finally { authBusy.value = false }
  finishAuth()
}
</script>

<template>
  <div class="card">
    <h2>{{ t('auth.loginTitle') }}</h2>
    <label for="f-identifier">{{ t('auth.identifier') }}</label>
    <input id="f-identifier" v-model="identifier" autocomplete="username" />
    <label for="f-password">{{ t('auth.password') }}</label>
    <input id="f-password" v-model="password" type="password" autocomplete="current-password" @keyup.enter="doLogin" />
    <!-- The two ways out of a failed sign-in: reset the password by mail, or
         read why the account cannot get in. Both are reachable logged out. -->
    <div class="row auth-links" style="margin-top: 8px; justify-content: space-between;">
      <a href="/help" class="muted" @click.prevent="goto('help')">{{ t('nav.help') }}</a>
      <a href="/forgot-password" class="muted" v-if="mailEnabled" @click.prevent="forgotSent = false; goto('forgot')">{{ t('auth.forgotLink') }}</a>
    </div>
    <div class="error" role="alert" v-if="authErr || locked">{{ locked ? waitText() : authErr }}</div>
    <div class="row" style="margin-top: 16px;">
      <button :disabled="authBusy || locked" @click="doLogin">{{ authBusy ? t('common.pleaseWait') : t('common.login') }}</button>
      <button class="secondary" @click="goto('register')">{{ t('common.register') }}</button>
    </div>
    <PluginSlot slot="auth.login.after" />
  </div>
</template>
