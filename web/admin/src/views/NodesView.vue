<script setup>
/**
 * Node monitoring.
 *
 * Currently a single node (the control plane and ingress are one process), but
 * the payload is already a list so a future distributed control plane needs no
 * UI change — only more items in GET /api/nodes.
 */
import { computed, onMounted, ref } from 'vue'

import TopBar from '@common/ui/TopBar.vue'
import PageHead from '@common/ui/PageHead.vue'
import KpiCard from '@common/ui/KpiCard.vue'
import TButton from '@common/ui/TButton.vue'
import EmptyState from '@common/ui/EmptyState.vue'
import StatusPill from '@common/ui/StatusPill.vue'
import Icon from '@common/ui/Icon.vue'
import { formatBytes, formatNumber, formatUptime } from '@common/format'
import { notifyError } from '@common/toast'

import { adminApi } from '../api'
import { live } from '../store'
import { state } from '../session'

const nodes = ref([])
const loading = ref(true)

const primary = computed(() => nodes.value[0] || {})

const heapPercent = computed(() => {
  const alloc = primary.value.heap_alloc || 0
  const sys = primary.value.heap_sys || 0
  return sys ? Math.min(100, Math.round((alloc / sys) * 100)) : 0
})

/** Goroutine count is a coarse but effective leak indicator for a Go service. */
const goroutineTone = computed(() => {
  const n = primary.value.goroutines || 0
  if (n > 5000) return 'danger'
  if (n > 2000) return 'warn'
  return 'ok'
})

/** Host-level numbers: what the machine is doing, not just the Go runtime. */
const cpuPercent = computed(() => Math.min(100, Math.max(0, Math.round(primary.value.cpu_percent || 0))))
const memPercent = computed(() => {
  const total = primary.value.mem_total || 0
  return total ? Math.min(100, Math.round(((primary.value.mem_used || 0) / total) * 100)) : 0
})
const hostTone = computed(() => (Math.max(cpuPercent.value, memPercent.value) >= 85 ? 'warn' : 'ok'))

async function load() {
  loading.value = true
  try {
    const payload = await adminApi.nodes()
    nodes.value = payload?.nodes || []
  } catch (err) {
    notifyError(err, '加载节点信息失败')
  } finally {
    loading.value = false
  }
}

onMounted(load)
</script>

<template>
  <TopBar title="节点" subtitle="控制面与数据面运行时" :live-state="live.link" :version="state.meta?.version">
    <template #actions>
      <TButton size="sm" icon="refresh" :loading="loading" @click="load">刷新</TButton>
    </template>
  </TopBar>

  <div class="wg-page">
    <PageHead title="节点监控" :description="`${nodes.length} 个节点 · 指标来自 Go runtime 与 Prometheus 计数器`" />

    <template v-if="nodes.length">
      <div class="wg-grid kpi">
        <KpiCard
          label="运行时长"
          :value="formatUptime(primary.uptime_sec)"
          icon="clock"
          tone="accent"
          :hint="primary.name"
        />
        <KpiCard
          label="协程数"
          :value="formatNumber(primary.goroutines)"
          icon="cpu"
          :tone="goroutineTone"
          hint="正常范围 < 2000"
        />
        <KpiCard
          v-if="primary.host_known"
          label="CPU 占用"
          :value="`${cpuPercent}%`"
          icon="gauge"
          :tone="hostTone"
          hint="宿主机整机平均"
        />
        <KpiCard
          v-if="primary.host_known"
          label="内存占用"
          :value="formatBytes(primary.mem_used)"
          icon="hardDrive"
          :tone="hostTone"
          :hint="`共 ${formatBytes(primary.mem_total)} · ${memPercent}%`"
        />
        <KpiCard
          label="堆内存"
          :value="formatBytes(primary.heap_alloc)"
          icon="database"
          tone="violet"
          :hint="`占用 ${heapPercent}% of ${formatBytes(primary.heap_sys)}`"
        />
        <KpiCard
          label="GC 次数"
          :value="formatNumber(primary.num_gc)"
          icon="refresh"
          tone="info"
          hint="进程启动以来"
        />
        <KpiCard
          label="在线客户端"
          :value="formatNumber(primary.clients)"
          icon="wifi"
          tone="ok"
          :hint="`隧道 ${primary.tunnels} 条`"
        />
      </div>

      <section class="wg-card">
        <div class="wg-card-head">
          <div class="wg-card-title">
            节点列表
            <span class="wg-card-sub">控制面 + 公网入口</span>
          </div>
        </div>
        <div class="wg-table-wrap">
          <table class="wg-table">
            <thead>
              <tr>
                <th>节点</th>
                <th>角色</th>
                <th>版本</th>
                <th class="num">客户端</th>
                <th class="num">隧道</th>
                <th class="num">协程</th>
                <th class="num">堆内存</th>
                <th>状态</th>
              </tr>
            </thead>
            <tbody>
              <tr v-for="n in nodes" :key="n.name">
                <td>
                  <div class="wg-primary-cell">
                    <span class="wg-dot live" data-tone="ok" />
                    <strong>{{ n.name }}</strong>
                  </div>
                </td>
                <td><span class="wg-badge outline wg-mono">{{ n.kind }}</span></td>
                <td class="wg-mono">{{ n.version }}</td>
                <td class="num">{{ n.clients }}</td>
                <td class="num">{{ n.tunnels }}</td>
                <td class="num">{{ formatNumber(n.goroutines) }}</td>
                <td class="num">{{ formatBytes(n.heap_alloc) }}</td>
                <td><StatusPill state="healthy" label="运行中" live /></td>
              </tr>
            </tbody>
          </table>
        </div>
        <div class="wg-card-foot">
          <Icon name="info" :size="13" style="color: var(--text-dim)" />
          <span class="wg-hint">
            指标端点：<code class="wg-code" style="display: inline; padding: 1px 5px">GET /metrics</code>（Prometheus 文本格式）
          </span>
        </div>
      </section>
    </template>

    <EmptyState v-else-if="!loading" icon="server" title="没有节点信息" description="节点接口未返回数据，请检查服务端日志。" />
  </div>
</template>
