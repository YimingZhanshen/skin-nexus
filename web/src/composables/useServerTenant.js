import { ref, shallowRef, readonly } from 'vue'
import { isSessionLoss } from '../session.js'

export function useServerTenant(api) {
  const tenant = shallowRef(null)
  const role = shallowRef('')
  const members = ref([])
  const invites = ref([])
  const loading = shallowRef(false)
  const busy = shallowRef(false)
  const error = shallowRef('')
  const unavailable = shallowRef(false)
  const forbidden = shallowRef(false)
  const total = shallowRef(0)
  const page = shallowRef(1)
  const size = shallowRef(50)
  let request = 0
  let mutation = 0

  function clearSessionState() {
    // Invalidate both the failing operation and any older request that may
    // still resolve after the shell has dropped back to login.
    request += 1
    mutation += 1
    tenant.value = null
    role.value = ''
    members.value = []
    invites.value = []
    total.value = 0
    page.value = 1
    loading.value = false
    busy.value = false
    error.value = ''
    unavailable.value = false
    forbidden.value = false
  }

  async function load() {
    const current = ++request
    loading.value = true
    error.value = ''
    unavailable.value = false
    forbidden.value = false
    try {
      const me = await api.tenantMe()
      const [memberData, inviteData] = await Promise.all([
        api.members(),
        me.role === 'owner' ? api.invites(page.value, size.value) : Promise.resolve({ invites: [], total: 0 }),
      ])
      if (current !== request) return false
      tenant.value = me.tenant
      role.value = me.role
      members.value = memberData.members || []
      invites.value = inviteData.invites || []
      total.value = inviteData.total || 0
      const lastPage = Math.max(1, Math.ceil(total.value / size.value))
      if (page.value > lastPage) {
        page.value = lastPage
        return load()
      }
      return true
    } catch (cause) {
      if (current !== request) return false
      if (isSessionLoss(cause)) {
        clearSessionState()
        return false
      }
      error.value = cause.message
      unavailable.value = cause.status === 404
      forbidden.value = cause.status === 403
      return false
    } finally {
      if (current === request) loading.value = false
    }
  }

  async function mutate(action) {
    if (busy.value) return null
    const current = request
    const currentMutation = ++mutation
    busy.value = true
    error.value = ''
    try {
      const result = await action()
      // A concurrent load may have failed with session loss while this action
      // was in flight. Do not publish its result or start a post-loss reload.
      if (current !== request) return null
      await load()
      return result
    } catch (cause) {
      if (current !== request) return null
      if (isSessionLoss(cause)) {
        clearSessionState()
        return null
      }
      error.value = cause.message
      return null
    } finally {
      // Session loss permits a fresh mutation while an older one is still
      // settling; only the current mutation may release the busy guard.
      if (currentMutation === mutation) busy.value = false
    }
  }

  return {
    tenant: readonly(tenant), role: readonly(role), members: readonly(members), invites: readonly(invites),
    loading: readonly(loading), busy: readonly(busy), error: readonly(error),
    unavailable: readonly(unavailable), forbidden: readonly(forbidden),
    total: readonly(total), page: readonly(page), size: readonly(size), load,
    pageInvites: (nextPage) => { page.value = nextPage; return load() },
    invite: (email, memberRole) => mutate(() => api.inviteMember(email, memberRole)),
    removeMember: (uid) => mutate(() => api.removeMember(uid)),
    revokeInvite: (id) => mutate(() => api.revokeInvite(id)),
  }
}
