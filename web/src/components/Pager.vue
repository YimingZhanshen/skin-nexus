<script setup>
// SkinNexus · Copyright (C) 2026 SkinNexus contributors · AGPL-3.0
// Pager — the one pagination surface for every list (v3.3 component layer).
import { useI18n } from 'vue-i18n'

const props = defineProps({
  page: { type: Number, required: true },
  pages: { type: Number, required: true },
  total: { type: Number, default: 0 },
  align: { type: String, default: 'center' },
})
const emit = defineEmits(['update:page'])
const { t } = useI18n()
</script>

<template>
  <div class="pager" :class="{ end: align === 'end' }" v-if="pages > 1">
    <button class="secondary small" :disabled="page <= 1" :aria-label="t('common.prevPage')"
            @click="emit('update:page', page - 1)">‹</button>
    <span class="muted">{{ page }} / {{ pages }}
      <template v-if="total"> · {{ t('common.total', { n: total }) }}</template>
    </span>
    <button class="secondary small" :disabled="page >= pages" :aria-label="t('common.nextPage')"
            @click="emit('update:page', page + 1)">›</button>
  </div>
</template>
