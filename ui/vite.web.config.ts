import { resolve } from 'node:path'
import { defineConfig } from 'vite'
import react from '@vitejs/plugin-react'

// Browser build served by `harness-core serve`: out/web/index.html + hashed assets.
// The server sets the Content-Security-Policy header; web.html carries none.
export default defineConfig({
  root: resolve('src/renderer'),
  base: '/',
  plugins: [
    react(),
    {
      // Emit the entry as index.html so the server can serve it at /.
      name: 'web-index',
      enforce: 'post',
      generateBundle(_o, bundle) {
        const h = bundle['web.html']
        if (h) {
          h.fileName = 'index.html'
        }
      }
    }
  ],
  build: {
    outDir: resolve('out/web'),
    emptyOutDir: true,
    rollupOptions: { input: resolve('src/renderer/web.html') }
  }
})
