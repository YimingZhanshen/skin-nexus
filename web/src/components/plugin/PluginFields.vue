<script setup>
// Host-rendered plugin fields for a built-in form. Values are gathered per
// plugin and posted as pluginData; the host validates them server-side.
import { computed, ref, watch } from 'vue'
import SchemaForm from '../SchemaForm.vue'
import { formsFor } from '../../pluginUI.js'

const props = defineProps({
  form: { type: String, required: true },
  modelValue: { type: Object, default: () => ({}) },
})
const emit = defineEmits(['update:modelValue'])

const items = computed(() => formsFor(props.form))
const values = ref({ ...(props.modelValue || {}) })
watch(() => props.modelValue, (v) => { values.value = { ...(v || {}) } }, { deep: true })

function set(plugin, value) {
  values.value = { ...values.value, [plugin]: value || {} }
  emit('update:modelValue', values.value)
}
</script>

<template>
  <div v-if="items.length" class="plugin-fields">
    <div v-for="f in items" :key="f.plugin">
      <h4 v-if="items.length > 1">{{ f.title }}</h4>
      <SchemaForm :schema="f.schema" :model-value="values[f.plugin] || {}"
                  @update:model-value="set(f.plugin, $event)" />
    </div>
  </div>
</template>

<style scoped>
.plugin-fields { margin-top: 10px; }
.plugin-fields h4 { font-size: var(--fs-s); margin: 0 0 6px; color: var(--muted); }
</style>
