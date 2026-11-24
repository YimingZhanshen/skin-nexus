import { ref, shallowRef, readonly } from 'vue'

export function useServerCatalog(api) {
  const servers = ref([])
  const selected = shallowRef(null)
  const policy = shallowRef(null)
  const loading = shallowRef(false)
  const busy = shallowRef(false)
  const error = shallowRef('')
  let request = 0

  async function load() {
    const current = ++request
    loading.value = true
    error.value = ''
    try {
      const data = await api.servers()
      if (current !== request) return false
      servers.value = data.servers || []
      if (selected.value && !servers.value.some((item) => item.id === selected.value.server.id)) {
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
      servers.value = [created, ...servers.value]
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
    load, select, clearSelection, create, update,
  }
}
