import { ref, shallowRef, readonly } from 'vue'

export function useServerWhitelist(api) {
  const rows = ref([])
  const loading = shallowRef(false)
  const busy = shallowRef(false)
  const error = shallowRef('')
  const resolved = shallowRef(null)
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
      const data = await api.whitelist(serverId)
      if (current === request) rows.value = data.whitelist || []
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

  async function add(serverId, player, note) {
    if (busy.value) return false
    const current = request
    busy.value = true
    error.value = ''
    try {
      await api.addWhitelist(serverId, player.uid, note)
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
      await api.removeWhitelist(serverId, uid)
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
