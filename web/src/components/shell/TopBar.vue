<script setup>
// TopBar.vue — brand, language switch, session actions, hamburger.
//
// Read-only state arrives as props (the shell owns the view and the drawer);
// the two things this bar does to the shell — go somewhere, log out — are
// emitted, and navigation itself goes through the frozen gotoView key.
import { inject } from 'vue'
import { useI18n } from 'vue-i18n'
import { setLocale } from '../../i18n.js'
import PluginSlot from '../plugin/PluginSlot.vue'

defineProps({
  view: { type: String, required: true },
  menuOpen: { type: Boolean, default: false },
  siteName: { type: String, default: '' },
  brandSvg: { type: String, default: '' },
})
const emit = defineEmits(['toggle-menu', 'logout'])

const { t, locale } = useI18n()
const user = inject('sessionUser', null)
const goto = inject('gotoView', null)

function onLocaleChange(e) {
  setLocale(e.target.value)
}
</script>

<template>
  <header class="topbar" :class="{ wide: view === 'admin' }">
    <div class="topbar-inner">
      <a class="brand" href="/" @click.prevent="goto(user ? 'dashboard' : 'login')" :title="t('site.tagline')">
        <span class="brand-n" v-html="brandSvg"></span>
        <span class="brand-name">{{ siteName }}</span>
      </a>
      <div class="topbar-right" :class="{ open: menuOpen }">
        <select :value="locale" class="locale-sel" @change="onLocaleChange">
          <option value="zh-CN">{{ t('common.langZh') }}</option>
          <option value="en">English</option>
        </select>
        <button v-if="view === 'admin'" class="secondary" @click="goto('dashboard')">← {{ t('nav.backToDashboard') }}</button>
        <template v-if="user">
          <span class="badge">{{ user.nickname }}</span>
          <button class="secondary" @click="emit('logout')">{{ t('common.logout') }}</button>
        </template>
        <template v-else>
          <button class="secondary" @click="goto('skinlib')">{{ t('nav.skinlib') }}</button>
          <button class="secondary" @click="goto('login')">{{ t('common.login') }}</button>
          <button @click="goto('register')">{{ t('common.register') }}</button>
        </template>
        <PluginSlot slot="app.topbar.after" />
      </div>
      <button class="secondary nav-toggle" :aria-expanded="menuOpen ? 'true' : 'false'"
              :aria-label="menuOpen ? t('common.close') : t('nav.menu')" @click="emit('toggle-menu')">
        <svg v-if="!menuOpen" class="nav-ico" viewBox="0 0 24 24" fill="none" stroke="currentColor"
             stroke-width="1.7" stroke-linecap="round" aria-hidden="true"><path d="M4 6h16M4 12h16M4 18h16" /></svg>
        <svg v-else class="nav-ico" viewBox="0 0 24 24" fill="none" stroke="currentColor"
             stroke-width="1.7" stroke-linecap="round" aria-hidden="true"><path d="M5 5l14 14M19 5L5 19" /></svg>
      </button>
    </div>
  </header>
</template>
