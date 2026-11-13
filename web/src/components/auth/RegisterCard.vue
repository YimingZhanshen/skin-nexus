<script setup>
// RegisterCard.vue — the sign-up card. The fields it shows follow the
// deployment's real configuration (invite required-or-absent, email field
// required when the verification gate is on), so the card never offers a box
// the server would ignore or demands one it never asked for.
//
// The gated-signup landing it hands off to lives in the shell: the landing is
// a route (/email-verify), and its token handling is pinned by a source gate
// that reads App.vue.
import { inject, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { api } from '../../api.js'
import PluginSlot from '../plugin/PluginSlot.vue'
import PluginFields from '../plugin/PluginFields.vue'

const { t } = useI18n()
const auth = inject('authState')
const goto = inject('gotoView')
const {
  user, busy: authBusy, err: authErr, finishAuth, authWaitingElsewhere,
  verifyGateEnabled, inviteIsRequired, inviteFieldShown, emailFieldShown,
  verifyPending, verifyMailTo, verifyMailNote, verifyState, forgotSent,
  pluginFormData,
} = auth

const email = ref('')
const nickname = ref('')
const password = ref('')
const invitationCode = ref('')

async function doRegister() {
  authErr.value = ''
  if (authBusy.value) return
  // The verification gate is enforced server-side too (webapi.go), but the
  // player learns it here, with the field named, rather than from a 400 after
  // pressing the button.
  if (verifyGateEnabled.value && !email.value.trim()) {
    authErr.value = t('auth.emailRequiredErr'); return
  }
  // Invite required → fail fast client-side with a clear message instead of
  // letting the server 400 the whole submission.
  if (inviteIsRequired.value && !invitationCode.value.trim()) {
    authErr.value = t('auth.inviteRequiredErr'); return
  }
  authBusy.value = true
  try {
    user.value = await api.register(email.value, nickname.value, password.value, invitationCode.value.trim(), pluginFormData.value)
    if (user.value && user.value.permission === undefined) user.value = { ...user.value, permission: 0 }
    // Same reason as in doLogin: the pending screen quotes the address, and the
    // signup response does not carry it.
    if (user.value && user.value.verifyRequired) {
      try { user.value = await api.me() } catch { /* the screen still renders */ }
    }
  } catch (e) { authErr.value = e.message; return } finally { authBusy.value = false }
  // The server mailed the verification link as it created the account, and the
  // gate keeps the player off the server until that link is clicked. Dropping
  // them on the dashboard, where the only trace is a muted 未验证 line on the
  // profile page, loses the one instruction they need right now — so this
  // screen carries it, unless another flow owns the landing.
  // With a zero grace window the account is pending from the first second, and
  // the blocking screen above already says everything this one would; the
  // informational landing is for the site that gave the player a window.
  if (verifyGateEnabled.value && !verifyPending.value && !authWaitingElsewhere()) {
    verifyMailTo.value = email.value.trim()
    verifyMailNote.value = ''
    goto('verify')
    verifyState.value = 'mailed'
    return
  }
  finishAuth()
}
</script>

<template>
  <div class="card">
    <h2>{{ t('auth.registerTitle') }}</h2>
    <label for="f-nickname">{{ t('auth.nickname') }}</label>
    <input id="f-nickname" v-model="nickname" />
    <span class="field-hint">{{ t('auth.nicknameRule') }}</span>
    <template v-if="emailFieldShown">
      <label :class="{ required: verifyGateEnabled }" for="f-email">{{ t(verifyGateEnabled ? 'auth.emailRequired' : 'auth.email') }}</label>
      <input id="f-email" v-model="email" type="email" :required="verifyGateEnabled" />
      <span v-if="verifyGateEnabled" class="field-hint">{{ t('auth.emailGateHint') }}</span>
    </template>
    <label for="f-register-password">{{ t('auth.passwordMin') }}</label>
    <input id="f-register-password" v-model="password" type="password" @keyup.enter="doRegister" />
    <template v-if="inviteFieldShown">
      <label class="required" for="f-invite">{{ t('auth.invitationCodeRequired') }}</label>
      <input id="f-invite" v-model="invitationCode" @keyup.enter="doRegister" />
    </template>
    <PluginFields form="auth.register" v-model="pluginFormData" />
    <div class="error" role="alert" v-if="authErr">{{ authErr }}</div>
    <div class="row" style="margin-top: 16px;">
      <button :disabled="authBusy" @click="doRegister">{{ authBusy ? t('common.pleaseWait') : t('common.register') }}</button>
      <button class="secondary" @click="goto('login')">{{ t('auth.toLogin') }}</button>
    </div>
    <PluginSlot slot="auth.register.after" />
  </div>
</template>
