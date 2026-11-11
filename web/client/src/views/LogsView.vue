<script setup>
/**
 * Live log stream over Server-Sent Events.
 *
 * Why SSE here and WebSocket elsewhere: logs are strictly server -> browser, so
 * SSE's native EventSource reconnection and plain-text framing are a better fit
 * than a bidirectional socket. The Go handler emits `event: log` frames whose
 * `data` is one JSON logx.Entry:
 *
 *   id: 41
 *   event: log
 *   data: {"seq":41,"time":"2026-11-10T03:12:44.51Z","level":"info",
 *          "message":"tunnel registered","fields":{"tunnel":"web"}}
 *
 * The ring is bounded (2000 lines) on the client: an operator leaving this tab
 * open for hours must not grow the heap without limit.
 */
import { computed, onBeforeUnmount, onMounted, nextTick, ref, watch } from 'vue'

import PageHead from '@common/ui/PageHead.vue'
import TopBar from '@common/ui/TopBar.vue'
import TButton from '@common/ui/TButton.vue'
import StatusPill from '@common/ui/StatusPill.vue'
import Icon from '@common/ui/Icon.vue'
import { subscribeSSE } from '@common/live'

import { live } from '../store'
import { state } from '../session'

const MAX_LINES = 2000
const LEVELS = [
  { value: 'debug', label: 'Debug' },
  { value: 'info', label: 'Info' },
  { value: 'warn', label: 'Warn' },
  { value: 'error', label: 'Error' }
]

const entries = ref([])
const level = ref('debug')
const paused = ref(false)
const autoScroll = ref(true)
const linkState = ref('closed')
const query = ref('')
const stream = ref(null)
const viewport = ref(null)

const filtered = computed(() => {
  const q = query.value.trim().toLowerCase()
  if (!q) return entries.value
  return entries.value.filter(
    (e) =>
      e.message?.toLowerCase().includes(q) ||
      JSON.stringify(e.fields || {}).toLowerCase().includes(q) ||
      e.level?.toLowerCase().includes(q)
  )
})

const counts = computed(() => {
  const acc = { debug: 0, info: 0, warn: 0, error: 0, other: 0 }
  for (const e of entries.value) {
    const key = (e.level || '').toLowerCase()
    if (key in acc) acc[key]++
    else acc.other++
  }
  return acc
})

function connect() {
  disconnect()
  stream.value = subscribeSSE({
    path: `/api/logs?level=${encodeURIComponent(level.value)}`,
    onState: (s) => {
      linkState.value = s === 'open' ? 'open' : 'closed'
    },
    onEvent: (entry) => {
      if (paused.value) return
      entries.value.push(entry)
      // Trim in one splice once past the cap: keeps the array contiguous and
      // avoids a per-line copy on long sessions.
      if (entries.value.length > MAX_LINES) {
        entries.value.splice(0, entries.value.length - MAX_LINES)
      }
      if (autoScroll.value) scrollToBottom()
    }
  })
}

function disconnect() {
  stream.value?.()
  stream.value = null
  linkState.value = 'closed'
}

async function scrollToBottom() {
  await nextTick()
  const el = viewport.value
  if (el) el.scrollTop = el.scrollHeight
}

function setLevel(next) {
  if (level.value === next) return
  level.value = next
  entries.value = []
  connect()
}

/** Detach from the bottom once the operator scrolls up (standard log UX). */
function onScroll() {
  const el = viewport.value
  if (!el) return
  autoScroll.value = el.scrollHeight - el.scrollTop - el.clientHeight < 40
}

function clear() {
  entries.value = []
}

function download() {
  const text = filtered.value
    .map((e) => `${e.time} ${String(e.level).toUpperCase().padEnd(5)} ${e.message} ${formatFields(e.fields)}`)
    .join('\n')
  const blob = new Blob([text], { type: 'text/plain;charset=utf-8' })
  const url = URL.createObjectURL(blob)
  const a = document.createElement('a')
  a.href = url
  a.download = `tunnelx-client-${new Date().toISOString().replace(/[:.]/g, '-')}.log`
  a.click()
  URL.revokeObjectURL(url)
}

function formatFields(fields) {
  if (!fields || Object.keys(fields).length === 0) return ''
  return Object.entries(fields)
    .map(([k, v]) => {
      const value = typeof v === 'object' ? JSON.stringify(v) : v
      return `${k}=${value}`
    })
    .join(' ')
}

function clock(time) {
  if (!time) return '--:--:--'
  return new Date(time).toLocaleTimeString('zh-CN', { hour12: false })
}

const LEVEL_TONE = { debug: 'default', info: 'info', warn: 'warn', error: 'danger', fatal: 'danger' }

watch(autoScroll, (on) => {
  if (on) scrollToBottom()
})

onMounted(connect)
onBeforeUnmount(disconnect)
</script>

<template>
  <TopBar
    title="日志"
    subtitle="客户端运行时日志，SSE 实时推送"
    :live-state="live.link"
    :version="state.meta?.version"
  />

  <div class="wg-page">
    <PageHead title="运行日志" :description="`保留最近 ${MAX_LINES} 行 · 当前 ${entries.length} 行`">
      <template #actions>
        <div class="wg-seg" role="group" aria-label="日志级别">
          <button v-for="l in LEVELS" :key="l.value" :aria-pressed="level === l.value" @click="setLevel(l.value)">
            {{ l.label }}
          </button>
        </div>
        <TButton size="sm" :icon="paused ? 'play' : 'pause'" @click="paused = !paused">
          {{ paused ? '继续' : '暂停' }}
        </TButton>
        <TButton size="sm" icon="download" :disabled="!filtered.length" @click="download">导出</TButton>
        <TButton size="sm" variant="ghost" icon="trash" :disabled="!entries.length" @click="clear">清空</TButton>
      </template>
    </PageHead>

    <section class="wg-card logcard">
      <div class="wg-card-head">
        <StatusPill
          :state="linkState === 'open' ? 'connected' : 'disconnected'"
          :label="linkState === 'open' ? '流已连接' : '连接中断'"
          :live="linkState === 'open'"
        />
        <span class="wg-spacer" />
        <div class="search">
          <Icon name="search" :size="13" />
          <input v-model="query" placeholder="过滤日志…" spellcheck="false" />
        </div>
        <label class="wg-switch">
          <input v-model="autoScroll" type="checkbox" />
          <span class="track" />
          自动滚动
        </label>
      </div>

      <div ref="viewport" class="logview wg-scroll" @scroll.passive="onScroll">
        <div v-if="!filtered.length" class="wg-empty" style="padding: 40px">
          <div class="wg-empty-icon"><Icon name="terminal" :size="20" /></div>
          <h4>{{ entries.length ? '没有匹配的日志' : '等待日志输出' }}</h4>
          <p>{{ entries.length ? '调整过滤关键字或切换级别。' : '级别过滤为 ' + level + '，客户端产生日志后会实时出现。' }}</p>
        </div>

        <div v-for="e in filtered" :key="e.seq" class="logline" :data-level="String(e.level).toLowerCase()">
          <span class="ltime wg-mono">{{ clock(e.time) }}</span>
          <span class="llevel wg-mono" :data-tone="LEVEL_TONE[String(e.level).toLowerCase()] || 'default'">
            {{ String(e.level).toUpperCase() }}
          </span>
          <span class="lmsg">{{ e.message }}</span>
          <span v-if="e.fields && Object.keys(e.fields).length" class="lfields wg-mono">{{ formatFields(e.fields) }}</span>
        </div>
      </div>

      <div class="wg-card-foot">
        <span class="wg-hint wg-mono">
          debug {{ counts.debug }} · info {{ counts.info }} · warn {{ counts.warn }} · error {{ counts.error }}
        </span>
        <span class="wg-spacer" />
        <span v-if="paused" class="wg-badge" data-tone="warn">已暂停（仅停止渲染）</span>
      </div>
    </section>
  </div>
</template>

<style scoped>
.logcard { display: flex; flex-direction: column; flex: 1; min-height: 420px; }

.logview {
  flex: 1;
  min-height: 360px;
  max-height: calc(100vh - 300px);
  overflow-y: auto;
  padding: 6px 0;
  font-family: var(--font-mono);
  font-size: var(--fs-12);
  background: var(--surface-2);
}

.logline {
  display: flex;
  align-items: baseline;
  gap: 10px;
  padding: 2px 14px;
  line-height: 1.7;
  border-left: 2px solid transparent;
  white-space: pre-wrap;
  word-break: break-word;
}
.logline:hover { background: var(--surface-3); }
.logline[data-level='warn'] { border-left-color: var(--warn); background: color-mix(in srgb, var(--warn-soft) 40%, transparent); }
.logline[data-level='error'],
.logline[data-level='fatal'] { border-left-color: var(--danger); background: color-mix(in srgb, var(--danger-soft) 45%, transparent); }

.ltime { color: var(--text-dim); flex: none; }
.llevel { flex: none; width: 46px; font-size: var(--fs-11); font-weight: 600; letter-spacing: .03em; }
.llevel[data-tone='info'] { color: var(--info); }
.llevel[data-tone='warn'] { color: var(--warn); }
.llevel[data-tone='danger'] { color: var(--danger); }
.llevel[data-tone='default'] { color: var(--text-dim); }
.lmsg { flex: 1; min-width: 0; color: var(--text); }
.lfields { color: var(--text-dim); flex: none; max-width: 45%; overflow: hidden; text-overflow: ellipsis; }

.search {
  display: flex;
  align-items: center;
  gap: 6px;
  height: 26px;
  padding: 0 9px;
  border-radius: var(--r-xs);
  background: var(--surface-2);
  border: 1px solid var(--hairline);
  color: var(--text-dim);
  width: 180px;
}
.search input {
  flex: 1;
  min-width: 0;
  background: none;
  border: 0;
  font-size: var(--fs-12);
  color: var(--text);
}
.search input::placeholder { color: var(--text-dim); }
</style>
