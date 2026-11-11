/**
 * Wormgate console HTTP client.
 *
 * Single place that knows about our auth/CSRF/proxy contract, so every page
 * and composable talks to the Go API the same way.
 *
 * Contract implemented here (mirrors internal/webui and internal/server):
 *  - Cookie auth: HttpOnly session cookie, sent with `credentials: 'same-origin'`.
 *  - CSRF double submit: the readable `wg_*_csrf` cookie value must be echoed in
 *    the `X-CSRF-Token` header on every unsafe method. The Go side compares the
 *    header against both the cookie and the server-side session, so the token
 *    cannot be forged by a third-party origin.
 *  - Errors: `{ error: string, issues?: string[] }` -> ApiError.
 *  - 401 flips the app into the login view exactly once (no toast storms when
 *    several panels fail together).
 */

export class ApiError extends Error {
  constructor(message, { status = 0, issues = [], cause = null } = {}) {
    super(message)
    this.name = 'ApiError'
    this.status = status
    this.issues = issues
    this.cause = cause
  }

  /** True when the failure is a validation problem the user can fix. */
  get isValidation() {
    return this.status === 400 || this.status === 422
  }
}

function readCookie(name) {
  const prefix = `${name}=`
  for (const part of document.cookie.split(';')) {
    const entry = part.trim()
    if (entry.startsWith(prefix)) return decodeURIComponent(entry.slice(prefix.length))
  }
  return ''
}

const UNSAFE = new Set(['POST', 'PUT', 'PATCH', 'DELETE'])

/**
 * Build an API client bound to one console.
 *
 * @param {object} options
 * @param {string} options.base        Prefix, e.g. '/api'.
 * @param {string} options.csrfCookie  Cookie holding the double-submit token.
 * @param {string} [options.csrfHeader]
 * @param {(err: ApiError) => void} [options.onUnauthorized]
 */
export function createApi({ base = '/api', csrfCookie, csrfHeader = 'X-CSRF-Token', onUnauthorized } = {}) {
  // A cached copy of the CSRF token; refreshed on every response so a rotated
  // session picks up the new value without a page reload.
  let csrf = readCookie(csrfCookie)

  function refreshCsrf() {
    const next = readCookie(csrfCookie)
    if (next) csrf = next
    return csrf
  }

  /** The token the UI may display/copy (never the session id). */
  function csrfToken() {
    return refreshCsrf()
  }

  async function request(path, { method = 'GET', body, headers = {}, raw = false, signal } = {}) {
    const url = `${base}${path}`
    const init = {
      method,
      credentials: 'same-origin',
      headers: { Accept: raw ? '*/*' : 'application/json', ...headers },
      signal
    }

    if (UNSAFE.has(method)) {
      // CSRF: always re-read the cookie; the server rotates it with the session.
      init.headers[csrfHeader] = refreshCsrf()
    }

    if (body !== undefined) {
      if (raw) {
        init.headers['Content-Type'] = 'application/yaml; charset=utf-8'
        init.body = body
      } else {
        init.headers['Content-Type'] = 'application/json'
        init.body = JSON.stringify(body)
      }
    }

    let response
    try {
      response = await fetch(url, init)
    } catch (err) {
      // AbortError is a deliberate cancellation, not a transport failure.
      if (err?.name === 'AbortError') throw err
      throw new ApiError('无法连接控制台后端（连接被拒绝或已断开）', { cause: err })
    }

    refreshCsrf()

    if (response.status === 401) {
      const err = new ApiError('登录已过期，请重新登录', { status: 401 })
      onUnauthorized?.(err)
      throw err
    }

    if (!response.ok) {
      throw await toApiError(response)
    }

    if (response.status === 204) return null
    if (raw) return response.text()

    const text = await response.text()
    if (!text) return null
    try {
      return JSON.parse(text)
    } catch {
      throw new ApiError('后端返回了无法解析的响应', { status: response.status })
    }
  }

  async function toApiError(response) {
    let message = `请求失败 (HTTP ${response.status})`
    let issues = []
    try {
      const payload = await response.json()
      if (payload?.error) message = payload.error
      if (Array.isArray(payload?.issues)) issues = payload.issues
    } catch {
      // Non-JSON error body (proxy, panic) -> keep the generic message.
    }
    return new ApiError(message, { status: response.status, issues })
  }

  return {
    csrfToken,
    get: (path, opts) => request(path, { ...opts, method: 'GET' }),
    post: (path, body, opts) => request(path, { ...opts, method: 'POST', body }),
    put: (path, body, opts) => request(path, { ...opts, method: 'PUT', body }),
    patch: (path, body, opts) => request(path, { ...opts, method: 'PATCH', body }),
    del: (path, opts) => request(path, { ...opts, method: 'DELETE' }),
    /** Raw text round-trip used by the YAML configuration editor. */
    getText: (path, opts) => request(path, { ...opts, method: 'GET', raw: true }),
    putText: (path, body, opts) => request(path, { ...opts, method: 'PUT', body, raw: true })
  }
}
