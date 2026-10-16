import { fileURLToPath, URL } from 'node:url'
import vue from '@vitejs/plugin-vue'
import { defineConfig } from 'vitest/config'

export default defineConfig({
  // Served by Caddy at https://ADMIN_DOMAIN/ (ARCHITECTURE §1).
  base: '/',
  plugins: [vue()],
  resolve: {
    alias: { '@': fileURLToPath(new URL('./src', import.meta.url)) },
  },
  define: {
    __VUE_OPTIONS_API__: 'false',
    __VUE_PROD_DEVTOOLS__: 'false',
    __VUE_PROD_HYDRATION_MISMATCH_DETAILS__: 'false',
    __VUE_I18N_FULL_INSTALL__: 'false',
    __VUE_I18N_LEGACY_API__: 'false',
    __INTLIFY_PROD_DEVTOOLS__: 'false',
  },
  server: {
    port: 5175,
    strictPort: true,
    proxy: {
      // The admin session cookie is Path=/api; the dev proxy keeps it same-origin.
      // JT_ADMIN_API_PROXY points the dev server at another local API instance.
      '/api': { target: process.env.JT_ADMIN_API_PROXY ?? 'http://localhost:8080', changeOrigin: true, ws: true },
    },
  },
  preview: { port: 4175, strictPort: true },
  build: {
    target: ['es2022', 'safari16', 'chrome110', 'firefox115'],
    sourcemap: false,
    // MapLibre GL alone is about 1 MB minified; it is a lazy chunk loaded only by map screens.
    chunkSizeWarningLimit: 1100,
  },
  test: {
    environment: 'jsdom',
    include: ['src/**/*.test.ts'],
    restoreMocks: true,
  },
})
