<script setup>
// InvitationView: generate form on top, then free codes, then used codes
// (blessing 管理面板 邀请码 layout parity — B1).
import { ref, onMounted } from 'vue'
import { fetchJSON } from '../../adminApi.js'

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
  const next = prompt('修改备注（description）：', ic.description || '')
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
    <h2 style="margin-top: 0;">邀请码（未用 {{ invFree.length }} / 已用 {{ invUsed.length }}）</h2>
    <!-- generate form on top (blessing layout) -->
    <div class="row">
      <input v-model.number="invNew.amount" type="number" min="1" max="1000" placeholder="数量" style="max-width: 100px;" />
      <input v-model="invNew.description" placeholder="备注（description，人可读标识，选填）" style="max-width: 320px;" />
      <button @click="invGenerate">生成</button>
    </div>
    <div class="error" v-if="invError">{{ invError }}</div>
    <h3>可用列表</h3>
    <div class="table-scroll" v-if="invFree.length">
    <table>
      <tr><th>ID</th><th>邀请码</th><th>备注</th><th>生成时间</th><th></th></tr>
      <tr v-for="ic in invFree" :key="ic.id">
        <td>{{ ic.id }}</td>
        <td><code>{{ ic.code }}</code></td>
        <td class="muted">{{ ic.description || '—' }}</td>
        <td class="muted">{{ (ic.generated_at || '').slice(0, 19).replace('T', ' ') }}</td>
        <td><button class="secondary" @click="invEdit(ic)">改备注</button></td>
      </tr>
    </table>
    </div>
    <p class="muted" v-else>暂无可用邀请码</p>
    <h3>已用列表</h3>
    <div class="table-scroll" v-if="invUsed.length">
    <table>
      <tr><th>ID</th><th>邀请码</th><th>备注</th><th>使用者 UID</th><th>使用时间</th></tr>
      <tr v-for="ic in invUsed" :key="ic.id">
        <td>{{ ic.id }}</td>
        <td><code>{{ ic.code }}</code></td>
        <td class="muted">{{ ic.description || '—' }}</td>
        <td>{{ ic.used_by }}</td>
        <td class="muted">{{ (ic.used_at || '').slice(0, 19).replace('T', ' ') }}</td>
      </tr>
    </table>
    </div>
    <p class="muted" v-else>暂无已用邀请码</p>
  </div>
</template>
