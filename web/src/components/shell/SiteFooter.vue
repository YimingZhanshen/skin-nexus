<script setup>
// SiteFooter.vue — the fixed attribution footer and the operator's own links.
// The footer content comes from site-config (text, links, site name, year),
// so it is a prop: the shell owns the fetch, this only renders it.
// Logo colours are brand assets and are never rethemed here.
import { useI18n } from 'vue-i18n'
import PluginSlot from '../plugin/PluginSlot.vue'

defineProps({
  siteFooter: { type: Object, required: true },
})
const { t } = useI18n()
</script>

<template>
  <PluginSlot slot="footer.before" />
  <!-- SkinNexus · Copyright (C) 2026 SkinNexus contributors · AGPL-3.0 -->
  <footer class="site-footer">
    <nav v-if="siteFooter.links.length" class="site-footer-links" :aria-label="t('common.footerLinks')">
      <a v-for="(l, i) in siteFooter.links" :key="i" :href="l.url" target="_blank" rel="noopener noreferrer">{{ l.label }}</a>
    </nav>
    <p v-if="siteFooter.text" class="site-footer-text">{{ siteFooter.text }}</p>
    <p class="site-footer-credit">
      Powered by <a href="https://github.com/YimingZhanshen/skin-nexus" target="_blank" rel="noopener noreferrer">SkinNexus</a><template v-if="siteFooter.siteName"> · © {{ siteFooter.year }} {{ siteFooter.siteName }}</template>
    </p>
  </footer>
</template>
