import { ref, shallowRef, readonly } from 'vue'

export function useServerBans(api) {
  const rows = ref([])
  const resolved = shallowRef(null)
  const loading = shallowRef(false)
  const busy = shallowRef(false)
  const error = shallowRef('')
  let activeServer = null
  let request = 0
  let lookupRequest = 0

  async function load(serverId) {
    const current = ++request
    if (activeServer !== serverId) rows.value = []
    activeServer = serverId
    loading.value = true
    error.value = ''
    try {
      const data = await api.bans(serverId)
      if (current === request) rows.value = data.bans || []
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
    busy: readonly(busy), error: readonly(error), load, lookup, add, remove,
    clearResolution: () => { lookupRequest++; resolved.value = null; error.value = '' },
  }
}
