<script setup>
/**
 * Admin console login (username + password, optional OAuth2).
 *
 * The OAuth entry point is a full-page navigation to GET /api/oauth/start, not
 * a fetch: the server must set a state cookie and issue a 302 to the identity
 * provider, and a cookie set on an XHR response would be discarded by the
 * browser (PKCE state would then never match on the callback).
 */
import { nextTick, onMounted, ref } from 'vue'
import { useRouter } from 'vue-router'

import BrandMark from '@common/ui/BrandMark.vue'
import TButton from '@common/ui/TButton.vue'
import Icon from '@common/ui/Icon.vue'
import { adminApi } from '../api'
import { session } from '../session'

const router = useRouter()
const username = ref('')
const password = ref('')
const busy = ref(false)
const error = ref('')
const reveal = ref(false)
const oauth = ref(false)
const version = ref('')
const userInput = ref(null)

onMounted(async () => {
  nextTick(() => userInput.value?.focus())
  try {
    const meta = await adminApi.meta()
    oauth.value = !!meta?.oauth_enabled
    version.value = meta?.version || ''
  } catch {
    // Meta is optional; the form still works without it.
  }
  // Surface a failed OAuth round-trip (?error=...) instead of failing silently.
  const code = new URLSearchParams(location.search).get('error')
  if (code) error.value = `单点登录失败：${code}`
})

async function submit() {
  if (busy.value) return
  if (!username.value.trim() || !password.value) {
    error.value = '请输入用户名和密码'
    return
  }
  busy.value = true
  error.value = ''
  try {
    await session.login(username.value.trim(), password.value)
    const redirect = router.currentRoute.value.query.redirect
    await router.replace(typeof redirect === 'string' && redirect.startsWith('/') ? redirect : '/')
  } catch (err) {
    error.value =
      err?.status === 401
        ? '用户名或密码不正确'
        : err?.status === 429
          ? '尝试过于频繁，请稍后再试'
          : err?.message || '登录失败'
    password.value = ''
  } finally {
    busy.value = false
  }
}
</script>

<template>
  <div class="login-screen">
    <div class="login-card wg-enter">
      <div class="login-head">
        <BrandMark :size="44" />
        <div>
          <h1>服务端控制台</h1>
          <p>使用管理员或租户账号登录，所有会话操作都会写入审计日志。</p>
        </div>
      </div>

      <form class="login-form" @submit.prevent="submit">
        <label class="wg-field">
          <span class="wg-label">用户名</span>
          <input
            ref="userInput"
            v-model="username"
            class="wg-input"
            autocomplete="username"
            spellcheck="false"
            placeholder="admin"
          />
        </label>

        <label class="wg-field">
          <span class="wg-label">密码</span>
          <div class="wg-input-group">
            <input
              v-model="password"
              :type="reveal ? 'text' : 'password'"
              class="wg-input"
              autocomplete="current-password"
              placeholder="••••••••"
            />
            <span class="wg-suffix">
              <button
                type="button"
                class="wg-icon-btn"
                :aria-label="reveal ? '隐藏密码' : '显示密码'"
                @click="reveal = !reveal"
              >
                <Icon :name="reveal ? 'eyeOff' : 'eye'" />
              </button>
            </span>
          </div>
        </label>

        <p v-if="error" class="wg-hint error" role="alert">
          <Icon name="alertCircle" :size="12" style="display: inline; vertical-align: -2px" />
          {{ error }}
        </p>

        <TButton variant="primary" size="lg" block :loading="busy" type="submit">
          {{ busy ? '验证中…' : '登录' }}
        </TButton>
      </form>

      <template v-if="oauth">
        <div class="divider"><span>或</span></div>
        <TButton block icon="shield" @click="location.assign('/api/oauth/start')">使用单点登录 (OAuth2)</TButton>
      </template>

      <div class="login-foot">
        <span class="wg-dot" data-tone="ok" />
        <span>JWT 会话 · CSRF 双提交校验</span>
        <span class="wg-spacer" />
        <span v-if="version" class="wg-mono">v{{ version }}</span>
      </div>
    </div>

    <button type="button" class="wg-btn ghost" data-i18n-toggle data-i18n-skip style="margin-top: 12px">
      中 / EN
    </button>
  </div>
</template>

<style scoped>
.login-screen {
  min-height: 100vh;
  display: grid;
  place-content: center;
  padding: 24px;
  position: relative;
  z-index: 1;
}

.login-card {
  width: min(430px, calc(100vw - 32px));
  padding: 28px;
  border-radius: var(--r-xl);
  background: var(--surface-1);
  border: 1px solid var(--hairline-strong);
  box-shadow: var(--shadow-3);
  position: relative;
  overflow: hidden;
}
.login-card::before {
  content: '';
  position: absolute;
  inset: 0 0 auto;
  height: 1px;
  background: linear-gradient(90deg, transparent, var(--violet), transparent);
  opacity: .75;
}

.login-head { display: flex; gap: 14px; align-items: flex-start; margin-bottom: 22px; }
.login-head h1 { font-size: var(--fs-16); margin-bottom: 3px; }
.login-head p { font-size: var(--fs-12); color: var(--text-dim); line-height: 1.55; }

.login-form { display: flex; flex-direction: column; gap: 13px; }

.divider {
  display: flex;
  align-items: center;
  gap: 10px;
  margin: 16px 0 12px;
  color: var(--text-dim);
  font-size: var(--fs-11);
}
.divider::before,
.divider::after { content: ''; flex: 1; height: 1px; background: var(--hairline); }

.login-foot {
  display: flex;
  align-items: center;
  gap: 7px;
  margin-top: 20px;
  padding-top: 15px;
  border-top: 1px solid var(--hairline);
  font-size: var(--fs-11);
  color: var(--text-dim);
}
</style>
