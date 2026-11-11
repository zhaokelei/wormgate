<script setup>
/**
 * Tunnel management: list, create, edit, start/stop, delete.
 *
 * Design notes:
 *  - The tunnel list is sourced from the live snapshot (instant updates from
 *    the control plane) while mutations go through the REST API and are then
 *    reflected by the next push. No optimistic local edits: a tunnel that fails
 *    validation server-side must never appear to exist.
 *  - Create/edit share one modal. `mode` decides the wording; the field set is
 *    identical because the API accepts a full replacement on PUT.
 *  - Subdomain / remote port are optional by design: the server allocates them,
 *    which is the whole point of the "服务端自动分配" rule.
 */
import { computed, onMounted, reactive, ref, watch } from 'vue'

import PageHead from '@common/ui/PageHead.vue'
import TopBar from '@common/ui/TopBar.vue'
import TButton from '@common/ui/TButton.vue'
import TModal from '@common/ui/TModal.vue'
import StatusPill from '@common/ui/StatusPill.vue'
import EmptyState from '@common/ui/EmptyState.vue'
import Icon from '@common/ui/Icon.vue'
import { formatBytes, copyText } from '@common/format'
import { notifyOk, notifyError } from '@common/toast'

import { clientApi } from '../api'
import { live, refreshStatus } from '../store'
import { state } from '../session'

const tunnels = computed(() => live.status.tunnels || [])
const busy = reactive(new Set())
const editorOpen = ref(false)
const editorMode = ref('create')
const saving = ref(false)
const pendingDelete = ref(null)

const form = reactive({
  name: '',
  type: 'http',
  local: '',
  subdomain: '',
  remote_port: null,
  enabled: true,
  encryption: false
})
const errors = reactive({ name: '', local: '', encryption: '' })

/** Whether a shared secret exists. The switch is useless without one. */
const streamSecretSet = ref(false)
const secretKnown = ref(false)

onMounted(async () => {
  try {
    const settings = await clientApi.getSettings()
    streamSecretSet.value = !!settings?.stream_secret_set
  } catch {
    /* a console that cannot read settings simply keeps the switch disabled */
  } finally {
    secretKnown.value = true
  }
})

/** Validation mirrors internal/config/validate.go so the UI fails fast. */
const NAME_RE = /^[a-z0-9](?:[a-z0-9-]{0,38}[a-z0-9])?$/
const LOCAL_RE = /^(?:[a-z0-9.-]+|\[[0-9a-f:]+\]):\d{1,5}$/

function resetForm(source) {
  form.name = source?.name || ''
  form.type = source?.type || 'http'
  form.local = source?.local || ''
  form.subdomain = source?.subdomain || ''
  form.remote_port = source?.remote_port || null
  form.enabled = source ? source.enabled : true
  form.encryption = !!source?.encryption
  errors.name = ''
  errors.local = ''
  errors.encryption = ''
}

function openCreate() {
  editorMode.value = 'create'
  resetForm()
  editorOpen.value = true
}

function openEdit(tunnel) {
  editorMode.value = 'edit'
  resetForm(tunnel)
  editorOpen.value = true
}

function validate() {
  errors.name = ''
  errors.local = ''
  errors.encryption = ''
  if (editorMode.value === 'create') {
    if (!form.name.trim()) errors.name = '名称不能为空'
    else if (!NAME_RE.test(form.name.trim())) errors.name = '只能使用小写字母、数字和中划线，长度 1-40，且不能以中划线开头或结尾'
    else if (tunnels.value.some((t) => t.name === form.name.trim())) errors.name = '同名隧道已存在'
  }
  if (!form.local.trim()) errors.local = '本地目标不能为空'
  else if (!LOCAL_RE.test(form.local.trim())) errors.local = '格式应为 host:port，例如 127.0.0.1:8080'
  if (form.type === 'tcp' && form.subdomain) {
    errors.local = 'TCP 隧道请使用远程端口，而不是子域名'
  }
  if (form.encryption && secretKnown.value && !streamSecretSet.value) {
    errors.encryption = '还没有配置共享口令，请先到「配置 → 隧道加密」生成'
  }
  return !errors.name && !errors.local && !errors.encryption
}

async function save() {
  if (!validate() || saving.value) return
  saving.value = true
  const payload = {
    name: form.name.trim(),
    type: form.type,
    local: form.local.trim(),
    subdomain: form.type === 'http' ? form.subdomain.trim().toLowerCase() : '',
    remote_port: form.type === 'tcp' ? Number(form.remote_port) || 0 : 0,
    enabled: form.enabled,
    encryption: form.encryption
  }
  try {
    if (editorMode.value === 'create') {
      await clientApi.createTunnel(payload)
      notifyOk('隧道已创建', `${payload.name} → ${payload.local}`)
    } else {
      await clientApi.updateTunnel(payload.name, payload)
      notifyOk('隧道已更新', payload.name)
    }
    editorOpen.value = false
    await refreshStatus()
  } catch (err) {
    notifyError(err, '保存失败')
  } finally {
    saving.value = false
  }
}

async function toggle(tunnel, next) {
  busy.add(tunnel.name)
  try {
    if (next) await clientApi.startTunnel(tunnel.name)
    else await clientApi.stopTunnel(tunnel.name)
    notifyOk(next ? '隧道已启用' : '隧道已停用', tunnel.name)
    await refreshStatus()
  } catch (err) {
    notifyError(err, '切换失败')
  } finally {
    busy.delete(tunnel.name)
  }
}

async function confirmDelete() {
  const target = pendingDelete.value
  if (!target) return
  busy.add(target.name)
  try {
    await clientApi.deleteTunnel(target.name)
    notifyOk('隧道已删除', target.name)
    pendingDelete.value = null
    await refreshStatus()
  } catch (err) {
    notifyError(err, '删除失败')
  } finally {
    busy.delete(target.name)
  }
}

async function copy(value) {
  if (!value) return
  const ok = await copyText(value)
  if (ok) notifyOk('已复制', value)
  else notifyError(new Error('浏览器拒绝了剪贴板访问'))
}

/** Auto-fill a suggestion from the local target, only while untouched. */
let nameTouched = false
watch(
  () => form.name,
  () => {
    nameTouched = form.name.length > 0
  }
)
watch(
  () => form.local,
  (value) => {
    if (editorMode.value !== 'create' || nameTouched) return
    const port = value.match(/:(\d{2,5})$/)?.[1]
    if (port) form.name = `web-${port}`
  }
)
</script>

<template>
  <TopBar
    title="隧道"
    subtitle="增删改查与启停，配置变更会即时写回并热加载"
    :live-state="live.link"
    :version="state.meta?.version"
  />

  <div class="wg-page">
    <PageHead title="隧道列表" :description="`共 ${tunnels.length} 条 · 已发布 ${tunnels.filter((t) => t.registered).length} 条`">
      <template #actions>
        <TButton size="sm" variant="primary" icon="plus" @click="openCreate">新建隧道</TButton>
      </template>
    </PageHead>

    <section class="wg-card">
      <div v-if="tunnels.length" class="wg-table-wrap">
        <table class="wg-table">
          <thead>
            <tr>
              <th style="width: 34px" />
              <th>名称</th>
              <th>类型</th>
              <th>公网地址</th>
              <th>本地目标</th>
              <th class="num">入站 / 出站</th>
              <th class="num">连接</th>
              <th style="width: 180px">状态</th>
              <th class="actions" style="width: 128px">操作</th>
            </tr>
          </thead>
          <tbody>
            <tr v-for="t in tunnels" :key="t.name">
              <td>
                <label class="wg-switch" :title="t.enabled ? '停用隧道' : '启用隧道'">
                  <input
                    type="checkbox"
                    :checked="t.enabled"
                    :disabled="busy.has(t.name)"
                    :aria-label="`${t.enabled ? '停用' : '启用'}隧道 ${t.name}`"
                    @change="toggle(t, $event.target.checked)"
                  />
                  <span class="track" />
                </label>
              </td>
              <td>
                <div class="wg-primary-cell">
                  <div style="min-width: 0">
                    <div class="name wg-truncate">{{ t.name }}</div>
                    <div class="meta">{{ formatBytes(t.bytes_in + t.bytes_out) }} 累计</div>
                  </div>
                </div>
              </td>
              <td>
                <span class="wg-badge outline wg-mono">{{ t.type }}</span>
                <span v-if="t.encryption" class="wg-badge lock" data-tone="ok" title="隧道内层 AES-256-GCM 加密">
                  <Icon name="lock" :size="10" />加密
                </span>
              </td>
              <td>
                <button v-if="t.public_addr" class="addr" @click="copy(t.public_addr)">
                  <span class="wg-mono wg-truncate">{{ t.public_addr }}</span>
                  <Icon name="copy" :size="12" />
                </button>
                <span v-else class="wg-dim">自动分配中</span>
              </td>
              <td class="wg-mono wg-truncate">{{ t.local }}</td>
              <td class="num">{{ formatBytes(t.bytes_in) }} / {{ formatBytes(t.bytes_out) }}</td>
              <td class="num">{{ t.active_connections }}</td>
              <td>
                <StatusPill
                  :state="t.registered ? 'registered' : t.enabled ? 'pending' : 'stopped'"
                  :label="t.registered ? '已发布' : t.enabled ? '注册中' : '已停用'"
                />
                <div v-if="t.last_error" class="err wg-truncate" :title="t.last_error">{{ t.last_error }}</div>
              </td>
              <td class="actions">
                <button class="wg-icon-btn" aria-label="编辑" title="编辑" @click="openEdit(t)">
                  <Icon name="edit" />
                </button>
                <button class="wg-icon-btn" aria-label="复制地址" title="复制公网地址" @click="copy(t.public_addr)">
                  <Icon name="link" />
                </button>
                <button
                  class="wg-icon-btn danger"
                  aria-label="删除"
                  title="删除"
                  :disabled="busy.has(t.name)"
                  @click="pendingDelete = t"
                >
                  <Icon name="trash" />
                </button>
              </td>
            </tr>
          </tbody>
        </table>
      </div>

      <EmptyState
        v-else
        icon="tunnel"
        title="还没有隧道"
        description="一条隧道把一个本机端口映射到公网。HTTP 隧道自动分配子域名，TCP 隧道自动分配端口。"
      >
        <TButton size="sm" variant="primary" icon="plus" @click="openCreate">新建隧道</TButton>
      </EmptyState>
    </section>
  </div>

  <!-- Create / edit -->
  <TModal
    v-model="editorOpen"
    :title="editorMode === 'create' ? '新建隧道' : '编辑隧道'"
    :description="editorMode === 'create' ? '保存后立即注册到服务端，无需重启客户端。' : '修改本地目标或类型会重新注册该隧道。'"
  >
    <form class="form" @submit.prevent="save">
      <div class="row-2">
        <label class="wg-field">
          <span class="wg-label">名称 <span class="req">*</span></span>
          <input
            v-model="form.name"
            class="wg-input mono"
            :class="{ invalid: errors.name }"
            :disabled="editorMode === 'edit'"
            placeholder="web-8080"
            spellcheck="false"
            autofocus
          />
          <span v-if="errors.name" class="wg-hint error">{{ errors.name }}</span>
          <span v-else class="wg-hint">小写字母、数字、中划线</span>
        </label>

        <label class="wg-field">
          <span class="wg-label">类型</span>
          <select v-model="form.type" class="wg-select">
            <option value="http">HTTP · 按子域名</option>
            <option value="tcp">TCP · 按端口</option>
          </select>
        </label>
      </div>

      <label class="wg-field">
        <span class="wg-label">本地目标 <span class="req">*</span></span>
        <input
          v-model="form.local"
          class="wg-input mono"
          :class="{ invalid: errors.local }"
          placeholder="127.0.0.1:8080"
          spellcheck="false"
        />
        <span v-if="errors.local" class="wg-hint error">{{ errors.local }}</span>
        <span v-else class="wg-hint">客户端所在机器上的服务地址</span>
      </label>

      <label v-if="form.type === 'http'" class="wg-field">
        <span class="wg-label">子域名 <span class="wg-dim">（可选）</span></span>
        <div class="wg-input-group">
          <input v-model="form.subdomain" class="wg-input mono" placeholder="留空由服务端自动分配" spellcheck="false" />
          <span class="wg-suffix"><span class="wg-badge outline">auto</span></span>
        </div>
        <span class="wg-hint">留空时服务端基于隧道名生成可用子域名</span>
      </label>

      <label v-else class="wg-field">
        <span class="wg-label">远程端口 <span class="wg-dim">（可选）</span></span>
        <div class="wg-input-group">
          <input
            v-model.number="form.remote_port"
            class="wg-input mono"
            type="number"
            min="0"
            max="65535"
            placeholder="留空由服务端自动分配"
          />
          <span class="wg-suffix"><span class="wg-badge outline">auto</span></span>
        </div>
        <span class="wg-hint">需在服务端 tcp_port_range 范围内</span>
      </label>

      <label class="wg-switch" style="margin-top: 2px">
        <input v-model="form.enabled" type="checkbox" />
        <span class="track" />
        保存后立即启用
      </label>

      <div class="enc-block">
        <label class="wg-switch">
          <input
            v-model="form.encryption"
            type="checkbox"
            :disabled="secretKnown && !streamSecretSet"
            :aria-label="'隧道内层加密'"
          />
          <span class="track" />
          <Icon name="lock" :size="13" />
          <span>隧道内层加密 · AES-256-GCM</span>
        </label>
        <span v-if="errors.encryption" class="wg-hint error">{{ errors.encryption }}</span>
        <span v-else-if="secretKnown && !streamSecretSet" class="wg-hint">
          尚未配置共享口令。请先到「配置 → 隧道加密」生成一个，再回到这里开启。
        </span>
        <span v-else class="wg-hint">
          在 TLS 之内再套一层加密，密钥由两端共享口令派生。服务端未启用时注册会被拒绝，不会静默降级为明文。
        </span>
      </div>
    </form>

    <template #footer>
      <TButton variant="ghost" @click="editorOpen = false">取消</TButton>
      <TButton variant="primary" :loading="saving" icon="save" @click="save">
        {{ editorMode === 'create' ? '创建' : '保存' }}
      </TButton>
    </template>
  </TModal>

  <!-- Delete confirmation -->
  <TModal
    :model-value="!!pendingDelete"
    title="删除隧道"
    :description="pendingDelete ? `将删除 ${pendingDelete.name} 并断开其公网地址。` : ''"
    @update:model-value="pendingDelete = null"
  >
    <p class="wg-hint" style="font-size: var(--fs-13)">
      该操作会写回配置文件并立即撤销公网入口，正在传输的连接会被关闭。
    </p>
    <template #footer>
      <TButton variant="ghost" @click="pendingDelete = null">取消</TButton>
      <TButton variant="danger" :loading="pendingDelete && busy.has(pendingDelete.name)" @click="confirmDelete">
        删除
      </TButton>
    </template>
  </TModal>
</template>

<style scoped>
.form { display: flex; flex-direction: column; gap: 14px; }
.row-2 { display: grid; grid-template-columns: 1fr 1fr; gap: 12px; }
@media (max-width: 520px) { .row-2 { grid-template-columns: 1fr; } }

.wg-input.invalid { border-color: var(--danger); }

.addr {
  display: inline-flex;
  align-items: center;
  gap: 5px;
  padding: 2px 6px;
  margin-left: -6px;
  border-radius: var(--r-xs);
  color: var(--accent);
  font-size: var(--fs-12);
  max-width: 24ch;
}
.addr:hover { background: var(--accent-soft); }
.addr svg { opacity: .5; flex: none; }

.err { font-size: var(--fs-11); color: var(--danger); max-width: 26ch; margin-top: 3px; }

.lock { display: inline-flex; align-items: center; gap: 3px; margin-left: 5px; vertical-align: middle; }

.enc-block {
  display: flex;
  flex-direction: column;
  gap: 5px;
  padding: 11px 12px;
  border-radius: var(--r-sm);
  background: var(--surface-2);
  border: 1px solid var(--hairline);
}
.enc-block .wg-switch { gap: 8px; }
.enc-block .wg-hint { margin-left: 2px; }
</style>
