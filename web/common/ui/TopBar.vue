<script setup>
/**
 * Global console header: identity on the left, live link state on the right.
 *
 * The connection chip is the console's honesty indicator — it reflects the
 * realtime transport, not a cached request result, so an operator can tell the
 * difference between "traffic is zero" and "the stream is down".
 */
import Icon from './Icon.vue'
import StatusPill from './StatusPill.vue'
import { lang, toggleLang } from '@common/i18n'

defineProps({
  title: { type: String, default: '' },
  subtitle: { type: String, default: '' },
  /** 'open' | 'connecting' | 'closed' */
  liveState: { type: String, default: 'closed' },
  version: { type: String, default: '' }
})

const LIVE_LABEL = {
  open: { state: 'ok', label: '实时连接' },
  connecting: { state: 'connecting', label: '连接中…' },
  closed: { state: 'disconnected', label: '实时断开' }
}
</script>

<template>
  <header class="wg-topbar">
    <div class="wg-topbar-main">
      <h1 v-if="title">{{ title }}</h1>
      <p v-if="subtitle">{{ subtitle }}</p>
    </div>
    <div class="wg-topbar-actions">
      <span class="wg-live">
        <StatusPill
          :state="LIVE_LABEL[liveState]?.state"
          :label="LIVE_LABEL[liveState]?.label"
          :live="liveState === 'open'"
          outline
        />
      </span>
      <slot name="actions" />
      <slot name="right">
        <span v-if="version" class="wg-badge outline wg-mono">v{{ version }}</span>
        <button
          class="wg-icon-btn"
          :aria-label="lang === 'zh' ? 'Switch to English' : 'Switch to Chinese'"
          :title="lang === 'zh' ? 'Switch to English' : 'Switch to Chinese'"
          @click="toggleLang"
        >
          <span class="wg-mono" style="font-size: 11px">{{ lang === 'zh' ? 'EN' : '中' }}</span>
        </button>
        <button
          class="wg-icon-btn"

          :aria-label="`切换到${$theme.current === 'dark' ? '浅色' : '深色'}主题`"
          :title="`切换到${$theme.current === 'dark' ? '浅色' : '深色'}主题`"
          @click="$theme.toggle()"
        >
          <Icon :name="$theme.current === 'dark' ? 'sun' : 'moon'" :size="15" />
        </button>
      </slot>
    </div>
  </header>
</template>

<style>
.wg-topbar {
  position: sticky;
  top: 0;
  z-index: 30;
  display: flex;
  align-items: center;
  gap: 16px;
  min-height: var(--topbar-h);
  padding: 10px 22px;
  border-bottom: 1px solid var(--hairline);
  /* Translucent + blur keeps content readable while scrolling underneath,
     which is what makes the console feel layered rather than flat. */
  background: color-mix(in srgb, var(--bg) 78%, transparent);
  backdrop-filter: blur(14px) saturate(1.3);
}
.wg-topbar-main { min-width: 0; flex: 1; }
.wg-topbar-main h1 { font-size: var(--fs-16); font-weight: 600; letter-spacing: -.01em; }
.wg-topbar-main p { font-size: var(--fs-12); color: var(--text-dim); margin-top: 1px; }
.wg-topbar-actions { display: flex; align-items: center; gap: 8px; flex: none; }
.wg-topbar-actions .wg-live { display: flex; }

@media (max-width: 720px) {
  .wg-live { display: none; }
}
</style>
