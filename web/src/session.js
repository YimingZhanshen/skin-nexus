// Session-wide unauthorized notifier: pages call notifyUnauthorized() when a
// 401 hits their guard; App.vue registered the handler that drops the shell
// back to the login card (review P1-6 semantics, now cross-component).
let handler = null
export function setUnauthorizedHandler(fn) {
  const previous = handler
  handler = fn
  // App may be mounted more than once in tests or embedded shells. Restoring
  // must not overwrite a newer handler installed after this one.
  return () => { if (handler === fn) handler = previous }
}
export function notifyUnauthorized() { if (handler) handler() }

export function clearLegacyAdminToken() {
  try {
    if (typeof localStorage !== 'undefined' && typeof localStorage.removeItem === 'function') {
      localStorage.removeItem('admin_token')
    }
  } catch { /* blocked storage must not break the session flow */ }
}

export function isSessionLoss(e) {
  return !!e && (e.status === 401 || (e.status === 403 && e.data && e.data.code === 'admin.token_invalid'))
}

// Shared session-loss guard for page-level catch blocks: true when the error
// invalidated the session (caller should stop its own error toast).
export function guard401(e) {
  if (!isSessionLoss(e)) return false
  notifyUnauthorized()
  return true
}
