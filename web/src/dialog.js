// dialog.js is the promise-based replacement for the browser's own
// prompt/confirm/alert. Those 42 call sites cannot stay native for two reasons
// that both end in a silent wrong answer: a sandboxed plugin frame is not
// allowed to open a modal dialog at all (the host proxies the request out, and
// `window.confirm` in the parent is itself suppressed in a background tab), and
// a suppressed dialog resolves as "dismissed" — so a delete asking for
// confirmation quietly deletes nothing, or worse, a plugin's confirmation reads
// as user consent that nobody gave.
//
// The API mirrors the native one so call sites keep their shape: prompt returns
// the text or null, confirm returns true/false, alert just acknowledges.
import { reactive, readonly } from 'vue'

const state = reactive({
  kind: '', // 'prompt' | 'confirm' | 'alert'
  open: false,
  title: '',
  message: '',
  value: '',
  danger: false,
})

const queue = []
let active = null

function show(entry) {
  active = entry
  Object.assign(state, entry.options, { open: true })
}

function open(next) {
  return new Promise((resolve) => {
    const entry = { options: next, resolve }
    if (active) queue.push(entry)
    else show(entry)
  })
}

// Only the active entry can settle. Once it does, the next queued dialog owns
// the resolver and is shown immediately; stale calls with no active entry do
// nothing and cannot consume a future dialog's resolver.
export function resolveDialog(result) {
  if (!active) return
  const done = active
  active = null
  state.open = false
  done.resolve(result)
  const next = queue.shift()
  if (next) show(next)
}

export const dialogState = readonly(state)

export function promptDialog(message, defaultValue = '', opts = {}) {
  return open({ kind: 'prompt', message, value: defaultValue, title: opts.title || '', danger: !!opts.danger })
}

export function confirmDialog(message, opts = {}) {
  return open({ kind: 'confirm', message, title: opts.title || '', danger: !!opts.danger })
}

export function alertDialog(message, opts = {}) {
  return open({ kind: 'alert', message, title: opts.title || '', danger: !!opts.danger })
}
