/** Byte/number/uptime formatting shared by the console pages. */

const BYTE_UNITS = ['B', 'KB', 'MB', 'GB', 'TB', 'PB']

export function formatBytes(value: unknown, digits = 2): string {
  let n = Number(value)
  if (!Number.isFinite(n) || n < 0) n = 0
  if (n < 1024) return `${Math.round(n)} B`
  let i = 0
  while (n >= 1024 && i < BYTE_UNITS.length - 1) {
    n /= 1024
    i += 1
  }
  const text = n >= 100 ? n.toFixed(0) : n.toFixed(digits)
  return `${text.replace(/\.0+$/, '')} ${BYTE_UNITS[i]}`
}

export function formatNumber(value: unknown): string {
  const n = Number(value)
  if (!Number.isFinite(n)) return '--'
  return n.toLocaleString('en-US')
}

/**
 * Compact, language-neutral duration ("42s", "5m 12s", "3h 04m", "2d 5h").
 *
 * The Vue console formats uptime the same way. Spelled-out Chinese units
 * cannot survive the switch to English: the translator sees a sentence it was
 * never given a pattern for and mangles it into "5 12 s".
 */
export function formatUptime(seconds: unknown): string {
  let s = Math.max(0, Math.floor(Number(seconds) || 0))
  if (s < 60) return `${s}s`
  const day = Math.floor(s / 86400)
  s -= day * 86400
  const hour = Math.floor(s / 3600)
  s -= hour * 3600
  const minute = Math.floor(s / 60)
  if (day > 0) return `${day}d ${hour}h`
  if (hour > 0) return `${hour}h ${String(minute).padStart(2, '0')}m`
  return `${minute}m ${s % 60}s`
}

export function percent(part: unknown, total: unknown): number {
  const p = Number(part) || 0
  const t = Number(total) || 0
  if (t <= 0) return 0
  return Math.min(100, Math.round((p / t) * 100))
}
