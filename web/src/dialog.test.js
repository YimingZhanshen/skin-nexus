import test, { afterEach } from 'node:test'
import assert from 'node:assert/strict'
import { alertDialog, confirmDialog, dialogState, promptDialog, resolveDialog } from './dialog.js'

afterEach(() => resolveDialog(null))

test('concurrent prompts and confirmations resolve independently in FIFO order', async () => {
  const results = []
  const first = promptDialog('First prompt', 'draft').then((value) => results.push(['prompt', value]))
  const second = confirmDialog('Second confirmation', { danger: true }).then((value) => results.push(['confirm', value]))

  assert.equal(dialogState.open, true)
  assert.equal(dialogState.kind, 'prompt')
  assert.equal(dialogState.message, 'First prompt')
  assert.equal(dialogState.value, 'draft')

  resolveDialog('first answer')
  await Promise.resolve()
  assert.deepEqual(results, [['prompt', 'first answer']])
  assert.equal(dialogState.open, true)
  assert.equal(dialogState.kind, 'confirm')
  assert.equal(dialogState.message, 'Second confirmation')
  assert.equal(dialogState.danger, true)

  resolveDialog(false)
  await Promise.all([first, second])
  assert.deepEqual(results, [['prompt', 'first answer'], ['confirm', false]])
  assert.equal(dialogState.open, false)
})

test('canceling the active prompt pumps the next confirmation without dangling resolvers', async () => {
  const results = []
  const first = promptDialog('Delete exact name?').then((value) => results.push(['prompt', value]))
  const second = confirmDialog('Continue?').then((value) => results.push(['confirm', value]))
  const third = alertDialog('Finished').then((value) => results.push(['alert', value]))

  resolveDialog(null)
  await Promise.resolve()
  assert.deepEqual(results, [['prompt', null]])
  assert.equal(dialogState.kind, 'confirm')

  resolveDialog(true)
  await Promise.resolve()
  assert.deepEqual(results, [['prompt', null], ['confirm', true]])
  assert.equal(dialogState.kind, 'alert')
  assert.equal(dialogState.open, true)

  resolveDialog(true)
  await Promise.all([first, second, third])
  assert.deepEqual(results, [['prompt', null], ['confirm', true], ['alert', true]])
  assert.equal(dialogState.open, false)

  // A stale close with no active entry must not consume a future resolver.
  resolveDialog(false)
  const next = confirmDialog('Next dialog')
  assert.equal(dialogState.message, 'Next dialog')
  resolveDialog(true)
  assert.equal(await next, true)
})
