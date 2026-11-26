// useSession.js — who is signed in, and the two things that can stop a
// sign-in attempt: the one-in-flight guard shared by the four auth cards, and
// the lockout countdown the server's 429 carries.
//
// The lock is a deadline, not a counter: a backgrounded tab gets its timer
// throttled to one tick a minute, and a counter would then show a frozen
// "804 seconds" that has long since expired. Reading the clock each tick stays
// correct whenever the page does get to run, and switching language
// re-renders the sentence at once.
//
// App.vue provides the refs to the tree under the frozen keys `sessionUser`
// (this module's `user`) and `authState`; pages inject those, never this file.
import { ref, computed, onBeforeUnmount } from 'vue'
import { useI18n } from 'vue-i18n'
import { api } from '../api.js'
import { bumpSessionVersion, clearLegacyAdminToken, isSessionLoss, notifyUnauthorized } from '../session.js'

export function useSession() {
  const { t } = useI18n()

  const user = ref(null)
  let generation = 0
  // Every set invalidates older /me work, but only an authentication identity
  // change invalidates in-flight transport responses from the previous account.
  function setSessionUser(next) {
    const previous = user.value
    const nextUser = next || null
    generation += 1
    if ((previous === null) !== (nextUser === null) || previous?.id !== nextUser?.id) {
      bumpSessionVersion()
    }
    clearLegacyAdminToken()
    user.value = nextUser
  }
  // Admin panel entry (blessing parity): only accounts with permission > 0 see
  // it — normal users never do. `permission` comes from GET /api/v1/me
  // (0 = normal, >0 = admin, -1 = banned).
  const isAdmin = computed(() => Number(user.value && user.value.permission) > 0)

  const authErr = ref('')
  const authBusy = ref(false)
  const lockUntil = ref(0)
  const nowTick = ref(Date.now())
  let tickTimer = null
  const locked = computed(() => lockUntil.value > nowTick.value)
  const lockSecondsLeft = computed(() => Math.max(0, Math.ceil((lockUntil.value - nowTick.value) / 1000)))
  function waitText() { return t('auth.lockedSeconds', { n: lockSecondsLeft.value }) }
  function startWait(sec) {
    const until = Date.now() + sec * 1000
    if (until <= lockUntil.value) return // never shorten a lock the page already knows
    lockUntil.value = until
    nowTick.value = Date.now()
    if (tickTimer) return
    tickTimer = setInterval(() => {
      nowTick.value = Date.now()
      if (lockUntil.value <= nowTick.value) { clearInterval(tickTimer); tickTimer = null }
    }, 1000)
  }
  onBeforeUnmount(() => {
    generation += 1
    if (tickTimer) clearInterval(tickTimer)
  })

  // The verdict, not the flag. /me, /login and /register all carry
  // `verifyRequired`, so the shell knows before it draws anything whether this
  // session is a member or a guest who happens to hold a cookie. The server is
  // the one refusing; this only decides what the player is shown while that is
  // true — a banner inside a usable panel would read as a suggestion.
  const verifyPending = computed(() => !!(user.value && user.value.verifyRequired === true))
  const pendingBusy = ref(false)
  const pendingNote = ref('')
  const pendingChangeOpen = ref(false)
  const pendingEmail = ref('')

  // The gated-signup landing carries this screen: no token is involved, the
  // server already minted one and tried to mail it.
  const verifyMailTo = ref('')
  const verifyMailBusy = ref(false)
  const verifyMailNote = ref('')

  // Page-level account reads share the shell's generation gate. A stale value
  // (including a stale 401) is discarded before it can mutate page or shell
  // state; only a current session-loss response notifies the shell.
  // Keep the generation beside the value until the caller commits it. A plain
  // value alone leaves a microtask gap in which another account can be selected
  // after validation but before the shell is updated.
  async function readMeResult() {
    const current = ++generation
    try {
      const next = await api.me()
      return { current, next: current === generation ? next : null }
    } catch (cause) {
      if (current !== generation) return { current, next: null }
      if (isSessionLoss(cause)) {
        setSessionUser(null)
        notifyUnauthorized()
        return { current, next: null }
      }
      // Network/5xx failures say nothing about the current cookie. Callers
      // that own an error surface decide whether to display the failure.
      throw cause
    }
  }

  // Page-level reads expose only the account value, never the internal token.
  async function readMe() {
    const { next } = await readMeResult()
    return next
  }

  async function refreshMe(options) {
    try {
      const { current, next } = await readMeResult()
      if (!next || current !== generation) return false
      setSessionUser(next)
      return true
    } catch (cause) {
      // Boot asks for propagation so it can show a retryable error; background
      // refreshes keep the good session and stay quiet.
      if (options && options.propagateErrors) throw cause
      return false
    }
  }

  async function pendingResend() {
    if (pendingBusy.value) return
    pendingBusy.value = true
    pendingNote.value = ''
    try {
      await api.requestEmailVerification()
      pendingNote.value = t('auth.verifySent')
    } catch (e) { pendingNote.value = e.message } finally { pendingBusy.value = false }
    await refreshMe()
  }

  async function pendingSaveEmail() {
    if (pendingBusy.value) return
    const next = pendingEmail.value.trim()
    if (!next) { pendingNote.value = t('auth.pendingEmailMissing'); return }
    pendingBusy.value = true
    pendingNote.value = ''
    try {
      // The nickname rides along unchanged: the route takes both fields, and the
      // address is the only thing a pending account is allowed to repair.
      await api.updateProfile(user.value.nickname, next)
      pendingChangeOpen.value = false
      pendingEmail.value = ''
      // Changing the address does not mail anything by itself (the server sends
      // only when asked), so the client asks right here. The note below is then
      // what the transport actually reported — not a promise that a link is on
      // its way.
      try {
        await api.requestEmailVerification()
        pendingNote.value = t('auth.pendingSaved')
      } catch (e) { pendingNote.value = t('auth.pendingSavedNoSend', { message: e.message }) }
    } catch (e) { pendingNote.value = e.message } finally { pendingBusy.value = false }
    await refreshMe()
  }

  async function resendVerificationMail() {
    if (verifyMailBusy.value) return
    verifyMailBusy.value = true
    verifyMailNote.value = ''
    try {
      await api.requestEmailVerification()
      verifyMailNote.value = t('auth.verifySent')
    } catch (e) { verifyMailNote.value = e.message } finally { verifyMailBusy.value = false }
  }

  // The inbox clears its badge locally when the page marks rows read; the count
  // is re-fetched on the next boot anyway.
  function onInboxRead() {
    if (user.value) setSessionUser({ ...user.value, unreadNotifications: 0 })
  }

  return {
    user, isAdmin, authErr, authBusy,
    locked, lockSecondsLeft, waitText, startWait,
    verifyPending, pendingBusy, pendingNote, pendingChangeOpen, pendingEmail,
    verifyMailTo, verifyMailBusy, verifyMailNote,
    readMe, refreshMe, setSessionUser, pendingResend, pendingSaveEmail, resendVerificationMail, onInboxRead,
  }
}
