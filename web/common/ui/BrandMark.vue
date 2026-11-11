<script setup>
/**
 * Brand mark + wordmark.
 *
 * Kept as inline SVG: the consoles run under `img-src 'self' data:`, so an
 * external logo file would need an extra request and a CSP exception. The
 * gradient is teal -> violet, which is also the chart palette, so the product
 * identity and the telemetry read as one system.
 */
defineProps({
  size: { type: Number, default: 32 },
  showText: { type: Boolean, default: false },
  subtitle: { type: String, default: '' }
})
</script>

<template>
  <div class="wg-brandmark">
    <svg
      :width="size"
      :height="size"
      viewBox="0 0 48 48"
      fill="none"
      xmlns="http://www.w3.org/2000/svg"
      role="img"
      aria-label="Wormgate"
    >
      <defs>
        <linearGradient id="wg-brand-arc" x1="6" y1="10" x2="42" y2="40" gradientUnits="userSpaceOnUse">
          <stop stop-color="#4096ff" />
          <stop offset=".55" stop-color="#1677ff" />
          <stop offset="1" stop-color="#9254de" />
        </linearGradient>
        <linearGradient id="wg-brand-core" x1="18" y1="18" x2="32" y2="34" gradientUnits="userSpaceOnUse">
          <stop stop-color="#f1f6fc" />
          <stop offset="1" stop-color="#93a7bd" />
        </linearGradient>
      </defs>
      <rect x="1" y="1" width="46" height="46" rx="13" fill="var(--surface-3)" stroke="var(--hairline-strong)" />
      <path
        d="M10 31.5c0-7.732 6.268-14 14-14s14 6.268 14 14"
        stroke="url(#wg-brand-arc)"
        stroke-width="3"
        stroke-linecap="round"
      />
      <path
        d="M16.5 33.2c0-4.142 3.358-7.5 7.5-7.5s7.5 3.358 7.5 7.5"
        stroke="url(#wg-brand-arc)"
        stroke-width="2.2"
        stroke-linecap="round"
        opacity=".65"
      />
      <circle cx="24" cy="33" r="2.9" fill="url(#wg-brand-core)" />
      <circle cx="24" cy="33" r="6.6" stroke="#1677ff" stroke-opacity=".32" stroke-width="1" />
    </svg>
    <div v-if="showText" class="wg-brandmark-text">
      <strong>Wormgate</strong>
      <span v-if="subtitle">{{ subtitle }}</span>
    </div>
  </div>
</template>

<style>
.wg-brandmark { display: flex; align-items: center; gap: 10px; min-width: 0; }
.wg-brandmark-text { display: flex; flex-direction: column; line-height: 1.2; min-width: 0; }
.wg-brandmark-text strong { font-size: var(--fs-14); letter-spacing: -.01em; }
.wg-brandmark-text span { font-size: var(--fs-11); color: var(--text-dim); }
</style>
