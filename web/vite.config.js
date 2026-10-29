import { defineConfig } from 'vite';
import { svelte } from '@sveltejs/vite-plugin-svelte';
import { fileURLToPath } from 'node:url';

// Dev: `npm run dev` proxies /api to a Box started with `go run ./cmd/nexovirt-box -dev -listen 127.0.0.1:8080`.
export default defineConfig({
  plugins: [svelte()],
  resolve: { alias: { $lib: fileURLToPath(new URL('./src/lib', import.meta.url)) } },
  build: {
    outDir: 'dist',
    emptyOutDir: true,
    assetsInlineLimit: 0,
    chunkSizeWarningLimit: 1500,
  },
  server: { proxy: { '/api': { target: 'http://127.0.0.1:8080', ws: true, changeOrigin: false } } },
});
