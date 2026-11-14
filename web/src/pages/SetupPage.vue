<script setup>
// SetupPage: first-run install wizard (empty-DB init). Shown automatically
// when GET /api/v1/setup/status reports needs_setup. One-way door: after
// completion the API returns 404 and this page redirects to login.
import { ref, onMounted } from 'vue'
import { useI18n } from 'vue-i18n'
import { describe } from '../errors.js'

const { t } = useI18n()

const step = ref(1) // 1 site -> 2 admin -> 3 skin domains -> 4 done
const error = ref('')
const loading = ref(false)

const siteName = ref('')
const baseURL = ref(location.origin)
const nickname = ref('')
const email = ref('')
const password = ref('')
const password2 = ref('')
const skinDomains = ref('')
const setupToken = ref('')
const tokenRequired = ref(false)
const done = ref(false)
const result = ref({ site_url: '', env_base_url: '', mismatch: false })

onMounted(async () => {
  try {
    const r = await fetch('/api/v1/setup/status')
    const d = await r.json()
    if (!d.needs_setup) { location.href = '/'; }
    tokenRequired.value = d.token_required === true
  } catch { /* server down — keep the form visible */ }
})

function next() {
  error.value = ''
  if (step.value === 1 && !siteName.value.trim()) {
    error.value = t('setup.siteNameRequired'); return
  }
  // Same rule the API enforces (and the panel's site_url setting uses), so a
  // typo like skin.example.com does not cost a submit round trip.
  if (step.value === 1 && baseURL.value.trim() && !/^https?:\/\//i.test(baseURL.value.trim())) {
    error.value = t('setup.baseUrlScheme'); return
  }
  if (step.value === 2) {
    if (!nickname.value.trim() || password.value.length < 8) {
      error.value = t('setup.adminFieldsRequired'); return
    }
    if (password.value !== password2.value) {
      error.value = t('setup.passwordMismatch'); return
    }
  }
  step.value++
}

async function finish() {
  error.value = ''; loading.value = true
  try {
    const r = await fetch('/api/v1/setup', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({
        site_name: siteName.value.trim(),
        site_url: baseURL.value.trim(),
        nickname: nickname.value.trim(),
        email: email.value.trim(),
        password: password.value,
        skin_domains: skinDomains.value.split('\n').map(s => s.trim()).filter(Boolean),
        setup_token: setupToken.value.trim(),
      }),
    })
    const d = await r.json()
    if (!r.ok) { error.value = describe(d, r.status); return }
    result.value = {
      site_url: d.site_url || '',
      env_base_url: d.env_base_url || '',
      mismatch: d.mismatch === true,
    }
    done.value = true
    step.value = 4
  } catch (e) {
    error.value = e.message
  } finally { loading.value = false }
}

// template expressions cannot reach `location` (not in Vue's globals
// whitelist) — handlers must live in script scope.
function goHome() {
  location.href = '/'
}
</script>

<template>
  <div class="card" style="max-width: 520px;">
    <h2>{{ t('setup.wizardTitle', { site: t('site.name') }) }}</h2>
    <div class="muted" style="margin-bottom: 12px;">{{ t('setup.step', { step: step, total: 4 }) }}</div>

    <!-- step 1: site identity -->
    <template v-if="step === 1">
      <label for="s-site-name">{{ t('setup.siteName') }}</label>
      <input id="s-site-name" v-model="siteName" :placeholder="t('setup.siteNameExample')" />
      <label for="s-base-url">{{ t('setup.baseUrl') }}</label>
      <input id="s-base-url" v-model="baseURL" placeholder="https://skin.example.com" />
      <template v-if="tokenRequired">
        <label for="s-setup-token">{{ t('setup.setupToken') }}</label>
        <input id="s-setup-token" v-model="setupToken" autocomplete="off" spellcheck="false" />
        <div class="muted" style="font-size: var(--fs-xs); margin-top: 8px;">
          {{ t('setup.setupTokenHint') }}
        </div>
      </template>
      <div class="muted" style="font-size: var(--fs-xs); margin-top: 8px;">
        {{ t('setup.baseUrlHint') }}
      </div>
    </template>

    <!-- step 2: admin account -->
    <template v-else-if="step === 2">
      <label for="s-nickname">{{ t('setup.nickname') }}</label>
      <input id="s-nickname" v-model="nickname" />
      <label for="s-email">{{ t('setup.email') }}</label>
      <input id="s-email" v-model="email" type="email" />
      <label for="s-password">{{ t('setup.password') }}</label>
      <input id="s-password" v-model="password" type="password" />
      <label for="s-password2">{{ t('setup.password2') }}</label>
      <input id="s-password2" v-model="password2" type="password" />
    </template>

    <!-- step 3: skin domains -->
    <template v-else-if="step === 3">
      <label for="s-domains">{{ t('setup.domains') }}</label>
      <textarea id="s-domains" v-model="skinDomains" rows="6" placeholder="skin.example.com"></textarea>
      <div class="muted" style="font-size: var(--fs-xs); margin-top: 8px;">
        {{ t('setup.domainsHint') }}
      </div>
    </template>

    <!-- step 4: done -->
    <template v-else>
      <div class="success" role="status">{{ t('setup.done') }}</div>
      <p class="muted" style="margin-top: 12px;">
        {{ t('setup.siteUrlWritten') }}
        <b>{{ result.site_url || t('setup.envFallback') }}</b>
      </p>
      <div class="error" role="alert" v-if="result.mismatch">
        {{ t('setup.mismatch', { url: result.env_base_url }) }}
      </div>
    </template>

    <div class="error" role="alert" v-if="error">{{ error }}</div>

    <div class="row" style="margin-top: 16px;">
      <button v-if="step > 1 && step < 4" class="secondary" @click="step--">{{ t('setup.prev') }}</button>
      <button v-if="step < 3" @click="next">{{ t('setup.next') }}</button>
      <button v-if="step === 3" :disabled="loading" @click="finish">{{ loading ? t('setup.submitting') : t('setup.finish') }}</button>
      <button v-if="step === 4" @click="goHome">{{ t('setup.enterSite') }}</button>
    </div>
  </div>
</template>
