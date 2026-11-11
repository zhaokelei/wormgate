/**
 * Realtime transports for the Wormgate consoles.
 *
 * Two mechanisms, deliberately separate:
 *  - `useLiveSocket`  WebSocket push for high-frequency telemetry (2 s cadence).
 *  - `subscribeSSE`   EventSource for the client log stream (`/api/logs`).
 *
 * Both auto-reconnect with exponential backoff + jitter. The jitter matters:
 * without it, every open console of a server restarts after a deploy would
 * reconnect in lockstep and deliver a thundering herd to the control plane.
 */

/** Backoff schedule shared by both transports. */
function backoffMs(attempt, base = 800, max = 15000) {
  const delay = Math.min(max, base * 2 ** attempt)
  // +/-20% jitter so reconnects are spread out.
  return Math.round(delay * (0.8 + Math.random() * 0.4))
}

/**
 * Observable WebSocket that survives server restarts.
 *
 * @param {object} options
 * @param {string} options.path       WebSocket path, e.g. '/api/ws'.
 * @param {(data: any) => void} options.onMessage
 * @param {(state: 'connecting'|'open'|'closed') => void} [options.onState]
 * @param {() => boolean} [options.enabled] Gate reconnect attempts.
 */
export function createLiveSocket({ path, onMessage, onState, enabled = () => true }) {
  let socket = null
  let attempt = 0
  let timer = null
  let stopped = false
  let state = 'closed'

  function emit(next) {
    if (state === next) return
    state = next
    onState?.(next)
  }

  function socketURL() {
    const proto = window.location.protocol === 'https:' ? 'wss:' : 'ws:'
    return `${proto}//${window.location.host}${path}`
  }

  function open() {
    if (stopped || !enabled()) return
    clearTimeout(timer)
    emit('connecting')
    let ws
    try {
      ws = new WebSocket(socketURL())
    } catch {
      schedule()
      return
    }
    socket = ws

    ws.onopen = () => {
      attempt = 0
      emit('open')
    }

    ws.onmessage = (event) => {
      if (typeof event.data !== 'string') return
      try {
        onMessage(JSON.parse(event.data))
      } catch {
        // A malformed frame must not kill the stream; drop it and carry on.
      }
    }

    ws.onerror = () => {
      // onclose always follows; reconnect is handled there.
    }

    ws.onclose = () => {
      socket = null
      emit('closed')
      schedule()
    }
  }

  function schedule() {
    if (stopped || !enabled()) return
    const delay = backoffMs(attempt++)
    clearTimeout(timer)
    timer = setTimeout(open, delay)
  }

  /** Force an immediate reconnect (used by the "reconnect" button). */
  function reconnectNow() {
    attempt = 0
    if (socket) {
      try {
        socket.close()
      } catch {
        /* already gone */
      }
      socket = null
    }
    clearTimeout(timer)
    open()
  }

  function start() {
    stopped = false
    if (socket || timer) return
    open()
  }

  function stop() {
    stopped = true
    clearTimeout(timer)
    timer = null
    if (socket) {
      try {
        socket.close()
      } catch {
        /* already gone */
      }
      socket = null
    }
    emit('closed')
  }

  return { start, stop, reconnectNow, get state() { return state } }
}

/**
 * Subscribe to a Server-Sent Events endpoint.
 *
 * @param {object} options
 * @param {string} options.path        e.g. '/api/logs?level=debug'.
 * @param {(payload: any, event: MessageEvent) => void} options.onEvent
 * @param {(state: 'open'|'error') => void} [options.onState]
 * @returns {() => void} unsubscribe
 */
export function subscribeSSE({ path, onEvent, onState }) {
  let source = null
  let attempt = 0
  let timer = null
  let disposed = false

  function connect() {
    if (disposed) return
    source = new EventSource(path)

    source.onopen = () => {
      attempt = 0
      onState?.('open')
    }

    // Named event used by the Go handler: `event: log`.
    source.addEventListener('log', (event) => {
      try {
        onEvent(JSON.parse(event.data), event)
      } catch {
        // Ignore a corrupt frame rather than tearing down the stream.
      }
    })

    source.onerror = () => {
      onState?.('error')
      // EventSource retries on its own, but its schedule is aggressive and it
      // cannot carry new query params. We own the retry so we can back off.
      source.close()
      source = null
      clearTimeout(timer)
      timer = setTimeout(connect, backoffMs(attempt++))
    }
  }

  connect()

  return () => {
    disposed = true
    clearTimeout(timer)
    if (source) {
      source.close()
      source = null
    }
  }
}
