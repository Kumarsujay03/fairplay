import { defineConfig } from 'vite'
import preact from '@preact/preset-vite'

export default defineConfig({
  plugins: [preact()],
  build: {
    outDir: '../internal/webui/dist',
    emptyOutDir: true,
    target: 'es2020',
    assetsInlineLimit: 0,
    cssCodeSplit: false,
    modulePreload: { polyfill: false },
    reportCompressedSize: true,
  },
  server: {
    proxy: {
      '/api': 'http://127.0.0.1:8321',
      '/hls': 'http://127.0.0.1:8321',
      '/ws': { target: 'ws://127.0.0.1:8321', ws: true },
    },
  },
})
