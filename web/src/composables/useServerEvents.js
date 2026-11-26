import { ref, shallowRef, readonly } from 'vue'

export function useServerEvents(api) {
  const rows = ref([])
  const nextBefore = shallowRef(0)
  const hasMore = shallowRef(false)
  const loading = shallowRef(false)
  const error = shallowRef('')
  let activeServer = null
  let request = 0

  async function fetchPage(serverId, before, append) {
    const current = ++request
    if (activeServer !== serverId) {
      rows.value = []
      nextBefore.value = 0
      hasMore.value = false
    }
    activeServer = serverId
    loading.value = true
    error.value = ''
    try {
      const data = await api.events(serverId, { before, size: 50 })
      if (current !== request) return false
      const incoming = data.events || []
      if (append) {
        const seen = new Set(rows.value.map((row) => row.id))
        rows.value = [...rows.value, ...incoming.filter((row) => !seen.has(row.id))]
      } else {
        rows.value = incoming
      }
      nextBefore.value = data.nextBefore || 0
      hasMore.value = !!data.hasMore
      return true
    } catch (cause) {
      if (current === request) error.value = cause.message
      return false
    } finally {
      if (current === request) loading.value = false
    }
  }

  return {
    rows: readonly(rows), nextBefore: readonly(nextBefore), hasMore: readonly(hasMore),
    loading: readonly(loading), error: readonly(error),
    load: (serverId) => fetchPage(serverId, 0, false),
    more: (serverId) => {
      if (loading.value || !hasMore.value || !nextBefore.value) return Promise.resolve(false)
      return fetchPage(serverId, nextBefore.value, true)
    },
  }
}
