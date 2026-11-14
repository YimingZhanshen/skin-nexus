<script setup>
// SettingsView: the 站点设置 group — env-migrated site knobs + OAuth
// clients + SEO + appearance/themes (moved out of the old card flow in B1;
// semantics unchanged: bool normalization, refetch-on-error, one-click SEO).
import { ref, computed, onMounted } from 'vue'
import { useI18n } from 'vue-i18n'
import { fetchJSON } from '../../adminApi.js'
import { confirmDialog } from '../../dialog.js'
import ThemeMarketSection from './ThemeMarketSection.vue'
import SeoSection from './SeoSection.vue'
import OidcClientsCard from './OidcClientsCard.vue'

// t() and nothing else renders these, so the sentences must stay legal message
// templates: check-locales rule 7 rejects a bare @ or | in settings.*.desc,
// which is what took the whole settings screen down (the compiler threw).
const { t } = useI18n()
const emit = defineEmits(['settings-changed'])

// Settings panel: env-migrated site knobs
const settings = ref([])
const setError = ref('')
const setOk = ref('')

// row widget routing (shared by loadSettings normalization + template)
const boolKeys = new Set([
  'invite_required', 'require_verification',
  'union_enabled', 'union_enable_update', 'union_enable_oauth2',
  'ygg_mojang_fallback', 'cdn_enabled',
])
const textKeys = new Set([
  'skin_domains', 'announcement', 'custom_player_name_regexp', 'texture_name_regexp',
  'union_oauth2_sig_public_key', 'cdn_headers',
])

// Panel section grouping: the backend tags every whitelist key with a
// `group`; this list is the display order (unknown ids fall to the end,
// keeping the API forward-compatible).
const GROUP_ORDER = ['basic', 'register', 'mail', 'security', 'oidc', 'ygg', 'texture', 'union']
const unionOn = computed(() => String(settings.value.find((s) => s.key === 'union_enabled')?.value || '0') === '1')
// When Union is off, hide its configuration pages; keep the enable switch
// under Security so it can be turned back on.
const visibleSettings = computed(() => settings.value.filter((s) =>
  unionOn.value || s.key === 'union_enabled' || !s.key.startsWith('union_')
))
const groupedSettings = computed(() => {
  const buckets = new Map()
  for (const s of visibleSettings.value) {
    const g = s.key === 'union_enabled' && !unionOn.value ? 'security' : (s.group || 'other')
    if (!buckets.has(g)) buckets.set(g, [])
    buckets.get(g).push(s)
  }
  const ordered = GROUP_ORDER.filter((g) => buckets.has(g))
    .map((g) => ({ id: g, items: buckets.get(g) }))
  for (const [g, items] of buckets) {
    if (!GROUP_ORDER.includes(g)) ordered.push({ id: g, items })
  }
  return ordered
})
// i18n title with a readable fallback for unknown/missing group ids
const groupTitle = (g) => {
  const key = `admin.settingsGroup.${g}`
  const title = t(key)
  return title === key ? g : title
}

async function loadSettings() {
  setError.value = ''
  try {
    const d = await fetchJSON('/api/admin/v1/settings')
    // Bool rows normalize to "1"/"0" on load (review B P2: the checkbox uses
    // true-value="1", so a stored "true"/"on"/"yes" rendered as UNCHECKED and
    // the next save silently wrote the feature back to "0").
    settings.value = (d.settings || []).map((s) =>
      boolKeys.has(s.key)
        ? { ...s, value: ['1', 'true', 'on', 'yes'].includes(String(s.value).toLowerCase()) ? '1' : '0' }
        : s
    )
  } catch (e) { setError.value = e.message }
}

async function saveSetting(s) {
  setError.value = ''
  setOk.value = ''
  try {
    await fetchJSON('/api/admin/v1/settings', {
      method: 'PUT', headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ key: s.key, value: s.value }),
    })
    setOk.value = t('common.saved')
    emit('settings-changed', s.key, s.value)
    await loadSettings()
  } catch (e) {
    // review P2-10: refetch so the UI doesn't keep a toggled-but-unsaved value
    await loadSettings()
    setError.value = e.message
  }
}

// A secret never comes back from the server, so an untouched field means
// "leave it as it is" — the backend says so, and `set` is the only thing the
// operator can see. Clearing is its own explicit action.
function secretHint(s) {
  if (!s.secret) return s.default || ''
  return s.set ? t('admin.settingsSecretSet') : t('admin.settingsSecretUnset')
}
async function clearSetting(s) {
  if (!(await confirmDialog(t('admin.settingsClearConfirm', { key: s.key }), { danger: true }))) return
  setError.value = ''
  try {
    await fetchJSON('/api/admin/v1/settings', {
      method: 'PUT', headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ key: s.key, clear: true }),
    })
    setOk.value = t('common.saved')
    emit('settings-changed', s.key, '')
    await loadSettings()
  } catch (e) {
    await loadSettings()
    setError.value = e.message
  }
}

// SEO (phase 3)
// Appearance + themes
const appearance = ref(null)
const appearError = ref('')
const appearOk = ref('')
const themes = ref([])
async function loadAppearance() {
  appearError.value = ''
  try {
    const data = await fetchJSON('/api/admin/v1/appearance')
    if (!Array.isArray(data.footer_links)) data.footer_links = []
    if (typeof data.footer_text !== 'string') data.footer_text = ''
    appearance.value = data
    const th = await fetchJSON('/api/admin/v1/themes')
    themes.value = th.themes || []
  } catch (e) { appearError.value = e.message }
}
async function appearSave() {
  appearError.value = ''
  appearOk.value = ''
  try {
    await fetchJSON('/api/admin/v1/appearance', {
      method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify(appearance.value),
    })
    appearOk.value = t('common.saved')
  } catch (e) {
    // review P2-10: refetch so the UI doesn't keep unsaved values
    await loadAppearance()
    appearError.value = e.message
  }
}

// OIDC clients

async function load() {
  // SEO and the OIDC clients fetch for themselves on mount — each owns its
  // own retry banner, so the settings screen does not serialize them.
  await loadSettings()
  await loadAppearance()
}
onMounted(load)
</script>

<template>
  <div>
    <!-- 站点设置（env 迁 UI），按后端 group 分节 -->
    <div class="card">
      <h2 style="margin-top: 0;">{{ t('admin.settings.cardTitle') }}</h2>
      <div class="error" role="alert" v-if="setError">{{ setError }}</div>
      <div class="success" role="status" v-if="setOk">{{ setOk }}</div>
      <template v-for="grp in groupedSettings" :key="grp.id">
        <h3 class="settings-group-title">{{ groupTitle(grp.id) }}</h3>
        <div class="row" v-for="s in grp.items" :key="s.key" style="margin-top: 12px; align-items: center;">
          <!-- The server sends a key, not a sentence: the panel's language is the
               operator's, and the copy exists once per locale. The raw option key
               stays below as the thing to grep for in the logs. -->
          <label style="width: 240px; font-weight: 600; font-size: var(--fs-xs);">
            {{ t(s.descKey) }}
            <span class="muted" style="display: block; font-weight: 400; font-size: var(--fs-xs);">{{ s.key }}</span>
          </label>
          <input v-if="boolKeys.has(s.key)"
                 type="checkbox" v-model="s.value" true-value="1" false-value="0" />
          <textarea v-else-if="!s.secret && textKeys.has(s.key)"
                    v-model="s.value" :style="s.key.startsWith('union_oauth2_sig_') ? 'max-width: 360px; min-height: 72px; font-family: monospace; font-size: var(--fs-xs);' : 'max-width: 360px; min-height: 40px;'"></textarea>
          <select v-else-if="s.key === 'player_name_rule'" v-model="s.value" style="max-width: 160px;">
            <option value="official">{{ t('admin.settings.ruleOfficial') }}</option>
            <option value="cjk">{{ t('admin.settings.ruleCjk') }}</option>
            <option value="utf8">{{ t('admin.settings.ruleUtf8') }}</option>
            <option value="custom">{{ t('admin.settings.ruleCustom') }}</option>
          </select>
          <select v-else-if="s.key === 'union_queryemail_mode'" v-model="s.value" style="max-width: 160px;">
            <option value="full">{{ t('admin.settings.emailFull') }}</option>
            <option value="off">{{ t('admin.settings.emailOff') }}</option>
          </select>
          <input v-else v-model="s.value" :type="s.secret ? 'password' : 'text'" style="max-width: 300px;"
                 :placeholder="secretHint(s)" autocomplete="new-password" />
          <button @click="saveSetting(s)">{{ t('common.save') }}</button>
          <button v-if="s.secret && s.set" class="secondary" @click="clearSetting(s)">{{ t('admin.settingsClear') }}</button>
          <span class="muted" style="font-size: var(--fs-xs);">{{ s.restart ? t('admin.settings.restartLater') : t('admin.settings.applyNow') }}</span>
        </div>
      </template>
    </div>

    <SeoSection />

    <!-- Appearance: themes + custom CSS/JS（失败卡外可见 + 重试） -->
    <div class="card" v-if="!appearance && appearError">
      <h2>{{ t('admin.appearance.title') }}</h2>
      <div class="error" role="alert">{{ appearError }}</div>
      <button class="secondary" style="margin-top: 12px;" @click="loadAppearance">{{ t('common.retry') }}</button>
    </div>
    <div class="card" v-if="appearance">
      <h2>{{ t('admin.appearance.title') }}</h2>
      <label for="ap-theme">{{ t('admin.appearance.activeTheme') }}</label>
      <select id="ap-theme" v-model="appearance.active_theme" style="max-width: 320px;">
        <option value="">{{ t('admin.appearance.noTheme') }}</option>
        <option v-for="th in themes" :key="th.id" :value="th.id">{{ th.name }}（{{ th.version }}）</option>
      </select>
      <div class="muted" v-if="!themes.length" style="font-size: var(--fs-xs);">
        {{ t('admin.appearance.themeDirHint') }}
      </div>
      <label style="margin-top: 12px;" for="ap-footer-text">{{ t('admin.appearance.footerText') }} <span class="muted">{{ '{year} / {site_name} / {site_url}' }}</span></label>
      <textarea id="ap-footer-text" v-model="appearance.footer_text" rows="3" maxlength="1000" :placeholder="t('admin.appearance.footerPlaceholder')"></textarea>
      <label style="margin-top: 12px;">{{ t('admin.appearance.footerLinks', { max: 8 }) }}</label>
      <div v-for="(link, i) in appearance.footer_links" :key="i" class="row" style="gap: 8px; margin-bottom: 8px; flex-wrap: wrap;">
        <input v-model="link.label" maxlength="40" :placeholder="t('admin.appearance.footerLinkName')" style="max-width: 180px;" />
        <input v-model="link.url" maxlength="2048" placeholder="https://" style="max-width: 420px;" />
        <button class="secondary" type="button" @click="appearance.footer_links.splice(i, 1)">{{ t('common.delete') }}</button>
      </div>
      <button class="secondary" type="button" v-if="appearance.footer_links.length < 8" @click="appearance.footer_links.push({ label: '', url: '' })">{{ t('admin.appearance.addLink') }}</button>
      <p class="muted" style="font-size: var(--fs-xs); margin-top: 8px;">{{ t('admin.appearance.footerPublicHint') }}</p>
      <label style="margin-top: 12px;" for="ap-css">{{ t('admin.appearance.css') }}</label>
      <textarea id="ap-css" v-model="appearance.custom_css" rows="5" placeholder=".card { border-radius: 16px; }"></textarea>
      <label for="ap-js">{{ t('admin.appearance.js') }}</label>
      <textarea id="ap-js" v-model="appearance.custom_js" rows="4" placeholder="document.addEventListener('DOMContentLoaded', () => { /* ... */ })"></textarea>
      <div class="muted" style="font-size: var(--fs-xs); margin-top: 8px;">
        {{ t('admin.appearance.jsTrustHint') }}
        {{ t('admin.appearance.jsDomHint') }}
        {{ t('admin.appearance.jsCspHint') }}
      </div>
      <div class="row" style="margin-top: 12px;">
        <button @click="appearSave">{{ t('admin.appearance.save') }}</button>
        <span class="success" role="status" v-if="appearOk">{{ appearOk }}</span>
      </div>
      <div class="error" role="alert" v-if="appearError">{{ appearError }}</div>
      <ThemeMarketSection @changed="loadAppearance" />
    </div>

    <OidcClientsCard />
  </div>
</template>

<style scoped>
/* settings section titles: quiet dividers between grouped knobs */
.settings-group-title {
  font-size: var(--fs-xs); font-weight: 700; color: var(--muted);
  margin: 24px 0 4px; padding: 16px 0 8px;
  border-top: 1px solid var(--border);
}
.settings-group-title:first-of-type { border-top: none; padding-top: 4px; margin-top: 16px; }
</style>
