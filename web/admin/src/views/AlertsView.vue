<script setup>
/**
 * Alerts feed.
 *
 * Source of truth is GET /api/alerts (the same ring the dashboard samples from,
 * but complete and including acknowledged items). The overview push is used to
 * know *when* something changed, then the full list is refetched — cheaper than
 * pushing hundreds of alert objects every two seconds.
 */
import { computed, onMounted, ref, watch } from 'vue'

import TopBar from '@common/ui/TopBar.vue'
import PageHead from '@common/ui/PageHead.vue'
import TButton from '@common/ui/TButton.vue'
import EmptyState from '@common/ui/EmptyState.vue'
import StatusPill from '@common/ui/StatusPill.vue'
import Icon from '@common/ui/Icon.vue'
import { formatTime, fromNow } from '@common/format'
import { notifyOk, notifyError } from '@common/toast'

import { adminApi } from '../api'
import { live } from '../store'
import { session, state } from '../session'

const alerts = ref([])
const loading = ref(true)
const levelFilter = ref('all')
const onlyOpen = ref(true)
const acking = ref('')

const LEVEL_TONE = { critical: 'danger', error: 'danger', warning: 'warn', info: 'info' }

const filtered = computed(() =>
  alerts.value.filter((a) => {
    if (onlyOpen.value && a.ack) return false
    if (levelFilter.value !== 'all' && String(a.level).toLowerCase() !== levelFilter.value) return false
    return true
  })
)

const openCount = computed(() => alerts.value.filter((a) => !a.ack).length)

async function load() {
  loading.value = true
  try {
    const payload = await adminApi.alerts()
    alerts.value = payload?.alerts || []
  } catch (err) {
    notifyError(err, '加载告警失败')
  } finally {
    loading.value = false
  }
}

async function ack(alert) {
  acking.value = alert.id
  try {
    await adminApi.ackAlert(alert.id)
    alert.ack = true
    notifyOk('告警已确认')
  } catch (err) {
    notifyError(err, '确认失败')
  } finally {
    acking.value = ''
  }
}

/**
 * Refetch when the pushed alert count changes. A shallow length compare is
 * enough: the ring only grows (or is trimmed at its cap), and acknowledgement
 * is applied locally.
 */
watch(
  () => live.overview.alerts?.length ?? 0,
  (next, prev) => {
    if (next > prev) load()
  }
)

onMounted(load)
</script>

<template>
  <TopBar title="告警" subtitle="运行时可操作事件" :live-state="live.link" :version="state.meta?.version">
    <template #actions>
      <TButton size="sm" icon="refresh" :loading="loading" @click="load">刷新</TButton>
    </template>
  </TopBar>

  <div class="wg-page">
    <PageHead title="告警中心" :description="`${openCount} 条未确认 · 共 ${alerts.length} 条（保留最近 200 条）`" />

    <div class="filters">
      <div class="wg-seg" role="group" aria-label="级别筛选">
        <button :aria-pressed="levelFilter === 'all'" @click="levelFilter = 'all'">全部</button>
        <button :aria-pressed="levelFilter === 'critical'" @click="levelFilter = 'critical'">Critical</button>
        <button :aria-pressed="levelFilter === 'warning'" @click="levelFilter = 'warning'">Warning</button>
        <button :aria-pressed="levelFilter === 'info'" @click="levelFilter = 'info'">Info</button>
      </div>
      <label class="wg-switch">
        <input v-model="onlyOpen" type="checkbox" />
        <span class="track" />
        仅看未确认
      </label>
    </div>

    <section class="wg-card">
      <div v-if="filtered.length" class="alert-list">
        <article
          v-for="a in filtered"
          :key="a.id"
          class="alert"
          :data-tone="LEVEL_TONE[String(a.level).toLowerCase()] || 'info'"
          :data-ack="a.ack || undefined"
        >
          <span class="alert-icon">
            <Icon :name="a.ack ? 'checkCircle' : 'alertTriangle'" :size="15" />
          </span>
          <div class="alert-body">
            <div class="alert-top">
              <strong class="wg-truncate">{{ a.message }}</strong>
              <span class="wg-badge outline wg-mono">{{ a.level }}</span>
              <span v-if="a.code" class="wg-badge outline wg-mono">{{ a.code }}</span>
            </div>
            <div class="alert-meta">
              <span :title="a.time">{{ formatTime(a.time) }}</span>
              <span class="wg-dim">{{ fromNow(a.time) }}</span>
              <span v-if="a.ack" class="wg-ok">已确认</span>
            </div>
          </div>
          <TButton
            v-if="!a.ack && session.isAdmin"
            size="sm"
            variant="ghost"
            :loading="acking === a.id"
            @click="ack(a)"
          >
            确认
          </TButton>
          <StatusPill v-else-if="a.ack" state="closed" label="已处理" />
        </article>
      </div>

      <EmptyState
        v-else-if="alerts.length"
        icon="checkCircle"
        title="没有符合条件的告警"
        description="切换级别筛选或勾选“仅看未确认”以查看历史。"
      >
        <button class="wg-btn sm" @click="onlyOpen = false; levelFilter = 'all'">显示全部</button>
      </EmptyState>

      <EmptyState
        v-else
        icon="checkCircle"
        title="一切正常"
        description="当前没有告警记录。配额超限、客户端异常离线等事件会实时出现在这里。"
      />
    </section>
  </div>
</template>

<style scoped>
.filters { display: flex; align-items: center; gap: 14px; flex-wrap: wrap; }

.alert-list { display: flex; flex-direction: column; }

.alert {
  display: flex;
  align-items: center;
  gap: 12px;
  padding: 12px 15px;
  border-bottom: 1px solid var(--hairline);
  border-left: 2px solid transparent;
}
.alert:last-child { border-bottom: 0; }
.alert[data-tone='danger'] { border-left-color: var(--danger); }
.alert[data-tone='warn'] { border-left-color: var(--warn); }
.alert[data-tone='info'] { border-left-color: var(--info); }
.alert[data-ack] { opacity: .58; }

.alert-icon {
  display: grid;
  place-items: center;
  width: 28px;
  height: 28px;
  border-radius: 50%;
  flex: none;
  background: var(--surface-2);
}
.alert[data-tone='danger'] .alert-icon { background: var(--danger-soft); color: var(--danger); }
.alert[data-tone='warn'] .alert-icon { background: var(--warn-soft); color: var(--warn); }
.alert[data-tone='info'] .alert-icon { background: var(--info-soft); color: var(--info); }

.alert-body { flex: 1; min-width: 0; display: flex; flex-direction: column; gap: 2px; }
.alert-top { display: flex; align-items: center; gap: 8px; min-width: 0; }
.alert-top strong { font-size: var(--fs-13); font-weight: 500; }
.alert-meta { display: flex; align-items: center; gap: 10px; font-size: var(--fs-11); color: var(--text-dim); }
</style>
