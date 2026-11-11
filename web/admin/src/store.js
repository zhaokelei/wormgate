/**
 * Live dashboard store for the admin console.
 *
 * Push payload (internal/server/admin.go handleAdminWS):
 *
 * {
 *   "type": "snapshot",
 *   "timestamp": 1762744802000,
 *   "data": {
 *     "overview": {
 *       "timestamp": 1762744801912,
 *       "uptime_sec": 8412,
 *       "clients": 3, "tunnels": 7,
 *       "active_connections": 12, "connections_total": 98124,
 *       "total_in": 8123456789, "total_out": 4210345678,
 *       "rate_in": 184320, "rate_out": 92160,
 *       "http_requests": 55210,
 *       "top_tunnels": [ { "id": "...", "name": "...", "owner": "...", ... } ],
 *       "alerts": [ { "id": "...", "level": "warning", "message": "...", ... } ],
 *       "node": { "name": "...", "goroutines": 128, "heap_alloc": 41943040, ... }
 *     },
 *     "recent": [ { "timestamp": 1762744801000, "bytes_in": 184320, "bytes_out": 92160 } ]
 *   }
 * }
 *
 * The store also keeps the full tunnel series from GET /api/stats for the
 * per-tunnel chart, which is fetched on demand rather than pushed (it is an
 * order of magnitude larger than the overview).
 */
import { reactive } from 'vue'
import { createLiveSocket } from '@common/live'

export const live = reactive({
  link: 'closed',
  lastUpdate: 0,
  /** overviewView from the Go server. */
  overview: {
    timestamp: 0,
    uptime_sec: 0,
    clients: 0,
    tunnels: 0,
    active_connections: 0,
    connections_total: 0,
    total_in: 0,
    total_out: 0,
    rate_in: 0,
    rate_out: 0,
    http_requests: 0,
    top_tunnels: [],
    alerts: [],
    node: {}
  },
  recent: [],
  /** Per-tunnel series from GET /api/stats (not pushed). */
  series: []
})

let socket = null

function applySnapshot(msg) {
  if (msg?.type !== 'snapshot' || !msg.data) return
  const { overview, recent } = msg.data
  if (overview) live.overview = overview
  if (Array.isArray(recent)) live.recent = recent.slice(-300)
  live.lastUpdate = msg.timestamp || Date.now()
}

export function startLive() {
  if (socket) return
  socket = createLiveSocket({
    path: '/api/ws',
    onState: (state) => {
      live.link = state
    },
    onMessage: applySnapshot
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
