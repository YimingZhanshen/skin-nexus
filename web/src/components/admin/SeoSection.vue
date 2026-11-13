<script setup>
// SeoSection.vue — the SEO card: metadata, sitemap/noindex switches, robots.
//
// It loads its own data on mount instead of being fed by the parent's load():
// the card is independently retryable (the failure banner lives here) and the
// settings screen has no reason to serialize its fetch behind the knobs.
import { ref, onMounted } from 'vue'
import { useI18n } from 'vue-i18n'
import { fetchJSON } from '../../adminApi.js'

const { t } = useI18n()

const seo = ref(null)
const seoError = ref('')
const seoOk = ref('')
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
  seoOk.value = ''
  try {
    await fetchJSON('/api/admin/v1/seo', {
      method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify(seo.value),
    })
    // Saving used to be silent on success: the operator was left staring at the
    // same fields, unsure whether the click had landed or the page had frozen.
    seoOk.value = t('common.saved')
  } catch (e) {
    // review P2-10: refetch so the UI doesn't keep unsaved values
    await loadSEO()
    seoError.value = e.message
  }
}

onMounted(loadSEO)
</script>

<template>
    <!-- SEO：加载失败不再静默（卡外横幅 + 重试），卡内横幅只管保存失败 -->
    <div class="card" v-if="!seo && seoError">
      <h2>{{ t('admin.seo.title') }}</h2>
      <div class="error" role="alert">{{ seoError }}</div>
      <button class="secondary" style="margin-top: 12px;" @click="loadSEO">{{ t('common.retry') }}</button>
    </div>
    <div class="card" v-if="seo">
      <h2>{{ t('admin.seo.title') }}</h2>
      <div class="row">
        <button class="secondary" @click="seoOneClick">{{ t('admin.seo.oneClick') }}</button>
        <button @click="seoSave">{{ t('admin.seo.save') }}</button>
        <span class="success" role="status" v-if="seoOk">{{ seoOk }}</span>
      </div>
      <label for="seo-title">{{ t('admin.seo.siteTitle') }}</label>
      <input id="seo-title" v-model="seo.title" />
      <p class="muted">
        {{ t('admin.seo.titleLength', { n: (seo.title || '').length }) }}
        {{ t('admin.seo.titleHint') }}
      </p>
      <label for="seo-desc">meta description</label>
      <textarea id="seo-desc" v-model="seo.description" rows="2"></textarea>
      <label for="seo-keywords">{{ t('admin.seo.keywords') }}</label>
      <input id="seo-keywords" v-model="seo.keywords" />
      <label for="seo-og">{{ t('admin.seo.ogImage') }}</label>
      <input id="seo-og" v-model="seo.og_image" />
      <label for="seo-canonical">{{ t('admin.seo.canonical') }}</label>
      <input id="seo-canonical" v-model="seo.canonical" />
      <div class="row" style="margin-top: 8px;">
        <label style="display: flex; align-items: center; gap: 4px;">
          <input type="checkbox" v-model="seo.sitemap" /> {{ t('admin.seo.enableSitemap') }}
        </label>
        <label style="display: flex; align-items: center; gap: 4px;">
          <input type="checkbox" v-model="seo.noindex" /> {{ t('admin.seo.noindex') }}
        </label>
      </div>
      <label style="margin-top: 8px;" for="seo-robots">{{ t('admin.seo.robots') }}</label>
      <textarea id="seo-robots" v-model="seo.robots" rows="3"></textarea>
      <div class="error" role="alert" v-if="seoError">{{ seoError }}</div>
    </div>
</template>
