<script setup>
/**
 * Application shell: fixed rail on the left, scrolling content on the right.
 *
 * The rail collapses to an icon strip below 1024px and the choice is sticky, so
 * a laptop user can keep the extra 156px of chart width permanently.
 */
import { ref } from 'vue'
import { RouterLink, useRoute } from 'vue-router'
import Icon from './Icon.vue'
import BrandMark from './BrandMark.vue'

const props = defineProps({
  brand: { type: String, default: 'Wormgate' },
  tagline: { type: String, default: '' },
  items: { type: Array, default: () => [] },
  /** Optional short status line under the brand, e.g. the control-plane state. */
  statusText: { type: String, default: '' },
  statusTone: { type: String, default: 'default' }
})

const route = useRoute()
const collapsed = ref(window.innerWidth < 1024)

function isActive(item) {
  if (item.exact) return route.path === item.to
  return route.path === item.to || route.path.startsWith(`${item.to}/`)
}
</script>

<template>
  <div class="wg-shell" :class="{ collapsed }">
    <aside class="wg-rail">
      <div class="wg-rail-brand">
        <BrandMark :size="30" />
        <div class="wg-rail-brand-text">
          <strong>{{ brand }}</strong>
          <span v-if="tagline">{{ tagline }}</span>
        </div>
      </div>

      <div v-if="statusText && !collapsed" class="wg-rail-status">
        <span class="wg-dot" :data-tone="statusTone === 'default' ? undefined : statusTone" />
        <span class="wg-truncate">{{ statusText }}</span>
      </div>

      <nav class="wg-rail-nav" aria-label="主导航">
        <RouterLink
          v-for="item in items"
          :key="item.to"
          :to="item.to"
          class="wg-nav-item"
          :class="{ active: isActive(item) }"
          :title="collapsed ? item.label : undefined"
        >
          <Icon :name="item.icon" :size="16" />
          <span class="wg-nav-label">{{ item.label }}</span>
          <span v-if="item.badge" class="wg-nav-badge wg-mono">{{ item.badge }}</span>
        </RouterLink>
      </nav>

      <div class="wg-rail-foot">
        <slot name="railFooter" />
        <button
          class="wg-nav-item as-button"
          :aria-label="collapsed ? '展开侧栏' : '收起侧栏'"
          @click="collapsed = !collapsed"
        >
          <Icon :name="collapsed ? 'chevronRight' : 'chevronLeft'" :size="16" />
          <span class="wg-nav-label">收起侧栏</span>
        </button>
      </div>
    </aside>

    <div class="wg-main">
      <slot />
    </div>
  </div>
</template>

<style>
.wg-shell {
  position: relative;
  z-index: 1;
  display: grid;
  grid-template-columns: var(--rail-w) minmax(0, 1fr);
  min-height: 100vh;
  transition: grid-template-columns var(--dur-3) var(--ease);
}
.wg-shell.collapsed { grid-template-columns: var(--rail-w-collapsed) minmax(0, 1fr); }

/* ---- rail -------------------------------------------------------------- */

.wg-rail {
  position: sticky;
  top: 0;
  align-self: start;
  height: 100vh;
  display: flex;
  flex-direction: column;
  gap: 14px;
  padding: 16px 12px;
  border-right: 1px solid var(--hairline);
  background: color-mix(in srgb, var(--surface-1) 72%, transparent);
  backdrop-filter: blur(12px);
  overflow: hidden;
  z-index: 20;
}

.wg-rail-brand {
  display: flex;
  align-items: center;
  gap: 10px;
  padding: 2px 4px 6px;
  min-width: 0;
}
.wg-rail-brand-text { display: flex; flex-direction: column; min-width: 0; line-height: 1.25; }
.wg-rail-brand-text strong { font-size: var(--fs-14); font-weight: 600; letter-spacing: -.01em; }
.wg-rail-brand-text span { font-size: var(--fs-11); color: var(--text-dim); }
.wg-shell.collapsed .wg-rail-brand-text { display: none; }

.wg-rail-status {
  display: flex;
  align-items: center;
  gap: 7px;
  margin: 0 4px;
  padding: 6px 8px;
  border-radius: var(--r-xs);
  background: var(--surface-2);
  border: 1px solid var(--hairline);
  font-size: var(--fs-11);
  color: var(--text-muted);
  min-width: 0;
}

.wg-rail-nav {
  display: flex;
  flex-direction: column;
  gap: 2px;
  flex: 1;
  min-height: 0;
  overflow-y: auto;
}

/* Nav item doubles as a button for the collapse control, hence the shared class
   with an `as-button` modifier rather than a second style block. */
.wg-nav-item {
  display: flex;
  align-items: center;
  gap: 10px;
  height: 36px;
  padding: 0 10px;
  border-radius: var(--r-sm);
  color: var(--text-muted);
  font-size: var(--fs-13);
  font-weight: 500;
  white-space: nowrap;
  position: relative;
  transition: background var(--dur-1) var(--ease), color var(--dur-1) var(--ease);
}
.wg-nav-item:hover { background: var(--surface-2); color: var(--text); }
.wg-nav-item svg { flex: none; }

.wg-nav-item.active {
  background: var(--accent-soft);
  color: var(--accent);
}
/* Active accent bar: anchored to the rail edge, gives a clear "you are here". */
.wg-nav-item.active::before {
  content: '';
  position: absolute;
  left: -12px;
  top: 50%;
  transform: translateY(-50%);
  width: 2px;
  height: 18px;
  border-radius: 0 2px 2px 0;
  background: var(--accent);
}

.wg-nav-badge {
  margin-left: auto;
  font-size: var(--fs-11);
  padding: 1px 6px;
  border-radius: 999px;
  background: var(--surface-3);
  color: var(--text-dim);
}
.wg-nav-item.active .wg-nav-badge { background: var(--accent-soft); color: var(--accent); }

.wg-rail-foot { display: flex; flex-direction: column; gap: 2px; }
.wg-nav-item.as-button { width: 100%; text-align: left; color: var(--text-dim); }
.wg-shell.collapsed .wg-nav-label { display: none; }
.wg-shell.collapsed .wg-nav-item { justify-content: center; padding: 0; }
.wg-shell.collapsed .wg-nav-badge { display: none; }
.wg-shell.collapsed .wg-nav-item.active::before { left: -12px; }

/* ---- main -------------------------------------------------------------- */

.wg-main { min-width: 0; display: flex; flex-direction: column; }

.wg-page {
  flex: 1;
  padding: 20px 22px 40px;
  display: flex;
  flex-direction: column;
  gap: 16px;
  max-width: 1560px;
  width: 100%;
}

@media (max-width: 640px) {
  .wg-page { padding: 14px 14px 32px; }
  .wg-shell { grid-template-columns: var(--rail-w-collapsed) minmax(0, 1fr); }
}
</style>
