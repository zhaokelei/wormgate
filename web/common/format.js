/**
 * Human-readable byte counts.
 *
 * Callers are not always well-typed: ECharts axis formatters may hand over a
 * `[value, index]` tuple, and API payloads may contain numeric strings or
 * null. Normalise here so every call site stays safe.
 */
export function formatBytes(bytes, digits = 1) {
  const raw = Array.isArray(bytes) ? bytes[0] : bytes
  const n = Number(raw)
  if (!Number.isFinite(n) || n <= 0) return '0 B'
  const units = ['B', 'KB', 'MB', 'GB', 'TB', 'PB']
  // 0 < n < 1 makes floor(log1024(n)) negative, which indexes units[-1] and
  // renders "922 undefined" on a chart axis. Clamp BOTH ends of the exponent.
  const i = Math.max(0, Math.min(Math.floor(Math.log(n) / Math.log(1024)), units.length - 1))
  const value = n / Math.pow(1024, i)
  return `${value.toFixed(i === 0 ? 0 : digits)} ${units[i]}`
}

export function formatRate(bytesPerSecond) {
  return `${formatBytes(bytesPerSecond, 1)}/s`
}

export function formatNumber(n) {
  return new Intl.NumberFormat('en-US').format(Number(n) || 0)
}

export function formatDuration(seconds) {
  const s = Math.max(0, Math.floor(Number(seconds) || 0))
  if (s < 60) return `${s}s`
  const m = Math.floor(s / 60)
  if (m < 60) return `${m}m ${s % 60}s`
  const h = Math.floor(m / 60)
  if (h < 24) return `${h}h ${m % 60}m`
  const d = Math.floor(h / 24)
  return `${d}d ${h % 24}h`
}

/**
 * Compact uptime for tight spaces ("41d 3h" -> "41d 3h", "2h 5m", "38s").
 * Unlike formatDuration this never returns two units for sub-hour values, so it
 * fits a rail chip without wrapping.
 */
export function formatUptime(seconds) {
  const s = Math.max(0, Math.floor(Number(seconds) || 0))
  if (s < 60) return `${s}s`
  const m = Math.floor(s / 60)
  if (m < 60) return `${m}m`
  const h = Math.floor(m / 60)
  if (h < 24) return `${h}h ${m % 60}m`
  const d = Math.floor(h / 24)
  return `${d}d ${h % 24}h`
}

export function formatClock(ts) {
  if (!ts) return '--:--:--'
  return new Date(ts).toLocaleTimeString('zh-CN', { hour12: false })
}

export function formatTime(ts) {
  if (!ts) return '--'
  const d = new Date(ts)
  return d.toLocaleString('zh-CN', {
    month: '2-digit',
    day: '2-digit',
    hour: '2-digit',
    minute: '2-digit',
    second: '2-digit',
    hour12: false
  })
}

export function formatDate(ts) {
  if (!ts) return '--'
  return new Date(ts).toLocaleString('zh-CN', {
    year: 'numeric',
    month: '2-digit',
    day: '2-digit',
    hour: '2-digit',
    minute: '2-digit',
    hour12: false
  })
}

export function fromNow(ts) {
  if (!ts) return '--'
  const diff = Date.now() - new Date(ts).getTime()
  if (diff < 5000) return '刚刚'
  if (diff < 60000) return `${Math.floor(diff / 1000)} 秒前`
  if (diff < 3600000) return `${Math.floor(diff / 60000)} 分钟前`
  if (diff < 86400000) return `${Math.floor(diff / 3600000)} 小时前`
  return formatDate(ts)
}

export function shortId(id, n = 8) {
  if (!id) return '--'
  return id.length <= n ? id : `${id.slice(0, n)}…`
}

export function formatPercent(part, total) {
  if (!total) return '0%'
  return `${Math.min(100, (part / total) * 100).toFixed(1)}%`
}

/**
 * Copy text to the clipboard, returning false instead of throwing.
 *
 * `navigator.clipboard` is only exposed in a secure context, so it is
 * `undefined` whenever the console is opened over plain HTTP on an IP or a
 * non-localhost host — which is exactly how operators reach the admin UI
 * (`http://<public-ip>:7100`). Falling straight through to the legacy
 * `execCommand` path keeps those deployments working.
 */
export async function copyText(text) {
  const value = text == null ? '' : String(text)
  if (!value) return false

  if (navigator.clipboard?.writeText) {
    try {
      await navigator.clipboard.writeText(value)
      return true
    } catch {
      // Permission denied or a document that is not focused yet: try the
      // synchronous path rather than failing the click.
    }
  }

  try {
    const area = document.createElement('textarea')
    area.value = value
    area.setAttribute('readonly', '')
    area.style.position = 'fixed'
    area.style.top = '0'
    area.style.left = '0'
    area.style.width = '1px'
    area.style.height = '1px'
    area.style.padding = '0'
    area.style.border = 'none'
    area.style.opacity = '0'
    document.body.appendChild(area)
    const selection = document.getSelection()
    const previous = selection && selection.rangeCount > 0 ? selection.getRangeAt(0) : null
    area.select()
    area.setSelectionRange(0, value.length)
    const ok = document.execCommand('copy')
    document.body.removeChild(area)
    if (previous && selection) {
      selection.removeAllRanges()
      selection.addRange(previous)
    }
    return ok
  } catch {
    return false
  }
}
