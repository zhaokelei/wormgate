<script setup>
/**
 * Admin dashboard: fleet health at a glance.
 *
 * Layout rationale (top to bottom = decreasing urgency):
 *   1. active alerts   — something is already wrong
 *   2. KPI row         — magnitudes (clients, tunnels, rates, connections)
 *   3. traffic chart   — the shape of the trend
 *   4. Top N tunnels   — where the traffic comes from
 *   5. node resources  — whether the control plane itself is healthy
 *
 * Everything except the node snapshot is pushed every 2 s over /api/ws, so this
 * page issues no polling requests while open.
 */
import { computed, onMounted, ref } from 'vue'
import { RouterLink } from 'vue-router'

import TopBar from '@common/ui/TopBar.vue'
import PageHead from '@common/ui/PageHead.vue'
import KpiCard from '@common/ui/KpiCard.vue'
import TrafficChart from '@common/ui/TrafficChart.vue'
import EmptyState from '@common/ui/EmptyState.vue'
import TButton from '@common/ui/TButton.vue'
import Icon from '@common/ui/Icon.vue'
import { formatBytes, formatNumber, formatUptime, formatPercent } from '@common/format'
import { notifyOk, notifyError } from '@common/toast'

import { adminApi } from '../api'
import { live, reconnectLive } from '../store'
import { session, state } from '../session'

const range = ref(120)
const acking = ref('')

const overview = computed(() => live.overview)
const topTunnels = computed(() => overview.value.top_tunnels || [])
const alerts = computed(() => (overview.value.alerts || []).filter((a) => !a.ack).slice(0, 3))
const node = computed(() => overview.value.node || {})

const chartSamples = computed(() => live.recent.slice(-range.value))

/** Traffic share of each Top-N tunnel, normalised against the visible set. */
const topIn = computed(() => topTunnels.value.reduce((acc, t) => acc + (t.bytes_in || 0), 0))
const topOut = computed(() => topTunnels.value.reduce((acc, t) => acc + (t.bytes_out || 0), 0))

const heapPercent = computed(() => {
  const alloc = node.value.heap_alloc || 0
  const sys = node.value.heap_sys || 0
  if (!sys) return 0
  return Math.min(100, Math.round((alloc / sys) * 100))
})

const memPercent = computed(() => {
  const total = node.value.mem_total || 0
  if (!total) return 0
  return Math.min(100, Math.round(((node.value.mem_used || 0) / total) * 100))
})

const cpuPercent = computed(() => Math.min(100, Math.max(0, Math.round(node.value.cpu_percent || 0))))

/** Everything across all tunnels, which is what an operator actually asks for. */
const fleetRate = computed(() => (overview.value.rate_in || 0) + (overview.value.rate_out || 0))

const ALERT_TONE = { critical: 'danger', error: 'danger', warning: 'warn', warn: 'warn', info: 'info' }

function alertTone(level) {
  return ALERT_TONE[String(level).toLowerCase()] || 'info'
}

function clock(value) {
  if (!value) return ''
  return new Date(value).toLocaleTimeString('zh-CN', { hour12: false })
}

async function ack(alert) {
  acking.value = alert.id
  try {
    await adminApi.ackAlert(alert.id)
    notifyOk('告警已确认', alert.message)
  } catch (err) {
    notifyError(err, '确认告警失败')
  } finally {
    acking.value = ''
  }
}

onMounted(() => {
  if (live.link === 'closed') reconnectLive()
})
</script>

<template>
  <TopBar title="总览" subtitle="全域客户端、隧道与流量" :live-state="live.link" :version="state.meta?.version">
    <template #actions>
      <TButton size="sm" icon="refresh" @click="reconnectLive">重连推送</TButton>
    </template>
  </TopBar>

  <div class="wg-page">
    <PageHead
      title="平台状态"
      :description="`节点运行 ${formatUptime(overview.uptime_sec)} · ${formatNumber(overview.connections_total)} 次累计连接`"
    >
      <template #actions>
        <RouterLink v-if="session.isAdmin" to="/users">
          <TButton size="sm" icon="userPlus">新建用户</TButton>
        </RouterLink>
      </template>
    </PageHead>

    <!-- 1. Active alerts -->
    <section v-if="alerts.length" class="alerts">
      <header class="alerts-head">
        <Icon name="alertTriangle" :size="15" />
        <strong>{{ alerts.length }} 条待处理告警</strong>
        <span class="wg-spacer" />
        <RouterLink to="/alerts" class="wg-btn sm ghost">全部</RouterLink>
      </header>
      <ul class="alerts-list">
        <li v-for="a in alerts" :key="a.id" class="alert-row" :data-tone="alertTone(a.level)">
          <span class="wg-dot" :data-tone="alertTone(a.level)" />
          <span class="alert-msg wg-truncate">{{ a.message }}</span>
          <span class="wg-dim wg-mono" style="font-size: var(--fs-11)">{{ a.code }}</span>
          <span class="wg-spacer" />
          <span class="wg-dim wg-mono" style="font-size: var(--fs-11)">{{ clock(a.time) }}</span>
          <TButton v-if="session.isAdmin" size="sm" variant="ghost" :loading="acking === a.id" @click="ack(a)">
            确认
          </TButton>
        </li>
      </ul>
    </section>

    <!-- 2. KPIs -->
    <div class="wg-grid kpi">
      <KpiCard
        label="在线客户端"
        :value="formatNumber(overview.clients)"
        icon="wifi"
        tone="accent"
        :hint="`已注册隧道 ${overview.tunnels} 条`"
      />
      <KpiCard
        label="活跃连接"
        :value="formatNumber(overview.active_connections)"
        icon="activity"
        tone="ok"
        :hint="`累计 ${formatNumber(overview.connections_total)} 次`"
      />
      <KpiCard
        label="入站速率"
        :value="formatBytes(overview.rate_in, 1)"
        small="/s"
        icon="arrowDownLeft"
        tone="info"
        :hint="`全部隧道合计 · 累计 ${formatBytes(overview.total_in)}`"
      />
      <KpiCard
        label="出站速率"
        :value="formatBytes(overview.rate_out, 1)"
        small="/s"
        icon="arrowUpRight"
        tone="violet"
        :hint="`全部隧道合计 · 累计 ${formatBytes(overview.total_out)}`"
      />
      <KpiCard
        label="HTTP 请求"
        :value="formatNumber(overview.http_requests)"
        icon="globe"
        tone="warn"
        hint="公网入口计数"
      />
    </div>

    <!-- 3. Traffic -->
    <section class="wg-card">
      <div class="wg-card-head">
        <div class="wg-card-title">
          全局流量
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
        <TrafficChart v-if="chartSamples.length" :samples="chartSamples" :height="272" />
        <EmptyState
          v-else
          icon="activity"
          title="等待采样数据"
          description="统计每秒采样一次，推送连接后约 2 秒出现第一个点。"
        />
      </div>
    </section>

    <div class="bottom-grid">
      <!-- 4. Top tunnels -->
      <section class="wg-card">
        <div class="wg-card-head">
          <div class="wg-card-title">
            Top 隧道
            <span class="wg-card-sub">按累计流量排序</span>
          </div>
          <span class="wg-spacer" />
          <RouterLink to="/tunnels" class="wg-btn sm ghost">全部</RouterLink>
        </div>

        <div v-if="topTunnels.length" class="wg-card-body tight">
          <div v-for="t in topTunnels" :key="t.id" class="top-row">
            <div class="top-main">
              <div class="top-id">
                <span class="wg-dot" data-tone="accent" />
                <strong class="wg-truncate">{{ t.name }}</strong>
                <span class="wg-badge outline">{{ t.owner }}</span>
              </div>
              <div class="top-addr wg-mono wg-truncate">{{ t.public_addr }}</div>
            </div>
            <div class="top-bars">
              <div class="bar" :title="`入站 ${formatBytes(t.bytes_in)}`">
                <span class="bar-in" :style="{ width: formatPercent(t.bytes_in, topIn || 1) }" />
              </div>
              <div class="bar" :title="`出站 ${formatBytes(t.bytes_out)}`">
                <span class="bar-out" :style="{ width: formatPercent(t.bytes_out, topOut || 1) }" />
              </div>
            </div>
            <div class="top-num">
              <span class="wg-mono">{{ formatBytes(t.bytes_in + t.bytes_out) }}</span>
              <span class="wg-dim" style="font-size: var(--fs-11)">{{ t.active_connections }} 连接</span>
            </div>
          </div>
        </div>

        <EmptyState v-else icon="tunnel" title="暂无隧道" description="客户端连入并注册隧道后，Top 列表会实时出现。" />
      </section>

      <!-- 5. Node -->
      <section class="wg-card">
        <div class="wg-card-head">
          <div class="wg-card-title">
            节点资源
            <span class="wg-card-sub">{{ node.name || '本机' }}</span>
          </div>
          <span class="wg-spacer" />
          <RouterLink to="/nodes" class="wg-btn sm ghost">详情</RouterLink>
        </div>
        <div class="wg-card-body">

          <dl class="wg-kv">
            <dt>版本</dt>
            <dd class="wg-mono">{{ node.version || '--' }}</dd>
            <dt>运行时长</dt>
            <dd class="wg-mono">{{ formatUptime(node.uptime_sec) }}</dd>
            <template v-if="node.host_known">
              <dt>CPU</dt>
              <dd>
                <div class="wg-row" style="gap: 8px">
                  <span class="wg-mono">{{ cpuPercent }}%</span>
                  <div class="bar" style="flex: 1">
                    <span
                      class="bar-in"
                      :class="{ hot: cpuPercent >= 85 }"
                      :style="{ width: cpuPercent + '%' }"
                    />
                  </div>
                </div>
              </dd>
              <dt>内存</dt>
              <dd>
                <div class="wg-row" style="gap: 8px">
                  <span class="wg-mono">{{ formatBytes(node.mem_used) }} / {{ formatBytes(node.mem_total) }}</span>
                  <div class="bar" style="flex: 1">
                    <span class="bar-in" :class="{ hot: memPercent >= 85 }" :style="{ width: memPercent + '%' }" />
                  </div>
                  <span class="wg-dim wg-mono" style="font-size: var(--fs-11)">{{ memPercent }}%</span>
                </div>
              </dd>
            </template>
            <dt>协程</dt>
            <dd class="wg-mono">{{ formatNumber(node.goroutines) }}</dd>
            <dt>堆内存</dt>
            <dd>
              <div class="wg-row" style="gap: 8px">
                <span class="wg-mono">{{ formatBytes(node.heap_alloc) }}</span>
                <div class="bar" style="flex: 1">
                  <span class="bar-in" :style="{ width: heapPercent + '%' }" />
                </div>
                <span class="wg-dim wg-mono" style="font-size: var(--fs-11)">{{ heapPercent }}%</span>
              </div>
            </dd>
            <dt>GC 次数</dt>
            <dd class="wg-mono">{{ formatNumber(node.num_gc) }}</dd>
          </dl>

          <!-- Fleet-wide traffic: every tunnel added together. -->
          <div class="fleet">
            <div class="fleet-item">
              <span class="fleet-label">全部隧道实时合计</span>
              <span class="fleet-value">
                {{ formatBytes(fleetRate, 1) }}<small>/s</small>
              </span>
              <span class="fleet-sub">
                <span class="c-in">入 {{ formatBytes(overview.rate_in, 1) }}/s</span>
                <span class="c-out">出 {{ formatBytes(overview.rate_out, 1) }}/s</span>
              </span>
            </div>
            <div class="fleet-item">
              <span class="fleet-label">累计入站</span>
              <span class="fleet-value">{{ formatBytes(overview.total_in) }}</span>
              <span class="fleet-sub">所有隧道相加，自进程启动</span>
            </div>
            <div class="fleet-item">
              <span class="fleet-label">累计出站</span>
              <span class="fleet-value">{{ formatBytes(overview.total_out) }}</span>
              <span class="fleet-sub">所有隧道相加，自进程启动</span>
            </div>
          </div>
        </div>
      </section>
    </div>
  </div>
</template>

<style scoped>
/* ---- alerts ------------------------------------------------------------ */

.alerts {
  border: 1px solid var(--danger-soft);
  border-radius: var(--r-lg);
  background: linear-gradient(180deg, var(--danger-soft), transparent 72%), var(--surface-1);
  overflow: hidden;
}
.alerts-head {
  display: flex;
  align-items: center;
  gap: 8px;
  padding: 10px 14px;
  font-size: var(--fs-12);
  color: var(--danger);
  border-bottom: 1px solid var(--hairline);
}
.alerts-head strong { font-weight: 600; }
.alerts-list { display: flex; flex-direction: column; }
.alert-row {
  display: flex;
  align-items: center;
  gap: 10px;
  padding: 9px 14px;
  font-size: var(--fs-12);
  border-bottom: 1px solid var(--hairline);
}
.alert-row:last-child { border-bottom: 0; }
.alert-row:hover { background: var(--surface-2); }
.alert-msg { min-width: 0; color: var(--text); }

/* ---- layout ------------------------------------------------------------ */

.bottom-grid {
  display: grid;
  grid-template-columns: minmax(0, 1.45fr) minmax(0, 1fr);
  gap: 14px;
  align-items: start;
}

/* ---- top tunnels ------------------------------------------------------- */

.top-row {
  display: grid;
  grid-template-columns: minmax(0, 1fr) 172px auto;
  gap: 14px;
  align-items: center;
  padding: 10px 0;
  border-bottom: 1px solid var(--hairline);
}
.top-row:last-child { border-bottom: 0; }
.top-main { min-width: 0; display: flex; flex-direction: column; gap: 3px; }
.top-id { display: flex; align-items: center; gap: 8px; min-width: 0; font-size: var(--fs-13); }
.top-addr { font-size: var(--fs-11); color: var(--text-dim); }
.top-bars { display: flex; flex-direction: column; gap: 5px; }
.top-num { display: flex; flex-direction: column; align-items: flex-end; gap: 2px; font-size: var(--fs-12); }

.bar {
  height: 5px;
  border-radius: 999px;
  background: var(--surface-3);
  overflow: hidden;
}
.bar > span { display: block; height: 100%; border-radius: inherit; }
.bar-in { background: linear-gradient(90deg, var(--accent), var(--accent-bright)); }
/* CPU / memory at 85%+ is the point where an operator should look. */
.bar-in.hot { background: linear-gradient(90deg, var(--warn), var(--danger)); }
.bar-out { background: linear-gradient(90deg, var(--violet), var(--violet-bright)); }

/* ---- fleet totals ------------------------------------------------------ */

.fleet {
  display: grid;
  grid-template-columns: repeat(auto-fit, minmax(112px, 1fr));
  gap: 12px;
  margin-top: 14px;
  padding-top: 13px;
  border-top: 1px solid var(--hairline);
}
.fleet-item { display: flex; flex-direction: column; gap: 3px; min-width: 0; }
.fleet-label { font-size: var(--fs-11); color: var(--text-dim); }
.fleet-value { font-family: var(--font-mono); font-size: var(--fs-16); color: var(--text); line-height: 1.25; }
.fleet-value small { font-size: var(--fs-11); color: var(--text-dim); }
.fleet-sub { display: flex; flex-wrap: wrap; gap: 8px; font-size: var(--fs-11); color: var(--text-dim); }
.fleet-sub .c-in { color: var(--ok); }
.fleet-sub .c-out { color: var(--accent); }

@media (max-width: 1024px) {
  .bottom-grid { grid-template-columns: minmax(0, 1fr); }
}
@media (max-width: 640px) {
  .top-row { grid-template-columns: minmax(0, 1fr) auto; }
  .top-bars { display: none; }
}
</style>
