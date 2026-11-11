<script setup>
/**
 * Local console login.
 *
 * Auth model: one shared secret (the token printed on first start, or the
 * optional password hash). There is deliberately no username field and no
 * "remember me": the console binds to 127.0.0.1 by default, and the session
 * cookie is HttpOnly + SameSite=Strict.
 */
import { nextTick, onMounted, ref } from 'vue'
import { useRouter } from 'vue-router'

import BrandMark from '@common/ui/BrandMark.vue'
import TButton from '@common/ui/TButton.vue'
import Icon from '@common/ui/Icon.vue'
import { session } from '../session'

const router = useRouter()
const secret = ref('')
const busy = ref(false)
const error = ref('')
const reveal = ref(false)
const secretInput = ref(null)

onMounted(() => nextTick(() => secretInput.value?.focus()))

async function submit() {
  if (busy.value) return
  const value = secret.value.trim()
  if (!value) {
    error.value = '请输入本地登录密码'
    secretInput.value?.focus()
    return
  }
  // The wg_ token authenticates fpc to fps. It belongs in the configuration
  // page, not here: entering it is the single most common login mistake.
  if (/^(tx|wg)_/i.test(value)) {
    error.value = '这是连接服务端的客户端令牌，请在「配置」页填写；此处只需输入本地登录密码'
    secretInput.value?.focus()
    return
  }
  busy.value = true
  error.value = ''
  try {
    await session.login(value)
    const redirect = router.currentRoute.value.query.redirect
    await router.replace(typeof redirect === 'string' && redirect.startsWith('/') ? redirect : '/')
  } catch (err) {
    error.value = err?.status === 401 ? '令牌或密码不正确' : err?.message || '登录失败'
    secret.value = ''
    nextTick(() => secretInput.value?.focus())
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
          <h1>Wormgate 客户端控制台</h1>
          <p>输入启动时打印的本地登录密码；连接服务端的令牌在「配置」页填写。</p>
        </div>
      </div>

      <form class="login-form" @submit.prevent="submit">
        <label class="wg-field">
          <span class="wg-label">本地登录密码</span>
          <div class="wg-input-group">
            <input
              ref="secretInput"
              v-model="secret"
              :type="reveal ? 'text' : 'password'"
              class="wg-input mono"
              placeholder="本地登录密码"
              autocomplete="current-password"
              spellcheck="false"
              :aria-invalid="error ? 'true' : undefined"
              aria-describedby="login-error"
            />
            <span class="wg-suffix">
              <button
                type="button"
                class="wg-icon-btn"
                :aria-label="reveal ? '隐藏令牌' : '显示令牌'"
                @click="reveal = !reveal"
              >
                <Icon :name="reveal ? 'eyeOff' : 'eye'" />
              </button>
            </span>
          </div>
        </label>

        <p v-if="error" id="login-error" class="wg-hint error" role="alert">
          <Icon name="alertCircle" :size="12" style="display: inline; vertical-align: -2px" />
          {{ error }}
        </p>

        <TButton variant="primary" size="lg" block :loading="busy" type="submit">
          {{ busy ? '验证中…' : '进入控制台' }}
        </TButton>
      </form>

      <div class="login-foot">
        <span class="wg-dot" data-tone="ok" />
        仅监听 127.0.0.1 · 会话 Cookie 为 HttpOnly
      </div>
    </div>

    <p class="login-hint">本地登录密码首次启动时打印，可在「安全」页修改；存于 data/client-console-password 或配置文件的 client.webui.password_hash。wg_ 令牌请在「配置」页填写。</p>
    <button
      type="button"
      class="wg-btn ghost"
      data-i18n-toggle
      data-i18n-skip
      style="margin-top: 12px"
    >
      中 / EN
    </button>
  </div>
</template>

<style scoped>
.login-screen {
  min-height: 100vh;
  display: grid;
  place-content: center;
  justify-items: center;
  gap: 16px;
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

/* Single accent hairline along the top edge: enough to lift the card off the
   background without a full gradient wash. */
.login-card::before {
  content: '';
  position: absolute;
  inset: 0 0 auto;
  height: 1px;
  background: linear-gradient(90deg, transparent, var(--accent), transparent);
  opacity: .7;
}

.login-head {
  display: flex;
  gap: 14px;
  align-items: flex-start;
  margin-bottom: 22px;
}
.login-head h1 { font-size: var(--fs-16); margin-bottom: 3px; }
.login-head p { font-size: var(--fs-12); color: var(--text-dim); line-height: 1.55; }

.login-form { display: flex; flex-direction: column; gap: 13px; }

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

.login-hint {
  font-size: var(--fs-11);
  color: var(--text-dim);
  text-align: center;
  max-width: 42ch;
  line-height: 1.7;
}
</style>
