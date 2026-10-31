<script setup>
// SkinNexus · Copyright (C) 2026 SkinNexus contributors · AGPL-3.0
// EmptyState — one quiet empty surface for every list (v3.3 layer).
// Optional default slot carries the action button(s).
defineProps({
  text: { type: String, required: true },
  hint: { type: String, default: '' },
})
</script>

<template>
  <div class="empty-state">
    <svg class="empty-icon" viewBox="0 0 24 24" fill="none" stroke="currentColor"
         stroke-width="1.5" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">
      <path d="M4 4h7v7H4zM13 4h7v7h-7zM4 13h7v7H4zM13 13h7v7h-7z" />
    </svg>
    <p>{{ text }}</p>
    <p v-if="hint">{{ hint }}</p>
    <slot />
  </div>
</template>
