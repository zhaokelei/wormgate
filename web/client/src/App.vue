<script setup>
/**
 * Client console root.
 *
 * Two layouts: `plain` for the login screen (no rail, no polling) and the
 * default shell. The live WebSocket is started here, once, and stopped on
 * unmount — keeping it out of individual views prevents duplicate sockets.
 */
import { computed, onBeforeUnmount, onMounted } from 'vue'
import { useRoute } from 'vue-router'

import AppShell from '@common/ui/AppShell.vue'
import ToastStack from '@common/ui/ToastStack.vue'
import TrafficChart from '@common/ui/TrafficChart.vue'
import { formatRate } from '@common/format'

import { session, state } from './session'
import { startLive, stopLive, live, rateIn, rateOut } from './store'

const route = useRoute()
const plain = computed(() => route.meta.layout === 'plain')

const navItems = computed(() => [
  { to: '/', label: '概览', icon: 'dashboard', exact: true },
  { to: '/tunnels', label: '隧道', icon: 'tunnel' },
  {
    to: '/connections',
    label: '连接',
    icon: 'activity',
    badge: live.connections.length ? String(live.connections.length) : ''
  },
  { to: '/config', label: '配置', icon: 'settings' },
  { to: '/logs', label: '日志', icon: 'terminal' },
  { to: '/security', label: '安全', icon: 'shield' }
])

/** Control-plane state -> semantic tone for the rail status dot. */
const STATE_TONE = {
  connected: 'ok',
  connecting: 'warn',
  reconnecting: 'warn',
  disconnected: 'danger',
  stopped: 'default'
}

const stateTone = computed(() => STATE_TONE[live.status.state] || 'default')
const stateLabel = computed(() => {
  const labels = {
    connected: '已连接控制面',
    connecting: '正在连接…',
    reconnecting: '正在重连…',
    disconnected: '未连接',
    stopped: '已停止'
  }
  return labels[live.status.state] || live.status.state
})

onMounted(async () => {
  if (!state.meta) session.loadMeta()
  startLive()
})

onBeforeUnmount(stopLive)
</script>

<template>
  <template v-if="plain">
    <RouterView />
  </template>
  <AppShell
    v-else
    brand="Wormgate"
    tagline="本地控制台"
    :items="navItems"
    :status-text="stateLabel"
    :status-tone="stateTone"
  >
    <template #railFooter>
      <RouterLink to="/logs" class="wg-nav-item">
        <span class="wg-dot live" :data-tone="live.link === 'open' ? 'ok' : 'warn'" />
        <span class="wg-nav-label wg-truncate">{{ live.link === 'open' ? '实时推送' : '推送中断' }}</span>
      </RouterLink>
    </template>

    <RouterView v-slot="{ Component }">
      <component :is="Component" />
    </RouterView>

    <ToastStack />
  </AppShell>
</template>
