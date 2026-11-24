import { ref, shallowRef, readonly } from 'vue'

const quotaFields = ['verifyRateLimit', 'verifyBurst', 'maxConcurrent', 'whitelistLimit', 'eventRetentionDays']

export function isTenantDeletionDue(row, now = Date.now()) {
  if (!row || row.status !== 'deleting' || !row.deletionDueAt) return false
  const due = Date.parse(row.deletionDueAt)
  return Number.isFinite(due) && due <= now
}

export function buildServerQuotaPatch(draft = {}) {
  const patch = {}
  for (const field of quotaFields) {
    const raw = draft[field]
    if (raw === '' || raw === null || raw === undefined) continue
    const value = Number(raw)
    if (!Number.isSafeInteger(value) || value < 1 || value > 1000000) return { patch: {}, valid: false }
    patch[field] = value
  }
  return { patch, valid: true }
}

export function useServerAccessPlatform(api) {
  const tenants = ref([])
  const servers = ref([])
  const audit = ref([])
  const tenantTotal = shallowRef(0)
  const serverTotal = shallowRef(0)
  const auditTotal = shallowRef(0)
  const tenantPage = shallowRef(1)
  const serverPage = shallowRef(1)
  const auditPage = shallowRef(1)
  const tenantQuery = shallowRef('')
  const serverQuery = shallowRef('')
  const auditQuery = shallowRef('')
  const fallback = shallowRef(true)
  const loading = shallowRef(false)
  const busy = shallowRef(false)
  const error = shallowRef('')
  let tenantRequest = 0
  let serverRequest = 0
  let auditRequest = 0
  let fallbackRequest = 0
  let loadRequest = 0

  async function loadTenants() {
    const current = ++tenantRequest
    try {
      const data = await api.platformTenants(tenantPage.value, tenantQuery.value)
      if (current === tenantRequest) {
        tenants.value = data.tenants || []
        tenantTotal.value = data.total || 0
      }
      return current === tenantRequest
    } catch (cause) { if (current === tenantRequest) error.value = cause.message; return false }
  }

  async function loadServers() {
    const current = ++serverRequest
    try {
      const data = await api.platformServers(serverPage.value, serverQuery.value)
      if (current === serverRequest) {
        servers.value = data.servers || []
        serverTotal.value = data.total || 0
      }
      return current === serverRequest
    } catch (cause) { if (current === serverRequest) error.value = cause.message; return false }
  }

  async function loadAudit() {
    const current = ++auditRequest
    try {
      const data = await api.platformAudit(auditPage.value, auditQuery.value)
      if (current === auditRequest) {
        audit.value = data.audit || []
        auditTotal.value = data.total || 0
      }
      return current === auditRequest
    } catch (cause) { if (current === auditRequest) error.value = cause.message; return false }
  }

  async function loadFallback() {
    const current = ++fallbackRequest
    try {
      const data = await api.platformFallback()
      if (current === fallbackRequest) fallback.value = !!data.enabled
      return current === fallbackRequest
    } catch (cause) { if (current === fallbackRequest) error.value = cause.message; return false }
  }

  async function load() {
    const current = ++loadRequest
    loading.value = true
    error.value = ''
    await Promise.all([loadTenants(), loadServers(), loadAudit(), loadFallback()])
    if (current === loadRequest) loading.value = false
  }

  async function mutate(action, refresh) {
    if (busy.value) return false
    busy.value = true
    error.value = ''
    try {
      await action()
      await refresh()
      return true
    } catch (cause) {
      error.value = cause.message
      return false
    } finally {
      busy.value = false
    }
  }

  return {
    tenants: readonly(tenants), servers: readonly(servers), audit: readonly(audit),
    tenantTotal: readonly(tenantTotal), serverTotal: readonly(serverTotal), auditTotal: readonly(auditTotal),
    tenantPage: readonly(tenantPage), serverPage: readonly(serverPage), auditPage: readonly(auditPage),
    tenantQuery: readonly(tenantQuery), serverQuery: readonly(serverQuery), auditQuery: readonly(auditQuery),
    fallback: readonly(fallback), loading: readonly(loading), busy: readonly(busy), error: readonly(error),
    load, loadTenants, loadServers, loadAudit,
    searchTenants: (query) => { tenantQuery.value = query; tenantPage.value = 1; return loadTenants() },
    searchServers: (query) => { serverQuery.value = query; serverPage.value = 1; return loadServers() },
    searchAudit: (query) => { auditQuery.value = query; auditPage.value = 1; return loadAudit() },
    pageTenants: (page) => { tenantPage.value = page; return loadTenants() },
    pageServers: (page) => { serverPage.value = page; return loadServers() },
    pageAudit: (page) => { auditPage.value = page; return loadAudit() },
    createTenant: (body) => mutate(() => api.createPlatformTenant(body), async () => {
      tenantPage.value = 1
      tenantQuery.value = ''
      auditPage.value = 1
      auditQuery.value = ''
      await Promise.all([loadTenants(), loadAudit()])
    }),
    updateTenant: (id, body) => mutate(() => api.updatePlatformTenant(id, body), loadTenants),
    deleteTenant: (id) => mutate(() => api.deletePlatformTenant(id), async () => {
      await Promise.all([loadTenants(), loadAudit()])
    }),
    updateServer: (id, body) => mutate(() => api.updatePlatformServer(id, body), loadServers),
    setFallback: (enabled) => mutate(async () => {
      const data = await api.setPlatformFallback(enabled)
      fallback.value = !!data.enabled
    }, loadAudit),
    revokeCredential: (id, cid) => mutate(() => api.revokePlatformCredential(id, cid), async () => {
      await Promise.all([loadServers(), loadAudit()])
    }),
  }
}
