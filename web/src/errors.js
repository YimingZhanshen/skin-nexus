// errors.js turns an API failure into the sentence the user reads.
//
// The backend answers `{error, code, message}`: `code` is the stable, translatable
// half, `message` is what a client that does not know the code falls back to.
// Resolving it here instead of at the ~90 call sites means a new code needs one
// dictionary entry and nothing else.
import { i18n } from './i18n.js'

// Envelope keys are not interpolation parameters.
const RESERVED = new Set(['error', 'message', 'code'])

// describe names the failure in the user's language. An unknown code falls
// through to the server's message, which is what lets the backend ship a code
// ahead of the dictionary that knows it.
export function describe(data, status) {
  const code = data && typeof data.code === 'string' ? data.code : ''
  if (code && i18n.global.te(`errors.${code}`)) {
    const params = {}
    for (const [k, v] of Object.entries(data)) {
      if (RESERVED.has(k)) continue
      // A list has no sensible default rendering in a sentence — vue-i18n would
      // print it as JSON. Names are what the message wants.
      params[k] = Array.isArray(v) ? v.join(', ') : v
    }
    return i18n.global.t(`errors.${code}`, params)
  }
  const text = data && (data.message || data.error)
  return text || `HTTP ${status}`
}

// transportError is for the failures that never produced a response body:
// 'network' and 'networkTimeout'.
export function transportError(kind) {
  return i18n.global.t(`errors.${kind}`)
}
