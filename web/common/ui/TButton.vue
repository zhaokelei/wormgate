<script setup>
/**
 * Application button with a built-in busy state.
 *
 * The loading label replaces the slot text (instead of appending a spinner to
 * it) so the button width does not jump while a request is in flight.
 */
import Icon from './Icon.vue'

defineProps({
  variant: {
    type: String,
    default: 'default',
    validator: (v) => ['default', 'primary', 'ghost', 'danger'].includes(v)
  },
  size: { type: String, default: 'md', validator: (v) => ['sm', 'md', 'lg'].includes(v) },
  icon: { type: String, default: '' },
  loading: { type: Boolean, default: false },
  disabled: { type: Boolean, default: false },
  type: { type: String, default: 'button' },
  block: { type: Boolean, default: false }
})
</script>

<template>
  <button
    :type="type"
    class="wg-btn"
    :class="[variant, size, { block }]"
    :disabled="disabled || loading"
    :aria-busy="loading || undefined"
  >
    <span v-if="loading" class="wg-spin" />
    <Icon v-else-if="icon" :name="icon" :size="size === 'sm' ? 13 : 15" />
    <slot />
  </button>
</template>
