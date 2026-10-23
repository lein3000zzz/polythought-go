import path from 'node:path'

import tailwindcss from '@tailwindcss/vite'
import react from '@vitejs/plugin-react'
import { defineConfig } from 'vitest/config'

// In development Vite proxies /api to the Go server, so the browser sees one origin, as it
// does behind Caddy in production. Start the server with
// POLYTHOUGHT_PUBLIC_URL=http://localhost:5173 so its origin check accepts this one.
const api = process.env.POLYTHOUGHT_API_URL ?? 'http://localhost:8080'

// The docs, from `make docs-dev`, under the same origin too.
const docs = process.env.POLYTHOUGHT_DOCS_URL ?? 'http://localhost:4321'

// The builder of workflow code lives beside the SDK, where the server embeds it (D74).
const sdk = path.resolve(import.meta.dirname, '../internal/adapters/tscompiler/sdk')

export default defineConfig({
  plugins: [react(), tailwindcss()],
  resolve: {
    alias: [
      { find: '@', replacement: path.resolve(import.meta.dirname, 'src') },
      // With ?raw too, for the editor's copy of it.
      { find: /^@polythought\/workflow(?=$|\?)/, replacement: path.join(sdk, 'workflow.ts') },
    ],
  },
  server: {
    proxy: { '/api': api, '/docs': docs, '/.well-known/oauth-': api },
    fs: { allow: ['.', sdk] },
  },
  // Monaco's workers are modules that import others.
  worker: { format: 'es' },
  build: {
    rolldownOptions: {
      output: {
        // React and the router change only on upgrades: in a chunk of their own, browsers
        // keep them cached across deploys of the app.
        codeSplitting: {
          groups: [
            {
              name: 'react',
              test: /node_modules[\\/](react|react-dom|scheduler|react-router)[\\/]/,
            },
          ],
        },
      },
    },
  },
  test: {
    include: ['src/**/*.test.{ts,tsx}'], // e2e/ is Playwright's
    environment: 'jsdom',
    setupFiles: ['src/test/setup.ts'],
    restoreMocks: true,
  },
})
