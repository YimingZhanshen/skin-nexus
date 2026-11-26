// Independent server-access client: ordinary same-origin web sessions only.
// It intentionally does not import adminApi or read the machine admin token.
import { fetchWithTimeout } from '../net.js'
import { describe } from '../errors.js'
import { captureSessionVersion, guard401 } from '../session.js'

async function request(method, path, body, { blob = false } = {}) {
  const epoch = captureSessionVersion()
  const options = { method, credentials: 'same-origin', headers: {} }
  if (body !== undefined) {
    options.headers['Content-Type'] = 'application/json'
    options.body = JSON.stringify(body)
  }
  const response = await fetchWithTimeout(path, options, 20000)
  if (!response.ok) {
    const data = await response.json().catch(() => ({}))
    const error = new Error(describe(data, response.status))
    error.status = response.status
    error.data = data
    guard401(error, epoch)
    throw error
  }
  return blob ? response.blob() : response.json().catch(() => ({}))
}

const ownerBase = '/api/serveraccess/v1'
const platformBase = '/api/admin/v1/serveraccess'

function ownerList(path, page, size, status = '') {
  const query = new URLSearchParams({ page: String(page), size: String(size) })
  if (status) query.set('status', status)
  return request('GET', `${path}?${query}`)
}

export const serverAccessApi = {
  tenantMe: () => request('GET', `${ownerBase}/tenants/me`),
  acceptInvite: (token) => request('POST', `${ownerBase}/invites/accept`, { token }),
  members: () => request('GET', `${ownerBase}/tenants/me/members`),
  invites: (page = 1, size = 50) => ownerList(`${ownerBase}/tenants/me/invites`, page, size),
  inviteMember: (email, role) => request('POST', `${ownerBase}/tenants/me/members/invite`, { email, role }),
  removeMember: (uid) => request('DELETE', `${ownerBase}/tenants/me/members/${uid}`),
  revokeInvite: (id) => request('DELETE', `${ownerBase}/tenants/me/invites/${id}`),
  exportTenant: () => request('GET', `${ownerBase}/tenants/me/export`, undefined, { blob: true }),
  requestTenantDeletion: (confirmation) => request('POST', `${ownerBase}/tenants/me/delete-request`, { confirmation }),

  servers: (page = 1, size = 50, status = '') => ownerList(`${ownerBase}/servers`, page, size, status),
  createServer: (name) => request('POST', `${ownerBase}/servers`, { name }),
  server: (id) => request('GET', `${ownerBase}/servers/${id}`),
  updateServer: (id, body) => request('PATCH', `${ownerBase}/servers/${id}`, body),

  whitelist: (id, page = 1, size = 50) => ownerList(`${ownerBase}/servers/${id}/whitelist`, page, size),
  addWhitelist: (id, uid, note) => request('POST', `${ownerBase}/servers/${id}/whitelist`, { uid, note }),
  removeWhitelist: (id, uid) => request('DELETE', `${ownerBase}/servers/${id}/whitelist/${uid}`),
  bans: (id, page = 1, size = 50) => ownerList(`${ownerBase}/servers/${id}/bans`, page, size),
  addBan: (id, body) => request('POST', `${ownerBase}/servers/${id}/bans`, body),
  removeBan: (id, uid) => request('DELETE', `${ownerBase}/servers/${id}/bans/${uid}`),

  credentials: (id) => request('GET', `${ownerBase}/servers/${id}/credentials`),
  createCredential: (id, mode) => request('POST', `${ownerBase}/servers/${id}/credentials`, { mode }),
  rotateCredential: (id, cid, mode) => request('POST', `${ownerBase}/servers/${id}/credentials/${cid}/rotate`, { mode }),
  revokeCredential: (id, cid) => request('POST', `${ownerBase}/servers/${id}/credentials/${cid}/revoke`, {}),
  events: (id, { before, size } = {}) => {
    const query = new URLSearchParams()
    if (before) query.set('before', String(before))
    if (size) query.set('size', String(size))
    const suffix = query.size ? `?${query}` : ''
    return request('GET', `${ownerBase}/servers/${id}/events${suffix}`)
  },
  lookupPlayer: (q) => request('GET', `${ownerBase}/players/lookup?q=${encodeURIComponent(q.trim())}`),

  platformTenants: (page = 1, q = '') => request('GET', `${platformBase}/tenants?page=${page}${q ? `&q=${encodeURIComponent(q)}` : ''}`),
  createPlatformTenant: (body) => request('POST', `${platformBase}/tenants`, body),
  platformServers: (page = 1, q = '') => request('GET', `${platformBase}/servers?page=${page}${q ? `&q=${encodeURIComponent(q)}` : ''}`),
  updatePlatformTenant: (id, body) => request('PATCH', `${platformBase}/tenants/${id}`, body),
  deletePlatformTenant: (id, confirmation) => request('POST', `${platformBase}/tenants/${id}/delete`, { confirmation }),
  updatePlatformServer: (id, body) => request('PATCH', `${platformBase}/servers/${id}`, body),
  revokePlatformCredential: (id, cid) => request('POST', `${platformBase}/servers/${id}/credentials/${cid}/revoke`, {}),
  platformFallback: () => request('GET', `${platformBase}/fallback`),
  setPlatformFallback: (enabled) => request('PUT', `${platformBase}/fallback`, { enabled }),
  platformAudit: (page = 1, q = '') => request('GET', `${platformBase}/audit?page=${page}${q ? `&q=${encodeURIComponent(q)}` : ''}`),
}
