<script setup>
/**
 * Status pill: dot + label, tone derived from a semantic state string.
 *
 * One mapping table keeps "connected / registered / admin / error" consistent
 * across both consoles instead of every page inventing its own colour logic.
 */
import { computed } from 'vue'

const props = defineProps({
  state: { type: String, default: '' },
  label: { type: String, default: '' },
  live: { type: Boolean, default: false },
  outline: { type: Boolean, default: false }
})

/** Canonical state -> tone map. Unknown states fall back to neutral. */
const TONES = {
  // liveness
  connected: 'ok',
  online: 'ok',
  ready: 'ok',
  registered: 'ok',
  ok: 'ok',
  healthy: 'ok',
  running: 'ok',
  active: 'ok',
  // in-progress
  connecting: 'warn',
  reconnecting: 'warn',
  pending: 'warn',
  degraded: 'warn',
  starting: 'warn',
  // failures
  disconnected: 'danger',
  offline: 'danger',
  error: 'danger',
  failed: 'danger',
  revoked: 'danger',
  disabled: 'danger',
  expired: 'danger',
  stopped: 'default',
  closed: 'default',
  idle: 'default'
}

const tone = computed(() => TONES[String(props.state).toLowerCase()] || 'default')
const text = computed(() => props.label || stateLabel(props.state))
const isLive = computed(() => props.live && ['ok', 'warn'].includes(tone.value))

function stateLabel(state) {
  if (!state) return '未知'
  const s = String(state)
  return s.charAt(0).toUpperCase() + s.slice(1)
}
</script>

<template>
  <span class="wg-badge" :class="{ outline }" :data-tone="tone === 'default' ? undefined : tone">
    <span class="wg-dot" :class="{ live: isLive }" :data-tone="tone === 'default' ? undefined : tone" />
    {{ text }}
  </span>
</template>
