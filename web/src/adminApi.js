// adminApi.js: shared admin fetch plumbing (extracted from AdminPanel.vue
// during the B1 sidebar refactor so every admin view speaks one envelope).
//
// Token semantics (blessing parity): an admin SESSION (me.permission > 0)
// reaches /api/admin/v1/* with cookies alone; X-Admin-Token is the machine
// fallback, attached only when present. A 401/403 flips needToken so the
// shell surfaces the token card.
import { ref } from 'vue'
import { fetchWithTimeout } from './net.js'

export const token = ref(localStorage.getItem('admin_token') || '')
export const needToken = ref(true)

export async function fetchJSON(path, opts) {
  const headers = { ...(opts && opts.headers) }
  if (token.value) headers['X-Admin-Token'] = token.value
  const res = await fetchWithTimeout(path, { ...opts, headers }, 20000)
  const data = await res.json().catch(() => ({}))
  if (!res.ok) {
    if (res.status === 401 || res.status === 403) needToken.value = true
    const err = new Error(data.message || data.error || `HTTP ${res.status}`)
    err.status = res.status // 404=模块关闭/未找到，401=会话失效：调用方按 status 分流
    throw err
  }
  return data
}

export function persistToken() {
  localStorage.setItem('admin_token', token.value)
}
