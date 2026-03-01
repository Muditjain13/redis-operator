import react from '@vitejs/plugin-react';
import { defineConfig } from 'vitest/config';

// `npm run dev` proxies the API to a locally running portal backend
// (go run ./cmd/dashboard), so the UI can be developed with hot reload.
export default defineConfig({
  plugins: [react()],
  server: {
    proxy: { '/api': 'http://localhost:8080' },
  },
  test: {
    environment: 'jsdom',
    setupFiles: ['./src/test/setup.ts'],
    css: false,
  },
});
