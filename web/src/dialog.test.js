import test, { afterEach } from 'node:test'
import assert from 'node:assert/strict'
import { nextTick } from 'vue'
import { alertDialog, confirmDialog, dialogState, promptDialog, resolveDialog } from './dialog.js'

afterEach(async () => {
  for (let i = 0; i < 4; i++) {
    resolveDialog(null)
    await nextTick()
  }
})

test('concurrent prompts and confirmations resolve independently in FIFO order', async () => {
  const results = []
  const first = promptDialog('First prompt', 'draft').then((value) => results.push(['prompt', value]))
  const second = confirmDialog('Second confirmation', { danger: true }).then((value) => results.push(['confirm', value]))

  assert.equal(dialogState.open, true)
  assert.equal(dialogState.kind, 'prompt')
  assert.equal(dialogState.message, 'First prompt')
  assert.equal(dialogState.value, 'draft')

  resolveDialog('first answer')
  await nextTick()
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

test('dialogs opened during the asynchronous close handoff stay queued in FIFO order', async () => {
  const results = []
  const first = promptDialog('First prompt', 'draft').then((value) => results.push(['first', value]))
  const second = confirmDialog('Second confirmation').then((value) => results.push(['second', value]))

  resolveDialog('first answer')
  assert.equal(dialogState.open, false, 'the current dialog must close before the next one opens')
  resolveDialog('duplicate answer')
  const third = alertDialog('Third alert').then((value) => results.push(['third', value]))
  assert.equal(dialogState.open, false, 'a newly opened dialog must wait for the handoff')
  assert.deepEqual(results, [])

  await nextTick()
  assert.deepEqual(results, [['first', 'first answer']])
  assert.equal(dialogState.open, true)
  assert.equal(dialogState.kind, 'confirm')
  assert.equal(dialogState.message, 'Second confirmation')

  resolveDialog(false)
  assert.equal(dialogState.open, false)
  resolveDialog('duplicate second answer')
  await nextTick()
  assert.deepEqual(results, [['first', 'first answer'], ['second', false]])
  assert.equal(dialogState.open, true)
  assert.equal(dialogState.kind, 'alert')
  assert.equal(dialogState.message, 'Third alert')

  resolveDialog(true)
  assert.equal(dialogState.open, false)
  await nextTick()
  await Promise.all([first, second, third])
  assert.deepEqual(results, [['first', 'first answer'], ['second', false], ['third', true]])
  assert.equal(dialogState.open, false, 'an empty queue must leave the dialog closed')
})

test('canceling the active prompt pumps the next confirmation without dangling resolvers', async () => {
  const results = []
  const first = promptDialog('Delete exact name?').then((value) => results.push(['prompt', value]))
  const second = confirmDialog('Continue?').then((value) => results.push(['confirm', value]))
  const third = alertDialog('Finished').then((value) => results.push(['alert', value]))

  resolveDialog(null)
  await nextTick()
  assert.deepEqual(results, [['prompt', null]])
  assert.equal(dialogState.kind, 'confirm')

  resolveDialog(true)
  await nextTick()
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
