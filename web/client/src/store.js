/**
 * Live telemetry store for the client console.
 *
 * One WebSocket per tab feeds every view. Rationale: the payload is a single
 * consistent snapshot (status + connections + recent samples), so a dashboard
 * that opened three sockets could show three different "current" states. The
 * store is started by App.vue and survives route changes.
 */
import { reactive, computed } from 'vue'
import { createLiveSocket } from '@common/live'
import { api } from './session'

/**
 * @typedef {object} Tunnel
 * @property {string} name
 * @property {'http'|'tcp'} type
 * @property {string} local
 * @property {string} [subdomain]
 * @property {number} [remote_port]
 * @property {boolean} enabled
 * @property {boolean} registered
 * @property {string} [public_addr]
 * @property {string} [last_error]
 * @property {number} bytes_in
 * @property {number} bytes_out
 * @property {number} connections
 * @property {number} active_connections
 */

const WINDOW = 300

export const live = reactive({
  /** 'connecting' | 'open' | 'closed' */
  link: 'closed',
  lastUpdate: 0,
  status: {
    state: 'disconnected',
    state_since: null,
    server: '',
    client_name: '',
    session_id: '',
    connected_at: null,
    last_error: '',
    reconnects: 0,
    uptime_sec: 0,
    total_in: 0,
    total_out: 0,
    tunnels: []
  },
  connections: [],
  samples: []
})

let socket = null

/** Mean rate over the last n samples, used for the header KPI readouts. */
export function meanRate(field, count = 6) {
  const list = live.samples.slice(-count)
  if (list.length === 0) return 0
  return Math.round(list.reduce((acc, s) => acc + (s[field] || 0), 0) / list.length)
}

export const rateIn = computed(() => meanRate('bytes_in'))
export const rateOut = computed(() => meanRate('bytes_out'))

/** Merge a pushed snapshot, keeping the ring buffer bounded client-side too. */
function apply(snapshot) {
  if (!snapshot) return
  const ts = snapshot.timestamp || Date.now()
  live.lastUpdate = ts
  if (snapshot.status) live.status = snapshot.status
  live.connections = Array.isArray(snapshot.connections) ? snapshot.connections : []

  const recent = Array.isArray(snapshot.recent) ? snapshot.recent : []
  // The server sends the tail of its own ring; replace rather than append so a
  // reconnected tab cannot duplicate points.
  live.samples = recent.slice(-WINDOW)
}

export function startLive() {
  if (socket) return
  socket = createLiveSocket({
    path: '/api/ws',
    onState: (state) => {
      live.link = state
    },
    onMessage: (msg) => {
      if (msg?.type === 'snapshot') apply(msg)
    }
  })
  socket.start()
}

export function stopLive() {
  socket?.stop()
  socket = null
  live.link = 'closed'
}

export function reconnectLive() {
  socket?.reconnectNow()
}

/** One-shot refresh used after mutations, before the next push arrives. */
export async function refreshStatus() {
  const [status, stats] = await Promise.allSettled([api.get('/status'), api.get('/stats')])
  if (status.status === 'fulfilled' && status.value) live.status = status.value
  if (stats.status === 'fulfilled' && stats.value?.global) {
    live.samples = stats.value.global.slice(-WINDOW)
  }
}
