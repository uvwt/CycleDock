import tailwindcss from '@tailwindcss/vite'
import react from '@vitejs/plugin-react'
import { defineConfig } from 'vite'

export default defineConfig({
  plugins: [react(), tailwindcss()],
  server: {
    host: '127.0.0.1',
    port: Number(process.env.DEV_WEB_PORT || 5173),
    strictPort: true,
    // 让 API、SSE 和授权回调共用浏览器入口，保持 Cookie 同源。
    proxy: Object.fromEntries(
      ['/api', '/auth', '/oauth', '/mcp', '/.well-known', '/healthz', '/readyz', '/docs/ai-connect', '/skills'].map(
        path => [path, { target: `http://${process.env.HTTP_ADDR || '127.0.0.1:8080'}` }],
      ),
    ),
  },
})
