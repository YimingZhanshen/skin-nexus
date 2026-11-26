// Browser administration is session-only. Machine tokens remain a backend API
// contract for cookie-less clients, but are never persisted or sent here.
import { fetchWithTimeout } from './net.js'
import { describe } from './errors.js'
import { captureSessionVersion, clearLegacyAdminToken, guard401 } from './session.js'

clearLegacyAdminToken()

export async function fetchJSON(path, opts) {
  // Also remove a key written by an older open tab; it is never read or sent.
  clearLegacyAdminToken()
  const epoch = captureSessionVersion()
  const headers = {}
  for (const [key, value] of Object.entries((opts && opts.headers) || {})) {
    if (key.toLowerCase() !== 'x-admin-token') headers[key] = value
  }
  const res = await fetchWithTimeout(path, { ...opts, credentials: 'same-origin', headers }, 20000)
  const data = await res.json().catch(() => ({}))
  if (!res.ok) {
    const err = new Error(describe(data, res.status))
    err.status = res.status
    err.data = data
    guard401(err, epoch)
    throw err
  }
  return data
}
