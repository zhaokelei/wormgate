<script setup>
/**
 * Connection settings + advanced YAML editor.
 *
 * The primary surface is a form: it edits the client-side connection fields
 * through GET/PUT /api/settings, which deep-merges only those keys into the
 * config file. Validation stays on the Go side; a rejected save returns
 * {error, issues[]} and we show each issue verbatim.
 *
 * The YAML tab is kept for fields that cannot be hot-reloaded (client.webui,
 * log.format, ...) and for power users. Both tabs share the same unsaved-change
 * guard and the same transactional save semantics.
 */
import { computed, onBeforeUnmount, onMounted, reactive, ref, watch } from 'vue'
import { onBeforeRouteLeave } from 'vue-router'

import PageHead from '@common/ui/PageHead.vue'
import TopBar from '@common/ui/TopBar.vue'
import TButton from '@common/ui/TButton.vue'
import TModal from '@common/ui/TModal.vue'
import Icon from '@common/ui/Icon.vue'
import { copyText } from '@common/format'
import { notifyOk, notifyError } from '@common/toast'

import { clientApi } from '../api'
import { live } from '../store'
import { state } from '../session'

const tab = ref('form')
const loading = ref(true)
const saving = ref(false)
const loadError = ref('')
const issues = ref([])
const configPath = ref('')
const readOnly = ref({})
const showToken = ref(false)
const showSecret = ref(false)
const reconnecting = ref(false)
/** Short SHA-256 of the secret, matching what the admin console prints. */
const secretFingerprint = ref('')

// -- form state -----------------------------------------------------------
const blankForm = () => ({
  server: '',
  token: '',
  stream_secret: '',
  client_name: '',
  tls: {
    ca_file: '',
    cert_file: '',
    key_file: '',
    server_name: '',
    insecure_skip_verify: false
  },
  heartbeat_interval: '15s',
  dial_timeout: '5s',
  reconnect: { min_backoff: '1s', max_backoff: '30s' }
})
const form = reactive(blankForm())
const originalForm = ref('{}')
const formDirty = computed(() => JSON.stringify(form) !== originalForm.value)

// -- YAML state -----------------------------------------------------------
const original = ref('')
const draft = ref('')
const yamlDirty = computed(() => draft.value !== original.value)
const lineCount = computed(() => draft.value.split('\n').length)

const dirty = computed(() => (tab.value === 'yaml' ? yamlDirty.value : formDirty.value))

/** Restore only the editable fields. Kept separate from fillForm so that
 *  "撤销修改" cannot wipe the read-only console info loaded from the server. */
function applyFormValues(v) {
  Object.assign(form, {
    server: v?.server || '',
    token: v?.token || '',
    stream_secret: v?.stream_secret || '',
    client_name: v?.client_name || '',
    heartbeat_interval: v?.heartbeat_interval || '15s',
    dial_timeout: v?.dial_timeout || '5s'
  })
  Object.assign(form.tls, blankForm().tls, v?.tls || {})
  Object.assign(form.reconnect, blankForm().reconnect, v?.reconnect || {})
}

function fillForm(s) {
  applyFormValues(s)
  readOnly.value = s?.read_only || {}
  configPath.value = s?.config_path || ''
  originalForm.value = JSON.stringify(form)
}

async function load() {
  loading.value = true
  loadError.value = ''
  try {
    const [settings, text] = await Promise.all([clientApi.getSettings(), clientApi.getConfig()])
    fillForm(settings)
    original.value = text || ''
    draft.value = text || ''
    issues.value = []
  } catch (err) {
    loadError.value = err?.message || '读取配置失败'
  } finally {
    loading.value = false
  }
}

// -- one-click import -----------------------------------------------------
// The admin console exports a small YAML profile (server / token / client_name
// / tls.insecure_skip_verify). Operators paste it or drop the .yaml file here
// instead of retyping an address and a 46-character token by hand.
const importOpen = ref(false)
const importText = ref('')
const importBusy = ref(false)
const importError = ref('')
const importParsed = ref(null)
const dragOver = ref(false)
const fileInput = ref(null)

function openImport() {
  importOpen.value = true
  importText.value = ''
  importError.value = ''
  importParsed.value = null
}

/** Tolerant reader: accepts the exported YAML, a flat k: v list, or JSON. */
function parseProfile(text) {
  const trimmed = (text || '').trim()
  if (!trimmed) throw new Error('请先粘贴配置内容，或选择一个 .yaml 文件')

  let fields
  if (trimmed.startsWith('{')) {
    let obj
    try {
      obj = JSON.parse(trimmed)
    } catch {
      throw new Error('JSON 解析失败，请检查内容是否完整')
    }
    fields = obj.client || obj
  } else {
    const flat = {}
    const stack = []
    for (const raw of trimmed.split(/\r?\n/)) {
      const line = raw.replace(/\t/g, '  ')
      const body = line.trim()
      if (!body || body.startsWith('#') || body.startsWith('-')) continue
      const indent = line.length - line.trimStart().length
      const m = body.match(/^([^:：=]+?)\s*[:：=]\s*(.*)$/)
      if (!m) continue
      const key = m[1].trim().toLowerCase()
      let value = m[2].trim()
      while (stack.length && indent <= stack[stack.length - 1].indent) stack.pop()
      if (!value) {
        stack.push({ indent, key })
        continue
      }
      value = value.replace(/\s+#.*$/, '')
      if (/^(["']).*\1$/.test(value)) value = value.slice(1, -1)
      flat[[...stack.map((s) => s.key), key].join('.')] = value
    }
    const pick = (...keys) => {
      for (const k of keys) if (flat[k]) return flat[k]
      return ''
    }
    fields = {
      server: pick('server', 'client.server'),
      token: pick('token', 'client.token'),
      client_name: pick('client.client_name', 'client_name', 'client.name', 'name'),
      insecure_skip_verify: pick(
        'insecure_skip_verify',
        'client.tls.insecure_skip_verify',
        'tls.insecure_skip_verify'
      )
    }
  }

  const server = String(fields.server || '').trim()
  const token = String(fields.token || '').trim()
  if (!server) throw new Error('缺少 server（服务端地址，形如 1.2.3.4:7001）')
  if (!/^\S+:\d{1,5}$/.test(server)) throw new Error(`服务端地址格式不对：「${server}」，应为 host:port`)
  if (!token) throw new Error('缺少 token（客户端令牌）')
  if (!token.startsWith('wg_') && !token.startsWith('tx_')) throw new Error('令牌格式不对，应以 wg_ 开头')
  const raw = fields.insecure_skip_verify
  const insecure = raw === undefined || raw === null || raw === '' ? true : /^(true|1|yes|on)$/i.test(String(raw))
  return { server, token, client_name: String(fields.client_name || '').trim(), insecure }
}

function refreshPreview() {
  importError.value = ''
  importParsed.value = null
  if (!importText.value.trim()) return
  try {
    importParsed.value = parseProfile(importText.value)
  } catch (err) {
    importError.value = err?.message || '解析失败'
  }
}

watch(importText, refreshPreview)

async function importFile(event) {
  const file = event.target?.files?.[0]
  event.target.value = ''
  if (!file) return
  if (file.size > 64 * 1024) {
    importError.value = '文件超过 64 KB，确认选的是客户端配置文件'
    return
  }
  importText.value = await file.text()
}

function onDrop(event) {
  dragOver.value = false
  const file = event.dataTransfer?.files?.[0]
  if (file) importFile({ target: { files: [file], value: '' } })
}

/** Copies the parsed profile into the form without saving. */
function fillFromProfile() {
  const p = importParsed.value || parseProfile(importText.value)
  form.server = p.server
  form.token = p.token
  if (p.client_name) form.client_name = p.client_name
  form.tls.insecure_skip_verify = p.insecure
  return p
}

function importOnly() {
  try {
    const p = fillFromProfile()
    importOpen.value = false
    notifyOk('已填入表单', `${p.server}，确认无误后点「保存并应用」`)
  } catch (err) {
    importError.value = err?.message || '导入失败'
  }
}

async function importAndApply() {
  if (importBusy.value) return
  importBusy.value = true
  try {
    const p = fillFromProfile()
    tab.value = 'form'
    const before = originalForm.value
    await save()
    if (originalForm.value === before) {
      // save() either found nothing to change or was rejected; both already
      // surfaced a toast, so keep the dialog open with the pasted text intact.
      return
    }
    importOpen.value = false
    notifyOk('已导入并应用', `${p.server} · ${p.client_name || '本机'}`)
  } catch (err) {
    importError.value = err?.message || '导入失败'
  } finally {
    importBusy.value = false
  }
}

async function save() {
  if (saving.value || !dirty.value) return
  if (tab.value === 'form' && secretTooShort.value) {
    notifyError(new Error('共享口令至少需要 16 个字符'), '隧道加密口令太短')
    return
  }
  saving.value = true
  issues.value = []
  try {
    if (tab.value === 'form') {
      const result = await clientApi.putSettings(JSON.parse(JSON.stringify(form)))
      originalForm.value = JSON.stringify(form)
      // The on-disk YAML changed too; refresh it so the advanced tab is not stale.
      const text = await clientApi.getConfig()
      original.value = text || ''
      draft.value = text || ''
      notifyOk('连接设置已保存', result?.note || '已写入配置文件并热加载')
    } else {
      const result = await clientApi.putConfig(draft.value)
      original.value = draft.value
      const settings = await clientApi.getSettings()
      fillForm(settings)
      notifyOk('配置已保存并热加载', '已建立的连接不会中断')
      if (result?.files) configPath.value = Object.values(result.files)[0] || configPath.value
    }
  } catch (err) {
    issues.value = Array.isArray(err?.issues) ? err.issues : []
    notifyError(err, '配置未通过校验')
  } finally {
    saving.value = false
  }
}

async function reconnect() {
  if (reconnecting.value) return
  reconnecting.value = true
  try {
    await clientApi.reconnect()
    notifyOk('已请求重新连接控制面', '新凭据将在下一次握手中生效')
  } catch (err) {
    notifyError(err, '重连失败')
  } finally {
    reconnecting.value = false
  }
}

/**
 * Shared secret for the optional in-tunnel AEAD layer.
 *
 * The value must be byte-identical on both ends, so it is shown in clear when
 * the operator asks for it and copied straight into the server console. The
 * fingerprint below is the same 8-byte SHA-256 prefix the server prints, which
 * makes "did these two match?" a glance instead of a diff.
 */
const secretTooShort = computed(() => {
  const v = form.stream_secret.trim()
  return v.length > 0 && v.length < 16
})
const streamSecretSet = computed(() => form.stream_secret.trim().length > 0)

function generateSecret() {
  const bytes = new Uint8Array(24)
  crypto.getRandomValues(bytes)
  form.stream_secret = btoa(String.fromCharCode(...bytes))
    .replace(/\+/g, '-')
    .replace(/\//g, '_')
    .replace(/=+$/, '')
  showSecret.value = true
}

async function copySecret() {
  const value = form.stream_secret.trim()
  if (!value) return
  const ok = await copyText(value)
  if (ok) notifyOk('已复制共享口令', '粘贴到服务端「安全 → 隧道加密」即可')
  else notifyError(new Error('浏览器拒绝了剪贴板访问'), '复制失败')
}

watch(
  () => form.stream_secret,
  async (value) => {
    const v = (value || '').trim()
    // crypto.subtle only exists in a secure context; the console usually runs
    // on 127.0.0.1, but the fingerprint is a convenience, not a requirement.
    if (!v || !globalThis.crypto?.subtle) {
      secretFingerprint.value = ''
      return
    }
    try {
      const digest = await crypto.subtle.digest('SHA-256', new TextEncoder().encode(v))
      secretFingerprint.value = [...new Uint8Array(digest).slice(0, 8)]
        .map((b) => b.toString(16).padStart(2, '0'))
        .join('')
    } catch {
      secretFingerprint.value = ''
    }
  },
  { immediate: true }
)

function reset() {
  if (tab.value === 'yaml') {
    draft.value = original.value
  } else {
    applyFormValues(JSON.parse(originalForm.value))
  }
  issues.value = []
}

function reload() {
  if (dirty.value && !window.confirm('放弃未保存的修改并重新读取配置？')) return
  load()
}

/** Tab inserts two spaces instead of moving focus: YAML is whitespace-sensitive. */
function onKeydown(event) {
  if (event.key !== 'Tab') return
  event.preventDefault()
  const el = event.target
  const { selectionStart: start, selectionEnd: end } = el
  draft.value = `${draft.value.slice(0, start)}  ${draft.value.slice(end)}`
  requestAnimationFrame(() => {
    el.selectionStart = el.selectionEnd = start + 2
  })
}

function beforeUnload(event) {
  if (!dirty.value) return
  event.preventDefault()
  event.returnValue = ''
}

onBeforeRouteLeave(() => {
  if (!dirty.value) return true
  return window.confirm('有未保存的配置修改，确定离开？')
})

onMounted(() => {
  load()
  window.addEventListener('beforeunload', beforeUnload)
})

onBeforeUnmount(() => window.removeEventListener('beforeunload', beforeUnload))
</script>

<template>
  <TopBar
    title="配置"
    subtitle="图形化配置服务端连接、TLS 与隧道参数"
    :live-state="live.link"
    :version="state.meta?.version"
  />

  <div class="wg-page">
    <PageHead title="连接与配置" description="表单修改会即时校验并热加载；服务端地址、令牌或 TLS 变化会自动重连，无需重启进程。">
      <template #actions>
        <div class="wg-seg" role="tablist" aria-label="配置视图">
          <button :aria-pressed="tab === 'form'" @click="tab = 'form'">连接设置</button>
          <button :aria-pressed="tab === 'yaml'" @click="tab = 'yaml'">YAML 高级</button>
        </div>
        <span v-if="dirty" class="wg-badge" data-tone="warn">未保存</span>
        <TButton size="sm" icon="upload" @click="openImport">一键导入</TButton>
        <TButton size="sm" icon="refresh" :loading="loading" @click="reload">重新读取</TButton>
        <TButton size="sm" icon="copy" :disabled="!dirty" @click="reset">撤销修改</TButton>
        <TButton size="sm" variant="primary" icon="save" :loading="saving" :disabled="!dirty" @click="save">
          保存并应用
        </TButton>
      </template>
    </PageHead>

    <section v-if="issues.length" class="issues" role="alert">
      <div class="issues-head">
        <Icon name="alertTriangle" :size="15" />
        <strong>校验未通过（{{ issues.length }} 项）</strong>
        <span class="wg-spacer" />
        <button class="wg-icon-btn" aria-label="关闭" @click="issues = []"><Icon name="x" :size="13" /></button>
      </div>
      <ul>
        <li v-for="(issue, i) in issues" :key="i" class="wg-mono">{{ issue }}</li>
      </ul>
    </section>

    <div v-if="loadError" class="wg-card">
      <div class="wg-card-body">
        <p class="wg-hint error">{{ loadError }}</p>
        <TButton size="sm" icon="refresh" style="margin-top: 10px" @click="load">重试</TButton>
      </div>
    </div>

    <!-- ==================== form view ==================== -->
    <div v-else-if="tab === 'form'" class="settings-stack">
      <section class="wg-card">
        <div class="wg-card-head">
          <div class="wg-card-title"><Icon name="link" :size="14" /> 服务端连接</div>
          <span class="wg-spacer" />
          <span class="wg-card-sub">控制面地址 · TLS 始终开启</span>
        </div>
        <div class="wg-card-body form-grid">
          <label class="wg-field">
            <span class="wg-label">服务端地址 <span class="req">*</span></span>
            <input v-model="form.server" class="wg-input mono" placeholder="example.com:7000" spellcheck="false" />
            <span class="wg-hint">只填 host:port，不要带 scheme；客户端会自动使用 wss://</span>
          </label>

          <label class="wg-field">
            <span class="wg-label">客户端名称</span>
            <input v-model="form.client_name" class="wg-input mono" placeholder="laptop-web" spellcheck="false" />
            <span class="wg-hint">留空时使用主机名；管理台用它区分不同机器</span>
          </label>

          <label class="wg-field span-2">
            <span class="wg-label">服务端令牌（连接 fps 用） <span class="req">*</span></span>
            <div class="wg-input-group">
              <input
                v-model="form.token"
                class="wg-input mono"
                :type="showToken ? 'text' : 'password'"
                placeholder="wg_..."
                autocomplete="off"
                spellcheck="false"
              />
              <span class="wg-suffix">
                <button class="wg-icon-btn" type="button" :aria-label="showToken ? '隐藏令牌' : '显示令牌'" @click="showToken = !showToken">
                  <Icon :name="showToken ? 'eyeOff' : 'eye'" :size="14" />
                </button>
              </span>
            </div>
            <span class="wg-hint">由服务端首次启动打印，或在管理台按用户签发 / 轮换；与登录控制台的本地密码无关</span>
          </label>
        </div>
        <div class="wg-card-foot">
          <span class="wg-hint">修改以上任意一项会自动触发控制面重连</span>
          <span class="wg-spacer" />
          <TButton size="sm" icon="refresh" :loading="reconnecting" @click="reconnect">立即重连</TButton>
        </div>
      </section>

      <section class="wg-card">
        <div class="wg-card-head">
          <div class="wg-card-title"><Icon name="shield" :size="14" /> 传输安全 (TLS)</div>
          <span class="wg-spacer" />
          <span class="wg-card-sub">控制通道与数据通道</span>
        </div>
        <div class="wg-card-body form-grid">
          <div class="wg-field span-2">
            <span class="wg-label">跳过证书校验</span>
            <label class="wg-switch" title="仅用于本地开发或自签名证书">
              <input v-model="form.tls.insecure_skip_verify" type="checkbox" />
              <span class="track" />
              <span class="switch-label">insecure_skip_verify</span>
            </label>
            <span v-if="form.tls.insecure_skip_verify" class="wg-hint error">已关闭证书验证，仅可用于本地开发；生产请配置 CA 证书。</span>
            <span v-else class="wg-hint">使用自签名证书时请填写 CA 证书路径，而不是打开此开关。</span>
          </div>

          <label class="wg-field">
            <span class="wg-label">CA 证书</span>
            <input v-model="form.tls.ca_file" class="wg-input mono" placeholder="/etc/wormgate/ca.pem" spellcheck="false" />
          </label>
          <label class="wg-field">
            <span class="wg-label">SNI / 服务器名</span>
            <input v-model="form.tls.server_name" class="wg-input mono" placeholder="留空则从地址推导" spellcheck="false" />
          </label>
          <label class="wg-field">
            <span class="wg-label">客户端证书 (mTLS)</span>
            <input v-model="form.tls.cert_file" class="wg-input mono" placeholder="cert.pem" spellcheck="false" />
          </label>
          <label class="wg-field">
            <span class="wg-label">客户端私钥 (mTLS)</span>
            <input v-model="form.tls.key_file" class="wg-input mono" placeholder="key.pem" spellcheck="false" />
          </label>
        </div>
      </section>

      <section class="wg-card">
        <div class="wg-card-head">
          <div class="wg-card-title"><Icon name="lock" :size="14" /> 隧道加密（可选）</div>
          <span class="wg-spacer" />
          <span class="wg-card-sub">AES-256-GCM · 在 TLS 之内再加一层</span>
        </div>
        <div class="wg-card-body form-grid">
          <div class="wg-field span-2">
            <span class="wg-label">共享口令 stream_secret</span>
            <div class="wg-input-group">
              <input
                v-model="form.stream_secret"
                class="wg-input mono"
                :type="showSecret ? 'text' : 'password'"
                placeholder="留空表示不启用加密"
                autocomplete="off"
                spellcheck="false"
              />
              <span class="wg-suffix">
                <button class="wg-icon-btn" type="button" :aria-label="showSecret ? '隐藏口令' : '显示口令'" @click="showSecret = !showSecret">
                  <Icon :name="showSecret ? 'eyeOff' : 'eye'" :size="14" />
                </button>
                <button class="wg-icon-btn" type="button" aria-label="复制口令" title="复制" @click="copySecret">
                  <Icon name="copy" :size="14" />
                </button>
              </span>
            </div>
            <span v-if="secretTooShort" class="wg-hint error">至少 16 个字符；太短的共享口令等于没有加密。</span>
            <span v-else-if="streamSecretSet" class="wg-hint">
              已配置。只有勾选了「隧道内层加密」的隧道才会使用它，其余隧道照旧。
            </span>
            <span v-else class="wg-hint">留空则不启用。启用后，未勾选加密的隧道不受影响。</span>
          </div>

          <div class="wg-field span-2">
            <span class="wg-label">口令指纹</span>
            <div class="secret-actions">
              <span class="wg-mono fp">{{ secretFingerprint || '—' }}</span>
              <span class="wg-spacer" />
              <TButton size="sm" icon="zap" @click="generateSecret">生成随机口令</TButton>
              <TButton size="sm" icon="copy" :disabled="!form.stream_secret.trim()" @click="copySecret">复制</TButton>
            </div>
            <span class="wg-hint">
              指纹必须与服务端「安全 → 隧道加密」页显示的一致。两端口令不同时，加密隧道的数据流会被拒绝（不会退化为明文）。
            </span>
          </div>
        </div>
      </section>

      <section class="wg-card">
        <div class="wg-card-head">
          <div class="wg-card-title"><Icon name="activity" :size="14" /> 连接行为</div>
          <span class="wg-spacer" />
          <span class="wg-card-sub">心跳 · 超时 · 指数退避</span>
        </div>
        <div class="wg-card-body form-grid cols-4">
          <label class="wg-field">
            <span class="wg-label">心跳间隔</span>
            <input v-model="form.heartbeat_interval" class="wg-input mono" placeholder="15s" spellcheck="false" />
          </label>
          <label class="wg-field">
            <span class="wg-label">拨号超时</span>
            <input v-model="form.dial_timeout" class="wg-input mono" placeholder="5s" spellcheck="false" />
          </label>
          <label class="wg-field">
            <span class="wg-label">最小退避</span>
            <input v-model="form.reconnect.min_backoff" class="wg-input mono" placeholder="1s" spellcheck="false" />
          </label>
          <label class="wg-field">
            <span class="wg-label">最大退避</span>
            <input v-model="form.reconnect.max_backoff" class="wg-input mono" placeholder="30s" spellcheck="false" />
          </label>
          <span class="wg-hint span-2">时长格式：15s / 2m / 1h。心跳与退避参数在下一次重连后生效。</span>
        </div>
      </section>

      <section class="wg-card">
        <div class="wg-card-head">
          <div class="wg-card-title"><Icon name="settings" :size="14" /> 本地控制台</div>
          <span class="wg-spacer" />
          <span class="wg-card-sub">只读 · 修改需重启进程</span>
        </div>
        <div class="wg-card-body">
          <dl class="wg-kv">
            <dt>状态</dt><dd>{{ readOnly.webui_enabled ? '已启用' : '已停用' }}</dd>
            <dt>监听地址</dt><dd class="wg-mono">{{ readOnly.webui_addr || '—' }}</dd>
            <dt>会话有效期</dt><dd class="wg-mono">{{ readOnly.webui_session_ttl || '—' }}</dd>
            <dt>配置文件</dt><dd class="wg-mono">{{ configPath || 'wormgate.yaml' }}</dd>
          </dl>
          <p class="wg-hint" style="margin-top: 12px">
            监听地址、会话有效期等属于启动期参数，热加载会被服务端拒绝；请切到「YAML 高级」修改并重启 fpc。
          </p>
        </div>
      </section>
    </div>

    <!-- ==================== YAML view ==================== -->
    <section v-else class="wg-card editor-card">
      <div class="wg-card-head">
        <div class="wg-card-title">
          <Icon name="terminal" :size="14" />
          <span class="wg-mono" style="font-size: var(--fs-12)">{{ configPath || 'wormgate.yaml' }}</span>
        </div>
        <span class="wg-spacer" />
        <span class="wg-card-sub wg-mono">{{ lineCount }} 行</span>
      </div>
      <div class="editor-wrap">
        <textarea
          v-model="draft"
          class="editor"
          spellcheck="false"
          autocapitalize="off"
          autocomplete="off"
          wrap="off"
          aria-label="YAML 配置"
          :disabled="loading"
          placeholder="loading…"
          @keydown="onKeydown"
        />
      </div>
      <div class="wg-card-foot">
        <span class="wg-hint">优先级：CLI 参数 &gt; 环境变量 &gt; 项目配置 &gt; 全局配置 &gt; 默认值</span>
        <span class="wg-spacer" />
        <span v-if="yamlDirty" class="wg-hint">保存后立即生效；校验失败会整体回滚</span>
        <span v-else class="wg-hint wg-ok">与磁盘一致</span>
      </div>
    </section>
  </div>

  <!-- One-click import: paste the profile exported by the admin console -->
  <TModal
    v-model="importOpen"
    wide
    title="一键导入连接信息"
    description="粘贴管理台「导出」得到的配置，或直接选择 .yaml 文件。只导入服务端地址、令牌、客户端名与 TLS 校验策略，不覆盖隧道定义。"
  >
    <div class="import">
      <label
        class="drop"
        :class="{ over: dragOver }"
        @dragover.prevent="dragOver = true"
        @dragleave="dragOver = false"
        @drop.prevent="onDrop"
      >
        <input
          ref="fileInput"
          type="file"
          accept=".yaml,.yml,.json,.txt"
          hidden
          @change="importFile"
        />
        <Icon name="upload" :size="15" />
        <span>把配置文件拖到这里，或<button type="button" class="pick" @click.prevent="fileInput?.click()">选择文件</button></span>
      </label>

      <textarea
        v-model="importText"
        class="wg-input mono"
        rows="8"
        spellcheck="false"
        placeholder="client:&#10;  server: 1.2.3.4:7001&#10;  token: wg_...&#10;  client_name: demo"
      />

      <p v-if="importError" class="import-err">{{ importError }}</p>

      <dl v-else-if="importParsed" class="import-preview">
        <dt>服务端</dt>
        <dd class="wg-mono">{{ importParsed.server }}</dd>
        <dt>令牌</dt>
        <dd class="wg-mono">{{ importParsed.token.slice(0, 10) }}…（{{ importParsed.token.length }} 字符）</dd>
        <dt>客户端名</dt>
        <dd class="wg-mono">{{ importParsed.client_name || '（保持当前）' }}</dd>
        <dt>跳过证书校验</dt>
        <dd class="wg-mono">{{ importParsed.insecure ? '是（自签名证书）' : '否' }}</dd>
      </dl>
    </div>

    <template #footer>
      <TButton variant="ghost" @click="importOpen = false">取消</TButton>
      <TButton :disabled="!importParsed" @click="importOnly">仅填入表单</TButton>
      <TButton variant="primary" icon="save" :loading="importBusy" :disabled="!importParsed" @click="importAndApply">
        导入并应用
      </TButton>
    </template>
  </TModal>
</template>

<style scoped>
.issues {
  border-radius: var(--r-md);
  border: 1px solid color-mix(in srgb, var(--danger) 34%, transparent);
  background: linear-gradient(180deg, var(--danger-soft), transparent);
  overflow: hidden;
  margin-bottom: 14px;
}
.issues-head {
  display: flex;
  align-items: center;
  gap: 8px;
  padding: 10px 13px;
  color: var(--danger);
  font-size: var(--fs-12);
}
.issues ul { padding: 0 13px 11px 30px; display: flex; flex-direction: column; gap: 4px; }
.issues li {
  font-size: var(--fs-11);
  color: var(--text-muted);
  list-style: disc;
  line-height: 1.6;
}

.settings-stack { display: flex; flex-direction: column; gap: 14px; }
.form-grid { display: grid; grid-template-columns: repeat(2, minmax(0, 1fr)); gap: 16px 18px; }
.form-grid.cols-4 { grid-template-columns: repeat(4, minmax(0, 1fr)); }
.span-2 { grid-column: 1 / -1; }
.switch-label { font-size: var(--fs-12); color: var(--text-muted); font-family: var(--font-mono); }

.secret-actions { display: flex; align-items: center; gap: 8px; flex-wrap: wrap; }
.secret-actions .fp {
  padding: 3px 8px;
  border-radius: var(--r-xs);
  background: var(--surface-2);
  border: 1px solid var(--hairline);
  font-size: var(--fs-11);
  color: var(--text-dim);
  letter-spacing: .04em;
}

@media (max-width: 1100px) {
  .form-grid.cols-4 { grid-template-columns: repeat(2, minmax(0, 1fr)); }
}
@media (max-width: 760px) {
  .form-grid, .form-grid.cols-4 { grid-template-columns: 1fr; }
}

.editor-card { display: flex; flex-direction: column; min-height: 420px; }
.editor-wrap { flex: 1; display: flex; min-height: 380px; }
.editor {
  flex: 1;
  width: 100%;
  min-height: 380px;
  padding: 14px 16px;
  background: var(--surface-2);
  border: 0;
  border-bottom: 1px solid var(--hairline);
  color: var(--text);
  font-family: var(--font-mono);
  font-size: var(--fs-12);
  line-height: 1.75;
  resize: vertical;
  tab-size: 2;
}
.editor:focus { outline: none; background: var(--surface-1); box-shadow: inset 0 0 0 1px var(--accent-line); }

/* one-click import */
.import { display: flex; flex-direction: column; gap: 10px; }
.drop {
  display: flex;
  align-items: center;
  justify-content: center;
  gap: 8px;
  padding: 14px;
  border-radius: var(--r-md);
  border: 1px dashed var(--hairline);
  background: var(--surface-2);
  color: var(--text-dim);
  font-size: var(--fs-12);
  cursor: pointer;
  transition: border-color .15s, background .15s, color .15s;
}
.drop:hover, .drop.over { border-color: var(--accent-line); background: var(--accent-soft); color: var(--text); }
.pick {
  margin-left: 4px;
  background: none;
  border: 0;
  padding: 0;
  color: var(--accent);
  font: inherit;
  cursor: pointer;
  text-decoration: underline;
  text-underline-offset: 2px;
}
.import textarea.wg-input {
  /* .wg-input is a 34px-tall single line by default; this one is a document. */
  height: auto;
  min-height: 160px;
  padding: 9px 11px;
  resize: vertical;
  line-height: 1.6;
}
.import-err { font-size: var(--fs-12); color: var(--danger); margin: 0; }
.import-preview {
  display: grid;
  grid-template-columns: auto 1fr;
  gap: 4px 14px;
  margin: 0;
  padding: 10px 12px;
  border-radius: var(--r-sm);
  background: var(--surface-2);
  font-size: var(--fs-12);
}
.import-preview dt { color: var(--text-dim); }
.import-preview dd { margin: 0; color: var(--text); overflow-wrap: anywhere; }
</style>
