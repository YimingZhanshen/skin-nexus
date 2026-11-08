// i18n: vue-i18n setup. Locale resolution: localStorage.skin_lang ->
// skin_lang cookie -> navigator.language -> zh-CN. Fallback is always zh-CN.
//
// The resolved locale is mirrored into a `skin_lang` cookie because the
// server-rendered pages (texture detail, landing, device) pick their language
// from it — without the mirror, switching to English in the app would leave
// every crawlable page in Chinese. The same cookie is then read back here, so
// the choice made on a server-rendered page survives the jump into the app:
// ignoring it and re-writing it from localStorage/navigator would erase the
// visitor's language on the first SPA load. Display preference, not a secret:
// no HttpOnly (the SPA must write and read it), SameSite=Lax (a launcher
// opening /device.html is a top-level navigation and must still carry it),
// Secure only over https, and a year-long lifetime to match localStorage.
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
  const saved = asLocale(localStorage.getItem(LANG_COOKIE))
  if (saved) return saved
  const mirrored = readLangCookie()
  if (mirrored) return mirrored
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
