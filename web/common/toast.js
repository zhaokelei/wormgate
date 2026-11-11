/**
 * Tiny reactive toast store.
 *
 * Deliberately not a plugin/provider: the consoles are single-page and small,
 * and the API error path must be able to call `notifyError` from plain JS.
 */
import { ref } from 'vue'

let seq = 0
const DEFAULT_TTL = { ok: 3200, info: 3600, warn: 5000, danger: 6500 }

export const toasts = ref([])

/**
 * Push a toast.
 *
 * @param {string} message  Primary line.
 * @param {object} [options]
 * @param {'ok'|'info'|'warn'|'danger'} [options.tone]
 * @param {string} [options.detail]  Secondary line (validation hints, endpoints).
 * @param {number} [options.ttl]     Milliseconds; 0 keeps it until dismissed.
 */
export function notify(message, { tone = 'info', detail = '', ttl } = {}) {
  const id = ++seq
  toasts.value = [...toasts.value, { id, message, detail, tone }]
  const timeout = ttl ?? DEFAULT_TTL[tone] ?? 4000
  if (timeout > 0) setTimeout(() => dismissToast(id), timeout)
  return id
}

export function dismissToast(id) {
  toasts.value = toasts.value.filter((t) => t.id !== id)
}

/** Success shorthand. */
export const notifyOk = (message, detail) => notify(message, { tone: 'ok', detail })

/**
 * Error shorthand that also folds in backend validation issues, so a rejected
 * YAML edit lists its line-level problems instead of a single opaque line.
 */
export function notifyError(err, fallback = '操作失败') {
  const message = err?.message || fallback
  const issues = Array.isArray(err?.issues) ? err.issues : []
  return notify(message, {
    tone: 'danger',
    detail: issues.length ? issues.slice(0, 3).join('；') : '',
    ttl: issues.length ? 9000 : undefined
  })
}
