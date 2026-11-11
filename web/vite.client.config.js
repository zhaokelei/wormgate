import { defineConfig } from 'vite'
import vue from '@vitejs/plugin-vue'
import { fileURLToPath, URL } from 'node:url'

// Client console build: served from the root of the embedded Go HTTP handler
// (internal/webui/clientui), so `base` must stay '/'.
export default defineConfig({
  root: fileURLToPath(new URL('./client', import.meta.url)),
  plugins: [vue()],
  base: '/',
  resolve: {
    alias: {
      '@': fileURLToPath(new URL('./client/src', import.meta.url)),
      '@common': fileURLToPath(new URL('./common', import.meta.url))
    }
  },
  build: {
    outDir: fileURLToPath(new URL('../internal/webui/clientui/dist', import.meta.url)),
    emptyOutDir: true,
    target: 'es2020',
    cssCodeSplit: false,
    reportCompressedSize: false,
    chunkSizeWarningLimit: 700,
    rollupOptions: {
      output: {
        // Keep the chart vendor in its own chunk: it is the only heavy
        // dependency and a cached copy survives app deploys.
        manualChunks(id) {
          if (id.includes('node_modules')) {
            if (id.includes('echarts') || id.includes('zrender')) return 'vendor-charts'
            if (id.includes('vue')) return 'vendor-vue'
            return 'vendor'
          }
          return undefined
        }
      }
    }
  },
  server: {
    port: 5173,
    // Dev targets the running client console (default 127.0.0.1:7200).
    proxy: {
      '/api': {
        target: 'http://127.0.0.1:7200',
        changeOrigin: false,
        ws: true
      }
    }
  }
})
