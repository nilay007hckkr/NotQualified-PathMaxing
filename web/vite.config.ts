import react from '@vitejs/plugin-react';
import { defineConfig } from 'vitest/config';

// The Go server serves the built app same-origin under /app/, so the API
// needs no CORS. In development the Vite server proxies /api to Go; add
// http://localhost:5173 to ALLOWED_ORIGINS so state-changing requests pass
// the server's Origin check.
export default defineConfig({
  base: '/app/',
  plugins: [react()],
  server: {
    port: 5173,
    proxy: {
      '/api': 'http://localhost:8080',
    },
  },
  build: {
    outDir: 'dist',
    sourcemap: true,
    rollupOptions: {
      output: {
        // Charts are the bulk of the bundle and change rarely; separate
        // chunks stay cached across app deploys.
        manualChunks: { charts: ['recharts'], vendor: ['react', 'react-dom', 'react-router', '@tanstack/react-query'] },
      },
    },
  },
  test: {
    environment: 'jsdom',
    setupFiles: ['./src/test/setup.ts'],
    restoreMocks: true,
  },
});
