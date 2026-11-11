/**
 * Theme controller.
 *
 * Dark is the default because the console is usually open next to a terminal.
 * The choice persists in localStorage and is applied on <html data-theme>; the
 * inline boot script in index.html applies it before first paint so there is
 * no white flash on a dark session.
 */
import { computed, ref } from 'vue'

const STORAGE_KEY = 'tunnelx.theme'
const root = document.documentElement

function initial() {
  const saved = localStorage.getItem(STORAGE_KEY)
  if (saved === 'light' || saved === 'dark') return saved
  return window.matchMedia('(prefers-color-scheme: light)').matches ? 'light' : 'dark'
}

const current = ref(initial())
root.setAttribute('data-theme', current.value)

/** Follow the OS only while the user has not chosen explicitly. */
const media = window.matchMedia('(prefers-color-scheme: light)')
media.addEventListener?.('change', (event) => {
  if (localStorage.getItem(STORAGE_KEY)) return
  current.value = event.matches ? 'light' : 'dark'
  root.setAttribute('data-theme', current.value)
})

function set(theme) {
  current.value = theme === 'light' ? 'light' : 'dark'
  root.setAttribute('data-theme', current.value)
  localStorage.setItem(STORAGE_KEY, current.value)
}

function toggle() {
  set(current.value === 'dark' ? 'light' : 'dark')
}

/**
 * Install as a Vue global property so templates can use `$theme` without an
 * import in every component.
 */
export const theme = {
  get current() {
    return current.value
  },
  isDark: computed(() => current.value === 'dark'),
  set,
  toggle
}

export function installTheme(app) {
  app.config.globalProperties.$theme = theme
  app.provide('theme', theme)
}
