<script setup>
// InvitationView: generate form on top, then free codes, then used codes
// (blessing 管理面板 邀请码 layout parity — B1).
import { ref, onMounted } from 'vue'
import { useI18n } from 'vue-i18n'
import { fetchJSON } from '../../adminApi.js'

const { t } = useI18n()
const invFree = ref([])
const invUsed = ref([])
const invError = ref('')
const invNew = ref({ amount: 1, description: '' })

async function load() {
  invError.value = ''
  try {
    const d = await fetchJSON('/api/admin/v1/invitation-codes')
    invFree.value = d.free || []
    invUsed.value = d.used || []
  } catch (e) { invError.value = e.message }
}

async function invGenerate() {
  invError.value = ''
  try {
    await fetchJSON('/api/admin/v1/invitation-codes', {
      method: 'POST', headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ amount: invNew.value.amount || 1, description: invNew.value.description }),
    })
    invNew.value = { amount: 1, description: '' }
    await load()
  } catch (e) { invError.value = e.message }
}

async function invEdit(ic) {
  const next = prompt(t('admin.invitationDescPrompt'), ic.description || '')
  if (next === null) return
  invError.value = ''
  try {
    await fetchJSON('/api/admin/v1/invitation-codes', {
      method: 'PUT', headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ id: ic.id, description: next }),
    })
    await load()
  } catch (e) { invError.value = e.message }
}

onMounted(load)
</script>

<template>
  <div class="card">
    <h2 style="margin-top: 0;">{{ t('admin.invitationTitle', { free: invFree.length, used: invUsed.length }) }}</h2>
    <!-- generate form on top (blessing layout) -->
    <div class="row">
      <input v-model.number="invNew.amount" type="number" min="1" max="1000" :placeholder="t('admin.invitationAmountPh')" style="max-width: 100px;" />
      <input v-model="invNew.description" :placeholder="t('admin.invitationDescPh')" style="max-width: 320px;" />
      <button @click="invGenerate">{{ t('admin.invitationGenerate') }}</button>
    </div>
    <div class="error" v-if="invError">{{ invError }}</div>
    <h3>{{ t('admin.invitationAvailable') }}</h3>
    <div class="table-scroll" v-if="invFree.length">
    <table>
      <tr><th>ID</th><th>{{ t('admin.invitationColCode') }}</th><th>{{ t('admin.invitationColDesc') }}</th><th>{{ t('admin.invitationColGenerated') }}</th><th></th></tr>
      <tr v-for="ic in invFree" :key="ic.id">
        <td>{{ ic.id }}</td>
        <td><code>{{ ic.code }}</code></td>
        <td class="muted">{{ ic.description || '—' }}</td>
        <td class="muted">{{ (ic.generated_at || '').slice(0, 19).replace('T', ' ') }}</td>
        <td><button class="secondary" @click="invEdit(ic)">{{ t('admin.invitationEditDesc') }}</button></td>
      </tr>
    </table>
    </div>
    <p class="muted" v-else>{{ t('admin.invitationFreeEmpty') }}</p>
    <h3>{{ t('admin.invitationUsedList') }}</h3>
    <div class="table-scroll" v-if="invUsed.length">
    <table>
      <tr><th>ID</th><th>{{ t('admin.invitationColCode') }}</th><th>{{ t('admin.invitationColDesc') }}</th><th>{{ t('admin.invitationColUsedBy') }}</th><th>{{ t('admin.invitationColUsedAt') }}</th></tr>
      <tr v-for="ic in invUsed" :key="ic.id">
        <td>{{ ic.id }}</td>
        <td><code>{{ ic.code }}</code></td>
        <td class="muted">{{ ic.description || '—' }}</td>
        <td>{{ ic.used_by }}</td>
        <td class="muted">{{ (ic.used_at || '').slice(0, 19).replace('T', ' ') }}</td>
      </tr>
    </table>
    </div>
    <p class="muted" v-else>{{ t('admin.invitationUsedEmpty') }}</p>
  </div>
</template>
