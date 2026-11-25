import { ref, shallowRef, readonly } from 'vue'

export function useServerCatalog(api) {
  const servers = ref([])
  const selected = shallowRef(null)
  const policy = shallowRef(null)
  const loading = shallowRef(false)
  const busy = shallowRef(false)
  const error = shallowRef('')
  const total = shallowRef(0)
  const page = shallowRef(1)
  const size = shallowRef(50)
  const quotaUsed = shallowRef(0)
  const status = shallowRef('')
  let request = 0

  async function load(nextPage = page.value, nextSize = size.value, nextStatus = status.value) {
    const current = ++request
    page.value = nextPage
    size.value = nextSize
    status.value = nextStatus
    loading.value = true
    error.value = ''
    try {
      const data = await api.servers(nextPage, nextSize, nextStatus)
      if (current !== request) return false
      servers.value = data.servers || []
      total.value = data.total || 0
      quotaUsed.value = data.quotaUsed ?? total.value
      if (selected.value && total.value === 0) {
        selected.value = null
        policy.value = null
      }
      return true
    } catch (cause) {
      if (current === request) error.value = cause.message
      return false
    } finally {
      if (current === request) loading.value = false
    }
  }

  async function select(id) {
    const current = ++request
    loading.value = true
    error.value = ''
    try {
      const data = await api.server(id)
      if (current !== request) return false
      selected.value = data
      policy.value = data.policy
      servers.value = servers.value.map((item) => item.id === id ? data.server : item)
      return true
    } catch (cause) {
      if (current === request) error.value = cause.message
      return false
    } finally {
      if (current === request) loading.value = false
    }
  }

  function clearSelection() {
    request++
    selected.value = null
    policy.value = null
    loading.value = false
  }

  async function create(name) {
    if (busy.value) return null
    busy.value = true
    error.value = ''
    try {
      const created = await api.createServer(name)
      quotaUsed.value++
      if (page.value === 1 && (!status.value || created.status === status.value)) {
        servers.value = [created, ...servers.value].slice(0, size.value)
        total.value++
      } else {
        await load(1, size.value, status.value)
      }
      return created
    } catch (cause) {
      error.value = cause.message
      return null
    } finally {
      busy.value = false
    }
  }

  async function update(id, body) {
    if (busy.value) return null
    busy.value = true
    error.value = ''
    try {
      const updated = await api.updateServer(id, body)
      servers.value = servers.value.map((item) => item.id === id ? { ...item, ...updated } : item)
      if (selected.value?.server.id === id) {
        const nextPolicy = body.policy ? { ...selected.value.policy, ...body.policy } : selected.value.policy
        selected.value = { ...selected.value, server: { ...selected.value.server, ...updated }, policy: nextPolicy }
        policy.value = nextPolicy
      }
      return updated
    } catch (cause) {
      error.value = cause.message
      return null
    } finally {
      busy.value = false
    }
  }

  return {
    servers: readonly(servers), selected: readonly(selected), policy: readonly(policy),
    loading: readonly(loading), busy: readonly(busy), error: readonly(error),
    total: readonly(total), page: readonly(page), size: readonly(size), quotaUsed: readonly(quotaUsed),
    status: readonly(status), load, select, clearSelection, create, update,
    goPage: (nextPage) => load(nextPage),
    filter: (nextStatus) => load(1, size.value, nextStatus),
  }
}
