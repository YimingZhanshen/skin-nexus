<script setup>
// DashboardPage (A 级批次): 仪表盘 —— 公告（Markdown 双层消毒之客户端层：
// markdown-it 渲染 + DOMPurify 消毒后才 v-html；服务端已 bluemonday 一层）、
// 使用情况（角色 n/limit + 存储字节）、启动器快速配置（API 地址复制/
// 可拖拽链接/HMCL PCL 引导）、上传、纹理列表。
import { ref, computed, onMounted, onBeforeUnmount, inject } from 'vue'
import { useI18n } from 'vue-i18n'
import MarkdownIt from 'markdown-it'
import PluginSlot from '../components/plugin/PluginSlot.vue'
import PluginFields from '../components/plugin/PluginFields.vue'
import DOMPurify from 'dompurify'
import SkinPreview from '../components/SkinPreview.vue'
import TexturesCard from './TexturesCard.vue'
import { api, uploadTexture } from '../api.js'
import { detectSkinModel } from '../skinModel.js'
import { nameFromFile } from '../textureLabel.js'
import { formatBytes } from '../format.js'
import { guard401 } from '../session.js'

const { t } = useI18n()
const texCard = ref(null) // TexturesCard exposes reload(newId)

const me = ref(null)
const announcement = ref('')

const uploadType = ref('skin')
// Read from the picked pixels, and only for the preview: the stored model is
// decided by the server on upload (server/internal/texture/model.go).
const previewModel = ref('default')
const modelChecked = ref(false)
const uploadName = ref('')
const uploadDetail = ref('')
const uploadSource = ref('original')
const uploadSourceUrl = ref('')
// The box is labelled "set as private", so the state carries that meaning:
// binding the label to the public flag made the default tick read as "private"
// while it actually meant "public", and un-ticking it made uploads private.
const uploadPrivate = ref(false)
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
  modelChecked.value = false
  previewModel.value = 'default'
}
// cape has no body render — show the flat texture instead of the 3D canvas
const previewIsCape = computed(() => uploadType.value === 'cape')
const sourceNeedsURL = computed(() => uploadSource.value === 'derivative' || uploadSource.value === 'repost')

// The server states the size its upload check will actually apply
// (`max_upload_kb` from /site-config). Refusing here rather than there is the
// difference between "your file is too large" and a metered connection spent on
// bytes that were never going to land.
const siteConf = inject('siteConfig', null)
const maxUploadBytes = computed(() => {
  const kb = Number(siteConf && siteConf.value ? siteConf.value.max_upload_kb : 0)
  return kb > 0 ? kb * 1024 : 0
})
const uploadLimitText = computed(() => (maxUploadBytes.value ? formatBytes(maxUploadBytes.value) : ''))
function oversize(f) {
  return maxUploadBytes.value > 0 && f.size > maxUploadBytes.value
}

function onDrop(e) {
  dragging.value = false
  const f = e.dataTransfer.files && e.dataTransfer.files[0]
  if (!f) return
  if (oversize(f)) {
    uploadErr.value = t('dashboard.fileTooLarge', { size: uploadLimitText.value })
    return
  }
  if (fileInput.value) {
    const dt = new DataTransfer()
    dt.items.add(f)
    fileInput.value.files = dt.files
  }
  uploadFile.value = f
  setPreviewFile(f)
  if (!uploadName.value.trim()) uploadName.value = nameFromFile(f.name)
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
  // The texture rows live in TexturesCard now (it owns paging, search and the
  // row sequence guard); this half is the account the usage card renders.
  try { me.value = await api.me() } catch (e) { if (!guard401(e)) pageErr.value = e.message }
}

async function loadAnnouncement() {  try {
    const d = await fetch('/api/v1/announcement').then((r) => r.json())
    announcement.value = d.announcement || ''
  } catch { announcement.value = '' }
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
      name, uploadPluginData.value,
      {
        detail: uploadDetail.value, source: uploadSource.value,
        sourceUrl: uploadSourceUrl.value.trim(), public: !uploadPrivate.value,
        onProgress: (p) => { uploadProgress.value = p },
      },
    )
    uploadOk.value = d.created === false
      ? t('dashboard.uploadAlreadyOwned')
      : (uploadPrivate.value ? t('dashboard.uploadPrivateSuccess') : t('dashboard.uploadSuccess'))
    uploadName.value = ''
    uploadDetail.value = ''
    uploadSource.value = 'original'
    uploadSourceUrl.value = ''
    uploadPrivate.value = false
    detailTab.value = 'edit'
    clearPreview()
    // 列表按 id 倒序，新材质只在第 1 页：以前传完停在原页码，玩家看不见刚传的，
    // 于是重复上传。回到第 1 页并描边那一行——分页与描边都归纹理卡管，
    // reload 内部会把页码拨回 1。
    await texCard.value?.reload(d.id || 0)
  } catch (e) {
    if (e.data && e.data.textureId) {
      uploadDup.value = e.data
    } else if (!guard401(e)) {
      uploadErr.value = e.message
    }
  } finally { uploading.value = false; uploadProgress.value = 0 }
}

function pickFile() {
  if (fileInput.value) fileInput.value.click()
}

function onPick(e) {
  const f = e.target.files[0] || null
  if (f && oversize(f)) {
    uploadErr.value = t('dashboard.fileTooLarge', { size: uploadLimitText.value })
    if (fileInput.value) fileInput.value.value = ''
    return
  }
  uploadFile.value = f
  setPreviewFile(f)
  modelChecked.value = false
  if (f && !uploadName.value.trim()) uploadName.value = nameFromFile(f.name)
  if (f) {
    detectSkinModel(f).then((m) => {
      // The file may have been swapped or cleared while decoding.
      if (uploadFile.value !== f) return
      previewModel.value = m
      modelChecked.value = true
    })
  }
}

onMounted(() => {
  load()
  loadAnnouncement()
  // 皮肤库顶部的「上传我的材质」用 #upload 深链到这里：滚过去并把焦点放在
  // 选文件按钮上，玩家不用自己找第几张卡是上传。
  if (location.hash === '#upload') {
    const card = document.getElementById('upload')
    if (card) {
      card.scrollIntoView({ block: 'start', behavior: 'smooth' })
      const btn = card.querySelector('button')
      if (btn) btn.focus()
    }
  }
})
onBeforeUnmount(() => { if (previewURL.value) URL.revokeObjectURL(previewURL.value) })
</script>

<template>
  <!-- 页面级数据错误（与上传卡错误位分离） -->
  <div class="error" role="alert" v-if="pageErr">{{ pageErr }}</div>
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
    <p class="muted" style="margin-top: 8px; font-size: var(--fs-xs);">{{ t('dashboard.launcherTutor') }}</p>
  </div>

  <!-- upload: 左控件列 + 右即时预览（选中文件即渲染 3D / 平面图） -->
  <div class="card" id="upload">
    <h2>{{ t('dashboard.uploadTitle') }}</h2>
    <div class="up-grid">
      <div class="up-controls">
        <!-- 文件区合一：拖放 + 区内选择按钮（键盘可达；stop 防区点击双发） -->
        <div class="drop-zone" :class="{ over: dragging }"
             @dragover.prevent="dragging = true" @dragleave.prevent="dragging = false"
             @drop.prevent="onDrop" @click="pickFile">
          <input type="file" accept="image/png" ref="fileInput" @change="onPick" class="file-hidden" />
          <span v-if="uploadFile" class="file-name" :title="uploadFile.name">{{ uploadFile.name }}</span>
          <span v-else class="muted">{{ t('dashboard.dropHint') }}</span>
          <button type="button" class="secondary pick-file" @click.stop="pickFile">{{ t('dashboard.chooseFile') }}</button>
          <button v-if="uploadFile" class="secondary file-clear" :aria-label="t('dashboard.clearFile')" @click.stop="clearPreview">×</button>
        </div>
        <span class="field-hint">{{ t('dashboard.uploadHint') }}{{ uploadLimitText ? t('dashboard.uploadLimit', { size: uploadLimitText }) : '' }}</span>
        <div class="row">
          <label class="grow">{{ t('dashboard.materialName') }}
            <input v-model="uploadName" maxlength="50" :placeholder="t('dashboard.materialNameHint')" />
          </label>
          <!-- 类型二选一收成分段按钮：无标签浮空下拉在带标签的名称框旁太突兀 -->
          <div class="type-field">
            <span class="type-label">{{ t('dashboard.colType') }}</span>
            <div class="seg" role="group" :aria-label="t('dashboard.colType')">
              <label class="seg-item" :class="{ active: uploadType === 'skin' }">
                <input type="radio" name="texture-type" value="skin" v-model="uploadType" />{{ t('dashboard.typeSkin') }}
              </label>
              <label class="seg-item" :class="{ active: uploadType === 'cape' }">
                <input type="radio" name="texture-type" value="cape" v-model="uploadType" />{{ t('dashboard.typeCape') }}
              </label>
            </div>
          </div>
        </div>
        <p v-if="uploadType === 'skin'" class="muted model-hint">
          <span v-if="uploadType === 'skin' && uploadFile" class="muted model-badge">
            {{ modelChecked
              ? t('dashboard.modelDetected', { model: previewModel === 'slim' ? t('dashboard.modelSlim') : t('dashboard.modelDefault') })
              : t('dashboard.modelChecking') }}
          </span>
          {{ t('dashboard.modelAutoHint') }}
        </p>
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
        <div class="detail-editor">
          <div class="tabs detail-tabs">
            <button type="button" class="tab" :class="{ active: detailTab === 'edit' }" @click="detailTab = 'edit'">{{ t('dashboard.detailEdit') }}</button>
            <button type="button" class="tab" :class="{ active: detailTab === 'preview' }" @click="detailTab = 'preview'">{{ t('dashboard.detailPreview') }}</button>
            <span class="muted char-count">{{ [...uploadDetail].length }} / 4000</span>
          </div>
          <textarea v-if="detailTab === 'edit'" v-model="uploadDetail" rows="6" maxlength="4000"
                    :placeholder="t('dashboard.detailHint')"></textarea>
          <div v-else class="md md-wide detail-preview">
            <div v-if="detailHtml" v-html="detailHtml"></div>
            <span v-else class="muted">{{ t('dashboard.detailEmpty') }}</span>
          </div>
        </div>
        <!-- 动作行：勾选居左、主按钮居右，进度紧随 -->
        <div class="row action-row">
          <label class="check-line"><input type="checkbox" v-model="uploadPrivate" /> {{ t('dashboard.privateUpload') }}</label>
          <button class="upload-btn" :disabled="uploading || !uploadFile" @click="doUpload">
            {{ uploading ? t('dashboard.uploading') : t('dashboard.upload') }}
          </button>
        </div>
        <div class="upload-progress" v-if="uploading">
          <div class="bar"><i :style="{ width: uploadProgress + '%' }"></i></div>
          <span class="muted">{{ uploadProgress }}%</span>
        </div>
        <div class="error" role="alert" v-if="uploadErr">{{ uploadErr }}</div>
        <div class="error" role="alert" v-if="uploadDup">
          {{ t('dashboard.uploadDuplicate') }}
          <a :href="uploadDup.url">{{ t('dashboard.uploadExisting') }}</a>
        </div>
        <div class="success" role="status" v-if="uploadOk">{{ uploadOk }}</div>
        <PluginFields form="texture.upload" v-model="uploadPluginData" />
      </div>
      <div class="prev-box">
        <template v-if="previewURL">
          <img v-if="previewIsCape || glLost" class="cape-prev" :src="previewURL" :alt="t('dashboard.colPreview')" />
          <SkinPreview v-else :url="previewURL" :model-type="previewModel"
                       @gl-unavailable="glLost = true" />
        </template>
        <span v-else class="muted">{{ t('dashboard.previewEmpty') }}</span>
      </div>
    </div>
    <PluginSlot slot="texture.upload.after" />
  </div>

  <TexturesCard ref="texCard" :page-err="pageErr"
                @error="pageErr = $event" @choose-file="pickFile" />
  <PluginSlot slot="dashboard.cards" />
</template>

<style scoped>
/* 公告 markdown 排版 v3「月下仙山」：宋体大标题、8px 节奏、青瓷链接、
   赭石引用线；内容里的 emoji 属公告作者表达，不剥离。 */
.md { margin-top: 8px; line-height: 1.75; font-size: var(--fs-m); }
/* Theme packs cap .md paragraphs at 34em for reading measure; the dashboard
   announcement/description cards should use the full card width. */
.md.md.md-wide :deep(p), .md.md.md-wide :deep(li) { max-width: none; }
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
.field-label { font-size: var(--fs-s); color: var(--muted); margin-bottom: 8px; }
.radio-row { display: flex; flex-wrap: wrap; gap: 16px; }
.radio-row label, .check-line { display: flex; gap: 8px; align-items: center; font-size: var(--fs-s); }
.source-hint { display: block; margin-top: 4px; font-size: var(--fs-xs); }
.model-badge { font-size: var(--fs-xs); }
.model-hint { margin: 0; font-size: var(--fs-xs); }
.model-fix { width: auto; font-size: var(--fs-xs); padding: 2px 8px; }
/* 类型分段按钮：与输入框同高（8px 内距 + 1px 边）；原生 radio 视觉隐藏但保焦点 */
.type-field { flex: none; margin: 12px 0 4px; }
.type-label { display: block; margin: 0; font-size: var(--fs-s); color: var(--muted); }
.seg { display: flex; gap: 4px; }
.seg-item {
  display: inline-flex; align-items: center; margin: 0;
  padding: 8px 16px; font-size: var(--fs-s); color: var(--muted); line-height: 16px;
  border: 1px solid var(--line-strong); border-radius: var(--r-s);
  cursor: pointer; white-space: nowrap;
  transition: color var(--t-fast) var(--ease), border-color var(--t-fast) var(--ease), background var(--t-fast) var(--ease);
}
.seg-item input { position: absolute; width: 0; height: 0; opacity: 0; }
.seg-item.active { color: var(--accent); border-color: var(--accent); background: var(--ink-3); }
.seg-item:focus-within { outline: 2px solid var(--accent); outline-offset: 2px; }
.action-row .upload-btn { margin-left: auto; }
.detail-tabs { width: 100%; }
.detail-tabs .char-count { margin-left: auto; font-size: var(--fs-xs); }
.detail-editor textarea { width: 100%; resize: vertical; }
.detail-preview { min-height: 120px; border: 1px solid var(--border); border-radius: var(--r-m);
  padding: 12px 12px; background: var(--bg); }
.upload-progress { display: flex; align-items: center; gap: 12px; }
.upload-progress .bar { flex: 1; height: 8px; background: var(--panel-2); border-radius: 3px; overflow: hidden; }
.upload-progress .bar i { display: block; height: 100%; background: var(--accent); transition: width var(--t-fast) var(--ease); }
.drop-zone {
  display: flex; align-items: center; gap: 8px;
  border: 1px dashed var(--line-strong); border-radius: var(--r-m);
  background: var(--bg); padding: 12px; cursor: pointer; min-height: 48px;
  transition: border-color var(--t-fast) var(--ease), background var(--t-fast) var(--ease);
}
.drop-zone:hover, .drop-zone.over { border-color: var(--gold); background: var(--ink-3); }
/* 刚上传那一行的标记：不是动画，避免 reduced-motion 下玩家看不到反馈 */
.row-new { outline: 2px solid var(--jade); outline-offset: -2px; border-radius: 4px; }
.file-hidden { display: none; }
.drop-zone > .muted { flex: 1; }
.file-name { flex: 1 1 auto; min-width: 0; font-size: var(--fs-xs); overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
label.grow { flex: 1; min-width: 0; }
.pick-file { min-height: 28px; padding: 4px 8px; font-size: var(--fs-xs); flex: none; }
.tx-name { max-width: 260px; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; font-weight: 500; }
.file-clear { min-height: 28px; padding: 0 8px; font-size: var(--fs-xs); flex: none; }
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
