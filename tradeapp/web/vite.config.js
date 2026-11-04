import { defineConfig } from 'vite'
import { svelte } from '@sveltejs/vite-plugin-svelte'
import tailwindcss from '@tailwindcss/vite'
import { fileURLToPath } from 'node:url'

// The API runs on :8091. Proxying /api keeps the browser on one origin
// (no CORS) and passes the SSE streams straight through.
export default defineConfig({
  plugins: [tailwindcss(), svelte()],
  resolve: {
    alias: { $lib: fileURLToPath(new URL('./src/lib', import.meta.url)) },
  },
  server: {
    port: 5173,
    proxy: {
      '/api': { target: 'http://localhost:8091', changeOrigin: true },
    },
  },
})
