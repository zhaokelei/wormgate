<script setup>
/**
 * Users & tenants (admin only).
 *
 * Sensitive handling, deliberately:
 *  - A client token is returned exactly once by POST /api/users or the rotate
 *    endpoint. It is shown in a one-time modal with a copy button.
 *  - The token stays readable afterwards (GET /users/{id}/token) because an
 *    operator who loses it otherwise has to rotate and reconnect every client.
 *    Every read and every export writes an audit entry naming the admin.
 *  - "导出" packages address + token into the exact YAML the client console can
 *    import in one click, so nobody retypes a 46-character token.
 *  - Passwords are only ever sent, never read back; the API exposes
 *    has_password/has_token booleans instead of the material.
 *  - Self-protection rules (cannot disable/delete yourself, cannot remove the
 *    last admin) are enforced server-side; the UI mirrors them as disabled
 *    controls so the operator is not surprised by a 400.
 */
import { computed, onMounted, reactive, ref } from 'vue'

import TopBar from '@common/ui/TopBar.vue'
import PageHead from '@common/ui/PageHead.vue'
import TButton from '@common/ui/TButton.vue'
import TModal from '@common/ui/TModal.vue'
import EmptyState from '@common/ui/EmptyState.vue'
import StatusPill from '@common/ui/StatusPill.vue'
import Icon from '@common/ui/Icon.vue'
import { formatBytes, formatDate, fromNow, copyText } from '@common/format'
import { notifyOk, notifyError } from '@common/toast'

import { adminApi } from '../api'
import { live } from '../store'
import { state } from '../session'

const users = ref([])
const loading = ref(true)
const query = ref('')
const roleFilter = ref('')

const editorOpen = ref(false)
const editorMode = ref('create')
const saving = ref(false)
const formErrors = reactive({ username: '', password: '' })

const form = reactive({
  id: '',
  username: '',
  display_name: '',
  password: '',
  role: 'user',
  disabled: false,
  max_tunnels: 0,
  max_connections: 0,
  daily_traffic_bytes: 0
})

/** One-time secret modal state (token or new password confirmation). */
const secretModal = reactive({ open: false, title: '', value: '', note: '' })
const pendingDelete = ref(null)
const deleting = ref(false)
const rotating = ref('')
const revealing = ref('')
/** Rotating a token invalidates the live one instantly, so it asks first. */
const pendingRotate = ref(null)
/** Disabling kicks every connected client, so it asks first too. */
const pendingDisable = ref(null)
/** Exported client profile (address + token + import-ready YAML). */
const exportState = reactive({ open: false, user: null, yaml: '', filename: 'wormgate-client.yaml', server: '' })
const exporting = ref('')
/** Admin-initiated password reset (no old password required). */
const reset = reactive({ open: false, user: null, value: '', busy: false })

const ROLE_META = {
  admin: { label: '管理员', tone: 'danger', hint: '全部权限' },
  user: { label: '用户', tone: 'info', hint: '管理自己的隧道' },
  viewer: { label: '只读', tone: 'ok', hint: '仅查看与审计' }
}

const filtered = computed(() => {
  const q = query.value.trim().toLowerCase()
  return users.value.filter((u) => {
    if (roleFilter.value && u.role !== roleFilter.value) return false
    if (!q) return true
    return [u.username, u.display_name, u.id].filter(Boolean).some((v) => String(v).toLowerCase().includes(q))
  })
})

const counts = computed(() => {
  const acc = { admin: 0, user: 0, viewer: 0 }
  for (const u of users.value) acc[u.role] = (acc[u.role] || 0) + 1
  return acc
})

const isSelf = (u) => u.id === state.user?.id

async function load() {
  loading.value = true
  try {
    const payload = await adminApi.users()
    users.value = payload?.users || []
  } catch (err) {
    notifyError(err, '加载用户失败')
  } finally {
    loading.value = false
  }
}

function resetForm() {
  form.id = ''
  form.username = ''
  form.display_name = ''
  form.password = ''
  form.role = 'user'
  form.disabled = false
  form.max_tunnels = 0
  form.max_connections = 0
  form.daily_traffic_bytes = 0
  formErrors.username = ''
  formErrors.password = ''
}

function openCreate() {
  editorMode.value = 'create'
  resetForm()
  editorOpen.value = true
}

function openEdit(user) {
  editorMode.value = 'edit'
  resetForm()
  form.id = user.id
  form.username = user.username
  form.display_name = user.display_name || ''
  form.role = user.role
  form.disabled = user.disabled
  form.max_tunnels = user.quota?.max_tunnels || 0
  form.max_connections = user.quota?.max_connections || 0
  form.daily_traffic_bytes = user.quota?.daily_traffic_bytes || 0
  editorOpen.value = true
}

function validate() {
  formErrors.username = ''
  formErrors.password = ''
  if (editorMode.value === 'create') {
    if (!form.username.trim()) formErrors.username = '用户名不能为空'
    else if (!/^[a-z0-9][a-z0-9_-]{1,31}$/i.test(form.username.trim()))
      formErrors.username = '2-32 位字母、数字、下划线或中划线'
    if (form.password && form.password.length < 8) formErrors.password = '密码至少 8 位'
  }
  return !formErrors.username && !formErrors.password
}

async function save() {
  if (!validate() || saving.value) return
  saving.value = true
  const quota = {
    max_tunnels: Number(form.max_tunnels) || 0,
    max_connections: Number(form.max_connections) || 0,
    daily_traffic_bytes: Number(form.daily_traffic_bytes) || 0
  }
  try {
    if (editorMode.value === 'create') {
      const payload = await adminApi.createUser({
        username: form.username.trim(),
        display_name: form.display_name.trim(),
        password: form.password,
        role: form.role,
        quota
      })
      editorOpen.value = false
      await load()
      if (payload?.token) {
        showSecret('客户端令牌（仅显示一次）', payload.token, '把它填入客户端的 --token 或配置文件 client.token，关闭后无法再次查看。')
      } else {
        notifyOk('用户已创建', form.username)
      }
    } else {
      await adminApi.updateUser(form.id, {
        display_name: form.display_name,
        role: form.role,
        disabled: form.disabled,
        quota
      })
      editorOpen.value = false
      notifyOk('用户已更新', form.username)
      await load()
    }
  } catch (err) {
    notifyError(err, '保存失败')
  } finally {
    saving.value = false
  }
}

function showSecret(title, value, note) {
  secretModal.title = title
  secretModal.value = value
  secretModal.note = note
  secretModal.open = true
}

/** Admin reset: distinct from the self-service Security page, which asks for
 *  the current password. This one kicks the target's existing sessions. */
function openReset(user) {
  reset.user = user
  reset.value = ''
  reset.open = true
}

async function confirmReset() {
  if (!reset.user || reset.busy) return
  if (reset.value.length < 8) {
    notifyError(new Error('密码至少 8 位'), '重置密码失败')
    return
  }
  reset.busy = true
  try {
    await adminApi.setPassword(reset.user.id, reset.value)
    notifyOk('密码已重置', `${reset.user.username} 的旧密码立即失效`)
    reset.open = false
    reset.user = null
    reset.value = ''
    await load()
  } catch (err) {
    notifyError(err, '重置密码失败')
  } finally {
    reset.busy = false
  }
}

async function rotate() {
  const user = pendingRotate.value
  if (!user) return
  pendingRotate.value = null
  rotating.value = user.id
  try {
    const payload = await adminApi.rotateToken(user.id)
    showSecret('新的客户端令牌', payload.token, '旧令牌已立即失效，正在连接的客户端会在重连时被拒绝。')
    await load()
  } catch (err) {
    notifyError(err, '轮换令牌失败')
  } finally {
    rotating.value = ''
  }
}

async function reveal(user) {
  if (revealing.value || !user.token_viewable) return
  revealing.value = user.id
  try {
    const payload = await adminApi.userToken(user.id)
    showSecret('当前客户端令牌', payload.token, '这就是该账号正在使用的令牌，不会自行变化。需要作废请点「轮换令牌」。')
  } catch (err) {
    notifyError(err, '查看令牌失败')
  } finally {
    revealing.value = ''
  }
}

async function exportClient(user) {
  exporting.value = user.id
  try {
    const payload = await adminApi.exportClient(user.id)
    Object.assign(exportState, {
      open: true,
      user,
      yaml: payload.yaml || '',
      filename: payload.filename || 'wormgate-client.yaml',
      server: payload.server || ''
    })
  } catch (err) {
    notifyError(err, '导出失败')
  } finally {
    exporting.value = ''
  }
}

async function copyExport() {
  const ok = await copyText(exportState.yaml)
  if (ok) notifyOk('已复制到剪贴板', '粘贴到客户端控制台的「一键导入」即可')
  else notifyError(new Error('浏览器拒绝了剪贴板访问'), '复制失败，请手动选择文本')
}

/** Hands the YAML to the browser as a file download. */
function downloadExport() {
  const blob = new Blob([exportState.yaml], { type: 'text/yaml;charset=utf-8' })
  const url = URL.createObjectURL(blob)
  const link = document.createElement('a')
  link.href = url
  link.download = exportState.filename
  document.body.appendChild(link)
  link.click()
  link.remove()
  setTimeout(() => URL.revokeObjectURL(url), 1000)
}

async function toggleDisabled(user) {
  // Enabling is harmless; disabling kicks every live session, so confirm first.
  if (!user.disabled) {
    pendingDisable.value = user
    return
  }
  await setDisabled(user, false)
}

async function confirmDisable() {
  const target = pendingDisable.value
  if (!target) return
  pendingDisable.value = null
  await setDisabled(target, true)
}

async function setDisabled(user, disabled) {
  try {
    await adminApi.updateUser(user.id, { disabled })
    notifyOk(disabled ? '账号已停用' : '账号已启用', user.username)
    await load()
  } catch (err) {
    notifyError(err, '操作失败')
  }
}

async function confirmDelete() {
  const target = pendingDelete.value
  if (!target) return
  deleting.value = true
  try {
    await adminApi.deleteUser(target.id)
    notifyOk('用户已删除', target.username)
    pendingDelete.value = null
    await load()
  } catch (err) {
    notifyError(err, '删除失败')
  } finally {
    deleting.value = false
  }
}

async function copySecret() {
  const ok = await copyText(secretModal.value)
  if (ok) notifyOk('已复制到剪贴板')
  else notifyError(new Error('剪贴板不可用'), '请手动选择复制')
}

onMounted(load)
</script>

<template>
  <TopBar title="用户与租户" subtitle="账号、角色与配额" :live-state="live.link" :version="state.meta?.version">
    <template #actions>
      <TButton size="sm" icon="refresh" :loading="loading" @click="load">刷新</TButton>
    </template>
  </TopBar>

  <div class="wg-page">
    <PageHead
      title="账号管理"
      :description="`${users.length} 个账号 · 管理员 ${counts.admin} · 用户 ${counts.user} · 只读 ${counts.viewer}`"
    >
      <template #actions>
        <div class="search">
          <Icon name="search" :size="14" />
          <input v-model="query" placeholder="搜索用户名" spellcheck="false" />
        </div>
        <select v-model="roleFilter" class="wg-select" style="width: 124px">
          <option value="">全部角色</option>
          <option value="admin">管理员</option>
          <option value="user">用户</option>
          <option value="viewer">只读</option>
        </select>
        <TButton size="sm" variant="primary" icon="userPlus" @click="openCreate">新建用户</TButton>
      </template>
    </PageHead>

    <section class="wg-card">
      <div v-if="loading && !users.length" class="wg-card-body">
        <div class="wg-skeleton" style="height: 18px; margin-bottom: 10px" />
        <div class="wg-skeleton" style="height: 18px" />
      </div>

      <div v-else-if="filtered.length" class="wg-table-wrap">
        <table class="wg-table">
          <thead>
            <tr>
              <th>账号</th>
              <th>角色</th>
              <th>配额</th>
              <th>凭据</th>
              <th>最近活跃</th>
              <th>状态</th>
              <th class="actions">操作</th>
            </tr>
          </thead>
          <tbody>
            <tr v-for="u in filtered" :key="u.id" :data-tone="u.disabled ? 'danger' : undefined">
              <td>
                <div class="wg-primary-cell">
                  <span class="avatar-sm" aria-hidden="true">{{ (u.display_name || u.username).slice(0, 1).toUpperCase() }}</span>
                  <div style="min-width: 0">
                    <div class="name wg-truncate">
                      {{ u.display_name || u.username }}
                      <span v-if="isSelf(u)" class="wg-badge outline" style="margin-left: 5px">你</span>
                    </div>
                    <div class="meta wg-mono">{{ u.username }}</div>
                  </div>
                </div>
              </td>
              <td>
                <span class="wg-badge" :data-tone="ROLE_META[u.role]?.tone">{{ ROLE_META[u.role]?.label || u.role }}</span>
              </td>
              <td class="quota">
                <span>{{ u.quota?.max_tunnels || '默认' }} 隧道</span>
                <span>{{ u.quota?.max_connections || '默认' }} 连接</span>
                <span>{{ u.quota?.daily_traffic_bytes ? formatBytes(u.quota.daily_traffic_bytes) : '默认' }}/天</span>
              </td>
              <td>
                <div class="wg-row" style="gap: 4px">
                  <span class="wg-badge" :data-tone="u.has_password ? 'ok' : undefined">密码</span>
                  <span class="wg-badge" :data-tone="u.has_token ? 'ok' : undefined">令牌</span>
                  <span v-if="u.oauth" class="wg-badge" data-tone="violet">OAuth</span>
                </div>
              </td>
              <td class="wg-dim" style="font-size: var(--fs-11)" :title="u.last_seen_at">
                {{ u.last_seen_at ? fromNow(u.last_seen_at) : '从未' }}
              </td>
              <td>
                <StatusPill :state="u.disabled ? 'disabled' : 'active'" :label="u.disabled ? '已停用' : '正常'" />
              </td>
              <td class="actions">
                <button class="wg-icon-btn" aria-label="编辑" title="编辑" @click="openEdit(u)">
                  <Icon name="edit" />
                </button>
                <button
                  class="wg-icon-btn"
                  aria-label="重置密码"
                  title="重置登录密码"
                  @click="openReset(u)"
                >
                  <Icon name="shield" />
                </button>
                <button
                  class="wg-icon-btn"
                  aria-label="查看令牌"
                  :title="u.token_viewable ? '查看当前客户端令牌' : '该令牌在本功能上线前生成，无法还原；轮换一次即可查看'"
                  :disabled="!u.token_viewable || revealing === u.id"
                  @click="reveal(u)"
                >
                  <Icon name="eye" />
                </button>
                <button
                  class="wg-icon-btn"
                  aria-label="导出客户端配置"
                  title="导出客户端配置（服务器地址 + 令牌，可直接导入客户端）"
                  :disabled="exporting === u.id"
                  @click="exportClient(u)"
                >
                  <Icon name="download" />
                </button>
                <button
                  class="wg-icon-btn"
                  aria-label="轮换令牌"
                  title="轮换客户端令牌（会二次确认）"
                  :disabled="rotating === u.id"
                  @click="pendingRotate = u"
                >
                  <Icon name="key" />
                </button>
                <button
                  class="wg-icon-btn"
                  :aria-label="u.disabled ? '启用' : '停用'"
                  :title="u.disabled ? '启用账号' : '停用账号'"
                  :disabled="isSelf(u)"
                  @click="toggleDisabled(u)"
                >
                  <Icon :name="u.disabled ? 'unlock' : 'lock'" />
                </button>
                <button
                  class="wg-icon-btn danger"
                  aria-label="删除"
                  title="删除账号"
                  :disabled="isSelf(u)"
                  @click="pendingDelete = u"
                >
                  <Icon name="trash" />
                </button>
              </td>
            </tr>
          </tbody>
        </table>
      </div>

      <EmptyState v-else-if="users.length" icon="filter" title="没有匹配的账号" description="调整搜索或角色筛选。">
        <button class="wg-btn sm" @click="query = ''; roleFilter = ''">清除筛选</button>
      </EmptyState>

      <EmptyState v-else icon="users" title="还没有账号" description="创建第一个租户账号并为其下发客户端令牌。">
        <TButton size="sm" variant="primary" icon="userPlus" @click="openCreate">新建用户</TButton>
      </EmptyState>
    </section>

    <p class="wg-hint" style="max-width: 74ch">
      提示：管理员同时拥有隧道属主与平台管理权限；「用户」只能查看和管理自己的客户端与隧道；「只读」可查看全局状态与审计日志，但不能执行任何写操作。配额为 0 表示继承服务端默认值。
    </p>
  </div>

  <!-- Create / edit -->
  <TModal
    v-model="editorOpen"
    :title="editorMode === 'create' ? '新建用户' : `编辑 ${form.username}`"
    :description="editorMode === 'create' ? '创建后立即返回一次客户端令牌。' : '留空密码字段表示不修改现有密码。'"
    wide
  >
    <form class="form" @submit.prevent="save">
      <div class="grid-2">
        <label class="wg-field">
          <span class="wg-label">用户名 <span class="req">*</span></span>
          <input
            v-model="form.username"
            class="wg-input"
            :disabled="editorMode === 'edit'"
            placeholder="team-a"
            spellcheck="false"
            autofocus
          />
          <span v-if="formErrors.username" class="wg-hint error">{{ formErrors.username }}</span>
        </label>
        <label class="wg-field">
          <span class="wg-label">显示名称</span>
          <input v-model="form.display_name" class="wg-input" placeholder="团队 A" />
        </label>
      </div>

      <div class="grid-2">
        <label v-if="editorMode === 'create'" class="wg-field">
          <span class="wg-label">初始密码 <span class="wg-dim">（可选）</span></span>
          <input v-model="form.password" type="password" class="wg-input" placeholder="不少于 8 位" autocomplete="new-password" />
          <span v-if="formErrors.password" class="wg-hint error">{{ formErrors.password }}</span>
        </label>
        <label class="wg-field">
          <span class="wg-label">角色</span>
          <select v-model="form.role" class="wg-select" :disabled="isSelf({ id: form.id })">
            <option value="admin">管理员 · 全部权限</option>
            <option value="user">用户 · 自助管理</option>
            <option value="viewer">只读 · 审计</option>
          </select>
        </label>
        <label v-if="editorMode === 'edit'" class="wg-switch" style="align-self: end; padding-bottom: 8px">
          <input v-model="form.disabled" type="checkbox" :disabled="isSelf({ id: form.id })" />
          <span class="track" />
          停用该账号
        </label>
      </div>

      <div class="quota-block">
        <div class="wg-section" style="margin-bottom: 8px">
          <h2 style="font-size: var(--fs-13)">配额</h2>
          <span class="wg-section-hint">0 表示继承服务端默认值</span>
        </div>
        <div class="grid-3">
          <label class="wg-field">
            <span class="wg-label">最大隧道数</span>
            <input v-model.number="form.max_tunnels" type="number" min="0" class="wg-input mono" />
          </label>
          <label class="wg-field">
            <span class="wg-label">最大并发连接</span>
            <input v-model.number="form.max_connections" type="number" min="0" class="wg-input mono" />
          </label>
          <label class="wg-field">
            <span class="wg-label">每日流量 (字节)</span>
            <input v-model.number="form.daily_traffic_bytes" type="number" min="0" class="wg-input mono" />
          </label>
        </div>
      </div>
    </form>

    <template #footer>
      <TButton variant="ghost" @click="editorOpen = false">取消</TButton>
      <TButton variant="primary" :loading="saving" icon="save" @click="save">
        {{ editorMode === 'create' ? '创建并下发令牌' : '保存' }}
      </TButton>
    </template>
  </TModal>

  <!-- One-time secret -->
  <TModal v-model="secretModal.open" :title="secretModal.title" persistent>
    <p class="wg-hint" style="margin-bottom: 12px">{{ secretModal.note }}</p>
    <div class="secret">
      <code class="wg-mono">{{ secretModal.value }}</code>
      <TButton size="sm" icon="copy" @click="copySecret">复制</TButton>
    </div>
    <template #footer>
      <TButton variant="primary" @click="secretModal.open = false; secretModal.value = ''">我已保存</TButton>
    </template>
  </TModal>

  <!-- Admin password reset -->
  <TModal
    :model-value="reset.open"
    title="重置登录密码"
    :description="reset.user ? `为 ${reset.user.username} 设置新的管理台登录密码` : ''"
    @update:model-value="reset.open = false"
  >
    <form class="form" @submit.prevent="confirmReset">
      <label class="wg-field">
        <span class="wg-label">新密码</span>
        <input
          v-model="reset.value"
          type="password"
          class="wg-input"
          placeholder="不少于 8 位"
          autocomplete="new-password"
          autofocus
        />
      </label>
      <p class="wg-hint" style="font-size: var(--fs-12)">
        只影响该账号登录管理台的密码，不会改变其客户端令牌。重置后该账号所有已登录会话立即失效；
        修改自己的密码请改用「安全」页（需要输入当前密码）。
      </p>
    </form>
    <template #footer>
      <TButton variant="ghost" @click="reset.open = false">取消</TButton>
      <TButton variant="primary" icon="save" :loading="reset.busy" @click="confirmReset">重置密码</TButton>
    </template>
  </TModal>

  <!-- Export client profile -->
  <TModal
    :model-value="exportState.open"
    wide
    title="导出客户端配置"
    :description="exportState.user ? `${exportState.user.username} → ${exportState.server}` : ''"
    @update:model-value="exportState.open = false"
  >
    <div class="export-body">
      <p class="wg-hint" style="font-size: var(--fs-13); margin: 0 0 10px">
        下载后，在客户端控制台「配置 → 一键导入」选择该文件（或粘贴下方内容）即可完成服务端地址、端口与令牌配置。
        文件内含令牌，等同于密码，请通过可信渠道传递。
      </p>
      <pre class="export-yaml wg-mono">{{ exportState.yaml }}</pre>
    </div>
    <template #footer>
      <TButton variant="ghost" @click="exportState.open = false">关闭</TButton>
      <TButton icon="copy" @click="copyExport">复制</TButton>
      <TButton variant="primary" icon="download" @click="downloadExport">下载 .yaml</TButton>
    </template>
  </TModal>

  <!-- Rotate token -->
  <TModal
    :model-value="!!pendingRotate"
    title="轮换客户端令牌"
    :description="pendingRotate ? `${pendingRotate.username}` : ''"
    @update:model-value="pendingRotate = null"
  >
    <p class="wg-hint" style="font-size: var(--fs-13)">
      旧令牌会立即失效，该账号下所有客户端都会在下次重连时被拒绝（401），隧道随之中断，直到你把新令牌填进客户端配置。
      如果只是想看一眼当前令牌，请用「查看令牌」。
    </p>
    <template #footer>
      <TButton variant="ghost" @click="pendingRotate = null">取消</TButton>
      <TButton variant="danger" :loading="rotating === pendingRotate?.id" @click="rotate">轮换令牌</TButton>
    </template>
  </TModal>

  <!-- Disable account -->
  <TModal
    :model-value="!!pendingDisable"
    title="停用账号"
    :description="pendingDisable ? `${pendingDisable.username}` : ''"
    @update:model-value="pendingDisable = null"
  >
    <p class="wg-hint" style="font-size: var(--fs-13)">
      停用会立即断开该账号的所有在线客户端并撤销其隧道，客户端会持续重连失败直到账号重新启用。令牌保持不变。
    </p>
    <template #footer>
      <TButton variant="ghost" @click="pendingDisable = null">取消</TButton>
      <TButton variant="danger" @click="confirmDisable">停用账号</TButton>
    </template>
  </TModal>

  <!-- Delete -->
  <TModal
    :model-value="!!pendingDelete"
    title="删除账号"
    :description="pendingDelete ? `${pendingDelete.username}` : ''"
    @update:model-value="pendingDelete = null"
  >
    <p class="wg-hint" style="font-size: var(--fs-13)">
      删除会立即断开该账号所有客户端会话并撤销其隧道，操作不可恢复，且会写入审计日志。
    </p>
    <template #footer>
      <TButton variant="ghost" @click="pendingDelete = null">取消</TButton>
      <TButton variant="danger" :loading="deleting" @click="confirmDelete">删除账号</TButton>
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
  width: 190px;
}
.search:focus-within { border-color: var(--accent-line); box-shadow: var(--ring); }
.search input { flex: 1; min-width: 0; background: none; border: 0; font-size: var(--fs-13); color: var(--text); }

.avatar-sm {
  display: grid;
  place-items: center;
  width: 26px;
  height: 26px;
  flex: none;
  border-radius: 50%;
  background: var(--accent);
  color: #fff;
  font-size: var(--fs-11);
  font-weight: 700;
}

.quota { display: flex; flex-direction: column; gap: 1px; font-size: var(--fs-11); color: var(--text-dim); }

.form { display: flex; flex-direction: column; gap: 14px; }
.grid-2 { display: grid; grid-template-columns: 1fr 1fr; gap: 12px; }
.grid-3 { display: grid; grid-template-columns: repeat(3, 1fr); gap: 12px; }
@media (max-width: 620px) { .grid-2, .grid-3 { grid-template-columns: 1fr; } }

.quota-block {
  padding: 12px;
  border-radius: var(--r-md);
  background: var(--surface-2);
  border: 1px solid var(--hairline);
}

.secret {
  display: flex;
  align-items: center;
  gap: 10px;
  padding: 11px 13px;
  border-radius: var(--r-sm);
  background: var(--surface-2);
  border: 1px dashed var(--accent-line);
}
.secret code { flex: 1; min-width: 0; font-size: var(--fs-12); color: var(--accent); overflow-wrap: anywhere; }

.export-yaml {
  margin: 0;
  padding: 12px 13px;
  max-height: 280px;
  overflow: auto;
  border-radius: var(--r-sm);
  background: var(--surface-2);
  border: 1px solid var(--hairline);
  font-size: var(--fs-12);
  line-height: 1.7;
  color: var(--text);
  white-space: pre;
}
</style>
