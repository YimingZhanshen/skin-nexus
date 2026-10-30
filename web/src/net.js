// fetch with a hard timeout. A stalled CDN edge must surface as a retryable
// error instead of leaving a page spinning forever.
export async function fetchWithTimeout(url, opts = {}, timeoutMS = 15000) {
  const ctl = new AbortController()
  const timer = setTimeout(() => ctl.abort(), timeoutMS)
  try {
    return await fetch(url, { ...opts, signal: ctl.signal })
  } catch (e) {
    if (e && e.name === 'AbortError') {
      const err = new Error('请求超时，请重试')
      err.code = 'timeout'
      err.status = 0
      throw err
    }
    throw e
  } finally {
    clearTimeout(timer)
  }
}
