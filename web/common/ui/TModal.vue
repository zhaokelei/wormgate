<script setup>
/**
 * Modal dialog.
 *
 * Accessibility contract: role=dialog + aria-modal, Escape and backdrop-click
 * close (unless `persistent`), focus moves into the panel on open and returns
 * to the invoking element on close, and Tab is trapped inside the panel.
 * Teleported to <body> so it is never clipped by an ancestor's overflow.
 */
import { nextTick, onBeforeUnmount, ref, watch } from 'vue'
import Icon from './Icon.vue'

const props = defineProps({
  modelValue: { type: Boolean, default: false },
  title: { type: String, default: '' },
  description: { type: String, default: '' },
  wide: { type: Boolean, default: false },
  persistent: { type: Boolean, default: false }
})

const emit = defineEmits(['update:modelValue'])
const panel = ref(null)
let previousFocus = null

const FOCUSABLE =
  'a[href], button:not([disabled]), input:not([disabled]), select:not([disabled]), textarea:not([disabled]), [tabindex]:not([tabindex="-1"])'

function close() {
  if (props.persistent) return
  emit('update:modelValue', false)
}

function onKeydown(event) {
  if (event.key === 'Escape') {
    event.stopPropagation()
    close()
    return
  }
  if (event.key !== 'Tab' || !panel.value) return
  const items = [...panel.value.querySelectorAll(FOCUSABLE)].filter((el) => el.offsetParent !== null)
  if (items.length === 0) return
  const first = items[0]
  const last = items[items.length - 1]
  if (event.shiftKey && document.activeElement === first) {
    event.preventDefault()
    last.focus()
  } else if (!event.shiftKey && document.activeElement === last) {
    event.preventDefault()
    first.focus()
  }
}

watch(
  () => props.modelValue,
  async (open) => {
    document.body.style.overflow = open ? 'hidden' : ''
    if (open) {
      previousFocus = document.activeElement
      await nextTick()
      const target = panel.value?.querySelector('[autofocus]') || panel.value?.querySelector(FOCUSABLE)
      target?.focus()
    } else {
      previousFocus?.focus?.()
      previousFocus = null
    }
  }
)

onBeforeUnmount(() => {
  document.body.style.overflow = ''
})
</script>

<template>
  <Teleport to="body">
    <Transition name="wg-fade">
      <div v-if="modelValue" class="wg-overlay" @click.self="close" />
    </Transition>
    <Transition name="wg-pop">
      <div
        v-if="modelValue"
        ref="panel"
        class="wg-modal"
        :class="{ wide }"
        role="dialog"
        aria-modal="true"
        :aria-label="title"
        @keydown="onKeydown"
      >
        <header class="wg-modal-head">
          <div style="flex: 1; min-width: 0">
            <h3>{{ title }}</h3>
            <p v-if="description">{{ description }}</p>
          </div>
          <button v-if="!persistent" class="wg-icon-btn" aria-label="关闭" @click="close">
            <Icon name="x" />
          </button>
        </header>
        <div class="wg-modal-body">
          <slot />
        </div>
        <footer v-if="$slots.footer" class="wg-modal-foot">
          <slot name="footer" :close="close" />
        </footer>
      </div>
    </Transition>
  </Teleport>
</template>

<style>
.wg-fade-enter-active,
.wg-fade-leave-active { transition: opacity var(--dur-2) var(--ease); }
.wg-fade-enter-from,
.wg-fade-leave-to { opacity: 0; }

.wg-pop-enter-active { transition: opacity var(--dur-2) var(--ease), transform var(--dur-3) var(--ease-out); }
.wg-pop-leave-active { transition: opacity var(--dur-1) var(--ease); }
.wg-pop-enter-from { opacity: 0; transform: scale(.97) translateY(8px); }
.wg-pop-leave-to { opacity: 0; }
</style>
