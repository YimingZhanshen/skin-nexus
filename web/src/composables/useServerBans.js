import { ref, shallowRef, readonly } from 'vue'

export function useServerBans(api) {
  const rows = ref([])
  const resolved = shallowRef(null)
  const loading = shallowRef(false)
  const busy = shallowRef(false)
  const error = shallowRef('')
  const total = shallowRef(0)
  const page = shallowRef(1)
  const size = shallowRef(50)
  let activeServer = null
  let request = 0
  let lookupRequest = 0

  async function load(serverId, nextPage, nextSize = size.value) {
    const current = ++request
    const switchingServer = activeServer !== serverId
    if (switchingServer) {
      rows.value = []
      total.value = 0
    }
    if (nextPage === undefined) nextPage = switchingServer ? 1 : page.value
    activeServer = serverId
    page.value = nextPage
    size.value = nextSize
    loading.value = true
    error.value = ''
    try {
      const data = await api.bans(serverId, nextPage, nextSize)
      if (current !== request) return false
      rows.value = data.bans || []
      total.value = data.total || 0
      const lastPage = Math.max(1, Math.ceil(total.value / nextSize))
      if (nextPage > lastPage) {
        page.value = lastPage
        return load(serverId, lastPage, nextSize)
      }
      return true
    } catch (cause) {
      if (current === request) error.value = cause.message
      return false
    } finally {
      if (current === request) loading.value = false
    }
  }

  async function lookup(query) {
    const current = ++lookupRequest
    error.value = ''
    resolved.value = null
    try {
      const player = await api.lookupPlayer(query)
      if (current !== lookupRequest) return null
      resolved.value = player
      return player
    } catch (cause) {
      if (current === lookupRequest) error.value = cause.message
      return null
    }
  }

  async function add(serverId, body) {
    if (busy.value) return false
    const current = request
    busy.value = true
    error.value = ''
    try {
      await api.addBan(serverId, body)
      if (current !== request || activeServer !== serverId) return false
      await load(serverId)
      return true
    } catch (cause) {
      if (current === request && activeServer === serverId) error.value = cause.message
      return false
    } finally {
      busy.value = false
    }
  }

  async function remove(serverId, uid) {
    if (busy.value) return false
    const current = request
    busy.value = true
    error.value = ''
    try {
      await api.removeBan(serverId, uid)
      if (current !== request || activeServer !== serverId) return false
      await load(serverId)
      return true
    } catch (cause) {
      if (current === request && activeServer === serverId) error.value = cause.message
      return false
    } finally {
      busy.value = false
    }
  }

  return {
    rows: readonly(rows), resolved: readonly(resolved), loading: readonly(loading),
    busy: readonly(busy), error: readonly(error), total: readonly(total), page: readonly(page), size: readonly(size),
    load, lookup, add, remove, goPage: (serverId, nextPage) => load(serverId, nextPage),
    clearResolution: () => { lookupRequest++; resolved.value = null; error.value = '' },
  }
}
