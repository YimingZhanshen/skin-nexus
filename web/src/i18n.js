// i18n: vue-i18n setup. Locale resolution: skin_lang cookie ->
// localStorage.skin_lang -> navigator.language -> zh-CN. Fallback is always zh-CN.
//
// Both layers write the same two records: the SPA writes the cookie alongside
// localStorage, and a server-rendered page writes the cookie when the visitor
// clicks 中文/English on it. The cookie therefore carries whichever choice was
// made *last*, which is why it is read first — a switch made on a
// server-rendered page has to survive the jump into the app. localStorage comes
// second and only matters where cookies are refused, because the server cannot
// see it and a browser that blocks cookies cannot be given a mirror.
//
// The cookie is not HttpOnly for exactly that reason: the SPA must read and
// write it. SameSite=Lax, because a launcher opening /device.html is a top-level
// navigation and must still carry it; Secure only over https; a year-long
// lifetime to match localStorage. Display preference, not a secret.
import { createI18n } from 'vue-i18n'
import zhCN from './locales/zh-CN.json'
import en from './locales/en.json'

const LANG_COOKIE = 'skin_lang'
const KNOWN_LOCALES = ['zh-CN', 'en']

// A locale we ship, or the empty string. Cookie values are compared raw: both
// accepted spellings are percent-escape-free, so decoding only adds a throw
// path on a hand-edited cookie — at module load, where a throw is a blank page.
function asLocale(value) {
  return KNOWN_LOCALES.includes(value) ? value : ''
}

function writeLangCookie(locale) {
  const secure = location.protocol === 'https:' ? '; Secure' : ''
  document.cookie = `${LANG_COOKIE}=${locale}; Path=/; Max-Age=31536000; SameSite=Lax${secure}`
}

function readLangCookie() {
  for (const pair of document.cookie.split(';')) {
    const eq = pair.indexOf('=')
    if (eq < 0 || pair.slice(0, eq).trim() !== LANG_COOKIE) continue
    return asLocale(pair.slice(eq + 1).trim())
  }
  return ''
}

function initialLocale() {
  const shared = readLangCookie()
  if (shared) return shared
  const saved = asLocale(localStorage.getItem(LANG_COOKIE))
  if (saved) return saved
  const nav = (navigator.language || '').toLowerCase()
  return nav.startsWith('en') ? 'en' : 'zh-CN'
}

export const i18n = createI18n({
  legacy: false,
  locale: initialLocale(),
  fallbackLocale: 'zh-CN',
  messages: { 'zh-CN': zhCN, en },
})

document.documentElement.lang = i18n.global.locale.value
writeLangCookie(i18n.global.locale.value)

export function setLocale(locale) {
  i18n.global.locale.value = locale
  localStorage.setItem(LANG_COOKIE, locale)
  document.documentElement.lang = locale
  writeLangCookie(locale)
}
