<script setup>
/**
 * Connected clients (control-plane sessions).
 *
 * A row is one authenticated control WebSocket. `data_ready` tells whether the
 * yamux data channel is attached — a client can be connected yet unable to
 * forward traffic, which is the failure operators most often misdiagnose.
 */
import { computed, onMounted, ref } from 'vue'

import TopBar from '@common/ui/TopBar.vue'
import PageHead from '@common/ui/PageHead.vue'
import TButton from '@common/ui/TButton.vue'
import TModal from '@common/ui/TModal.vue'
import EmptyState from '@common/ui/EmptyState.vue'
import StatusPill from '@common/ui/StatusPill.vue'
import Icon from '@common/ui/Icon.vue'
import { formatDuration, fromNow, shortId } from '@common/format'
import { notifyOk, notifyError } from '@common/toast'

import { adminApi } from '../api'
import { live } from '../store'
import { session, state } from '../session'

const clients = ref([])
const loading = ref(true)
const query = ref('')
const pendingKick = ref(null)
const kicking = ref(null)

const filtered = computed(() => {
  const q = query.value.trim().toLowerCase()
  const now = Date.now()
  return clients.value
    .filter((c) => {
      if (!q) return true
      return [c.client_name, c.owner, c.remote_addr, c.session_id]
        .filter(Boolean)
        .some((v) => String(v).toLowerCase().includes(q))
    })
    .sort((a, b) => new Date(b.connected_at) - new Date(a.connected_at))
    .map((c) => ({
      ...c,
      // Age is frozen at fetch time by design: recomputing per render would
      // make the table churn every second without new information.
      age: (now - new Date(c.connected_at).getTime()) / 1000
    }))
})

const dataReadyCount = computed(() => clients.value.filter((c) => c.data_ready).length)

async function load() {
  loading.value = true
  try {
    const payload = await adminApi.clients()
    clients.value = payload?.clients || []
  } catch (err) {
    notifyError(err, '加载客户端失败')
  } finally {
    loading.value = false
  }
}

async function confirmKick() {
  const target = pendingKick.value
  if (!target) return
  kicking.value = target.session_id
  try {
    await adminApi.kickClient(target.session_id)
    notifyOk('已强制下线', target.client_name)
    pendingKick.value = null
    await load()
  } catch (err) {
    notifyError(err, '下线失败')
  } finally {
    kicking.value = null
  }
}

onMounted(load)
</script>

<template>
  <TopBar title="客户端" subtitle="控制通道会话与数据面状态" :live-state="live.link" :version="state.meta?.version">
    <template #actions>
      <TButton size="sm" icon="refresh" :loading="loading" @click="load">刷新</TButton>
    </template>
  </TopBar>

  <div class="wg-page">
    <PageHead
      title="在线客户端"
      :description="`${clients.length} 个会话 · 数据面就绪 ${dataReadyCount} 个`"
    >
      <template #actions>
        <div class="search">
          <Icon name="search" :size="14" />
          <input v-model="query" placeholder="搜索名称 / 属主 / 来源 IP" spellcheck="false" />
        </div>
      </template>
    </PageHead>

    <section class="wg-card">
      <div v-if="loading && !clients.length" class="wg-card-body">
        <div class="wg-skeleton" style="height: 18px; margin-bottom: 10px" />
        <div class="wg-skeleton" style="height: 18px" />
      </div>

      <div v-else-if="filtered.length" class="wg-table-wrap">
        <table class="wg-table">
          <thead>
            <tr>
              <th>客户端</th>
              <th>属主</th>
              <th>来源地址</th>
              <th>隧道</th>
              <th>数据面</th>
              <th>在线时长</th>
              <th>最近心跳</th>
              <th class="actions">操作</th>
            </tr>
          </thead>
          <tbody>
            <tr v-for="c in filtered" :key="c.session_id">
              <td>
                <div class="wg-primary-cell">
                  <span class="wg-dot live" :data-tone="c.data_ready ? 'ok' : 'warn'" />
                  <div style="min-width: 0">
                    <div class="name wg-truncate">{{ c.client_name || '未命名客户端' }}</div>
                    <div class="meta wg-mono">{{ shortId(c.session_id, 12) }}</div>
                  </div>
                </div>
              </td>
              <td><span class="wg-badge outline">{{ c.owner }}</span></td>
              <td class="wg-mono wg-truncate">{{ c.remote_addr }}</td>
              <td class="num">{{ c.tunnels }}</td>
              <td>
                <StatusPill
                  :state="c.data_ready ? 'ready' : 'degraded'"
                  :label="c.data_ready ? '已就绪' : '未就绪'"
                />
              </td>
              <td class="wg-mono">{{ formatDuration(c.age) }}</td>
              <td class="wg-dim" style="font-size: var(--fs-11)">{{ fromNow(c.last_seen) }}</td>
              <td class="actions">
                <button
                  v-if="session.canRole('admin') || c.owner_id === state.user?.id"
                  class="wg-icon-btn danger"
                  aria-label="强制下线"
                  title="强制下线"
                  @click="pendingKick = c"
                >
                  <Icon name="power" />
                </button>
                <span v-else class="wg-dim" style="font-size: var(--fs-11)">只读</span>
              </td>
            </tr>
          </tbody>
        </table>
      </div>

      <EmptyState
        v-else-if="clients.length"
        icon="filter"
        title="没有匹配的客户端"
        description="换个关键字试试。"
      >
        <button class="wg-btn sm" @click="query = ''">清除搜索</button>
      </EmptyState>

      <EmptyState
        v-else
        icon="wifiOff"
        title="当前没有在线客户端"
        description="客户端主动连接服务端后，会话会出现在这里；可查看数据面是否就绪。"
      />
    </section>
  </div>

  <TModal
    :model-value="!!pendingKick"
    title="强制下线客户端"
    :description="pendingKick ? `${pendingKick.client_name}（${pendingKick.remote_addr}）` : ''"
    @update:model-value="pendingKick = null"
  >
    <p class="wg-hint" style="font-size: var(--fs-13)">
      将关闭控制通道并撤销该会话的全部隧道，客户端会收到下线原因并按退避策略重连。操作会写入审计日志。
    </p>
    <template #footer>
      <TButton variant="ghost" @click="pendingKick = null">取消</TButton>
      <TButton variant="danger" :loading="kicking === pendingKick?.session_id" @click="confirmKick">确认下线</TButton>
    </template>
  </TModal>
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
  width: 260px;
}
.search:focus-within { border-color: var(--accent-line); box-shadow: var(--ring); }
.search input { flex: 1; min-width: 0; background: none; border: 0; font-size: var(--fs-13); color: var(--text); }
.search input::placeholder { color: var(--text-dim); }
</style>
