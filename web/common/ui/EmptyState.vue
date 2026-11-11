<script setup>
/**
 * Empty state.
 *
 * Used whenever a list has no rows. `action` slot is intentionally optional:
 * an empty list caused by a filter needs "clear filter", not "create".
 */
import Icon from './Icon.vue'

defineProps({
  icon: { type: String, default: 'inbox' },
  title: { type: String, default: '暂无数据' },
  description: { type: String, default: '' }
})
</script>

<template>
  <div class="wg-empty">
    <div class="wg-empty-icon"><Icon :name="icon" :size="20" /></div>
    <h4>{{ title }}</h4>
    <p v-if="description">{{ description }}</p>
    <div v-if="$slots.default" style="margin-top: 4px">
      <slot />
    </div>
  </div>
</template>
