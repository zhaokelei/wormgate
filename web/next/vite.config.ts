import { fileURLToPath, URL } from 'node:url'
import { defineConfig } from 'vite'
import react from '@vitejs/plugin-react'

/**
 * The React console is served by the same Go binary as the Vue one, mounted at
 * /next/ so both can run side by side while the rebuild is validated. `base`
 * must match that prefix or asset URLs 404 behind the SPA handler.
 */
export default defineConfig({
  base: '/next/',
  plugins: [react()],
  /**
   * `@common` points at the shared web/ folder so the React console can read the
   * same translation catalogue the two Vue consoles use — one glossary, one
   * behaviour, no second translation pass to keep in sync.
   */
  resolve: {
    alias: { '@common': fileURLToPath(new URL('../common', import.meta.url)) }
  },
  build: {
    outDir: '../../internal/webui/adminnext/dist',
    emptyOutDir: true,
    chunkSizeWarningLimit: 1400,
    rollupOptions: {
      output: {
        manualChunks: {
          react: ['react', 'react-dom', 'react-router'],
          antd: ['antd'],
          charts: ['uplot']
        }
      }
    }
  },
  server: {
    port: 5273,
    proxy: {
      '/api': { target: 'http://127.0.0.1:7100', changeOrigin: false }
    }
  }
})
