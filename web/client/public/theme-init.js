/**
 * Pre-paint theme boot.
 *
 * Served as a real file rather than an inline <script> because the console sets
 * `Content-Security-Policy: script-src 'self'`, which silently blocked the
 * inline version — the saved light theme then only applied after the bundle
 * ran, i.e. one dark frame first.
 *
 * Mirrors common/theme.js: same storage key and same default rules.
 */
;(function () {
  try {
    var saved = localStorage.getItem('wormgate.theme') || localStorage.getItem('tunnelx.theme')
    var theme =
      saved === 'light' || saved === 'dark'
        ? saved
        : window.matchMedia('(prefers-color-scheme: light)').matches
          ? 'light'
          : 'dark'
    document.documentElement.setAttribute('data-theme', theme)
  } catch (e) {
    /* storage disabled: keep the dark default from <html data-theme> */
  }
})()
