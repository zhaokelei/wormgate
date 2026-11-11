<script setup>
/**
 * Admin console root.
 *
 * Navigation is role-aware: items an operator cannot use are hidden, but every
 * endpoint behind them re-checks the role server-side, so hiding is purely a
 * usability decision.
 */
import { computed, onBeforeUnmount, onMounted } from 'vue'
import { useRoute, useRouter } from 'vue-router'

import AppShell from '@common/ui/AppShell.vue'
import ToastStack from '@common/ui/ToastStack.vue'
import Icon from '@common/ui/Icon.vue'
import { notifyOk, notifyError } from '@common/toast'
import { formatUptime } from '@common/format'

import { session, state } from './session'
import { startLive, stopLive, live } from './store'

const route = useRoute()
const router = useRouter()
const plain = computed(() => route.meta.layout === 'plain')

/** Unacknowledged alerts, used for the nav badge. */
const alertCount = computed(() => (live.overview.alerts || []).filter((a) => !a.ack).length)

const navItems = computed(() => {
  const items = [
    { to: '/', label: '总览', icon: 'dashboard', exact: true },
    { to: '/tunnels', label: '隧道', icon: 'tunnel' },
    {
      to: '/clients',
      label: '客户端',
      icon: 'wifi',
      badge: live.overview.clients ? String(live.overview.clients) : ''
    },
    { to: '/nodes', label: '节点', icon: 'server' },
    { to: '/alerts', label: '告警', icon: 'bell', badge: alertCount.value ? String(alertCount.value) : '' }
  ]
  if (session.canRole('admin', 'viewer')) items.push({ to: '/audit', label: '审计', icon: 'list' })
  if (session.isAdmin) items.push({ to: '/users', label: '用户与租户', icon: 'users' })
  items.push({ to: '/security', label: '安全', icon: 'shield' })
  return items
})

const roleLabel = computed(() => {
  const map = { admin: '管理员', user: '用户', viewer: '只读' }
  return map[session.role] || session.role || '未知'
})

async function logout() {
  try {
    await session.logout()
    notifyOk('已退出登录')
    router.replace('/login')
  } catch (err) {
    notifyError(err, '退出失败')
  }
}

onMounted(startLive)
onBeforeUnmount(stopLive)
</script>

<template>
  <template v-if="plain">
    <RouterView />
  </template>
  <AppShell
    v-else
    brand="Wormgate"
    tagline="服务端控制台"
    :items="navItems"
    :status-text="`运行 ${formatUptime(live.overview.uptime_sec)}`"
    :status-tone="live.link === 'open' ? 'ok' : 'warn'"
  >
    <template #railFooter>
      <div class="identity">
        <span class="avatar" aria-hidden="true">{{ (state.user?.username || '?').slice(0, 1).toUpperCase() }}</span>
        <div class="identity-text">
          <strong class="wg-truncate">{{ state.user?.display_name || state.user?.username || '未登录' }}</strong>
          <span class="wg-badge outline" style="height: 17px; font-size: 10px">{{ roleLabel }}</span>
        </div>
        <button class="wg-icon-btn" aria-label="退出登录" title="退出登录" @click="logout">
          <Icon name="logout" />
        </button>
      </div>
    </template>

    <RouterView v-slot="{ Component }">
      <component :is="Component" />
    </RouterView>

    <ToastStack />
  </AppShell>
</template>

<style>
.identity {
  display: flex;
  align-items: center;
  gap: 9px;
  padding: 8px;
  margin-bottom: 6px;
  border-radius: var(--r-sm);
  background: var(--surface-2);
  border: 1px solid var(--hairline);
  min-width: 0;
}
.avatar {
  display: grid;
  place-items: center;
  width: 28px;
  height: 28px;
  flex: none;
  border-radius: 50%;
  background: var(--accent);
  color: #fff;
  font-size: var(--fs-12);
  font-weight: 700;
}
.identity-text { display: flex; flex-direction: column; gap: 2px; min-width: 0; flex: 1; }
.identity-text strong { font-size: var(--fs-12); font-weight: 600; }
.wg-shell.collapsed .identity { display: none; }
</style>
