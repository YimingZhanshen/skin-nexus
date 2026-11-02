// fetch with a hard timeout. A stalled CDN edge must surface as a retryable
// error instead of leaving a page spinning forever.
import { transportError } from './errors.js'

export async function fetchWithTimeout(url, opts = {}, timeoutMS = 15000) {
  const ctl = new AbortController()
  const timer = setTimeout(() => ctl.abort(), timeoutMS)
  try {
    return await fetch(url, { ...opts, signal: ctl.signal })
  } catch (e) {
    if (e && e.name === 'AbortError') {
      // Named, then translated: a literal sentence here bypassed the whole
      // dictionary and served Chinese to an English UI on a slow link.
      const err = new Error(transportError('networkTimeout'))
      err.code = 'networkTimeout'
      err.status = 0
      throw err
    }
    throw e
  } finally {
    clearTimeout(timer)
  }
}
