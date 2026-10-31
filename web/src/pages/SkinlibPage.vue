<script setup>
// Skinlib — public texture library (blessing skinlib.index parity).
// Like heart = closet add/remove (legacy semantics). Owner sees rename /
// visibility toggle on their own entries.
import { ref, computed, onMounted } from 'vue'
import { useI18n } from 'vue-i18n'
import { api } from '../api.js'
import { guard401 } from '../session.js'
import PluginSlot from '../components/plugin/PluginSlot.vue'
import Pager from '../components/Pager.vue'
import EmptyState from '../components/EmptyState.vue'

const { t } = useI18n()
const emit = defineEmits(['login'])
// App.vue already resolved /me during boot; re-fetching here cost every
// visitor an extra RTT on high-latency links.
const props = defineProps({ user: { type: Object, default: null } })

const items = ref([])
const total = ref(0)
const page = ref(1)
const perPage = 20
const filter = ref('skin')
const sort = ref('time')
const keyword = ref('')
const mine = ref(false)
const loading = ref(false)
const err = ref('')
const ok = ref('')
const me = computed(() => props.user)

const totalPages = computed(() => Math.max(1, Math.ceil(total.value / perPage)))
const canEdit = (it) => me.value && (me.value.id === it.uploader || me.value.permission > 0)
const sourceBadge = (source) => ({
  original: t('dashboard.sourceOriginalBadge'),
  derivative: t('dashboard.sourceDerivativeBadge'),
  repost: t('dashboard.sourceRepostBadge'),
}[source] || '')

let reqSeq = 0
async function load() {
  const seq = ++reqSeq
  loading.value = true
  err.value = ''
  try {
    const data = await api.skinlib({
      filter: filter.value,
      sort: sort.value,
      keyword: keyword.value.trim(),
      page: page.value,
      uploader: mine.value && me.value ? me.value.id : '',
    })
    if (seq !== reqSeq) return // a newer tab/sort/page request won
    items.value = data.items || []
    total.value = data.total || 0
  } catch (e) {
    if (seq !== reqSeq) return
    if (!guard401(e)) err.value = e.message
  } finally {
    if (seq === reqSeq) loading.value = false
  }
}

let searchTimer = null
function onSearch() {
  clearTimeout(searchTimer)
  searchTimer = setTimeout(() => { page.value = 1; load() }, 300)
}

function setFilter(f) { filter.value = f; page.value = 1; load() }
function setSort(s) { sort.value = s; page.value = 1; load() }
function toggleMine() { mine.value = !mine.value; page.value = 1; load() }
function goPage(p) {
  if (p < 1 || p > totalPages.value || p === page.value) return
  page.value = p
  load()
}

async function toggleLike(it) {
  // 访客点 ♥ = 明确登录引导（2026-10-27 逻辑修复：原只塞一行红字）
  if (!me.value) { emit('login'); return }
  err.value = ''
  try {
    if (it.liked) {
      const res = await api.skinlibUnlike(it.tid)
      it.liked = false
      it.likes = res.likes
    } else {
      const res = await api.skinlibLike(it.tid)
      it.liked = true
      it.likes = res.likes
    }
  } catch (e) {
    if (!guard401(e)) err.value = e.message
  }
}

async function togglePublic(it) {
  err.value = ''
  ok.value = ''
  try {
    await api.skinlibPatch(it.tid, { public: !it.public })
    it.public = !it.public
    ok.value = t('common.saved')
  } catch (e) {
    if (!guard401(e)) err.value = e.message
  }
}

async function renameItem(it) {
  const next = prompt(t('skinlib.renamePrompt'), it.name || '')
  if (next === null) return
  err.value = ''
  ok.value = ''
  try {
    await api.skinlibPatch(it.tid, { name: next.trim() })
    it.name = next.trim()
    ok.value = t('common.saved')
  } catch (e) {
    if (!guard401(e)) err.value = e.message
  }
}

// Server-rendered preview PNG (avatargen /preview/{tid}: back+front views).
// One <img> per card — a skinview3d canvas per card burns a WebGL context
// each and the grid exceeds the browser's ~16-context budget: oldest
// contexts get dropped (white cards + "Context Lost" + renderer crashes).
// The API hands back a versioned previewURL (?v=rendererVersion) so the
// year-long immutable cache re-fetches after a rasterizer fix.
// Capes have no body render; serve the flat texture instead.
function previewURL(it) {
  return it.type === 'skin' ? (it.previewURL || `/preview/${it.tid}`) : it.textureURL
}

onMounted(() => { load() })
</script>

<template>
  <nav class="crumb" aria-label="breadcrumb">
    <a href="/">{{ t('nav.home') }}</a><span class="sep" aria-hidden="true">›</span><span aria-current="page">{{ t('nav.skinlib') }}</span>
  </nav>
  <div class="card">
    <h2 style="margin-top: 0;">{{ t('skinlib.title') }}</h2>
    <p class="muted">{{ t('skinlib.subtitle') }}</p>

    <div class="row" style="flex-wrap: wrap; gap: 8px; align-items: center;">
      <div class="tabs">
        <button class="tab" :class="{ active: filter === 'skin' }" @click="setFilter('skin')">{{ t('skinlib.tabSkin') }}</button>
        <button class="tab" :class="{ active: filter === 'cape' }" @click="setFilter('cape')">{{ t('skinlib.tabCape') }}</button>
        <button class="tab" :class="{ active: filter === 'steve' }" @click="setFilter('steve')">{{ t('skinlib.tabSteve') }}</button>
        <button class="tab" :class="{ active: filter === 'alex' }" @click="setFilter('alex')">{{ t('skinlib.tabAlex') }}</button>
      </div>
      <input v-model="keyword" :placeholder="t('skinlib.searchPlaceholder')" style="max-width: 220px;" @input="onSearch" />
      <select :value="sort" style="max-width: 140px;" @change="setSort($event.target.value)">
        <option value="time">{{ t('skinlib.sortTime') }}</option>
        <option value="likes">{{ t('skinlib.sortLikes') }}</option>
      </select>
      <button v-if="me" class="secondary" :class="{ 'mine-on': mine }" @click="toggleMine">{{ t('skinlib.mineOnly') }}</button>
    </div>
    <PluginSlot slot="skinlib.list.toolbar" />

    <div class="error" v-if="err">{{ err }}</div>
    <div class="success" v-if="ok">{{ ok }}</div>

    <div class="skinlib-grid" v-if="loading" aria-hidden="true">
      <div class="skin-card" v-for="n in 8" :key="n">
        <div class="skin-card-prev skeleton"></div>
        <div class="skin-card-body">
          <div class="skeleton skel-line"></div>
          <div class="skeleton skel-line short"></div>
        </div>
      </div>
    </div>
    <EmptyState v-else-if="!items.length" :text="t('skinlib.empty')" />

    <div class="skinlib-grid" v-else>
      <div class="skin-card" v-for="it in items" :key="it.tid">
        <div class="skin-card-prev">
          <a :href="'/skinlib/' + it.tid" :title="it.name || ('#' + it.tid)">
            <img v-if="it.type === 'skin'" :src="previewURL(it)" :alt="it.name" class="skin-preview-img" loading="lazy" decoding="async" />
            <img v-else :src="previewURL(it)" :alt="it.name" class="cape-thumb" loading="lazy" decoding="async" />
          </a>
        </div>
        <div class="skin-card-body">
          <div class="skin-card-name" :title="it.name">
            <span v-if="!it.public" class="lock">密</span>
            <a :href="'/skinlib/' + it.tid" class="skin-card-link">{{ it.name || `#${it.tid}` }}</a>
          </div>
          <div class="skin-card-meta">
            <!-- 披风无模型之分，不渲染 Steve/Alex 徽章（cape 空 model 被误判 Steve） -->
            <span v-if="it.type !== 'cape'" class="badge" :class="it.model === 'slim' ? 'alex' : 'steve'">
              {{ it.model === 'slim' ? 'Alex' : 'Steve' }}
            </span>
            <span v-if="sourceBadge(it.source)" class="badge source-badge" :class="'source-' + it.source">
              {{ sourceBadge(it.source) }}
            </span>
            <span class="uploader">{{ it.nickname }}</span>
          </div>
          <div class="skin-card-actions">
            <button class="like-btn" :class="{ liked: it.liked }" :title="t('skinlib.likeTip')" @click="toggleLike(it)">
              ♥ {{ it.likes }}
            </button>
            <template v-if="canEdit(it)">
              <button class="ghost" @click="renameItem(it)">{{ t('common.rename') }}</button>
              <button class="ghost" @click="togglePublic(it)">
                {{ it.public ? t('skinlib.makePrivate') : t('skinlib.makePublic') }}
              </button>
            </template>
          </div>
        </div>
      </div>
    </div>

    <Pager :page="page" :pages="totalPages" :total="total" @update:page="goPage" />
  </div>
</template>

<style scoped>
.mine-on { border-color: var(--accent) !important; color: var(--accent) !important; }
.source-badge { background: var(--panel-2); color: var(--muted); border: 1px solid var(--border); }
.source-original { color: var(--jade, #7dd3a0); border-color: currentColor; }
.source-derivative { color: var(--gold, #d5a84f); border-color: currentColor; }
.source-repost { color: var(--accent); border-color: currentColor; }

.skinlib-grid {
  display: grid;
  grid-template-columns: repeat(auto-fill, minmax(200px, 1fr));
  gap: 16px; margin-top: 16px;
  /* 材质徽章色（scoped 令牌，供子组件样式引用） */
  --steve-bg: rgba(47, 191, 107, .16); --steve-ink: #7ee2a8;
  --alex-bg: rgba(224, 166, 60, .16); --alex-ink: #ecc288;
}
.skin-card {
  border: 1px solid var(--border); border-radius: var(--r-m); overflow: hidden;
  background: var(--bg); display: flex; flex-direction: column;
}
.skin-card-prev {
  height: 180px; display: flex; align-items: center; justify-content: center;
  background: var(--panel-2);
}
.skin-preview-img { height: 150px; image-rendering: pixelated; object-fit: contain; }
.cape-thumb { max-width: 90%; max-height: 150px; image-rendering: pixelated; }
.skin-card-body { padding: 12px; display: flex; flex-direction: column; gap: 8px; }
.skin-card-name {
  font-weight: 600; font-size: var(--fs-s); white-space: nowrap;
  overflow: hidden; text-overflow: ellipsis;
}
.skin-card-link { color: inherit; text-decoration: none; }
.skin-card-link:hover { color: var(--accent); }
.skin-card-prev a { display: flex; align-items: center; justify-content: center; height: 100%; }
/* 私密标记：方块小徽章（原 emoji 锁按中文 UI 规范去图标化） */
.lock {
  display: inline-block; margin-right: 4px; padding: 0 4px;
  border: 1px solid var(--gold); border-radius: var(--r-s);
  font-size: var(--fs-xs); font-weight: 600; color: var(--gold); line-height: 1.5;
}
.skin-card-meta { display: flex; align-items: center; gap: 8px; font-size: var(--fs-xs); }
.badge.steve { background: var(--steve-bg); color: var(--steve-ink); border-color: transparent; }
.badge.alex { background: var(--alex-bg); color: var(--alex-ink); border-color: transparent; }
.uploader { color: var(--muted); }
.skin-card-actions { display: flex; gap: 8px; align-items: center; flex-wrap: wrap; }
.like-btn {
  border: 1px solid var(--border); background: transparent; color: var(--muted);
  border-radius: var(--r-s, 4px); padding: 4px 8px; min-height: 32px; cursor: pointer; font-size: var(--fs-xs, 12px); font-weight: 500;
}
.like-btn:hover { color: var(--danger); border-color: var(--danger); background: transparent; box-shadow: none; transform: none; }
.like-btn.liked { color: var(--danger); border-color: var(--danger); }
.skel-line { height: 14px; border-radius: var(--r-s); }
.skel-line.short { width: 60%; }
</style>
