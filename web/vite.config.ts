import path from 'node:path'

import tailwindcss from '@tailwindcss/vite'
import react from '@vitejs/plugin-react'
import { defineConfig } from 'vitest/config'

// In development Vite proxies /api to the Go server, so the browser sees one origin, as it
// does behind Caddy in production. Start the server with
// POLYTHOUGHT_PUBLIC_URL=http://localhost:5173 so its origin check accepts this one.
const api = process.env.POLYTHOUGHT_API_URL ?? 'http://localhost:8080'

export default defineConfig({
  plugins: [react(), tailwindcss()],
  resolve: {
    alias: { '@': path.resolve(import.meta.dirname, 'src') },
  },
  server: {
    proxy: { '/api': api },
  },
  test: {
    environment: 'jsdom',
    setupFiles: ['src/test/setup.ts'],
    restoreMocks: true,
  },
})
