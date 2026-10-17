import { fileURLToPath, URL } from 'node:url'
import vue from '@vitejs/plugin-vue'
import { defineConfig } from 'vitest/config'

// `--mode demo` builds the presentation version (mocked Telegram and API, see
// src/demo). VITE_DEMO is defined as a literal so production builds drop the
// demo import entirely.
export default defineConfig(({ mode }) => ({
  // Served by Caddy at https://APP_DOMAIN/driver/ (ARCHITECTURE §1).
  base: '/driver/',
  plugins: [vue()],
  resolve: {
    alias: { '@': fileURLToPath(new URL('./src', import.meta.url)) },
  },
  define: {
    'import.meta.env.VITE_DEMO': JSON.stringify(mode === 'demo' ? '1' : ''),
    __VUE_OPTIONS_API__: 'false',
    __VUE_PROD_DEVTOOLS__: 'false',
    __VUE_PROD_HYDRATION_MISMATCH_DETAILS__: 'false',
    __VUE_I18N_FULL_INSTALL__: 'false',
    __VUE_I18N_LEGACY_API__: 'false',
    __INTLIFY_PROD_DEVTOOLS__: 'false',
  },
  server: {
    port: 5174,
    strictPort: true,
    proxy: {
      '/api': { target: 'http://localhost:8080', changeOrigin: true, ws: true },
    },
  },
  preview: { port: 4174, strictPort: true },
  build: {
    // WebGL2 (required by MapLibre 6) means iOS 15+ / current Android WebView.
    target: ['es2022', 'safari15', 'chrome100'],
    sourcemap: false,
    // maplibre-gl alone is ~1 MB minified; it ships in the lazily loaded map chunk.
    chunkSizeWarningLimit: 1200,
  },
  test: {
    environment: 'jsdom',
    include: ['src/**/*.test.ts'],
    restoreMocks: true,
  },
}))
