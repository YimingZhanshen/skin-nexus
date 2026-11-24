// Independent server-access client: ordinary same-origin web sessions only.
// It intentionally does not import adminApi or read the machine admin token.
import { fetchWithTimeout } from '../net.js'
import { describe } from '../errors.js'

async function request(method, path, body) {
  const options = { method, credentials: 'same-origin', headers: {} }
  if (body !== undefined) {
    options.headers['Content-Type'] = 'application/json'
    options.body = JSON.stringify(body)
  }
  const response = await fetchWithTimeout(path, options, 20000)
  const data = await response.json().catch(() => ({}))
  if (!response.ok) {
    const error = new Error(describe(data, response.status))
    error.status = response.status
    error.data = data
    throw error
  }
  return data
}

const ownerBase = '/api/serveraccess/v1'
const platformBase = '/api/admin/v1/serveraccess'

export const serverAccessApi = {
  tenantMe: () => request('GET', `${ownerBase}/tenants/me`),
  members: () => request('GET', `${ownerBase}/tenants/me/members`),
  invites: () => request('GET', `${ownerBase}/tenants/me/invites`),
  inviteMember: (email, role) => request('POST', `${ownerBase}/tenants/me/members/invite`, { email, role }),
  removeMember: (uid) => request('DELETE', `${ownerBase}/tenants/me/members/${uid}`),
  revokeInvite: (id) => request('DELETE', `${ownerBase}/tenants/me/invites/${id}`),

  servers: () => request('GET', `${ownerBase}/servers`),
  createServer: (name) => request('POST', `${ownerBase}/servers`, { name }),
  server: (id) => request('GET', `${ownerBase}/servers/${id}`),
  updateServer: (id, body) => request('PATCH', `${ownerBase}/servers/${id}`, body),

  whitelist: (id) => request('GET', `${ownerBase}/servers/${id}/whitelist`),
  addWhitelist: (id, uid, note) => request('POST', `${ownerBase}/servers/${id}/whitelist`, { uid, note }),
  removeWhitelist: (id, uid) => request('DELETE', `${ownerBase}/servers/${id}/whitelist/${uid}`),
  bans: (id) => request('GET', `${ownerBase}/servers/${id}/bans`),
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
  platformServers: (page = 1, q = '') => request('GET', `${platformBase}/servers?page=${page}${q ? `&q=${encodeURIComponent(q)}` : ''}`),
  updatePlatformTenant: (id, body) => request('PATCH', `${platformBase}/tenants/${id}`, body),
  updatePlatformServer: (id, body) => request('PATCH', `${platformBase}/servers/${id}`, body),
  revokePlatformCredential: (id, cid) => request('POST', `${platformBase}/servers/${id}/credentials/${cid}/revoke`, {}),
  platformFallback: () => request('GET', `${platformBase}/fallback`),
  setPlatformFallback: (enabled) => request('PUT', `${platformBase}/fallback`, { enabled }),
  platformAudit: (page = 1, q = '') => request('GET', `${platformBase}/audit?page=${page}${q ? `&q=${encodeURIComponent(q)}` : ''}`),
}
