import { defineConfig } from 'vite';
import react from '@vitejs/plugin-react';
import tailwindcss from '@tailwindcss/vite';

const apiPort = Number(process.env.AGENTLAB_API_PORT || 8787);
const webPort = Number(process.env.AGENTLAB_WEB_PORT || 5173);
if (![apiPort, webPort].every((port) => Number.isInteger(port) && port > 0 && port <= 65535))
  throw new Error('Invalid local development port');

export default defineConfig({
  plugins: [
    react(),
    tailwindcss(),
    {
      name: 'development-csp',
      apply: 'serve',
      transformIndexHtml(html) {
        // React Fast Refresh injects a development-only inline preamble.
        return html.replace(
          "connect-src 'self'",
          `connect-src 'self' ws://127.0.0.1:${webPort} ws://localhost:${webPort}`,
        );
      },
    },
  ],
  base: './',
  preview: {
    proxy: {
      '/api': { target: `http://127.0.0.1:${apiPort}`, changeOrigin: true },
      '/v1': { target: `http://127.0.0.1:${apiPort}`, changeOrigin: true },
      '/health': { target: `http://127.0.0.1:${apiPort}`, changeOrigin: true },
    },
  },
  server: {
    port: webPort,
    strictPort: true,
    proxy: {
      '/api': { target: `http://127.0.0.1:${apiPort}`, changeOrigin: true },
      '/v1': { target: `http://127.0.0.1:${apiPort}`, changeOrigin: true },
      '/health': { target: `http://127.0.0.1:${apiPort}`, changeOrigin: true },
    },
  },
});
