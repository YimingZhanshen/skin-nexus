import test from 'node:test'
import assert from 'node:assert/strict'
import { bumpSessionVersion, captureSessionVersion, guard401, notifyUnauthorized, setUnauthorizedHandler } from './session.js'

function responseError(status, code) {
  return Object.assign(new Error(code || `HTTP ${status}`), { status, data: code ? { code } : {} })
}

test('401 and admin.token_invalid are session loss and notify the shell', () => {
  const notifications = []
  setUnauthorizedHandler(() => notifications.push('unauthorized'))
  try {
    assert.equal(guard401(responseError(401)), true)
    assert.equal(guard401(responseError(403, 'admin.token_invalid')), true)
    assert.deepEqual(notifications, ['unauthorized', 'unauthorized'])
  } finally {
    setUnauthorizedHandler(null)
  }
})

test('authorization, module, server, and network failures do not end the session', () => {
  const notifications = []
  setUnauthorizedHandler(() => notifications.push('unauthorized'))
  try {
    for (const error of [
      responseError(403, 'admin.super_admin_required'),
      responseError(403, 'admin.web_session_required'),
      responseError(403, 'serveraccess.forbidden'),
      responseError(403),
      responseError(404, 'serveraccess.not_found'),
      responseError(500),
      responseError(0, 'networkTimeout'),
      null,
    ]) {
      assert.equal(guard401(error), false)
    }
    assert.deepEqual(notifications, [])
  } finally {
    setUnauthorizedHandler(null)
  }
})

test('a session-loss response from an older session epoch is ignored', () => {
  const notifications = []
  setUnauthorizedHandler(() => notifications.push('unauthorized'))
  try {
    const first = captureSessionVersion()
    const second = bumpSessionVersion()
    assert.ok(second > first)
    assert.equal(captureSessionVersion(), second)
    assert.equal(guard401(responseError(401), first), false)
    assert.equal(guard401(responseError(403, 'admin.token_invalid'), first), false)
    assert.deepEqual(notifications, [])
    assert.equal(guard401(responseError(401), second), true)
    assert.deepEqual(notifications, ['unauthorized'])
  } finally {
    setUnauthorizedHandler(null)
  }
})

test('notifyUnauthorized invokes only the currently registered handler', () => {
  const calls = []
  setUnauthorizedHandler(() => calls.push('first'))
  notifyUnauthorized()
  setUnauthorizedHandler(() => calls.push('second'))
  notifyUnauthorized()
  setUnauthorizedHandler(null)
  notifyUnauthorized()
  assert.deepEqual(calls, ['first', 'second'])
})
