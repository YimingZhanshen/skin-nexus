// useGoto.js — the view the shell is on, and every transition between views.
//
// The URL half of the same problem (viewURLs, legacyRoutes, the pushState
// watch, popstate and the cold-boot route) deliberately stays in App.vue: the
// source gates read those out of that file, and the address bar is the one
// surface a shared link depends on, so it keeps living next to the shell that
// serves it. This module is the other half — which view, and how a page hands
// the player to another one without a reload that drops the boot state.
import { ref } from 'vue'
import { pageByPath } from '../pluginUI.js'

export function useGoto({ isAdmin, user }) {
  // login | register | setup | device | dashboard | closet | players |
  // profile | apps | union | admin
  const view = ref('login')
  const extPath = ref('')
  const pendingExt = ref('')
  // mobile topbar drawer (≤640px hamburger)
  const menuOpen = ref(false)
  // Deep-linked view held for after login (open /user/closet logged-out → login
  // card first, then drop straight into the closet).
  const pendingView = ref('')

  function openExt(path) {
    const page = pageByPath(path)
    if (page && page.area === 'admin' && !isAdmin.value) {
      view.value = 'dashboard'
      return
    }
    extPath.value = path
    view.value = 'ext'
    if (location.pathname !== path) history.pushState({ ext: path }, '', path)
  }

  // Same-origin ?next target as an absolute href, or '' when absent or foreign.
  // Decided by the browser's own URL parser rather than a hand-written prefix
  // rule: `/\evil.com` starts with one "/" and is not "//", yet resolves to
  // another host. Origin equality also rejects javascript: URIs.
  function sameOriginNext() {
    const next = new URLSearchParams(location.search).get('next')
    if (!next) return ''
    const target = new URL(next, location.origin)
    return target.origin === location.origin ? target.href : ''
  }

  // 登录/注册共同收尾：?next= 回跳（详情页 viewer 401 引导来的）优先整页跳回；
  // 其次设备页流程、launcher 深链恢复；落地即清 pendingView（防"迟到恢复"跳页）。
  function finishAuth() {
    const href = sameOriginNext()
    if (href) {
      pendingView.value = ''
      location.href = href
      return
    }
    if (location.pathname === '/device') view.value = 'device'
    else if (pendingExt.value) { const p = pendingExt.value; pendingExt.value = ''; openExt(p) }
    // A panel deep link survives the login card unchanged in the address bar, so
    // the URL itself is the resume target — more robust than remembering a view
    // name, and it keeps the tab/plugin segment AdminPanel is about to read.
    else if (location.pathname === '/admin' || location.pathname.startsWith('/admin/')) view.value = 'admin'
    else if (pendingView.value) { view.value = pendingView.value; pendingView.value = '' }
    else view.value = 'dashboard'
  }

  // Is another flow about to take the screen? The gated-signup landing asks this
  // before stepping in, so an OAuth or device hand-off is never hijacked — it
  // reads exactly the conditions finishAuth acts on above.
  function authWaitingElsewhere() {
    return !!sameOriginNext() || location.pathname === '/device' || !!pendingExt.value || !!pendingView.value
  }

  // 皮肤库客访点 ♥ 等需登录操作：登录后回到皮肤库（pendingView 闭环）
  function skinlibLogin() { pendingView.value = 'skinlib'; view.value = 'login' }

  // 品牌点击 = 回工作台（URL 由 App 的 watch 同步为 /user/dashboard）
  function goHome() {
    view.value = user.value ? 'dashboard' : 'login'
  }

  function backToDashboard() {
    // restore the site root URL when leaving the device page
    if (location.pathname === '/device') history.replaceState(null, '', '/')
    view.value = 'dashboard'
  }

  return {
    view, extPath, pendingExt, pendingView, menuOpen,
    openExt, finishAuth, authWaitingElsewhere, skinlibLogin, goHome, backToDashboard,
    sameOriginNext,
  }
}
