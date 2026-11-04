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
    error.value = '站点名必填'; return
  }
  // Same rule the API enforces (and the panel's site_url setting uses), so a
  // typo like skin.example.com does not cost a submit round trip.
  if (step.value === 1 && baseURL.value.trim() && !/^https?:\/\//i.test(baseURL.value.trim())) {
    error.value = '站点地址要以 http:// 或 https:// 开头'; return
  }
  if (step.value === 2) {
    if (!nickname.value.trim() || password.value.length < 8) {
      error.value = '昵称必填、密码至少 8 位'; return
    }
    if (password.value !== password2.value) {
      error.value = '两次密码不一致'; return
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
    <h2>{{ t('site.name') }} 安装向导</h2>
    <div class="muted" style="margin-bottom: 12px;">步骤 {{ step }} / 4</div>

    <!-- step 1: site identity -->
    <template v-if="step === 1">
      <label for="s-site-name">站点名称</label>
      <input id="s-site-name" v-model="siteName" placeholder="例如：示例皮肤站" />
      <label for="s-base-url">站点地址（Base URL）</label>
      <input id="s-base-url" v-model="baseURL" placeholder="https://skin.example.com" />
      <template v-if="tokenRequired">
        <label for="s-setup-token">安装令牌</label>
        <input id="s-setup-token" v-model="setupToken" autocomplete="off" spellcheck="false" />
        <div class="muted" style="font-size: var(--fs-xs); margin-top: 6px;">
          站点还没有初始化时这个表单任何人都能提交，所以先要一个只有运维者拿得到的值：
          它写在服务器日志那行 install token 里（docker compose logs 可见），
          也可以在环境变量 SKIN_SETUP_TOKEN 里自己定一个。填错会被拒绝，不会写进任何数据。
        </div>
      </template>
      <div class="muted" style="font-size: var(--fs-xs); margin-top: 6px;">
        默认取当前地址。它会被写进「管理 → 设置 → 站点地址」，材质与预览链接都以它为前缀：
        请用玩家能访问到的域名，不要用局域网 IP 或 http://localhost。
      </div>
    </template>

    <!-- step 2: admin account -->
    <template v-else-if="step === 2">
      <label for="s-nickname">管理员昵称</label>
      <input id="s-nickname" v-model="nickname" />
      <label for="s-email">管理员邮箱（可留空）</label>
      <input id="s-email" v-model="email" type="email" />
      <label for="s-password">管理员密码（≥8 位）</label>
      <input id="s-password" v-model="password" type="password" />
      <label for="s-password2">确认密码</label>
      <input id="s-password2" v-model="password2" type="password" />
    </template>

    <!-- step 3: skin domains -->
    <template v-else-if="step === 3">
      <label for="s-domains">皮肤域清单（每行一个，用于 Yggdrasil 元数据）</label>
      <textarea id="s-domains" v-model="skinDomains" rows="6" placeholder="skin.example.com"></textarea>
      <div class="muted" style="font-size: var(--fs-xs); margin-top: 6px;">
        可留空，之后在环境变量 SKIN_SKIN_DOMAINS 或数据库里补充。
      </div>
    </template>

    <!-- step 4: done -->
    <template v-else>
      <div class="success" role="status">初始化完成！请用刚才创建的管理员账号登录。</div>
      <p class="muted" style="margin-top: 10px;">
        站点地址已写入设置：
        <b>{{ result.site_url || '（未填，沿用环境变量 SKIN_BASE_URL）' }}</b>
      </p>
      <div class="error" role="alert" v-if="result.mismatch">
        它与环境变量 SKIN_BASE_URL（{{ result.env_base_url }}）不一致：站点以你刚填的地址为准，
        材质链接、页面链接与 sitemap 都会用它。如果这是安装时随手用的局域网地址，
        请现在去「管理 → 设置 → 站点地址」改成玩家能访问的域名，改完重启容器。
      </div>
    </template>

    <div class="error" role="alert" v-if="error">{{ error }}</div>

    <div class="row" style="margin-top: 14px;">
      <button v-if="step > 1 && step < 4" class="secondary" @click="step--">上一步</button>
      <button v-if="step < 3" @click="next">下一步</button>
      <button v-if="step === 3" :disabled="loading" @click="finish">{{ loading ? '提交中…' : '完成初始化' }}</button>
      <button v-if="step === 4" @click="goHome">进入站点</button>
    </div>
  </div>
</template>
