<script setup>
// VerifyCard.vue — the /email-verify landing and the gated-signup "check your
// mail" screen.
//
// Deliberately stateless: the landing reads a single-use token off the URL and
// must NOT spend it on load (mail gateways and WeChat/QQ built-in browsers
// prefetch links), so the token handling lives in the shell and is pinned by a
// source gate that reads App.vue — `prepareEmailVerify` and `doEmailVerify`
// are asserted to be there. This file only renders which state the shell is in
// and forwards the clicks.
import { inject } from 'vue'
import { useI18n } from 'vue-i18n'

const { t } = useI18n()
const auth = inject('authState')
const goto = inject('gotoView')
const {
  user, mailEnabled, verifyState, verifyMailTo, verifyMailNote, verifyMailBusy,
  doEmailVerify, resendVerificationMail,
} = auth
</script>

<template>
  <div class="card">
    <h2>{{ t('auth.verifyTitle') }}</h2>
    <p class="muted" v-if="verifyState === 'working'">{{ t('common.loading') }}</p>
    <template v-else-if="verifyState === 'done'">
      <p class="success" role="status">{{ t('auth.verifyDone') }}</p>
      <div class="row" style="margin-top: 16px;">
        <button @click="goto(user ? 'profile' : 'login')">{{ user ? t('nav.userCenter') : t('common.login') }}</button>
      </div>
    </template>
    <template v-else-if="verifyState === 'ready'">
      <p class="muted">{{ t('auth.verifyConfirmHint') }}</p>
      <div class="row" style="margin-top: 16px;">
        <button @click="doEmailVerify">{{ t('auth.verifyConfirm') }}</button>
        <button class="secondary" @click="goto(user ? 'profile' : 'login')">{{ user ? t('nav.userCenter') : t('common.login') }}</button>
      </div>
    </template>
    <!-- Gated signup: the account exists, the link is in the mailbox (or the
         site cannot mail at all — said plainly, because the gate is closed
         either way and the player would otherwise guess at a missing email). -->
    <template v-else-if="verifyState === 'mailed'">
      <p class="success" role="status" v-if="mailEnabled">{{ t('auth.verifyMailed', { email: verifyMailTo }) }}</p>
      <p class="error" role="alert" v-else>{{ t('auth.verifyMailedNoMail') }}</p>
      <p class="muted" role="status" v-if="verifyMailNote">{{ verifyMailNote }}</p>
      <div class="row" style="margin-top: 16px;">
        <button class="secondary" v-if="mailEnabled" :disabled="verifyMailBusy" @click="resendVerificationMail">{{ verifyMailBusy ? t('auth.verifySubmitting') : t('auth.verifySubmit') }}</button>
        <button @click="goto('profile')">{{ t('nav.userCenter') }}</button>
      </div>
    </template>
    <template v-else>
      <p class="error" role="alert">{{ t('auth.verifyInvalid') }}</p>
      <div class="row" style="margin-top: 16px;">
        <button v-if="user" @click="goto('profile')">{{ t('auth.verifyBackToProfile') }}</button>
        <button v-else class="secondary" @click="goto('login')">{{ t('auth.toLogin') }}</button>
      </div>
    </template>
  </div>
</template>
