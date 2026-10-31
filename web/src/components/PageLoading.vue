<script setup>
import { useI18n } from 'vue-i18n'
const { t } = useI18n()
</script>

<template>
  <div class="card page-loading" role="status" aria-live="polite">
    <span class="spinner" aria-hidden="true"></span>
    <span class="muted">{{ t('common.loading') }}</span>
  </div>
</template>

<style scoped>
.page-loading {
  display: flex;
  align-items: center;
  justify-content: center;
  gap: 10px;
  min-height: 160px;
}
.page-loading .spinner { width: 18px; height: 18px; }
</style>
