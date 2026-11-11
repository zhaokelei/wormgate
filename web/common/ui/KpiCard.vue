<script setup>
/**
 * KPI tile: one headline number plus context.
 *
 * `value` is rendered in tabular monospace so a counter that ticks every second
 * never changes width; `small` carries the unit ("/s") so the number itself can
 * stay large.
 */
import Icon from './Icon.vue'

defineProps({
  label: { type: String, required: true },
  value: { type: [String, Number], default: '--' },
  small: { type: String, default: '' },
  hint: { type: String, default: '' },
  icon: { type: String, default: '' },
  tone: {
    type: String,
    default: 'accent',
    validator: (v) => ['accent', 'violet', 'ok', 'warn', 'danger', 'info'].includes(v)
  },
  mono: { type: Boolean, default: true }
})
</script>

<template>
  <div class="wg-kpi" :data-tone="tone">
    <div class="wg-kpi-head">
      <span class="wg-kpi-label">{{ label }}</span>
      <span v-if="icon" class="wg-kpi-icon"><Icon :name="icon" :size="14" /></span>
    </div>
    <div class="wg-kpi-value" :class="{ 'wg-mono': mono }">
      {{ value }}<small v-if="small">{{ small }}</small>
    </div>
    <div class="wg-kpi-hint">
      <slot name="hint">{{ hint }}</slot>
    </div>
  </div>
</template>
