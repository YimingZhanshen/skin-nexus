// i18n: vue-i18n setup. Locale resolution: localStorage.skin_lang ->
// navigator.language -> zh-CN. Fallback is always zh-CN.
//
// The resolved locale is mirrored into a `skin_lang` cookie because the
// server-rendered pages (texture detail, landing, device) pick their language
// from it — without the mirror, switching to English in the app would leave
// every crawlable page in Chinese. Display preference, not a secret: no
// HttpOnly (the SPA must write it), SameSite=Lax (a launcher opening
// /device.html is a top-level navigation and must still carry it), Secure only
// over https, and a year-long lifetime to match the localStorage entry.
import { createI18n } from 'vue-i18n'
import zhCN from './locales/zh-CN.json'
import en from './locales/en.json'

const LANG_COOKIE = 'skin_lang'

function writeLangCookie(locale) {
  const secure = location.protocol === 'https:' ? '; Secure' : ''
  document.cookie = `${LANG_COOKIE}=${locale}; Path=/; Max-Age=31536000; SameSite=Lax${secure}`
}

function initialLocale() {
  const saved = localStorage.getItem(LANG_COOKIE)
  if (saved && (saved === 'zh-CN' || saved === 'en')) return saved
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
