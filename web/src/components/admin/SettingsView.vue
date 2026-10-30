<script setup>
// SettingsView: the 站点设置 group — env-migrated site knobs + OAuth
// clients + SEO + appearance/themes (moved out of the old card flow in B1;
// semantics unchanged: bool normalization, refetch-on-error, one-click SEO).
import { ref, computed, onMounted } from 'vue'
import { useI18n } from 'vue-i18n'
import { fetchJSON } from '../../adminApi.js'

const { t } = useI18n()

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
  'union_oauth2_sig_private_key', 'union_oauth2_sig_public_key', 'cdn_headers',
])
// secrets never render as plain text inputs
const secretKeys = new Set(['union_member_key', 'smtp_pass'])

// Panel section grouping: the backend tags every whitelist key with a
// `group`; this list is the display order (unknown ids fall to the end,
// keeping the API forward-compatible).
const GROUP_ORDER = ['basic', 'register', 'mail', 'security', 'ygg', 'texture', 'union']
const groupedSettings = computed(() => {
  const buckets = new Map()
  for (const s of settings.value) {
    const g = s.group || 'other'
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
    await loadSettings()
  } catch (e) {
    // review P2-10: refetch so the UI doesn't keep a toggled-but-unsaved value
    await loadSettings()
    setError.value = e.message
  }
}

// SEO (phase 3)
const seo = ref(null)
const seoError = ref('')
async function loadSEO() {
  seoError.value = ''
  try { seo.value = await fetchJSON('/api/admin/v1/seo') } catch (e) { seoError.value = e.message }
}
async function seoOneClick() {
  seoError.value = ''
  try { seo.value = await fetchJSON('/api/admin/v1/seo/oneclick', { method: 'POST' }) }
  catch (e) { seoError.value = e.message }
}
async function seoSave() {
  seoError.value = ''
  try {
    await fetchJSON('/api/admin/v1/seo', {
      method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify(seo.value),
    })
  } catch (e) {
    // review P2-10: refetch so the UI doesn't keep unsaved values
    await loadSEO()
    seoError.value = e.message
  }
}

// Appearance + themes
const appearance = ref(null)
const appearError = ref('')
const themes = ref([])
async function loadAppearance() {
  appearError.value = ''
  try {
    appearance.value = await fetchJSON('/api/admin/v1/appearance')
    const th = await fetchJSON('/api/admin/v1/themes')
    themes.value = th.themes || []
  } catch (e) { appearError.value = e.message }
}
async function appearSave() {
  appearError.value = ''
  try {
    await fetchJSON('/api/admin/v1/appearance', {
      method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify(appearance.value),
    })
  } catch (e) {
    // review P2-10: refetch so the UI doesn't keep unsaved values
    await loadAppearance()
    appearError.value = e.message
  }
}

// OIDC clients
const oidcClients = ref([])
const oidcError = ref('')
const newClient = ref({ client_id: '', name: '', redirect_uris: '', public: true })
const lastSecret = ref('') // shown once after create

async function loadOIDC() {
  oidcError.value = ''
  try {
    const c = await fetchJSON('/api/admin/v1/oidc/clients')
    oidcClients.value = c.clients || []
  } catch (e) { oidcError.value = e.message }
}
async function createClient() {
  oidcError.value = ''; lastSecret.value = ''
  const body = {
    client_id: newClient.value.client_id.trim(),
    name: newClient.value.name.trim() || newClient.value.client_id.trim(),
    redirect_uris: newClient.value.redirect_uris.split('\n').map(s => s.trim()).filter(Boolean),
    grant_types: ['authorization_code', 'refresh_token'],
    public: newClient.value.public,
  }
  if (!body.client_id) { oidcError.value = t('admin.oidcIdRequired'); return }
  try {
    const d = await fetchJSON('/api/admin/v1/oidc/clients', {
      method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify(body),
    })
    if (d.secret) lastSecret.value = d.secret // only visible now — copy it!
    newClient.value = { client_id: '', name: '', redirect_uris: '', public: true }
    await loadOIDC()
  } catch (e) { oidcError.value = e.message }
}
async function deleteClient(id) {
  if (!confirm(t('admin.oidcDeleteConfirm', { id }))) return
  oidcError.value = ''
  try {
    await fetchJSON(`/api/admin/v1/oidc/clients/${encodeURIComponent(id)}`, { method: 'DELETE' })
    oidcClients.value = oidcClients.value.filter(c => c.client_id !== id)
  } catch (e) { oidcError.value = e.message }
}

async function load() {
  await loadSettings()
  await loadSEO()
  await loadAppearance()
  await loadOIDC()
}
onMounted(load)
</script>

<template>
  <div>
    <!-- 站点设置（env 迁 UI），按后端 group 分节 -->
    <div class="card">
      <h2 style="margin-top: 0;">站点设置（原环境变量，管理员可改）</h2>
      <div class="error" v-if="setError">{{ setError }}</div>
      <div class="success" v-if="setOk">{{ setOk }}</div>
      <template v-for="grp in groupedSettings" :key="grp.id">
        <h3 class="settings-group-title">{{ groupTitle(grp.id) }}</h3>
        <div class="row" v-for="s in grp.items" :key="s.key" style="margin-top: 12px; align-items: center;">
          <!-- Chinese-first label: server-provided desc is primary, the raw key
               demoted to a small muted hint -->
          <label style="width: 240px; font-weight: 600; font-size: var(--fs-xs);">
            {{ s.desc }}
            <span class="muted" style="display: block; font-weight: 400; font-size: var(--fs-xs);">{{ s.key }}</span>
          </label>
          <input v-if="boolKeys.has(s.key)"
                 type="checkbox" v-model="s.value" true-value="1" false-value="0" style="width: auto;" />
          <textarea v-else-if="textKeys.has(s.key)"
                    v-model="s.value" :style="s.key.startsWith('union_oauth2_sig_') ? 'max-width: 360px; min-height: 72px; font-family: monospace; font-size: var(--fs-xs);' : 'max-width: 360px; min-height: 38px;'"></textarea>
          <select v-else-if="s.key === 'player_name_rule'" v-model="s.value" style="max-width: 160px;">
            <option value="official">official（字母/数字/下划线）</option>
            <option value="cjk">cjk（+ 中文/§）</option>
            <option value="utf8">utf8（任意非空白）</option>
            <option value="custom">custom（自定义正则）</option>
          </select>
          <select v-else-if="s.key === 'union_queryemail_mode'" v-model="s.value" style="max-width: 160px;">
            <option value="full">full（按 union 协议返回邮箱）</option>
            <option value="off">off（不返回）</option>
          </select>
          <input v-else v-model="s.value" :type="secretKeys.has(s.key) ? 'password' : 'text'" style="max-width: 300px;" :placeholder="s.default" />
          <button @click="saveSetting(s)">保存</button>
          <span class="muted" style="font-size: var(--fs-xs);">{{ s.restart ? '重启后生效' : '立即生效' }}</span>
        </div>
      </template>
    </div>

    <!-- SEO：加载失败不再静默（卡外横幅 + 重试），卡内横幅只管保存失败 -->
    <div class="card" v-if="!seo && seoError">
      <h2>SEO 配置</h2>
      <div class="error">{{ seoError }}</div>
      <button class="secondary" style="margin-top: 12px;" @click="loadSEO">{{ t('common.retry') }}</button>
    </div>
    <div class="card" v-if="seo">
      <h2>SEO 配置</h2>
      <div class="row">
        <button class="secondary" @click="seoOneClick">一键优化（填充默认值）</button>
        <button @click="seoSave">保存 SEO</button>
      </div>
      <label>站点标题</label>
      <input v-model="seo.title" />
      <p class="muted">
        当前 {{ (seo.title || '').length }} 字符；建议 15–30 个字符。
        搜索引擎会把过短的标题判定为信息不足（Bing 阈值 15 字符）。
      </p>
      <label>meta description</label>
      <textarea v-model="seo.description" rows="2"></textarea>
      <label>meta keywords（逗号分隔）</label>
      <input v-model="seo.keywords" />
      <label>OG 分享图 URL</label>
      <input v-model="seo.og_image" />
      <label>canonical 基址</label>
      <input v-model="seo.canonical" />
      <div class="row" style="margin-top: 8px;">
        <label style="display: flex; align-items: center; gap: 4px;">
          <input type="checkbox" v-model="seo.sitemap" /> 启用 sitemap.xml
        </label>
        <label style="display: flex; align-items: center; gap: 4px;">
          <input type="checkbox" v-model="seo.noindex" /> 整站 noindex（预发）
        </label>
      </div>
      <label style="margin-top: 8px;">robots.txt 内容</label>
      <textarea v-model="seo.robots" rows="3"></textarea>
      <div class="error" v-if="seoError">{{ seoError }}</div>
    </div>

    <!-- Appearance: themes + custom CSS/JS（失败卡外可见 + 重试） -->
    <div class="card" v-if="!appearance && appearError">
      <h2>外观（主题 / 自定义 CSS / JS）</h2>
      <div class="error">{{ appearError }}</div>
      <button class="secondary" style="margin-top: 12px;" @click="loadAppearance">{{ t('common.retry') }}</button>
    </div>
    <div class="card" v-if="appearance">
      <h2>外观（主题 / 自定义 CSS / JS）</h2>
      <label>启用主题包</label>
      <select v-model="appearance.active_theme" style="max-width: 320px;">
        <option value="">无（默认样式）</option>
        <option v-for="th in themes" :key="th.id" :value="th.id">{{ th.name }}（{{ th.version }}）</option>
      </select>
      <div class="muted" v-if="!themes.length" style="font-size: 12px;">
        将主题包放入 data/themes/&lt;id&gt;/（含 theme.json），重启服务后在此识别（新增/删除主题包需重启）。规范见 docs/theme-spec.md。
      </div>
      <label style="margin-top: 12px;">自定义 CSS（约 64KB 上限）</label>
      <textarea v-model="appearance.custom_css" rows="5" placeholder=".card { border-radius: 16px; }"></textarea>
      <label>自定义 JS（约 64KB 上限）</label>
      <textarea v-model="appearance.custom_js" rows="4" placeholder="document.addEventListener('DOMContentLoaded', () => { /* ... */ })"></textarea>
      <div class="muted" style="font-size: 12px; margin-top: 8px;">
        自定义 JS 与主题 JS 以管理员信任面原样注入（与 blessing 行为一致）——请只粘贴自己信任的代码。
        JS 在 Vue 挂载前执行，操作界面 DOM 请包在 DOMContentLoaded 回调里。
      </div>
      <div class="row" style="margin-top: 12px;">
        <button @click="appearSave">保存外观</button>
      </div>
      <div class="error" v-if="appearError">{{ appearError }}</div>
    </div>

    <!-- OIDC clients -->
    <div class="card">
      <h2>{{ t('admin.oidcClientsTitle', { count: oidcClients.length }) }}</h2>
      <div class="row">
        <input v-model="newClient.client_id" :placeholder="t('admin.oidcClientId')" style="max-width: 180px;" />
        <input v-model="newClient.name" :placeholder="t('admin.oidcClientName')" style="max-width: 180px;" />
        <label style="display: flex; align-items: center; gap: 4px; white-space: nowrap;">
          <input type="checkbox" v-model="newClient.public" /> {{ t('admin.oidcPublicCheckbox') }}
        </label>
        <button @click="createClient">{{ t('admin.oidcCreate') }}</button>
      </div>
      <textarea v-model="newClient.redirect_uris" :placeholder="t('admin.oidcRedirectPlaceholder')" rows="2"
                style="margin-top: 8px; width: 100%;"></textarea>
      <div class="success" v-if="lastSecret" style="margin-top: 8px;">
        {{ t('admin.oidcSecretOnce') }}<code>{{ lastSecret }}</code>
      </div>
      <div class="error" v-if="oidcError">{{ oidcError }}</div>
      <table v-if="oidcClients.length" style="margin-top: 12px;">
        <tr><th>{{ t('admin.oidcClientId') }}</th><th>{{ t('admin.oidcClientName') }}</th><th>{{ t('admin.oidcClientType') }}</th><th>{{ t('admin.oidcClientGrants') }}</th><th>{{ t('admin.oidcClientRedirects') }}</th><th></th></tr>
        <tr v-for="c in oidcClients" :key="c.client_id">
          <td><code>{{ c.client_id }}</code></td>
          <td>{{ c.name }}</td>
          <td><span class="badge">{{ c.public ? t('admin.oidcPublic') : t('admin.oidcConfidential') }}</span></td>
          <td class="muted" style="font-size: 12px;">{{ (c.grant_types || []).join(', ') }}</td>
          <td class="muted" style="font-size: 12px;">{{ (c.redirect_uris || []).join(', ') || '—' }}</td>
          <td><button class="secondary" @click="deleteClient(c.client_id)">{{ t('admin.oidcDelete') }}</button></td>
        </tr>
      </table>
    </div>
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
