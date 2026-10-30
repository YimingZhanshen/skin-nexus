<script setup>
// DevicePage: RFC 8628 verification page (SPA). Reached at /device?user_code=…
// (launcher verification_uri). Without a code it shows an input box for the
// bare verification_uri case. Submit is the JSON endpoint /api/v1/oidc/device/submit.
import { ref, onMounted } from 'vue'
import { useI18n } from 'vue-i18n'
import { oidc } from '../api.js'

const { t } = useI18n()

const userCode = ref(new URLSearchParams(location.search).get('user_code') || '')
const inputCode = ref('')
const info = ref(null)
const error = ref('')
const selectedPID = ref(0)
const done = ref('')
const loading = ref(false)
const loadingInfo = ref(false)

async function loadInfo() {
  error.value = ''; done.value = ''
  if (!userCode.value) { info.value = null; return }
  loadingInfo.value = true
  try {
    const d = await oidc.deviceInfo(userCode.value)
    if (d.error) { error.value = d.error; info.value = null; return }
    info.value = d
    // review P2-12: the backend only sets default_pid for single-player
    // accounts on purpose — never auto-pick players[0], that enabled
    // one-click wrong-profile approval. 0 forces an explicit choice
    // (the approve button stays disabled on !selectedPID).
    selectedPID.value = d.default_pid || 0
  } catch (e) { error.value = e.message; info.value = null }
  finally { loadingInfo.value = false }
}

function useCode() {
  const c = (inputCode.value || '').trim().toUpperCase()
  if (!c) return
  // keep the URL shareable/refreshable
  history.replaceState(null, '', '/device?user_code=' + encodeURIComponent(c))
  userCode.value = c
  loadInfo()
}

async function submit(decision) {
  loading.value = true; error.value = ''
  try {
    const d = await oidc.deviceSubmit(userCode.value, decision, selectedPID.value)
    if (d.error) { error.value = d.error; return }
    done.value = decision === 'approve' ? t('device.approved') : t('device.denied')
    info.value = null
  } catch (e) {
    // 401 = 未登录提交（提示而非跳转，设备流程留在本页）；用 status 判定，
    // 不再靠消息文本猜（2026-10-27 逻辑修复）
    error.value = e.status === 401 ? t('device.needLoginOnSubmit') : e.message
  } finally { loading.value = false }
}

onMounted(loadInfo)
</script>

<template>
  <div class="card" style="max-width: 480px;">
    <h2>{{ t('device.title') }}</h2>

    <!-- done -->
    <div v-if="done" class="success">{{ done }}</div>

    <!-- no user_code: manual input -->
    <template v-else-if="!userCode">
      <p class="muted">{{ t('device.manualPrompt') }}</p>
      <div class="row">
        <input v-model="inputCode" :placeholder="t('device.userCodePlaceholder')" style="max-width: 160px; text-transform: uppercase;"
               @keyup.enter="useCode" />
        <button @click="useCode">{{ t('device.continue') }}</button>
      </div>
    </template>

    <!-- pending info -->
    <p v-else-if="loadingInfo" class="muted">{{ t('common.loading') }}</p>
    <template v-else-if="info">
      <p>{{ t('device.userCode') }}：<b style="font-size: var(--fs-l); letter-spacing: 2px;">{{ info.user_code }}</b></p>
      <p>{{ t('device.requestClient') }}：<b>{{ info.client_name || info.client_id }}</b></p>
      <p class="muted">{{ t('device.requestScopes', { scopes: (info.scopes || []).join(', ') }) }}</p>
      <template v-if="info.players && info.players.length">
        <label>{{ t('device.selectProfile') }}</label>
        <div v-for="p in info.players" :key="p.id" style="margin: 4px 0;">
          <label style="display: flex; gap: 8px; align-items: center; cursor: pointer;">
            <input type="radio" :value="p.id" v-model="selectedPID" />
            <strong>{{ p.name }}</strong>
            <span class="muted" style="font-size: var(--fs-xs);">{{ (p.uuid || '').slice(0, 16) }}…</span>
          </label>
        </div>
      </template>
      <div v-else class="error">{{ t('device.noProfiles') }}</div>
      <div class="error" v-if="error">{{ error }}</div>
      <div class="row" style="margin-top: 16px;">
        <button :disabled="loading || !selectedPID" @click="submit('approve')">{{ t('device.approve') }}</button>
        <button class="secondary" :disabled="loading" @click="submit('deny')">{{ t('device.deny') }}</button>
      </div>
    </template>

    <!-- error / no info -->
    <div v-else>
      <div class="error" v-if="error">{{ error }}</div>
      <div class="row">
        <input v-model="inputCode" :placeholder="t('device.userCodePlaceholder')" style="max-width: 160px; text-transform: uppercase;"
               @keyup.enter="useCode" />
        <button @click="useCode">{{ t('device.query') }}</button>
      </div>
    </div>
  </div>
</template>
