/**
 * Console session state (client console).
 *
 * The client console is protected by a single shared secret (the token printed
 * at startup, or an optional password hash). Auth material lives in two cookies
 * managed by Go:
 *   wg_client_session  HttpOnly, opaque session id
 *   wg_client_csrf     readable double-submit CSRF token
 * This module only tracks whether we are authenticated; it never stores the
 * secret in localStorage, which keeps the token out of reach of XSS.
 */
import { reactive, computed } from 'vue'
import { createApi } from '@common/http'

/** @type {{ status: 'unknown'|'authed'|'anon', meta: object|null, expiresAt: string|null }} */
export const state = reactive({
  status: 'unknown',
  meta: null,
  expiresAt: null
})

let inflight = null

export const api = createApi({
  base: '/api',
  csrfCookie: 'wg_client_csrf',
  onUnauthorized: () => {
    state.status = 'anon'
    state.meta = null
    // Bounce to the login view without importing the router (avoids a cycle).
    if (!location.pathname.startsWith('/login')) {
      location.assign('/login')
    }
  }
})

export const session = {
  get authed() {
    return state.status === 'authed'
  },

  /** True once we know whether the visitor is authenticated. */
  get ready() {
    return state.status !== 'unknown'
  },

  /**
   * Resolve the session once per page load.
   * Concurrent callers share one request, so a router guard plus a component
   * mount never double-fetch.
   */
  ensure(force = false) {
    if (!force) {
      if (state.status === 'authed') return Promise.resolve(true)
      if (state.status === 'anon') return Promise.resolve(false)
    }
    if (inflight) return inflight
    inflight = (async () => {
      try {
        const payload = await withTimeout(api.get('/session'), 6000)
        state.expiresAt = payload?.expires_at || null
        state.status = 'authed'
        return true
      } catch (err) {
        if (err?.status !== 401) {
          // Transport failure: treat as anonymous but let the page surface it.
          state.status = 'anon'
          return false
        }
        state.status = 'anon'
        return false
      } finally {
        inflight = null
      }
    })()
    return inflight
  },

  /** Authenticate with the console secret; throws ApiError on failure. */
  async login(secret) {
    const payload = await api.post('/login', { secret })
    state.status = 'authed'
    state.expiresAt = payload?.expires_at || null
    await this.loadMeta()
    return payload
  },

  async logout() {
    try {
      await api.post('/logout', {})
    } finally {
      state.status = 'anon'
      state.meta = null
    }
  },

  /** Public runtime metadata (version, server address, config path). */
  async loadMeta() {
    try {
      state.meta = await api.get('/meta')
    } catch {
      // Meta is decorative; a failure must not block the console.
    }
    return state.meta
  }
}

export const isAuthed = computed(() => state.status === 'authed')

/** Abort a hung request instead of leaving the login button spinning forever. */
function withTimeout(promise, ms) {
  return Promise.race([
    promise,
    new Promise((_, reject) =>
      setTimeout(() => reject(new Error('请求超时，请确认客户端进程仍在运行')), ms)
    )
  ])
}
