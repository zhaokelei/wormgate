/**
 * Admin console session (JWT cookie + CSRF double submit).
 *
 * Auth differences from the client console, all server-enforced in
 * internal/server/admin.go:
 *   - identity is a signed JWT in the HttpOnly `wg_admin_session` cookie
 *   - the readable `wg_admin_csrf` cookie is mirrored into X-CSRF-Token
 *   - role check happens on every request; this module only mirrors the role
 *     for UI affordances (never for authorisation decisions)
 */
import { computed, reactive } from 'vue'
import { createApi } from '@common/http'

/** @type {{ status: 'unknown'|'authed'|'anon', user: object|null, csrf: string }} */
export const state = reactive({
  status: 'unknown',
  user: null,
  csrf: ''
})

let inflight = null

export const api = createApi({
  base: '/api',
  csrfCookie: 'wg_admin_csrf',
  onUnauthorized: () => {
    state.status = 'anon'
    state.user = null
    const base = '/admin'
    if (!location.pathname.startsWith(`${base}/login`)) {
      location.assign(`${base}/login`)
    }
  }
})

export const session = {
  get authed() {
    return state.status === 'authed'
  },

  get user() {
    return state.user
  },

  get role() {
    return state.user?.role || ''
  },

  get isAdmin() {
    return this.role === 'admin'
  },

  get isViewer() {
    return this.role === 'viewer'
  },

  /** Exact match against one of the given roles. */
  canRole(...roles) {
    return roles.includes(this.role)
  },

  /** Resolve identity once per page load (memoised until invalidation). */
  ensure(force = false) {
    if (!force) {
      if (state.status === 'authed') return Promise.resolve(true)
      if (state.status === 'anon') return Promise.resolve(false)
    }
    if (inflight) return inflight
    inflight = (async () => {
      try {
        const payload = await api.get('/me')
        state.user = payload?.user || null
        state.csrf = payload?.csrf || ''
        state.status = state.user ? 'authed' : 'anon'
        return state.status === 'authed'
      } catch (err) {
        if (err?.status !== 401) {
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

  async login(username, password) {
    const payload = await api.post('/login', { username, password })
    state.user = payload?.user || null
    state.csrf = payload?.csrf || ''
    state.status = 'authed'
    return payload
  },

  async logout() {
    try {
      await api.post('/logout', {})
    } finally {
      state.status = 'anon'
      state.user = null
    }
  },

  /** Refresh the cached user after a self-service profile change. */
  async reload() {
    return this.ensure(true)
  }
}

/** Convenience computed used by App.vue for the rail identity block. */
export const currentUser = computed(() => state.user)
