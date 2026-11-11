<script setup>
/**
 * Shared "change my console password" form.
 *
 * Both consoles authenticate a browser with a password that has nothing to do
 * with the tx_ token used between fpc and fps. The Go side enforces the real
 * rules (current password, >= 8 characters, CSRF); the checks here only save a
 * round trip. `submit` is injected by each console so this component stays
 * ignorant of which API it is talking to.
 */
import { reactive, ref } from 'vue'

import TButton from './TButton.vue'
import { notifyOk, notifyError } from '../toast'

const props = defineProps({
  /** async ({ current_password, new_password }) => Promise */
  submit: { type: Function, required: true },
  /** Admin console shows the account name; the local console has none. */
  username: { type: String, default: '' }
})

const emit = defineEmits(['changed'])

const form = reactive({ current: '', next: '', confirm: '' })
const busy = ref(false)
const error = ref('')
const done = ref(false)

function validate() {
  if (!form.current) return '请输入当前密码'
  if (form.next.length < 8) return '新密码至少 8 位'
  if (form.next !== form.confirm) return '两次输入的新密码不一致'
  if (form.next === form.current) return '新密码不能与当前密码相同'
  return ''
}

async function save() {
  if (busy.value) return
  error.value = ''
  done.value = false
  const problem = validate()
  if (problem) {
    error.value = problem
    return
  }
  busy.value = true
  try {
    await props.submit({ current_password: form.current, new_password: form.next })
    form.current = ''
    form.next = ''
    form.confirm = ''
    done.value = true
    notifyOk('密码已修改', '其他已登录的会话需要重新登录')
    emit('changed')
  } catch (err) {
    error.value = err?.message || '修改失败'
    notifyError(err, '修改密码失败')
  } finally {
    busy.value = false
  }
}
</script>

<template>
  <section class="wg-card">
    <div class="wg-card-body">
      <div class="pw-grid">
        <label v-if="username" class="wg-field">
          <span class="wg-label">账号</span>
          <input class="wg-input" :value="username" disabled />
        </label>
        <label class="wg-field">
          <span class="wg-label">当前密码</span>
          <input
            v-model="form.current"
            type="password"
            class="wg-input"
            autocomplete="current-password"
            placeholder="请输入当前登录密码"
          />
        </label>
        <label class="wg-field">
          <span class="wg-label">新密码</span>
          <input
            v-model="form.next"
            type="password"
            class="wg-input"
            autocomplete="new-password"
            placeholder="至少 8 位"
          />
        </label>
        <label class="wg-field">
          <span class="wg-label">确认新密码</span>
          <input
            v-model="form.confirm"
            type="password"
            class="wg-input"
            autocomplete="new-password"
            placeholder="再输入一次新密码"
          />
        </label>
      </div>

      <p v-if="error" class="wg-hint error" role="alert">{{ error }}</p>
      <p v-else-if="done" class="wg-hint pw-ok">密码已更新，请牢记新密码。</p>

      <div class="pw-actions">
        <TButton variant="primary" icon="key" :loading="busy" @click="save">修改密码</TButton>
        <span class="wg-hint">修改后立即生效，无需重启进程。</span>
      </div>
    </div>
  </section>
</template>

<style scoped>
.pw-grid {
  display: grid;
  grid-template-columns: repeat(2, minmax(0, 1fr));
  gap: 14px;
  max-width: 640px;
}
.pw-ok { color: var(--accent); }
.pw-actions {
  display: flex;
  align-items: center;
  gap: 12px;
  flex-wrap: wrap;
  margin-top: 18px;
  padding-top: 14px;
  border-top: 1px solid var(--hairline);
}
@media (max-width: 620px) {
  .pw-grid { grid-template-columns: 1fr; }
}
</style>
