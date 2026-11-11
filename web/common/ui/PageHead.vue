<script setup>
/**
 * Per-view page header: title, one-line description, action slot.
 *
 * View-level headers sit *below* the sticky TopBar. Keeping the two separate
 * means the global context (live link state, theme) never scrolls away while a
 * page's own title can, which is the behaviour operators expect from a console.
 */
defineProps({
  title: { type: String, required: true },
  description: { type: String, default: '' }
})
</script>

<template>
  <div class="wg-page-head">
    <div class="wg-page-head-main">
      <h1>{{ title }}</h1>
      <p v-if="description">{{ description }}</p>
    </div>
    <div v-if="$slots.actions" class="wg-page-head-actions">
      <slot name="actions" />
    </div>
  </div>
</template>

<style>
.wg-page-head {
  display: flex;
  align-items: flex-end;
  gap: 14px;
  flex-wrap: wrap;
  margin-bottom: 2px;
}
.wg-page-head-main { min-width: 0; flex: 1; }
.wg-page-head-main h1 { font-size: var(--fs-20); letter-spacing: -.02em; }
.wg-page-head-main p { font-size: var(--fs-12); color: var(--text-dim); margin-top: 3px; }
.wg-page-head-actions { display: flex; align-items: center; gap: 8px; flex: none; }
</style>
