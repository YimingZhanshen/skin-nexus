import { defineConfig } from 'vite'
import vue from '@vitejs/plugin-vue'
import { resolve } from 'path'

// Dev proxy: Vite serves the SPA and forwards API calls to the Go server.
export default defineConfig({
  plugins: [vue()],
  server: {
    port: 5173,
    proxy: {
      '/api': { target: 'http://localhost:8080', changeOrigin: true },
      '/textures': { target: 'http://localhost:8080', changeOrigin: true },
    },
  },
  build: {
    outDir: 'dist',
    emptyOutDir: true,
    // Themes are loaded as a stylesheet AFTER the app bundle. CSS code
    // splitting injects lazy-page styles later, which then beat theme
    // overrides with equal specificity (azure-glass admin dock regressed to
    // a giant column). One eager CSS file keeps the documented order:
    // app base → theme pack → custom CSS.
    cssCodeSplit: false,
    rollupOptions: {
      input: {
        main: resolve(__dirname, 'index.html'),
        // Fixed-name entry for the server-rendered texture detail page
        // (texturepage.go references /assets/viewer.js?v=1 — a hashed name
        // would break that contract). skinview3d is code-split into its own
        // chunk so the main SPA bundle doesn't pay for it.
        viewer: resolve(__dirname, 'viewer-src.js'),
      },
      output: {
        entryFileNames: (chunk) =>
          chunk.name === 'viewer' ? 'assets/viewer.js' : 'assets/[name]-[hash].js',
        chunkFileNames: 'assets/[name]-[hash].js',
        assetFileNames: 'assets/[name]-[hash][extname]',
      },
    },
  },
})
