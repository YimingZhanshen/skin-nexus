<script setup>
import { ref, shallowRef, watch } from 'vue'
import { useI18n } from 'vue-i18n'

const props = defineProps({
  policy: { type: Object, required: true },
  canWrite: Boolean,
  busy: Boolean,
})
const emit = defineEmits(['save'])
const { t } = useI18n()
const whitelist = shallowRef(false)
const fallback = shallowRef(false)
watch(() => props.policy, (value) => {
  whitelist.value = !!value.whitelistEnabled
  fallback.value = !!value.premiumFallbackEnabled
}, { immediate: true })

function save() {
  if (!props.canWrite || props.busy) return
  emit('save', { policy: { whitelistEnabled: whitelist.value, premiumFallbackEnabled: fallback.value } })
}
</script>

<template>
  <section class="card policy-panel">
    <h2>{{ t('serverAccess.policy') }}</h2>
    <label class="check-row"><input type="checkbox" v-model="whitelist" :disabled="!canWrite || busy" /> {{ t('serverAccess.whitelistEnabled') }}</label>
    <label class="check-row"><input type="checkbox" v-model="fallback" :disabled="!canWrite || busy" /> {{ t('serverAccess.premiumFallback') }}</label>
    <p class="muted hint">{{ t('serverAccess.fallbackHint') }}</p>
    <p class="muted hint">{{ t('serverAccess.proxyHint') }}</p>
    <button v-if="canWrite" :disabled="busy" @click="save">{{ t('serverAccess.save') }}</button>
  </section>
</template>

<style scoped>
.policy-panel { display: grid; gap: var(--sp-3); }
.policy-panel h2 { margin: 0; font-size: var(--fs-m); }
.check-row { display: flex; align-items: center; gap: var(--sp-2); }
.hint { margin: 0; font-size: var(--fs-s); }
.policy-panel button { justify-self: start; }
</style>
