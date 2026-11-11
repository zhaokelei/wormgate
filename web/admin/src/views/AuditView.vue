<script setup>
/**
 * Audit log.
 *
 * Append-only, newest first. Available to admin and viewer (read-only) roles —
 * an auditor needs the trail without the ability to change state, which is why
 * the endpoint is role-gated on the server and not just hidden here.
 */
import { computed, onMounted, ref } from 'vue'

import TopBar from '@common/ui/TopBar.vue'
import PageHead from '@common/ui/PageHead.vue'
import TButton from '@common/ui/TButton.vue'
import EmptyState from '@common/ui/EmptyState.vue'
import StatusPill from '@common/ui/StatusPill.vue'
import Icon from '@common/ui/Icon.vue'
import { formatTime, fromNow } from '@common/format'
import { notifyError } from '@common/toast'

import { adminApi } from '../api'
import { live } from '../store'
import { state } from '../session'

const events = ref([])
const loading = ref(true)
const query = ref('')
const severity = ref('all')
const limit = ref(200)

const SEVERITY_TONE = { info: 'info', warning: 'warn', critical: 'danger', error: 'danger' }

/**
 * Action verbs are namespaced ("user.create", "tunnel.revoke"); showing the
 * namespace as a badge makes the trail scannable without reading every row.
 */
function splitAction(action) {
  const [group, verb] = String(action || '').split('.')
  return { group: verb ? group : 'event', verb: verb || group }
}

const filtered = computed(() => {
  const q = query.value.trim().toLowerCase()
  return events.value.filter((e) => {
    if (severity.value !== 'all' && String(e.severity).toLowerCase() !== severity.value) return false
    if (!q) return true
    return [e.actor, e.action, e.target, e.detail, e.ip]
      .filter(Boolean)
      .some((v) => String(v).toLowerCase().includes(q))
  })
})

async function load() {
  loading.value = true
  try {
    const payload = await adminApi.audit(limit.value)
    events.value = payload?.events || []
  } catch (err) {
    notifyError(err, '加载审计日志失败')
  } finally {
    loading.value = false
  }
}

function exportJSON() {
  const blob = new Blob([JSON.stringify(filtered.value, null, 2)], { type: 'application/json' })
  const url = URL.createObjectURL(blob)
  const a = document.createElement('a')
  a.href = url
  a.download = `wormgate-audit-${new Date().toISOString().replace(/[:.]/g, '-')}.json`
  a.click()
  URL.revokeObjectURL(url)
}

onMounted(load)
</script>

<template>
  <TopBar title="审计" subtitle="控制台与隧道操作留痕" :live-state="live.link" :version="state.meta?.version">
    <template #actions>
      <TButton size="sm" icon="refresh" :loading="loading" @click="load">刷新</TButton>
    </template>
  </TopBar>

  <div class="wg-page">
    <PageHead title="审计日志" :description="`${filtered.length} 条事件 · 按时间倒序 · 最多 ${limit} 条`">
      <template #actions>
        <div class="search">
          <Icon name="search" :size="14" />
          <input v-model="query" placeholder="搜索操作者 / 动作 / 目标 IP" spellcheck="false" />
        </div>
        <select v-model="severity" class="wg-select" style="width: 130px">
          <option value="all">全部级别</option>
          <option value="info">info</option>
          <option value="warning">warning</option>
          <option value="critical">critical</option>
        </select>
        <TButton size="sm" icon="download" :disabled="!filtered.length" @click="exportJSON">导出</TButton>
      </template>
    </PageHead>

    <section class="wg-card">
      <div v-if="loading && !events.length" class="wg-card-body">
        <div class="wg-skeleton" style="height: 18px; margin-bottom: 10px" />
        <div class="wg-skeleton" style="height: 18px" />
      </div>

      <div v-else-if="filtered.length" class="wg-table-wrap">
        <table class="wg-table">
          <thead>
            <tr>
              <th style="width: 148px">时间</th>
              <th>操作者</th>
              <th>动作</th>
              <th>目标</th>
              <th>详情</th>
              <th style="width: 128px">来源 IP</th>
              <th style="width: 104px">级别</th>
            </tr>
          </thead>
          <tbody>
            <tr v-for="e in filtered" :key="e.id">
              <td>
                <div class="wg-mono" style="font-size: var(--fs-11)">{{ formatTime(e.time) }}</div>
                <div class="wg-dim" style="font-size: var(--fs-10, 10px)">{{ fromNow(e.time) }}</div>
              </td>
              <td>
                <div class="wg-primary-cell">
                  <span class="avatar-sm" aria-hidden="true">{{ (e.actor || '?').slice(0, 1).toUpperCase() }}</span>
                  <span class="wg-truncate">{{ e.actor }}</span>
                </div>
              </td>
              <td>
                <span class="wg-badge outline wg-mono">{{ splitAction(e.action).group }}</span>
                <span class="wg-mono" style="font-size: var(--fs-11); margin-left: 5px">{{ splitAction(e.action).verb }}</span>
              </td>
              <td class="wg-truncate wg-mono" style="font-size: var(--fs-12)">{{ e.target || '--' }}</td>
              <td class="wg-muted wg-truncate" style="font-size: var(--fs-12); max-width: 34ch">{{ e.detail || '--' }}</td>
              <td class="wg-mono" style="font-size: var(--fs-11)">{{ e.ip || '--' }}</td>
              <td>
                <StatusPill :state="SEVERITY_TONE[String(e.severity).toLowerCase()] === 'danger' ? 'error' : SEVERITY_TONE[String(e.severity).toLowerCase()] === 'warn' ? 'degraded' : 'ok'"
                  :label="e.severity" />
              </td>
            </tr>
          </tbody>
        </table>
      </div>

      <EmptyState v-else-if="events.length" icon="filter" title="没有匹配的事件" description="调整关键字或级别筛选。">
        <button class="wg-btn sm" @click="query = ''; severity = 'all'">清除筛选</button>
      </EmptyState>

      <EmptyState v-else icon="list" title="暂无审计事件" description="登录、隧道撤销、用户变更等操作都会记录在这里。" />
    </section>
  </div>
</template>

<style scoped>
.search {
  display: flex;
  align-items: center;
  gap: 7px;
  height: 34px;
  padding: 0 11px;
  border-radius: var(--r-sm);
  background: var(--surface-2);
  border: 1px solid var(--hairline);
  color: var(--text-dim);
  width: 250px;
}
.search:focus-within { border-color: var(--accent-line); box-shadow: var(--ring); }
.search input { flex: 1; min-width: 0; background: none; border: 0; font-size: var(--fs-13); color: var(--text); }
.search input::placeholder { color: var(--text-dim); }

.avatar-sm {
  display: grid;
  place-items: center;
  width: 22px;
  height: 22px;
  flex: none;
  border-radius: 50%;
  background: var(--surface-3);
  border: 1px solid var(--hairline);
  font-size: 10px;
  font-weight: 600;
  color: var(--text-muted);
}
</style>
