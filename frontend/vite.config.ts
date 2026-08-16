import { defineConfig } from 'vite'
import react from '@vitejs/plugin-react'
import tailwindcss from '@tailwindcss/vite'

// https://vite.dev/config/
export default defineConfig({
  plugins: [react(), tailwindcss()],
  server: {
    proxy: {
      '/api': {
        // Overridable so a second stack can run alongside the one you already
        // have up, on its own ports, without editing this file.
        target: process.env.WOLLOW_API_TARGET || 'http://localhost:8080',
        changeOrigin: true,
        // Bulk actions are detached and return immediately, but a mailbox sync
        // over a large account legitimately holds a request open for a while.
        // The default 30s proxy timeout would report those as a socket hang up.
        timeout: 120_000,
        proxyTimeout: 120_000,
      },
    },
  },
})
