import { ref, shallowRef, readonly } from 'vue'

export function useServerCredentials(api) {
  const credentials = ref([])
  const secret = shallowRef('')
  const busy = shallowRef(false)
  const error = shallowRef('')
  let activeServer = null
  let request = 0
  let secretEpoch = 0

  function clearSecret() {
    secretEpoch++
    secret.value = ''
  }

  function clear() {
    request++
    secretEpoch++
    activeServer = null
    credentials.value = []
    secret.value = ''
    error.value = ''
  }

  async function refresh(serverId, current, epoch) {
    const data = await api.credentials(serverId)
    if (current === request && epoch === secretEpoch) credentials.value = data.credentials || []
  }

  async function load(serverId) {
    const current = ++request
    const epoch = ++secretEpoch
    if (activeServer !== serverId) credentials.value = []
    activeServer = serverId
    secret.value = ''
    error.value = ''
    try {
      await refresh(serverId, current, epoch)
      return current === request && epoch === secretEpoch
    } catch (cause) {
      if (current === request && epoch === secretEpoch) error.value = cause.message
      return false
    }
  }

  async function issue(serverId, action) {
    if (busy.value) return null
    const current = ++request
    const epoch = ++secretEpoch
    if (activeServer !== serverId) credentials.value = []
    activeServer = serverId
    busy.value = true
    error.value = ''
    secret.value = ''
    try {
      const result = await action()
      if (current !== request || epoch !== secretEpoch) return null
      try {
        await refresh(serverId, current, epoch)
      } catch (cause) {
        // The credential already exists. Keep its one-time secret available
        // even when the follow-up metadata refresh fails.
        if (current === request && epoch === secretEpoch) error.value = cause.message
      }
      // Closing, hiding, changing server or starting another load invalidates
      // this epoch. A late response must never republish a discarded secret.
      if (current !== request || epoch !== secretEpoch) return null
      secret.value = result.secret
      return result
    } catch (cause) {
      if (current === request && epoch === secretEpoch) error.value = cause.message
      return null
    } finally {
      busy.value = false
    }
  }

  return {
    credentials: readonly(credentials), secret: readonly(secret), busy: readonly(busy), error: readonly(error), load,
    clearSecret, clear,
    create: (serverId, mode) => issue(serverId, () => api.createCredential(serverId, mode)),
    rotate: (serverId, cid, mode) => issue(serverId, () => api.rotateCredential(serverId, cid, mode)),
    revoke: async (serverId, cid) => {
      if (busy.value) return false
      const current = request
      busy.value = true
      error.value = ''
      try {
        await api.revokeCredential(serverId, cid)
        if (current !== request || activeServer !== serverId) return false
        await load(serverId)
        return true
      } catch (cause) {
        if (current === request && activeServer === serverId) error.value = cause.message
        return false
      } finally {
        busy.value = false
      }
    },
  }
}
