<script setup>
// DashboardPage (A 级批次): 仪表盘 —— 公告（Markdown 双层消毒之客户端层：
// markdown-it 渲染 + DOMPurify 消毒后才 v-html；服务端已 bluemonday 一层）、
// 使用情况（角色 n/limit + 存储字节）、启动器快速配置（API 地址复制/
// 可拖拽链接/HMCL PCL 引导）、上传、纹理列表。
import { ref, computed, onMounted, onBeforeUnmount } from 'vue'
import { useI18n } from 'vue-i18n'
import MarkdownIt from 'markdown-it'
import PluginSlot from '../components/plugin/PluginSlot.vue'
import PluginFields from '../components/plugin/PluginFields.vue'
import DOMPurify from 'dompurify'
import SkinPreview from '../components/SkinPreview.vue'
import { api, uploadTexture } from '../api.js'
import { guard401 } from '../session.js'

const { t } = useI18n()

const me = ref(null)
const textures = ref([])
const closetIds = ref(new Set())
const announcement = ref('')

const uploadType = ref('skin')
const uploadModel = ref('default')
const uploadName = ref('')
const uploadDetail = ref('')
const uploadSource = ref('original')
const uploadSourceUrl = ref('')
const uploadPublic = ref(true)
const detailTab = ref('edit')
const uploadProgress = ref(0)
const uploadFile = ref(null)
const fileInput = ref(null)
const uploading = ref(false)
const pageErr = ref('') // 页面级数据错误（不混进上传卡）
const uploadErr = ref('')
const uploadOk = ref('')
const uploadDup = ref(null)
const uploadPluginData = ref({})
// 选中文件的本地预览（objectURL，随选择换新、卸载时回收）
const previewURL = ref('')
const dragging = ref(false)
const glLost = ref(false)

function setPreviewFile(f) {
  if (previewURL.value) URL.revokeObjectURL(previewURL.value)
  previewURL.value = f ? URL.createObjectURL(f) : ''
  glLost.value = false
}
function clearPreview() {
  if (previewURL.value) URL.revokeObjectURL(previewURL.value)
  previewURL.value = ''
  uploadFile.value = null
  if (fileInput.value) fileInput.value.value = ''
  glLost.value = false
}
// cape has no body render — show the flat texture instead of the 3D canvas
const previewIsCape = computed(() => uploadType.value === 'cape')
const sourceNeedsURL = computed(() => uploadSource.value === 'derivative' || uploadSource.value === 'repost')

function onDrop(e) {
  dragging.value = false
  const f = e.dataTransfer.files && e.dataTransfer.files[0]
  if (!f) return
  if (fileInput.value) {
    const dt = new DataTransfer()
    dt.items.add(f)
    fileInput.value.files = dt.files
  }
  uploadFile.value = f
  setPreviewFile(f)
}

// ---- announcement: client sanitization layer ----
// markdown-it renders the RAW markdown (linkify: bare URLs inside CJK prose
// become real links — 2026-10-26 批注修复；html:false + DOMPurify strips
// anything hostile). The server html field exists for API consumers — the
// SPA renders its own copy so both layers (拍板双层消毒) always run.
const md = new MarkdownIt({ html: false, linkify: true, typographer: false })
const announcementHtml = computed(() => {
  if (!announcement.value) return ''
  const raw = md.render(announcement.value)
  return DOMPurify.sanitize(raw)
})
const detailHtml = computed(() => {
  if (!uploadDetail.value.trim()) return ''
  return DOMPurify.sanitize(md.render(uploadDetail.value))
})

// ---- usage card ----
const usageText = computed(() => {
  if (!me.value) return ''
  const n = me.value.playerCount ?? 0
  const limit = me.value.playerLimit ?? '—'
  const bytes = me.value.storageBytes ?? 0
  return t('dashboard.usageLine', { n, limit, storage: formatBytes(bytes) })
})

function formatBytes(b) {
  if (b < 1024) return `${b} B`
  const units = ['KB', 'MB', 'GB']
  let v = b
  let i = -1
  do { v /= 1024; i++ } while (v >= 1024 && i < units.length - 1)
  return `${v.toFixed(1)} ${units[i]}`
}

// ---- launcher quick config ----
const aliUrl = `${location.origin}/api/yggdrasil`
const copyState = ref('') // '' | 'ok' | 'fail'

async function copyAli() {
  try {
    await navigator.clipboard.writeText(aliUrl)
    copyState.value = 'ok'
  } catch { copyState.value = 'fail' }
  setTimeout(() => { copyState.value = '' }, 2000)
}

// 拖拽快速配置：authlib-injector《启动器技术规范》「通过拖拽设置」规定
// text/plain 载荷 = authlib-injector:yggdrasil-server:{encodeURIComponent(API根)}、效果 copy；
// PCL2 FormMain_Drop 取 DataFormats.Text、前缀匹配后 WebUtility.UrlDecode（FormMain.xaml.cs:934）。
// 注意：裸 <a href> 拖出去的是 text/uri-list 裸 URL，启动器不认——必须走本 handler。
function onDragLauncher(e) {
  e.dataTransfer.setData('text/plain', 'authlib-injector:yggdrasil-server:' + encodeURIComponent(aliUrl))
  e.dataTransfer.dropEffect = 'copy'
}

async function load() {
  try {
    const [u, tx, cl] = await Promise.all([api.me(), api.textures(), api.closet()])
    me.value = u
    textures.value = tx.textures || []
    closetIds.value = new Set((cl.closet || []).map((c) => c.textureId))
  } catch (e) { if (!guard401(e)) pageErr.value = e.message } // 数据加载失败≠上传失败
}

async function loadAnnouncement() {
  try {
    const d = await fetch('/api/v1/announcement').then((r) => r.json())
    announcement.value = d.announcement || ''
  } catch { announcement.value = '' }
}

async function addToCloset(tx) {
  pageErr.value = ''
  try {
    await api.closetAdd(tx.id)
    const cl = await api.closet()
    closetIds.value = new Set((cl.closet || []).map((c) => c.textureId))
  } catch (e) { if (!guard401(e)) pageErr.value = e.message } // 入柜失败显示在纹理卡区（页级横幅）
}

async function doUpload() {
  const name = uploadName.value.trim()
  if (!uploadFile.value) { uploadErr.value = t('dashboard.pickPng'); return }
  if (!name) { uploadErr.value = t('dashboard.nameRequired'); return }
  if ([...name].length > 50) { uploadErr.value = t('dashboard.nameTooLong'); return }
  if ([...uploadDetail.value].length > 4000) { uploadErr.value = t('dashboard.detailTooLong'); return }
  if (sourceNeedsURL.value) {
    const raw = uploadSourceUrl.value.trim()
    let ok = false
    try {
      const u = new URL(raw)
      ok = (u.protocol === 'http:' || u.protocol === 'https:') && !!u.host
    } catch { ok = false }
    if (!ok) { uploadErr.value = t('dashboard.sourceUrlRequired'); return }
  }
  uploadErr.value = ''; uploadOk.value = ''; uploadDup.value = null
  uploading.value = true; uploadProgress.value = 0
  try {
    const d = await uploadTexture(
      uploadType.value, uploadFile.value,
      uploadType.value === 'skin' ? uploadModel.value : '',
      name, uploadPluginData.value,
      {
        detail: uploadDetail.value, source: uploadSource.value,
        sourceUrl: uploadSourceUrl.value.trim(), public: uploadPublic.value,
        onProgress: (p) => { uploadProgress.value = p },
      },
    )
    uploadOk.value = d.created === false
      ? t('dashboard.uploadAlreadyOwned')
      : (uploadPublic.value ? t('dashboard.uploadSuccess') : t('dashboard.uploadPrivateSuccess'))
    uploadName.value = ''
    uploadDetail.value = ''
    uploadSource.value = 'original'
    uploadSourceUrl.value = ''
    uploadPublic.value = true
    detailTab.value = 'edit'
    clearPreview()
    await load()
  } catch (e) {
    if (e.data && e.data.textureId) {
      uploadDup.value = e.data
    } else if (!guard401(e)) {
      uploadErr.value = e.message
    }
  } finally { uploading.value = false; uploadProgress.value = 0 }
}

function onPick(e) {
  const f = e.target.files[0] || null
  uploadFile.value = f
  setPreviewFile(f)
}

onMounted(() => { load(); loadAnnouncement() })
onBeforeUnmount(() => { if (previewURL.value) URL.revokeObjectURL(previewURL.value) })
</script>

<template>
  <!-- 页面级数据错误（与上传卡错误位分离） -->
  <div class="error" v-if="pageErr">{{ pageErr }}</div>
  <!-- announcement (Markdown, double-sanitized) -->
  <div class="card" v-if="announcement">
    <h2>{{ t('dashboard.announcement') }}</h2>
    <div class="md md-wide" v-html="announcementHtml"></div>
  </div>

  <!-- usage + launcher quick config -->
  <div class="card">
    <h2>{{ t('dashboard.usageTitle') }}</h2>
    <div style="margin: 4px 0 12px;">{{ usageText }}</div>
    <h3>{{ t('dashboard.launcherTitle') }}</h3>
    <div class="muted" style="margin: 4px 0 8px;">{{ t('dashboard.launcherHint') }}</div>
    <div class="row">
      <code class="ali">{{ aliUrl }}</code>
      <button class="secondary" @click="copyAli">{{ copyState === 'ok' ? t('dashboard.copied') : copyState === 'fail' ? t('dashboard.copyFail') : t('dashboard.copy') }}</button>
      <!-- draggable label: payload authlib-injector:yggdrasil-server:{url} (launcher spec DnD) -->
      <a class="drag-btn" :href="aliUrl" draggable="true" @dragstart="onDragLauncher">{{ t('dashboard.dragBtn') }}</a>
    </div>
    <p class="muted" style="margin-top: 8px; font-size: 12px;">{{ t('dashboard.launcherTutor') }}</p>
  </div>

  <!-- upload: 左控件列 + 右即时预览（选中文件即渲染 3D / 平面图） -->
  <div class="card">
    <h2>{{ t('dashboard.uploadTitle') }}</h2>
    <div class="up-grid">
      <div class="up-controls">
        <label>{{ t('dashboard.materialName') }}
          <input v-model="uploadName" maxlength="50" :placeholder="t('dashboard.materialNameHint')" />
        </label>
        <div>
          <div class="field-label">{{ t('dashboard.materialSource') }}</div>
          <div class="radio-row">
            <label><input type="radio" value="original" v-model="uploadSource" /> {{ t('dashboard.sourceOriginal') }}</label>
            <label><input type="radio" value="derivative" v-model="uploadSource" /> {{ t('dashboard.sourceDerivative') }}</label>
            <label><input type="radio" value="repost" v-model="uploadSource" /> {{ t('dashboard.sourceRepost') }}</label>
          </div>
        </div>
        <label v-if="sourceNeedsURL">{{ t('dashboard.sourceUrl') }}
          <input v-model="uploadSourceUrl" type="url" placeholder="https://" />
          <span class="muted source-hint">{{ t('dashboard.sourceUrlHint') }}</span>
        </label>
        <div class="row">
          <select v-model="uploadType" style="width: 120px;">
            <option value="skin">{{ t('dashboard.typeSkin') }}</option>
            <option value="cape">{{ t('dashboard.typeCape') }}</option>
          </select>
          <select v-model="uploadModel" style="width: 140px;" :disabled="uploadType !== 'skin'">
            <option value="default">{{ t('dashboard.modelDefault') }}</option>
            <option value="slim">{{ t('dashboard.modelSlim') }}</option>
          </select>
        </div>
        <div class="drop-zone" :class="{ over: dragging }"
             @dragover.prevent="dragging = true" @dragleave.prevent="dragging = false"
             @drop.prevent="onDrop" @click="fileInput && fileInput.click()">
          <input type="file" accept="image/png" ref="fileInput" @change="onPick" class="file-hidden" />
          <template v-if="uploadFile">
            <span class="file-name" :title="uploadFile.name">{{ uploadFile.name }}</span>
            <button class="secondary file-clear" :aria-label="t('dashboard.clearFile')" @click.stop="clearPreview">×</button>
          </template>
          <span v-else class="muted">{{ t('dashboard.dropHint') }}</span>
        </div>
        <div class="detail-editor">
          <div class="tabs detail-tabs">
            <button type="button" :class="{ active: detailTab === 'edit' }" @click="detailTab = 'edit'">{{ t('dashboard.detailEdit') }}</button>
            <button type="button" :class="{ active: detailTab === 'preview' }" @click="detailTab = 'preview'">{{ t('dashboard.detailPreview') }}</button>
            <span class="muted char-count">{{ [...uploadDetail].length }} / 4000</span>
          </div>
          <textarea v-if="detailTab === 'edit'" v-model="uploadDetail" rows="6" maxlength="4000"
                    :placeholder="t('dashboard.detailHint')"></textarea>
          <div v-else class="md md-wide detail-preview">
            <div v-if="detailHtml" v-html="detailHtml"></div>
            <span v-else class="muted">{{ t('dashboard.detailEmpty') }}</span>
          </div>
        </div>
        <label class="check-line"><input type="checkbox" v-model="uploadPublic" /> {{ t('dashboard.privateUpload') }}</label>
        <div class="row">
          <button :disabled="uploading || !uploadFile" @click="doUpload">
            {{ uploading ? t('dashboard.uploading') : t('dashboard.upload') }}
          </button>
        </div>
        <div class="upload-progress" v-if="uploading">
          <div class="bar"><i :style="{ width: uploadProgress + '%' }"></i></div>
          <span class="muted">{{ uploadProgress }}%</span>
        </div>
        <div class="error" v-if="uploadErr">{{ uploadErr }}</div>
        <div class="error" v-if="uploadDup">
          {{ t('dashboard.uploadDuplicate') }}
          <a :href="uploadDup.url">{{ t('dashboard.uploadExisting') }}</a>
        </div>
        <div class="success" v-if="uploadOk">{{ uploadOk }}</div>
        <PluginFields form="texture.upload" v-model="uploadPluginData" />
        <div class="muted">{{ t('dashboard.uploadHint') }}</div>
      </div>
      <div class="prev-box">
        <template v-if="previewURL">
          <img v-if="previewIsCape || glLost" class="cape-prev" :src="previewURL" alt="preview" />
          <SkinPreview v-else :url="previewURL" :model-type="uploadModel"
                       @gl-unavailable="glLost = true" />
        </template>
        <span v-else class="muted">{{ t('dashboard.previewEmpty') }}</span>
      </div>
    </div>
    <PluginSlot slot="texture.upload.after" />
  </div>

  <!-- textures -->
  <div class="card">
    <h2>{{ t('dashboard.texturesTitle', { count: textures.length }) }}</h2>
    <table v-if="textures.length">
      <tr><th>{{ t('dashboard.colType') }}</th><th>{{ t('dashboard.colModel') }}</th><th>{{ t('dashboard.colPreview') }}</th><th>{{ t('dashboard.colHash') }}</th><th></th></tr>
      <tr v-for="tx in textures" :key="tx.id">
        <td>{{ tx.type }}</td>
        <td>{{ tx.model || '—' }}</td>
        <td><img :src="tx.url" style="height: 48px; image-rendering: pixelated;" /></td>
        <td class="muted">{{ tx.hash.slice(0, 12) }}…</td>
        <td>
          <button class="secondary" v-if="!closetIds.has(tx.id)" @click="addToCloset(tx)">{{ t('dashboard.closetAdd') }}</button>
          <span class="muted" v-else style="font-size: 12px;">{{ t('dashboard.closetIn') }}</span>
        </td>
      </tr>
    </table>
    <div v-else class="muted">{{ t('dashboard.texturesEmpty') }}</div>
  </div>
  <PluginSlot slot="dashboard.cards" />
</template>

<style scoped>
/* 公告 markdown 排版 v3「月下仙山」：宋体大标题、8px 节奏、青瓷链接、
   赭石引用线；内容里的 emoji 属公告作者表达，不剥离。 */
.md { margin-top: 8px; line-height: 1.75; font-size: var(--fs-m); }
/* Theme packs cap .md paragraphs at 34em for reading measure; the dashboard
   announcement/description cards should use the full card width. */
.md.md.md-wide p, .md.md.md-wide li { max-width: none; }
.md :deep(h1) { font-family: var(--font-serif); font-size: var(--fs-xl); letter-spacing: .03em;
                margin: 4px 0 8px; padding-bottom: 8px; border-bottom: 1px solid var(--border); }
.md :deep(h2) { font-size: var(--fs-l); margin: 24px 0 8px; }
.md :deep(h3) { font-size: var(--fs-m); margin: 16px 0 4px; }
.md :deep(p) { margin: 8px 0; }
.md :deep(ul), .md :deep(ol) { margin: 8px 0; padding-left: 24px; }
.md :deep(li) { margin: 4px 0; }
.md :deep(a) { color: var(--jade); text-decoration: underline; text-underline-offset: 3px; }
.md :deep(a:hover) { color: var(--jade-hi); }
.md :deep(code) { background: var(--bg); padding: 2px 4px; border-radius: var(--r-s); font-size: var(--fs-xs); }
.md :deep(img) { max-width: 100%; border-radius: var(--r-m); }
.md :deep(blockquote) { margin: 8px 0; padding: 4px 16px; border-left: 3px solid var(--gold);
                color: var(--muted); }
.md :deep(strong) { color: var(--text); }
.md :deep(hr) { border: none; border-top: 1px solid var(--border); margin: 16px 0; }
.md :deep(table) { border-collapse: collapse; font-size: var(--fs-xs); margin: 8px 0; }
.md :deep(th), .md :deep(td) { border: 1px solid var(--border); padding: 8px; }
h3 { font-size: var(--fs-s); margin: 16px 0 4px; color: var(--muted); font-weight: 600; }
.ali { background: var(--bg); border: 1px solid var(--border); padding: 8px 12px; border-radius: var(--r-m); font-size: var(--fs-xs); user-select: all; }
.drag-btn {
  display: inline-block; padding: 8px 16px; border: 1px dashed var(--accent);
  color: var(--accent); border-radius: var(--r-m); text-decoration: none; font-size: var(--fs-xs);
}
/* 上传卡 v3：双栏（控件 | 即时预览）、拖放区、披风平面图 */
.up-grid { display: grid; grid-template-columns: minmax(0, 1fr) 220px; gap: 16px; align-items: start; }
.up-controls { display: flex; flex-direction: column; gap: 12px; }
.field-label { font-size: var(--fs-s); color: var(--muted); margin-bottom: 6px; }
.radio-row { display: flex; flex-wrap: wrap; gap: 14px; }
.radio-row label, .check-line { display: flex; gap: 6px; align-items: center; font-size: var(--fs-s); }
.source-hint { display: block; margin-top: 4px; font-size: var(--fs-xs); }
.detail-tabs { display: flex; align-items: center; gap: 6px; }
.detail-tabs button {
  padding: 6px 12px; border: 1px solid var(--border); background: transparent;
  color: var(--muted); border-radius: var(--r-s); cursor: pointer;
}
.detail-tabs button.active { border-color: var(--accent); color: var(--accent); }
.detail-tabs .char-count { margin-left: auto; font-size: var(--fs-xs); }
.detail-editor textarea { width: 100%; resize: vertical; }
.detail-preview { min-height: 120px; border: 1px solid var(--border); border-radius: var(--r-m);
  padding: 10px 12px; background: var(--bg); }
.upload-progress { display: flex; align-items: center; gap: 10px; }
.upload-progress .bar { flex: 1; height: 6px; background: var(--panel-2); border-radius: 3px; overflow: hidden; }
.upload-progress .bar i { display: block; height: 100%; background: var(--accent); transition: width .15s linear; }
.drop-zone {
  display: flex; align-items: center; gap: 8px;
  border: 1px dashed var(--line-strong); border-radius: var(--r-m);
  background: var(--bg); padding: 12px; cursor: pointer; min-height: 48px;
  transition: border-color .14s var(--ease), background .14s var(--ease);
}
.drop-zone:hover, .drop-zone.over { border-color: var(--gold); background: var(--ink-3); }
.file-hidden { display: none; }
.file-name { font-size: var(--fs-xs); overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
.file-clear { min-height: 24px; padding: 0 8px; font-size: var(--fs-xs); margin-left: auto; flex: none; }
.prev-box {
  display: flex; align-items: center; justify-content: center;
  min-height: 220px; border: 1px solid var(--border); border-radius: var(--r-m);
  background: var(--bg); padding: 8px;
}
.cape-prev { max-width: 100%; max-height: 220px; image-rendering: pixelated; }
@media (max-width: 720px) {
  .up-grid { grid-template-columns: 1fr; }
  .prev-box { min-height: 200px; }
}
</style>
