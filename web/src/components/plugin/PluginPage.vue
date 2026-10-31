<script setup>
// Plugin page rendered from the manifest UI declaration. P1 supports iframe
// pages (zero-build, sandboxed). Schema pages stay available for host-rendered
// forms later; a schema page shows a clear unsupported notice instead of
// silently rendering nothing.
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import { pageByPath, pluginLabel } from '../../pluginUI.js'
import PluginFrame from './PluginFrame.vue'

const props = defineProps({ path: { type: String, required: true } })
const { t } = useI18n()
const page = computed(() => pageByPath(props.path))
const title = computed(() => {
  const p = page.value
  if (!p) return ''
  if (p.nav && p.nav.label) return pluginLabel(p.plugin, 'nav.' + p.id, p.nav.label)
  return p.plugin
})
</script>

<template>
  <div v-if="!page" class="card">
    <h2>{{ t('pluginUi.notFound') }}</h2>
  </div>
  <div v-else class="card">
    <h2 style="margin-top: 0;">{{ title }}</h2>
    <PluginFrame v-if="page.render.type === 'iframe'" :plugin="page.plugin" :src="page.render.src" :title="title" :height="420" />
    <p v-else class="muted">{{ t('pluginUi.schemaUnsupported') }}</p>
  </div>
</template>
