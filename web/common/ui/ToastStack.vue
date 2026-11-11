<script setup>
/**
 * Toast stack.
 *
 * Mounted once per console and fed through a small store (`useToasts`) so any
 * module — including the API error handler — can raise a notification without
 * prop-drilling. Errors linger longer than successes: they usually carry an
 * instruction the user has to read.
 */
import { toasts, dismissToast } from '../toast'
import Icon from './Icon.vue'

const ICONS = {
  ok: 'checkCircle',
  danger: 'xCircle',
  warn: 'alertTriangle',
  info: 'info'
}
</script>

<template>
  <div class="wg-toasts" role="status" aria-live="polite">
    <TransitionGroup name="wg-toast">
      <div v-for="toast in toasts" :key="toast.id" class="wg-toast" :data-tone="toast.tone">
        <span class="wg-toast-icon"><Icon :name="ICONS[toast.tone] || 'info'" :size="15" /></span>
        <div style="flex: 1; min-width: 0">
          <div>{{ toast.message }}</div>
          <div v-if="toast.detail" class="wg-hint" style="margin-top: 2px">{{ toast.detail }}</div>
        </div>
        <button class="wg-icon-btn" aria-label="关闭提示" @click="dismissToast(toast.id)">
          <Icon name="x" :size="13" />
        </button>
      </div>
    </TransitionGroup>
  </div>
</template>

<style>
.wg-toast-enter-active { transition: opacity var(--dur-2) var(--ease), transform var(--dur-3) var(--ease-out); }
.wg-toast-leave-active { transition: opacity var(--dur-2) var(--ease), transform var(--dur-2) var(--ease); }
.wg-toast-enter-from { opacity: 0; transform: translateY(10px) scale(.98); }
.wg-toast-leave-to { opacity: 0; transform: translateX(16px); }
.wg-toast-move { transition: transform var(--dur-2) var(--ease); }
</style>
