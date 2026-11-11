<script setup>
/**
 * Shared icon renderer.
 *
 * Renders one of the inline stroke paths from common/icons.js. Because the
 * paths are stroked and inherit `currentColor`, an icon takes its colour from
 * the surrounding text and needs no per-use styling.
 */
import { computed } from 'vue'
import { icons, iconViewBox } from '@common/icons'

const props = defineProps({
  name: { type: String, required: true },
  size: { type: [Number, String], default: 16 },
  strokeWidth: { type: [Number, String], default: 1.7 }
})

const path = computed(() => icons[props.name] || '')
</script>

<template>
  <svg
    :viewBox="iconViewBox"
    :width="size"
    :height="size"
    fill="none"
    stroke="currentColor"
    :stroke-width="strokeWidth"
    stroke-linecap="round"
    stroke-linejoin="round"
    aria-hidden="true"
    focusable="false"
  >
    <path :d="path" />
  </svg>
</template>
