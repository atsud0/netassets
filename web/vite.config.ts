import { defineConfig } from 'vitest/config'
import vue from '@vitejs/plugin-vue'
import Components from 'unplugin-vue-components/vite'
import { ElementPlusResolver } from 'unplugin-vue-components/resolvers'

export default defineConfig({
  plugins: [
    vue(),
    Components({
      dts: false,
      resolvers: [ElementPlusResolver({ importStyle: false })],
    }),
  ],
  build: {
    // Avoid colliding with the Vue business route /assets in the Go static server.
    assetsDir: '_assets',
    rollupOptions: {
      output: {
        manualChunks(id) {
          if (id.indexOf('node_modules') === -1) return undefined
          if (
            id.indexOf('/vue/') !== -1 ||
            id.indexOf('/vue-router/') !== -1 ||
            id.indexOf('/pinia/') !== -1
          ) {
            return 'vue'
          }
          if (id.indexOf('/axios/') !== -1) return 'axios'
          return undefined
        },
      },
    },
  },
  server: {
    port: 5173,
    proxy: {
      '/api': 'http://127.0.0.1:8080',
      '/health': 'http://127.0.0.1:8080',
    },
  },
  test: {
    environment: 'jsdom',
  },
})
