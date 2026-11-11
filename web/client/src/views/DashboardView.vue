<script setup>
/**
 * Wormgate client console — overview.
 *
 * Answers three questions in one screen, top to bottom:
 *   1. Is the control plane up, and to which server?   (status banner + KPIs)
 *   2. Is traffic flowing, and how much?               (live chart)
 *   3. Are my tunnels actually published?              (tunnel table)
 *
 * All telemetry comes from the shared live store, so this page costs zero
 * polling requests once the WebSocket is up.
 */
import { computed, onMounted, ref } from 'vue'
import { RouterLink } from 'vue-router'

import PageHead from '@common/ui/PageHead.vue'
import TopBar from '@common/ui/TopBar.vue'
import KpiCard from '@common/ui/KpiCard.vue'
import TrafficChart from '@common/ui/TrafficChart.vue'
import StatusPill from '@common/ui/StatusPill.vue'
import EmptyState from '@common/ui/EmptyState.vue'
import TButton from '@common/ui/TButton.vue'
import Icon from '@common/ui/Icon.vue'
import { formatBytes, formatDuration, formatTime, copyText } from '@common/format'
import { notifyOk, notifyError } from '@common/toast'

import { live, rateIn, rateOut, refreshStatus } from '../store'
import { clientApi } from '../api'
import { state } from '../session'

const reconnecting = ref(false)
const meta = computed(() => state.meta)
const range = ref(120)

const status = computed(() => live.status)
const tunnels = computed(() => status.value.tunnels || [])
const published = computed(() => tunnels.value.filter((t) => t.registered))

/** Trim the sample window for the range selector. */
const chartSamples = computed(() => live.samples.slice(-range.value))

const healthTone = computed(() => {
  if (status.value.state === 'connected') return 'ok'
  if (status.value.state === 'connecting' || status.value.state === 'reconnecting') return 'warn'
  return 'danger'
})

const healthText = computed(() => {
  const map = {
    connected: '控制面已连接，隧道流量正常转发',
    connecting: '正在与服务器建立控制通道…',
    reconnecting: '连接已中断，正在自动重连',
    disconnected: '尚未连接服务器，请检查地址与令牌',
    stopped: '客户端已停止'
  }
  return map[status.value.state] || '状态未知'
})

async function reconnect() {
  reconnecting.value = true
  try {
    await clientApi.reconnect()
    notifyOk('已请求重连', '控制通道将在 1 秒内重新建立')
  } catch (err) {
    notifyError(err, '重连失败')
  } finally {
    // The button must not stay busy: the actual reconnect is asynchronous and
    // observed through the status stream.
    setTimeout(() => (reconnecting.value = false), 800)
  }
}

async function copyAddr(addr) {
  if (!addr) return
  const ok = await copyText(addr)
  if (ok) notifyOk('已复制', addr)
  else notifyError(new Error('浏览器拒绝了剪贴板访问'), '请手动选择复制')
}

onMounted(() => {
  refreshStatus().catch(() => {})
})
</script>

<template>
  <TopBar
    title="概览"
    :subtitle="live.status.server ? `服务器 ${live.status.server}` : '尚未配置服务器'"
    :live-state="live.link"
    :version="meta?.version"
  />

  <div class="wg-page">
    <PageHead title="运行状态" :description="`客户端 ${status.client_name || '--'} · 已运行 ${formatDuration(status.uptime_sec)}`">
      <template #actions>
        <TButton size="sm" icon="refresh" :loading="reconnecting" @click="reconnect">重连</TButton>
        <RouterLink to="/tunnels"><TButton size="sm" variant="primary" icon="plus">新建隧道</TButton></RouterLink>
      </template>
    </PageHead>

    <!-- Control-plane banner: the single most important state on this page. -->
    <section class="health" :data-tone="healthTone">
      <span class="wg-dot live" :data-tone="healthTone" />
      <div class="health-text">
        <strong>{{ healthText }}</strong>
        <span v-if="status.last_error" class="health-error wg-truncate">{{ status.last_error }}</span>
      </div>
      <div class="health-meta">
        <span v-if="status.session_id" class="wg-mono wg-truncate">会话 {{ status.session_id.slice(0, 12) }}</span>
        <span v-if="status.connected_at" class="wg-dim">{{ formatTime(status.connected_at) }} 起</span>
        <span v-if="status.reconnects" class="wg-dim">重连 {{ status.reconnects }} 次</span>
      </div>
    </section>

    <div class="wg-grid kpi">
      <KpiCard
        label="入站速率"
        :value="formatBytes(rateIn, 1)"
        small="/s"
        icon="arrowDownLeft"
        tone="accent"
        :hint="`累计 ${formatBytes(status.total_in)}`"
      />
      <KpiCard
        label="出站速率"
        :value="formatBytes(rateOut, 1)"
        small="/s"
        icon="arrowUpRight"
        tone="violet"
        :hint="`累计 ${formatBytes(status.total_out)}`"
      />
      <KpiCard
        label="活跃连接"
        :value="live.connections.length"
        icon="activity"
        tone="ok"
        :hint="`已发布隧道 ${published.length}/${tunnels.length}`"
      />
      <KpiCard
        label="控制通道"
        :value="status.state === 'connected' ? '在线' : '离线'"
        icon="wifi"
        :tone="healthTone"
        :hint="status.reconnects ? `已重连 ${status.reconnects} 次` : '无重连'"
        mono
      />
    </div>

    <section class="wg-card">
      <div class="wg-card-head">
        <div class="wg-card-title">
          流量
          <span class="wg-card-sub">每秒采样 · 最近 {{ range }} 秒</span>
        </div>
        <span class="wg-spacer" />
        <div class="wg-seg" role="group" aria-label="时间范围">
          <button :aria-pressed="range === 60" @click="range = 60">1 分钟</button>
          <button :aria-pressed="range === 120" @click="range = 120">2 分钟</button>
          <button :aria-pressed="range === 300" @click="range = 300">5 分钟</button>
        </div>
      </div>
      <div class="wg-card-body tight">
        <TrafficChart v-if="chartSamples.length" :samples="chartSamples" :height="264" />
        <EmptyState
          v-else
          icon="activity"
          title="等待采样数据"
          description="连接控制面后每秒产生一个采样点，约 2 秒后图表出现。"
        />
      </div>
    </section>

    <section class="wg-card">
      <div class="wg-card-head">
        <div class="wg-card-title">
          隧道
          <span class="wg-card-sub">{{ published.length }} 个已发布 / 共 {{ tunnels.length }} 个</span>
        </div>
        <span class="wg-spacer" />
        <RouterLink to="/tunnels" class="wg-btn sm ghost">管理</RouterLink>
      </div>

      <div v-if="tunnels.length" class="wg-table-wrap">
        <table class="wg-table">
          <thead>
            <tr>
              <th>名称</th>
              <th>类型</th>
              <th>公网地址</th>
              <th>本地目标</th>
              <th class="num">入站</th>
              <th class="num">出站</th>
              <th class="num">连接</th>
              <th>状态</th>
            </tr>
          </thead>
          <tbody>
            <tr v-for="t in tunnels" :key="t.name">
              <td>
                <div class="wg-primary-cell">
                  <span class="wg-dot" :data-tone="t.registered ? 'ok' : t.enabled ? 'warn' : 'default'" />
                  <div style="min-width: 0">
                    <div class="name wg-truncate">{{ t.name }}</div>
                    <div v-if="t.last_error" class="meta wg-truncate">{{ t.last_error }}</div>
                  </div>
                </div>
              </td>
              <td><span class="wg-badge outline wg-mono">{{ t.type }}</span></td>
              <td>
                <button
                  v-if="t.public_addr"
                  class="addr"
                  :title="`复制 ${t.public_addr}`"
                  @click="copyAddr(t.public_addr)"
                >
                  <span class="wg-mono wg-truncate">{{ t.public_addr }}</span>
                  <Icon name="copy" :size="12" />
                </button>
                <span v-else class="wg-dim">等待分配</span>
              </td>
              <td class="wg-mono wg-truncate">{{ t.local }}</td>
              <td class="num">{{ formatBytes(t.bytes_in) }}</td>
              <td class="num">{{ formatBytes(t.bytes_out) }}</td>
              <td class="num">{{ t.active_connections }}</td>
              <td>
                <StatusPill
                  :state="t.registered ? 'registered' : t.enabled ? 'pending' : 'stopped'"
                  :label="t.registered ? '已发布' : t.enabled ? '注册中' : '已停用'"
                />
              </td>
            </tr>
          </tbody>
        </table>
      </div>

      <EmptyState
        v-else
        icon="tunnel"
        title="还没有隧道"
        description="创建一条隧道把本机服务暴露到公网，服务端会自动分配子域名或端口。"
      >
        <RouterLink to="/tunnels"><TButton size="sm" variant="primary" icon="plus">新建隧道</TButton></RouterLink>
      </EmptyState>
    </section>
  </div>
</template>

<style scoped>
.health {
  display: flex;
  align-items: center;
  gap: 12px;
  padding: 12px 15px;
  border-radius: var(--r-md);
  border: 1px solid var(--hairline);
  background: var(--surface-1);
  flex-wrap: wrap;
}
.health[data-tone='ok'] { border-color: color-mix(in srgb, var(--ok) 30%, transparent); background: linear-gradient(90deg, var(--ok-soft), transparent 60%); }
.health[data-tone='warn'] { border-color: color-mix(in srgb, var(--warn) 30%, transparent); background: linear-gradient(90deg, var(--warn-soft), transparent 60%); }
.health[data-tone='danger'] { border-color: color-mix(in srgb, var(--danger) 32%, transparent); background: linear-gradient(90deg, var(--danger-soft), transparent 60%); }

.health-text { display: flex; flex-direction: column; gap: 1px; min-width: 0; flex: 1; }
.health-text strong { font-size: var(--fs-13); font-weight: 600; }
.health-error { font-size: var(--fs-11); color: var(--danger); max-width: 60ch; }
.health-meta { display: flex; align-items: center; gap: 12px; font-size: var(--fs-11); color: var(--text-muted); flex-wrap: wrap; }

.addr {
  display: inline-flex;
  align-items: center;
  gap: 5px;
  padding: 2px 6px;
  margin-left: -6px;
  border-radius: var(--r-xs);
  color: var(--accent);
  font-size: var(--fs-12);
  max-width: 22ch;
  transition: background var(--dur-1) var(--ease);
}
.addr:hover { background: var(--accent-soft); }
.addr svg { opacity: .5; flex: none; }
.addr:hover svg { opacity: 1; }
</style>
