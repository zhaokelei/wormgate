/**
 * Console HTTP client — same contract as the Vue one it replaces:
 *  - HttpOnly session cookie, `credentials: 'same-origin'`
 *  - CSRF double submit: readable `wg_admin_csrf` cookie echoed in X-CSRF-Token
 *  - errors arrive as { error, issues? } and become ApiError
 */
export class ApiError extends Error {
  status: number
  issues: string[]

  constructor(message: string, { status = 0, issues = [] }: { status?: number; issues?: string[] } = {}) {
    super(message)
    this.name = 'ApiError'
    this.status = status
    this.issues = issues
  }
}

const CSRF_COOKIE = 'wg_admin_csrf'
const UNSAFE = new Set(['POST', 'PUT', 'PATCH', 'DELETE'])

let onUnauthorized: (() => void) | null = null

/** Registered once by the auth provider so a 401 bounces to the login page. */
export function setUnauthorizedHandler(fn: () => void) {
  onUnauthorized = fn
}

function readCookie(name: string): string {
  const prefix = `${name}=`
  for (const part of document.cookie.split(';')) {
    const entry = part.trim()
    if (entry.startsWith(prefix)) return decodeURIComponent(entry.slice(prefix.length))
  }
  return ''
}

export async function api<T = any>(
  path: string,
  { method = 'GET', body }: { method?: string; body?: unknown } = {}
): Promise<T> {
  const init: RequestInit = {
    method,
    credentials: 'same-origin',
    headers: { Accept: 'application/json' } as Record<string, string>
  }

  if (UNSAFE.has(method)) {
    ;(init.headers as Record<string, string>)['X-CSRF-Token'] = readCookie(CSRF_COOKIE)
  }
  if (body !== undefined) {
    ;(init.headers as Record<string, string>)['Content-Type'] = 'application/json'
    init.body = JSON.stringify(body)
  }

  const response = await fetch(`/api${path}`, init)

  if (response.status === 401) {
    onUnauthorized?.()
    throw new ApiError('登录已过期，请重新登录', { status: 401 })
  }
  if (!response.ok) {
    let message = `请求失败 (HTTP ${response.status})`
    let issues: string[] = []
    try {
      const payload = await response.json()
      if (payload?.error) message = payload.error
      if (Array.isArray(payload?.issues)) issues = payload.issues
    } catch {
      /* non-JSON body: keep the generic message */
    }
    throw new ApiError(message, { status: response.status, issues })
  }
  if (response.status === 204) return null as T

  const text = await response.text()
  return (text ? JSON.parse(text) : null) as T
}
