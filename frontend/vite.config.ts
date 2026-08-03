/// <reference types="vitest/config" />
import { defineConfig } from 'vite'
import react from '@vitejs/plugin-react'

// The frontend is a thin client: it calls the control-plane HTTP API (ADR-0001).
// In `wails dev` the desktop app injects the API base URL; in a browser it defaults
// to the daemon on 127.0.0.1:7373 (override with VITE_OSB_API).
export default defineConfig({
  plugins: [react()],
  server: { port: 5173 },
  build: { outDir: 'dist', emptyOutDir: true },
  // Component/unit tests run under jsdom via vitest (`npm test`). Tests live next to the code they cover
  // as `*.test.tsx`; shared setup + helpers are in src/test/.
  test: {
    globals: true,
    environment: 'jsdom',
    setupFiles: './src/test/setup.ts',
    css: false, // components style via className strings; skip CSS processing for speed
    include: ['src/**/*.test.{ts,tsx}'],
  },
})
