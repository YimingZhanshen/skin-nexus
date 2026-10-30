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
.spinner {
  width: 18px;
  height: 18px;
  border-radius: 50%;
  border: 2px solid var(--border, rgba(169, 138, 95, .3));
  border-top-color: var(--accent, #b0453a);
  animation: page-spin .8s linear infinite;
}
@keyframes page-spin { to { transform: rotate(360deg); } }
@media (prefers-reduced-motion: reduce) {
  .spinner { animation-duration: 2.4s; }
}
</style>
