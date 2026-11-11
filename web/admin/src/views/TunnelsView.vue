<script setup>
/**
 * Global tunnel inventory with per-tunnel traffic detail.
 *
 * The table is fed by GET /api/tunnels (server-scoped by role: an `user` only
 * sees their own registrations). Selecting a row lazily pulls the full series
 * from GET /api/stats and renders it inline — the series payload is ~50x the
 * table payload, so it is never fetched until an operator asks for it.
 */
import { computed, onMounted, ref } from 'vue'

import TopBar from '@common/ui/TopBar.vue'
import PageHead from '@common/ui/PageHead.vue'
import TButton from '@common/ui/TButton.vue'
import TModal from '@common/ui/TModal.vue'
import TrafficChart from '@common/ui/TrafficChart.vue'
import EmptyState from '@common/ui/EmptyState.vue'
import StatusPill from '@common/ui/StatusPill.vue'
import Icon from '@common/ui/Icon.vue'
import { formatBytes, formatTime, copyText } from '@common/format'
import { notifyOk, notifyError } from '@common/toast'

import { adminApi } from '../api'
import { live } from '../store'
import { session, state } from '../session'

const tunnels = ref([])
const loading = ref(true)
const query = ref('')
const ownerFilter = ref('')
const revoking = ref(null)
const pendingRevoke = ref(null)
const detail = ref(null)
const detailSeries = ref([])
const detailLoading = ref(false)

const owners = computed(() => {
  const set = new Set(tunnels.value.map((t) => t.owner).filter(Boolean))
  return [...set].sort()
})

const filtered = computed(() => {
  const q = query.value.trim().toLowerCase()
  return tunnels.value.filter((t) => {
    if (ownerFilter.value && t.owner !== ownerFilter.value) return false
    if (!q) return true
    return [t.name, t.owner, t.public_addr, t.client, t.type]
      .filter(Boolean)
      .some((v) => String(v).toLowerCase().includes(q))
  })
})

const totals = computed(() =>
  filtered.value.reduce(
    (acc, t) => {
      acc.in += t.bytes_in || 0
      acc.out += t.bytes_out || 0
      acc.conn += t.active_connections || 0
      return acc
    },
    { in: 0, out: 0, conn: 0 }
  )
)

async function load() {
  loading.value = true
  try {
    const payload = await adminApi.tunnels()
    tunnels.value = payload?.tunnels || []
  } catch (err) {
    notifyError(err, '加载隧道失败')
  } finally {
    loading.value = false
  }
}

async function confirmRevoke() {
  const target = pendingRevoke.value
  if (!target) return
  revoking.value = target.id
  try {
    await adminApi.revokeTunnel(target.id)
    notifyOk('隧道已强制下线', target.name)
    pendingRevoke.value = null
    await load()
  } catch (err) {
    notifyError(err, '操作失败')
  } finally {
    revoking.value = null
  }
}

/**
 * Open the detail drawer for one tunnel.
 * Falls back to an empty series when /api/stats has not sampled the tunnel yet.
 */
async function openDetail(tunnel) {
  detail.value = tunnel
  detailSeries.value = []
  detailLoading.value = true
  try {
    const payload = await adminApi.stats()
    const match = (payload?.tunnels || []).find((s) => s.id === tunnel.id)
    detailSeries.value = match?.samples || []
  } catch (err) {
    notifyError(err, '加载流量明细失败')
  } finally {
    detailLoading.value = false
  }
}

async function copy(value) {
  if (!value) return
  const ok = await copyText(value)
  if (ok) notifyOk('已复制', value)
  else notifyError(new Error('浏览器拒绝了剪贴板访问'), '请手动选择地址复制')
}

onMounted(load)
</script>

<template>
  <TopBar title="隧道" subtitle="全平台注册的隧道与实时流量" :live-state="live.link" :version="state.meta?.version">
    <template #actions>
      <TButton size="sm" icon="refresh" :loading="loading" @click="load">刷新</TButton>
    </template>
  </TopBar>

  <div class="wg-page">
    <PageHead
      title="全局隧道"
      :description="`${filtered.length} 条 · 活跃连接 ${totals.conn} · 入站 ${formatBytes(totals.in)} / 出站 ${formatBytes(totals.out)}`"
    />

    <div class="filters">
      <div class="search">
        <Icon name="search" :size="14" />
        <input v-model="query" placeholder="搜索名称 / 属主 / 公网地址" spellcheck="false" />
      </div>
      <select v-model="ownerFilter" class="wg-select" style="width: 170px">
        <option value="">全部属主</option>
        <option v-for="o in owners" :key="o" :value="o">{{ o }}</option>
      </select>
      <span class="wg-spacer" />
      <span class="wg-hint">数据随实时推送刷新，点击行查看流量明细</span>
    </div>

    <section class="wg-card">
      <div v-if="loading && !tunnels.length" class="wg-card-body">
        <div class="wg-skeleton" style="height: 18px; margin-bottom: 10px" />
        <div class="wg-skeleton" style="height: 18px; margin-bottom: 10px" />
        <div class="wg-skeleton" style="height: 18px" />
      </div>

      <div v-else-if="filtered.length" class="wg-table-wrap">
        <table class="wg-table">
          <thead>
            <tr>
              <th>隧道</th>
              <th>属主</th>
              <th>公网地址</th>
              <th>类型</th>
              <th class="num">活跃</th>
              <th class="num">入站</th>
              <th class="num">出站</th>
              <th>创建时间</th>
              <th class="actions">操作</th>
            </tr>
          </thead>
          <tbody>
            <tr v-for="t in filtered" :key="t.id" class="clickable" @click="openDetail(t)">
              <td>
                <div class="wg-primary-cell">
                  <span class="wg-dot live" data-tone="ok" />
                  <div style="min-width: 0">
                    <div class="name wg-truncate">{{ t.name }}</div>
                    <div class="meta wg-truncate">{{ t.client }}</div>
                  </div>
                </div>
              </td>
              <td><span class="wg-badge outline">{{ t.owner }}</span></td>
              <td>
                <button class="addr" @click.stop="copy(t.public_addr)">
                  <span class="wg-mono wg-truncate">{{ t.public_addr }}</span>
                  <Icon name="copy" :size="12" />
                </button>
              </td>
              <td>
                <span class="wg-badge outline wg-mono">{{ t.type }}</span>
                <!-- 传输层始终是 TLS，内层 AES 是每条隧道单独开的，两者分开显示 -->
                <span class="wg-badge outline" title="客户端 ↔ 服务器通道：TLS 1.3，始终开启">
                  <Icon name="lock" :size="10" />TLS
                </span>
                <span v-if="t.encryption" class="wg-badge lock" data-tone="ok" title="隧道内层：AES-256-GCM，密钥来自两端共享的 stream_secret">
                  <Icon name="lock" :size="10" />隧道加密
                </span>
                <span v-else class="wg-badge outline" data-tone="warn" title="隧道内层未加密，只有外层 TLS 保护">
                  内层明文
                </span>
              </td>
              <td class="num">{{ t.active_connections }}</td>
              <td class="num">{{ formatBytes(t.bytes_in) }}</td>
              <td class="num">{{ formatBytes(t.bytes_out) }}</td>
              <td class="wg-dim" style="font-size: var(--fs-11)">{{ formatTime(t.created_at) }}</td>
              <td class="actions">
                <button
                  class="wg-icon-btn"
                  aria-label="查看明细"
                  title="流量明细"
                  @click.stop="openDetail(t)"
                >
                  <Icon name="activity" />
                </button>
                <button
                  v-if="session.canRole('admin') || t.owner_id === state.user?.id"
                  class="wg-icon-btn danger"
                  aria-label="强制下线"
                  title="强制下线"
                  @click.stop="pendingRevoke = t"
                >
                  <Icon name="power" />
                </button>
              </td>
            </tr>
          </tbody>
        </table>
      </div>

      <EmptyState
        v-else-if="tunnels.length"
        icon="filter"
        title="没有匹配的隧道"
        description="调整搜索关键字或属主筛选。"
      >
        <button class="wg-btn sm" @click="query = ''; ownerFilter = ''">清除筛选</button>
      </EmptyState>

      <EmptyState v-else icon="tunnel" title="暂无注册隧道" description="客户端连接并通过认证后，其隧道会出现在这里。" />
    </section>
  </div>

  <!-- Detail drawer -->
  <Teleport to="body">
    <Transition name="wg-fade">
      <div v-if="detail" class="wg-overlay" @click.self="detail = null" />
    </Transition>
    <Transition name="wg-drawer">
      <aside v-if="detail" class="wg-drawer" role="dialog" aria-modal="true" aria-label="隧道明细">
        <header class="wg-modal-head">
          <div style="flex: 1; min-width: 0">
            <h3 class="wg-truncate">{{ detail.name }}</h3>
            <p class="wg-mono">{{ detail.public_addr }}</p>
          </div>
          <button class="wg-icon-btn" aria-label="关闭" @click="detail = null"><Icon name="x" /></button>
        </header>

        <div class="wg-modal-body" style="padding-bottom: 20px">
          <dl class="wg-kv" style="margin-bottom: 16px">
            <dt>属主</dt>
            <dd>{{ detail.owner }}</dd>
            <dt>客户端</dt>
            <dd class="wg-mono">{{ detail.client }}</dd>
            <dt>类型</dt>
            <dd class="wg-mono">{{ detail.type }}</dd>
            <dt>子域名</dt>
            <dd class="wg-mono">{{ detail.subdomain || '--' }}</dd>
            <dt>远程端口</dt>
            <dd class="wg-mono">{{ detail.remote_port || '--' }}</dd>
            <dt>创建时间</dt>
            <dd>{{ formatTime(detail.created_at) }}</dd>
            <dt>活跃连接</dt>
            <dd class="wg-mono">{{ detail.active_connections }} / 累计 {{ detail.connections }}</dd>
            <dt>累计流量</dt>
            <dd class="wg-mono">↓ {{ formatBytes(detail.bytes_in) }} · ↑ {{ formatBytes(detail.bytes_out) }}</dd>
          </dl>

          <div class="wg-section">
            <h2>流量曲线</h2>
            <span class="wg-section-hint">最近 5 分钟，每秒采样</span>
          </div>

          <div v-if="detailLoading" class="wg-skeleton" style="height: 200px" />
          <TrafficChart v-else-if="detailSeries.length" :samples="detailSeries" :height="200" compact />
          <EmptyState v-else icon="activity" title="暂无采样" description="该隧道刚开始传输，稍后重试。" />
        </div>

        <footer class="wg-modal-foot">
          <TButton variant="ghost" @click="detail = null">关闭</TButton>
          <TButton
            v-if="session.canRole('admin') || detail.owner_id === state.user?.id"
            variant="danger"
            icon="power"
            @click="pendingRevoke = detail; detail = null"
          >
            强制下线
          </TButton>
        </footer>
      </aside>
    </Transition>
  </Teleport>

  <!-- Revoke confirmation -->
  <TModal
    :model-value="!!pendingRevoke"
    title="强制下线隧道"
    :description="pendingRevoke ? `${pendingRevoke.name}（属主 ${pendingRevoke.owner}）` : ''"
    @update:model-value="pendingRevoke = null"
  >
    <p class="wg-hint" style="font-size: var(--fs-13)">
      将立即撤销公网入口并断开所有活动连接，操作会写入审计日志。客户端会按退避策略尝试重新注册。
    </p>
    <template #footer>
      <TButton variant="ghost" @click="pendingRevoke = null">取消</TButton>
      <TButton variant="danger" :loading="revoking === pendingRevoke?.id" @click="confirmRevoke">确认下线</TButton>
    </template>
  </TModal>
</template>

<style scoped>
.filters { display: flex; align-items: center; gap: 10px; flex-wrap: wrap; }

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
  width: 280px;
}
.search:focus-within { border-color: var(--accent-line); box-shadow: var(--ring); }
.search input { flex: 1; min-width: 0; background: none; border: 0; font-size: var(--fs-13); color: var(--text); }
.search input::placeholder { color: var(--text-dim); }

.clickable { cursor: pointer; }

.addr {
  display: inline-flex;
  align-items: center;
  gap: 5px;
  padding: 2px 6px;
  margin-left: -6px;
  border-radius: var(--r-xs);
  color: var(--accent);
  font-size: var(--fs-12);
  max-width: 30ch;
}
.addr:hover { background: var(--accent-soft); }
.addr svg { opacity: .5; flex: none; }

.lock { display: inline-flex; align-items: center; gap: 3px; margin-left: 5px; vertical-align: middle; }

.wg-drawer-enter-active { transition: transform var(--dur-3) var(--ease-out); }
.wg-drawer-leave-active { transition: transform var(--dur-2) var(--ease); }
.wg-drawer-enter-from,
.wg-drawer-leave-to { transform: translateX(100%); }
</style>
