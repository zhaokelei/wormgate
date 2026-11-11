<script setup>
/**
 * Live connection table.
 *
 * Data comes from the push snapshot, so rows appear and disappear without a
 * request. Local filtering runs in memory: a single console rarely sees more
 * than a few hundred live connections, and server-side paging here would add
 * latency to the one view where freshness matters most.
 */
import { computed, onMounted, ref } from 'vue'

import PageHead from '@common/ui/PageHead.vue'
import TopBar from '@common/ui/TopBar.vue'
import EmptyState from '@common/ui/EmptyState.vue'
import StatusPill from '@common/ui/StatusPill.vue'
import Icon from '@common/ui/Icon.vue'
import { formatBytes, formatDuration, fromNow, shortId } from '@common/format'

import { live, refreshStatus } from '../store'
import { state } from '../session'

const query = ref('')
const tunnelFilter = ref('')

const connections = computed(() => live.connections)

const tunnelNames = computed(() => {
  const names = new Set(connections.value.map((c) => c.tunnel))
  return [...names].sort()
})

const filtered = computed(() => {
  const q = query.value.trim().toLowerCase()
  return connections.value.filter((c) => {
    if (tunnelFilter.value && c.tunnel !== tunnelFilter.value) return false
    if (!q) return true
    return (
      c.tunnel?.toLowerCase().includes(q) ||
      c.source?.toLowerCase().includes(q) ||
      c.local?.toLowerCase().includes(q) ||
      c.id?.toLowerCase().includes(q)
    )
  })
})

const totals = computed(() =>
  filtered.value.reduce(
    (acc, c) => {
      acc.in += c.bytes_in || 0
      acc.out += c.bytes_out || 0
      return acc
    },
    { in: 0, out: 0 }
  )
)

function elapsed(startedAt) {
  if (!startedAt) return '--'
  return formatDuration((Date.now() - new Date(startedAt).getTime()) / 1000)
}

onMounted(() => {
  if (connections.value.length === 0) refreshStatus().catch(() => {})
})
</script>

<template>
  <TopBar
    title="连接"
    subtitle="当前经由隧道转发的活动连接"
    :live-state="live.link"
    :version="state.meta?.version"
  />

  <div class="wg-page">
    <PageHead
      title="活动连接"
      :description="`${connections.length} 条活动连接 · 合计入站 ${formatBytes(totals.in)} / 出站 ${formatBytes(totals.out)}`"
    >
      <template #actions>
        <div class="search">
          <Icon name="search" :size="14" />
          <input v-model="query" class="search-input" placeholder="搜索隧道 / 来源 / 本地" spellcheck="false" />
        </div>
        <select v-model="tunnelFilter" class="wg-select" style="width: 148px; height: 30px; font-size: var(--fs-12)">
          <option value="">全部隧道</option>
          <option v-for="name in tunnelNames" :key="name" :value="name">{{ name }}</option>
        </select>
      </template>
    </PageHead>

    <section class="wg-card">
      <div v-if="filtered.length" class="wg-table-wrap">
        <table class="wg-table">
          <thead>
            <tr>
              <th>连接</th>
              <th>隧道</th>
              <th>来源</th>
              <th>本地</th>
              <th>持续时间</th>
              <th class="num">入站</th>
              <th class="num">出站</th>
              <th>状态</th>
            </tr>
          </thead>
          <tbody>
            <tr v-for="c in filtered" :key="c.id">
              <td class="wg-mono" style="font-size: var(--fs-12); color: var(--text-muted)">
                {{ shortId(c.id, 10) }}
              </td>
              <td><span class="wg-badge outline">{{ c.tunnel }}</span></td>
              <td class="wg-mono wg-truncate">{{ c.source || '--' }}</td>
              <td class="wg-mono wg-truncate">{{ c.local || '--' }}</td>
              <td :title="c.started_at">{{ elapsed(c.started_at) }}</td>
              <td class="num">{{ formatBytes(c.bytes_in) }}</td>
              <td class="num">{{ formatBytes(c.bytes_out) }}</td>
              <td><StatusPill state="active" label="传输中" live /></td>
            </tr>
          </tbody>
        </table>
      </div>

      <EmptyState
        v-else-if="connections.length"
        icon="filter"
        title="没有匹配的连接"
        description="调整搜索条件或切换隧道筛选。"
      >
        <button class="wg-btn sm" @click="query = ''; tunnelFilter = ''">清除筛选</button>
      </EmptyState>

      <EmptyState
        v-else
        icon="wifi"
        title="暂无活动连接"
        description="当外部用户访问隧道公网地址时，连接会实时出现在这里，无需刷新。"
      />
    </section>
  </div>
</template>

<style scoped>
.search {
  display: flex;
  align-items: center;
  gap: 7px;
  height: 30px;
  padding: 0 10px;
  border-radius: var(--r-sm);
  background: var(--surface-2);
  border: 1px solid var(--hairline);
  color: var(--text-dim);
  width: 220px;
  transition: border-color var(--dur-1) var(--ease), box-shadow var(--dur-1) var(--ease);
}
.search:focus-within { border-color: var(--accent-line); box-shadow: var(--ring); }
.search-input {
  flex: 1;
  min-width: 0;
  background: none;
  border: 0;
  font-size: var(--fs-12);
  color: var(--text);
}
.search-input::placeholder { color: var(--text-dim); }
</style>
