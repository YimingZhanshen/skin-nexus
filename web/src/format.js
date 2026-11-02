// Shared value formatters. Two things these replace: a byte formatter that
// existed twice with different rounding (the same texture read "1.5 MB" in one
// list and "2 MB" in another), and dates rendered with the browser's language
// while the whole rest of the screen was in the site's language.
import { i18n } from './i18n.js'

export function formatBytes(n) {
  if (n === null || n === undefined || n === '') return '—'
  const units = ['B', 'KB', 'MB', 'GB', 'TB']
  let i = 0
  let v = Number(n)
  if (!Number.isFinite(v)) return String(n)
  while (v >= 1024 && i < units.length - 1) { v /= 1024; i++ }
  return `${v >= 10 || i === 0 ? Math.round(v) : v.toFixed(1)} ${units[i]}`
}

export function formatWhen(value) {
  const d = value instanceof Date ? value : new Date(value)
  if (Number.isNaN(d.getTime())) return String(value ?? '')
  return new Intl.DateTimeFormat(i18n.global.locale.value, { dateStyle: 'medium', timeStyle: 'short' }).format(d)
}
