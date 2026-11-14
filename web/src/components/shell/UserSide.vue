<script setup>
// UserSide.vue — the signed-in sidebar: grouped nav, badge counts, and the
// user chip with logout. It renders the nav model the shell builds (navGroups)
// and asks the shell to move; it never mutates the view itself. On narrow
// screens the groups collapse behind a menu toggle (same pattern as TopBar).
import { computed, ref, inject } from 'vue'
import { useI18n } from 'vue-i18n'

const props = defineProps({
  view: { type: String, required: true },
  extPath: { type: String, default: '' },
  navGroups: { type: Array, default: () => [] },
  user: { type: Object, default: null },
})
const emit = defineEmits(['open-ext', 'logout'])

const { t } = useI18n()
const goto = inject('gotoView', null)
const menuOpen = ref(false)

const currentLabel = computed(() => {
  for (const g of props.navGroups) {
    for (const n of g.items) {
      const hit = n.path ? (props.view === 'ext' && props.extPath === n.path) : props.view === n.id
      if (hit) return n.label || t(n.labelKey)
    }
  }
  return t('nav.menu')
})
</script>

<template>
  <aside class="card user-side" :class="{ open: menuOpen }">
    <button class="secondary nav-toggle side-toggle" :aria-expanded="menuOpen ? 'true' : 'false'"
            :aria-label="t('nav.menu')" @click="menuOpen = !menuOpen">
      <svg viewBox="0 0 24 24" width="16" height="16" fill="none" stroke="currentColor"
           stroke-width="1.7" stroke-linecap="round" aria-hidden="true">
        <path d="M4 7h16M4 12h16M4 17h16"/>
      </svg>
      <span>{{ currentLabel }}</span>
    </button>
    <nav v-for="(g, gi) in navGroups" :key="gi" class="nav-group" @click="menuOpen = false">
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
