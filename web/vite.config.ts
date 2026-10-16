import { defineConfig } from 'vite'
import { svelte } from '@sveltejs/vite-plugin-svelte'

export default defineConfig({
  plugins: [svelte()],
  server: {
    strictPort: true,
    proxy: {
      '/api': 'http://127.0.0.1:8080',
      '/healthz': 'http://127.0.0.1:8080',
      '/mcp': 'http://127.0.0.1:8080',
    },
  },
  build: {
    outDir: '../internal/webui/assets/dist',
    emptyOutDir: true,
  },
})
