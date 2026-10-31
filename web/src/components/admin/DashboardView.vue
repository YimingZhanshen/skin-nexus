<script setup>
// DashboardView: stat cards (blessing 管理面板 仪表盘) + risk badges.
// Cards link to their management views via the @go event the shell provides.
import { ref, onMounted } from 'vue'
import { useI18n } from 'vue-i18n'
import { fetchJSON } from '../../adminApi.js'

const { t } = useI18n()
const emit = defineEmits(['go'])

const overview = ref(null)
const error = ref('')

function fmtBytes(n) {
  if (n === null || n === undefined) return '—'
  const units = ['B', 'KB', 'MB', 'GB', 'TB']
  let i = 0
  let v = Number(n)
  while (v >= 1024 && i < units.length - 1) { v /= 1024; i++ }
  return `${v >= 10 || i === 0 ? Math.round(v) : v.toFixed(1)} ${units[i]}`
}

onMounted(async () => {
  error.value = ''
  try {
    overview.value = await fetchJSON('/api/admin/v1/overview')
  } catch (e) { error.value = e.message }
})
</script>

<template>
  <div>
    <div class="card">
      <h2 style="margin-top: 0;">{{ t('admin.overviewTitle') }}</h2>
      <div class="stat-grid" v-if="overview">
        <button class="stat-card" @click="emit('go', 'users')">
          <span class="stat-num">{{ overview.users }}</span>
          <span class="stat-label">{{ t('admin.users') }}</span>
        </button>
        <button class="stat-card" @click="emit('go', 'players')">
          <span class="stat-num">{{ overview.players }}</span>
          <span class="stat-label">{{ t('admin.players') }}</span>
        </button>
        <!-- 材质无独立管理视图，展示型卡（不误导可点跳转） -->
        <div class="stat-card">
          <span class="stat-num">{{ overview.textures }}</span>
          <span class="stat-label">{{ t('admin.textures') }}</span>
        </div>
        <div class="stat-card">
          <span class="stat-num">{{ fmtBytes(overview.storageBytes) }}</span>
          <span class="stat-label">{{ t('admin.storage') }}</span>
        </div>
      </div>
      <div class="row" style="margin-top: 12px;" v-if="overview">
        <span class="badge" :class="{ danger: overview.weakHashUsers > 0 }">
          {{ t('admin.weakHash') }} {{ overview.weakHashUsers }}
        </span>
        <span class="badge">{{ t('admin.signKeyFingerprint') }} {{ overview.signKeyFingerprint }}</span>
      </div>
      <div class="error" v-if="error">{{ error }}</div>
    </div>
  </div>
</template>

<style scoped>
.stat-grid {
  display: grid; grid-template-columns: repeat(auto-fit, minmax(140px, 1fr));
  gap: 12px;
}
.stat-card {
  display: flex; flex-direction: column; gap: 4px; padding: 16px;
  border: 1px solid var(--border); border-radius: var(--r-l);
  background: none; cursor: pointer; text-align: left;
  color: var(--fg, inherit);
}
.stat-card:hover { border-color: var(--accent); }
.stat-num { font-size: 24px; font-weight: 700; }
.stat-label { font-size: var(--fs-xs); color: var(--muted); }
</style>
