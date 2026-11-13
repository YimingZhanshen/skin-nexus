<script setup>
// UserSide.vue — the signed-in sidebar: grouped nav, badge counts, and the
// user chip with logout. It renders the nav model the shell builds (navGroups)
// and asks the shell to move; it never mutates the view itself.
import { inject } from 'vue'
import { useI18n } from 'vue-i18n'

defineProps({
  view: { type: String, required: true },
  extPath: { type: String, default: '' },
  navGroups: { type: Array, default: () => [] },
  user: { type: Object, default: null },
})
const emit = defineEmits(['open-ext', 'logout'])

const { t } = useI18n()
const goto = inject('gotoView', null)
</script>

<template>
  <aside class="card user-side">
    <nav v-for="(g, gi) in navGroups" :key="gi" class="nav-group">
      <div class="nav-title" v-if="g.items.length">{{ gi === 0 ? t('nav.userCenter') : t('nav.manageGroup') }}</div>
      <button v-for="n in g.items" :key="n.id" class="nav-item"
              :class="{ active: n.path ? (view === 'ext' && extPath === n.path) : view === n.id }"
              @click="n.path ? emit('open-ext', n.path) : goto(n.id)">
        <svg class="nav-ico" viewBox="0 0 24 24" fill="none" stroke="currentColor"
             stroke-width="1.7" stroke-linecap="round" stroke-linejoin="round" v-html="n.icon"></svg>
        <span>{{ n.label || t(n.labelKey) }}</span>
        <span v-if="n.badge" class="nav-badge">{{ n.badge > 99 ? '99+' : n.badge }}</span>
      </button>
    </nav>
    <!-- 底部用户卡：昵称 + 退出（v3 布局） -->
    <div class="user-chip">
      <span class="who" :title="user && user.nickname">{{ user && user.nickname }}</span>
      <button class="secondary" @click="emit('logout')">{{ t('common.logout') }}</button>
    </div>
  </aside>
</template>
