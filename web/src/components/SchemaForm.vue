<script setup>
// SchemaForm: renders the plugin config JSON-Schema subset with the same
// widgets/classes as SettingsView. Secret values arrive masked; leaving the
// mask untouched tells the host to keep the stored secret.
import { computed, ref, watch } from 'vue'

const props = defineProps({
  schema: { type: Object, default: () => ({}) },
  modelValue: { type: Object, default: () => ({}) },
  disabled: { type: Boolean, default: false },
})
const emit = defineEmits(['update:modelValue'])

const values = ref({ ...(props.modelValue || {}) })
watch(() => props.modelValue, (v) => { values.value = { ...(v || {}) } }, { deep: true })

const fields = computed(() => {
  const list = []
  const req = new Set(props.schema.required || [])
  for (const [name, def] of Object.entries(props.schema.properties || {})) {
    list.push({ name, ...def, required: req.has(name) })
  }
  return list.sort((a, b) => a.name.localeCompare(b.name))
})

function setField(name, value) {
  values.value = { ...values.value, [name]: value }
  emit('update:modelValue', values.value)
}

function setNested(parent, name, value) {
  const next = { ...(values.value[parent] || {}) }
  next[name] = value
  setField(parent, next)
}

function uiOf(f) {
  if (f['x-ui']) return f['x-ui']
  if (f.format === 'secret') return 'password'
  if (f.enum && f.enum.length) return 'select'
  if (f.type === 'boolean') return 'checkbox'
  if (f.type === 'integer' || f.type === 'number') return 'number'
  if (f.type === 'array') return 'list'
  if (f.type === 'object') return 'group'
  return 'text'
}

// label↔control association (click-to-focus + AT naming)
function sfId(parent, name) {
  return 'sf-' + (parent ? parent + '-' : '') + name
}

function listValue(f) {
  const v = values.value[f.name]
  return Array.isArray(v) ? v.join('\n') : ''
}
function setList(f, raw) {
  const arr = String(raw).split(/\r?\n/).map((s) => s.trim()).filter(Boolean)
  setField(f.name, arr)
}
</script>

<template>
  <div class="schema-form">
    <template v-for="f in fields" :key="f.name">
      <div v-if="uiOf(f) === 'group'" class="schema-group">
        <h4>{{ f.title || f.name }}</h4>
        <p v-if="f.description" class="muted">{{ f.description }}</p>
        <div class="row" v-for="c in Object.entries(f.properties || {}).map(([n, d]) => ({ name: n, ...d }))" :key="c.name"
             style="margin-top: 10px; align-items: center;">
          <label :for="sfId(f.name, c.name)" style="width: 220px; font-weight: 600; font-size: var(--fs-xs);">
            {{ c.title || c.name }}<span v-if="(f.required || []).includes(c.name)"> *</span>
            <span v-if="c.description" class="muted" style="display:block;font-weight:400;">{{ c.description }}</span>
          </label>
          <input v-if="uiOf(c) === 'checkbox'" :id="sfId(f.name, c.name)" type="checkbox" :disabled="disabled"
                 :checked="!!(values[f.name] || {})[c.name]"
                 @change="setNested(f.name, c.name, $event.target.checked)" />
          <input v-else-if="uiOf(c) === 'number'" :id="sfId(f.name, c.name)" type="number" :disabled="disabled"
                 :value="(values[f.name] || {})[c.name]"
                 @input="setNested(f.name, c.name, Number($event.target.value))" style="max-width:180px;" />
          <select v-else-if="uiOf(c) === 'select'" :id="sfId(f.name, c.name)" :disabled="disabled"
                  :value="(values[f.name] || {})[c.name]"
                  @change="setNested(f.name, c.name, $event.target.value)" style="max-width:220px;">
            <option v-for="o in (c.enum || [])" :key="o" :value="o">{{ o }}</option>
          </select>
          <textarea v-else-if="uiOf(c) === 'textarea'" :id="sfId(f.name, c.name)" :disabled="disabled"
                    :value="(values[f.name] || {})[c.name]"
                    @input="setNested(f.name, c.name, $event.target.value)"
                    style="max-width:420px;min-height:64px;"></textarea>
          <input v-else :id="sfId(f.name, c.name)" :type="uiOf(c) === 'password' ? 'password' : 'text'" :disabled="disabled"
                 :value="(values[f.name] || {})[c.name]"
                 @input="setNested(f.name, c.name, $event.target.value)" style="max-width:320px;" />
        </div>
      </div>

      <div v-else class="row" style="margin-top: 12px; align-items: center;">
        <label :for="sfId(null, f.name)" style="width: 220px; font-weight: 600; font-size: var(--fs-xs);">
          {{ f.title || f.name }}<span v-if="f.required"> *</span>
          <span v-if="f.description" class="muted" style="display:block;font-weight:400;">{{ f.description }}</span>
        </label>
        <input v-if="uiOf(f) === 'checkbox'" :id="sfId(null, f.name)" type="checkbox" :disabled="disabled"
               :checked="!!values[f.name]" @change="setField(f.name, $event.target.checked)" />
        <input v-else-if="uiOf(f) === 'number'" :id="sfId(null, f.name)" type="number" :disabled="disabled"
               :value="values[f.name]" @input="setField(f.name, Number($event.target.value))" style="max-width:180px;" />
        <select v-else-if="uiOf(f) === 'select'" :id="sfId(null, f.name)" :disabled="disabled"
                :value="values[f.name]" @change="setField(f.name, $event.target.value)" style="max-width:220px;">
          <option v-for="o in (f.enum || [])" :key="o" :value="o">{{ o }}</option>
        </select>
        <textarea v-else-if="uiOf(f) === 'list'" :id="sfId(null, f.name)" :disabled="disabled"
                  :value="listValue(f)" @input="setList(f, $event.target.value)"
                  style="max-width:420px;min-height:64px;"></textarea>
        <textarea v-else-if="uiOf(f) === 'textarea'" :id="sfId(null, f.name)" :disabled="disabled"
                  :value="values[f.name]" @input="setField(f.name, $event.target.value)"
                  style="max-width:420px;min-height:64px;"></textarea>
        <input v-else :id="sfId(null, f.name)" :type="uiOf(f) === 'password' ? 'password' : 'text'" :disabled="disabled"
               :value="values[f.name]" @input="setField(f.name, $event.target.value)" style="max-width:320px;" />
      </div>
    </template>
    <p v-if="!fields.length" class="muted">—</p>
  </div>
</template>

<style scoped>
.schema-group { margin-top: 16px; padding: 12px; border: 1px solid var(--border); border-radius: var(--r-m); }
.schema-group h4 { margin: 0 0 4px; }
</style>
