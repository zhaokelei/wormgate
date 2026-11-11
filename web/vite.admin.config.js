import { defineConfig } from 'vite'
import vue from '@vitejs/plugin-vue'
import { fileURLToPath, URL } from 'node:url'

// Admin console build: mounted under /admin/ by internal/server/admin.go, so
// `base` must match or asset URLs 404 behind the SPA handler.
export default defineConfig({
  root: fileURLToPath(new URL('./admin', import.meta.url)),
  plugins: [vue()],
  base: '/admin/',
  resolve: {
    alias: {
      '@': fileURLToPath(new URL('./admin/src', import.meta.url)),
      '@common': fileURLToPath(new URL('./common', import.meta.url))
    }
  },
  build: {
    outDir: fileURLToPath(new URL('../internal/webui/adminui/dist', import.meta.url)),
    emptyOutDir: true,
    target: 'es2020',
    cssCodeSplit: false,
    reportCompressedSize: false,
    chunkSizeWarningLimit: 700,
    rollupOptions: {
      output: {
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
    port: 5174,
    // Dev targets the running server admin console (default 127.0.0.1:7100).
    proxy: {
      '/api': {
        target: 'http://127.0.0.1:7100',
        changeOrigin: false,
        ws: true
      }
    }
  }
})
