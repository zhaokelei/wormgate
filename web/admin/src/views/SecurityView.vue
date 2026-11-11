<script setup>
/**
 * Admin console → Security.
 *
 * Two independent things live here:
 *
 *  1. Self-service password change for whichever account is signed in (any
 *     role). Resetting somebody else's password stays an admin-only action on
 *     the Users page. The wg_ client tokens are unrelated and are rotated
 *     there instead.
 *
 *  2. The stream-encryption key (server.stream_secret, admin only). It keys the
 *     optional AES-256-GCM layer that runs inside each tunnel stream. The value
 *     must be byte-identical on the client, so this panel shows it in clear and
 *     offers copy/generate — hiding it would only force an SSH session.
 */
import { computed, onMounted, ref } from 'vue'

import TopBar from '@common/ui/TopBar.vue'
import PageHead from '@common/ui/PageHead.vue'
import PasswordForm from '@common/ui/PasswordForm.vue'
import TButton from '@common/ui/TButton.vue'
import Icon from '@common/ui/Icon.vue'
import { copyText } from '@common/format'
import { notifyOk, notifyError } from '@common/toast'

import { adminApi } from '../api'
import { live } from '../store'
import { session, state } from '../session'

// -- stream encryption ------------------------------------------------------
const enc = ref(null)
const encLoading = ref(true)
const encSaving = ref(false)
const encError = ref('')
const draft = ref('')
const revealSecret = ref(false)

const canManage = computed(() => session.isAdmin)
const encEnabled = computed(() => !!enc.value?.enabled)
const fingerprint = computed(() => enc.value?.fingerprint || '')
const draftTooShort = computed(() => {
  const v = draft.value.trim()
  return v.length > 0 && v.length < 16
})
/** Highlight when the typed value would produce the same key as the server. */
const draftMatches = computed(
  () => !!draft.value.trim() && draft.value.trim() === (enc.value?.secret || '')
)

async function loadEncryption() {
  encLoading.value = true
  encError.value = ''
  try {
    enc.value = await adminApi.encryption()
  } catch (err) {
    encError.value = err?.message || '读取加密配置失败'
  } finally {
    encLoading.value = false
  }
}

async function applyEncryption(payload, okMessage) {
  if (encSaving.value) return
  encSaving.value = true
  try {
    enc.value = await adminApi.setEncryption(payload)
    draft.value = ''
    revealSecret.value = false
    notifyOk(okMessage, '已写入配置文件并热加载')
  } catch (err) {
    notifyError(err, '保存失败')
  } finally {
    encSaving.value = false
  }
}

const generateSecret = () => applyEncryption({ generate: true }, '已生成新的共享口令')
const saveSecret = () => {
  const value = draft.value.trim()
  if (!value) return
  applyEncryption({ secret: value }, '共享口令已更新')
}
const disableEncryption = () => {
  if (!window.confirm('关闭后，服务端不再接受加密隧道；已开启加密的客户端会注册失败，直到它们也取消勾选。继续？')) return
  applyEncryption({ disable: true }, '已关闭隧道加密')
}

async function copySecret() {
  const value = enc.value?.secret || ''
  if (!value) return
  const ok = await copyText(value)
  if (ok) notifyOk('已复制共享口令', '粘贴到客户端「配置 → 隧道加密」')
  else notifyError(new Error('浏览器拒绝了剪贴板访问'), '复制失败')
}

onMounted(() => {
  if (canManage.value) loadEncryption()
})
</script>

<template>
  <TopBar title="安全" subtitle="账号登录密码 · 隧道加密" :live-state="live.link" :version="state.meta?.version" />

  <div class="wg-page">
    <PageHead title="修改登录密码" description="仅用于登录服务端管理台，不影响客户端令牌。">
      <template #actions>
        <span class="wg-badge outline">/admin</span>
      </template>
    </PageHead>

    <PasswordForm :submit="adminApi.changePassword" :username="state.user?.username || ''" />

    <div class="wg-card">
      <div class="wg-card-body pw-notes">
        <h2>凭据分工</h2>
        <p>账号密码（本页）用于登录服务端管理台 UI，按用户存放，以 argon2id 哈希写入 users.json。</p>
        <p>客户端令牌 wg_... 用于 fpc 连接 fps 控制面，在「用户与租户」页创建或轮换。改本页密码不会改变任何令牌。</p>
        <p>修改成功后，本浏览器会自动换发新的会话，其他已登录的会话立即失效并写入审计日志。OAuth 专用账号没有本地密码，需要管理员在「用户与租户」页先设置。</p>
      </div>
    </div>

    <!-- ==================== stream encryption ==================== -->
    <PageHead
      title="隧道加密"
      description="可选的 AES-256-GCM 内层加密，位于 TLS 之内。密钥由两端共享口令派生，必须完全一致。"
    >
      <template #actions>
        <span v-if="encEnabled" class="wg-badge" data-tone="ok">已启用</span>
        <span v-else class="wg-badge outline">未启用</span>
      </template>
    </PageHead>

    <div v-if="!canManage" class="wg-card">
      <div class="wg-card-body">
        <p class="wg-hint">只有管理员可以查看和修改隧道加密口令。</p>
      </div>
    </div>

    <div v-else-if="encError" class="wg-card">
      <div class="wg-card-body">
        <p class="wg-hint error">{{ encError }}</p>
        <TButton size="sm" icon="refresh" style="margin-top: 10px" @click="loadEncryption">重试</TButton>
      </div>
    </div>

    <section v-else class="wg-card">
      <div class="wg-card-head">
        <div class="wg-card-title"><Icon name="lock" :size="14" /> 共享口令 server.stream_secret</div>
        <span class="wg-spacer" />
        <span class="wg-card-sub">写入 {{ enc?.config_path || 'wormgate.yaml' }}</span>
      </div>

      <div v-if="encLoading" class="wg-card-body"><p class="wg-hint">读取中…</p></div>

      <div v-else class="wg-card-body enc-body">
        <dl class="wg-kv">
          <dt>状态</dt>
          <dd>
            <span v-if="encEnabled" class="wg-ok">已启用 · 服务端可以接受加密隧道</span>
            <span v-else class="wg-dim">未启用 · 客户端请求加密会被拒绝</span>
          </dd>
          <dt>口令指纹</dt>
          <dd class="wg-mono">{{ fingerprint || '—' }}</dd>
          <dt>当前口令</dt>
          <dd class="wg-mono secret-row">
            <span v-if="!encEnabled" class="wg-dim">—</span>
            <span v-else class="secret">{{ revealSecret ? enc.secret : '•'.repeat(Math.min(enc.secret.length, 40)) }}</span>
            <button
              v-if="encEnabled"
              class="wg-icon-btn"
              :aria-label="revealSecret ? '隐藏口令' : '显示口令'"
              @click="revealSecret = !revealSecret"
            >
              <Icon :name="revealSecret ? 'eyeOff' : 'eye'" :size="14" />
            </button>
            <button v-if="encEnabled" class="wg-icon-btn" aria-label="复制口令" title="复制" @click="copySecret">
              <Icon name="copy" :size="14" />
            </button>
          </dd>
          <dt>已加密隧道</dt>
          <dd class="wg-mono">
            {{ enc.tunnels_encrypted }} 条加密 / {{ enc.tunnels_plain }} 条明文
          </dd>
        </dl>

        <div class="enc-form">
          <label class="wg-field">
            <span class="wg-label">{{ encEnabled ? '替换口令' : '设置口令' }}</span>
            <input
              v-model="draft"
              class="wg-input mono"
              placeholder="至少 16 个字符，或点右侧自动生成"
              autocomplete="off"
              spellcheck="false"
            />
            <span v-if="draftTooShort" class="wg-hint error">至少 16 个字符；太短的共享口令等于没有加密。</span>
            <span v-else-if="draftMatches" class="wg-hint wg-ok">与服务端当前口令一致。</span>
            <span v-else class="wg-hint">客户端必须配置同一个值，否则加密隧道的数据流会被拒绝认证。</span>
          </label>

          <div class="enc-actions">
            <TButton size="sm" icon="save" variant="primary" :loading="encSaving" :disabled="!draft.trim() || draftTooShort" @click="saveSecret">
              保存口令
            </TButton>
            <TButton size="sm" icon="zap" :loading="encSaving" @click="generateSecret">
              {{ encEnabled ? '重新生成' : '生成并启用' }}
            </TButton>
            <TButton v-if="encEnabled" size="sm" variant="danger" icon="power" :loading="encSaving" @click="disableEncryption">
              关闭加密
            </TButton>
          </div>
        </div>

        <div class="enc-notes">
          <p><strong>替换口令的影响：</strong>服务器会立刻改用新口令校验。已连接的客户端仍持旧密钥，它们的数据流会认证失败（被拒绝，不会退化成明文），直到客户端填入同一口令并重连。</p>
          <p><strong>这层加密解决什么：</strong>控制面已经是 TLS 1.3，但客户端若用 <span class="wg-mono">insecure_skip_verify</span> 连接，证书不被校验，中间人即可解密。内层 AEAD 不依赖证书，补的正是这个缺口。</p>
          <p><strong>不覆盖的部分：</strong>公网入口（HTTP 的 443/TCP 隧道端口）仍然是明文的；那一端要么走 HTTPS 反代，要么本来就是明文裸端口。</p>
        </div>
      </div>
    </section>
  </div>
</template>

<style scoped>
.pw-notes { display: flex; flex-direction: column; gap: 10px; max-width: 88ch; }
.pw-notes h2 { font-size: var(--fs-13); font-weight: 600; margin: 0; }
.pw-notes p { font-size: var(--fs-12); line-height: 1.7; color: var(--text-dim); margin: 0; }
.pw-notes strong { color: var(--text); font-weight: 600; }

.wg-page > .wg-card { margin-bottom: 14px; }
.wg-page > .wg-card:last-child { margin-bottom: 0; }

.enc-body { display: flex; flex-direction: column; gap: 16px; }
.secret-row { display: flex; align-items: center; gap: 6px; }
.secret-row .secret { word-break: break-all; }

.enc-form { display: flex; flex-direction: column; gap: 10px; max-width: 62ch; }
.enc-actions { display: flex; gap: 8px; flex-wrap: wrap; }

.enc-notes {
  display: flex;
  flex-direction: column;
  gap: 8px;
  padding: 12px 13px;
  border-radius: var(--r-sm);
  background: var(--surface-2);
  border: 1px solid var(--hairline);
}
.enc-notes p { margin: 0; font-size: var(--fs-11); line-height: 1.75; color: var(--text-dim); }
.enc-notes strong { color: var(--text); font-weight: 600; }
</style>
