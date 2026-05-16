import { defineConfig } from 'vite';
import react from '@vitejs/plugin-react';

// Produces the single UMD bundle that go-iiif embeds at
// internal/serve/viewer/mirador.min.js. Mirador + the MAE annotation
// editor + our storage adapter are bundled together (React, MUI, etc.
// all inlined) so the iiifpreserve binary needs no Node and no second
// asset — exactly the existing vendoring model, just rebuilt from source
// with annotation authoring folded in. Mirrors the upstream Mirador
// vite-umd.config.js (UMD, global name "Mirador", named exports).
export default defineConfig({
  build: {
    emptyOutDir: false,
    lib: {
      entry: './src/index.js',
      fileName: () => 'mirador.min.js',
      formats: ['umd'],
      name: 'Mirador',
    },
    rollupOptions: {
      output: { assetFileNames: 'mirador.[ext]', exports: 'named' },
    },
    sourcemap: false,
  },
  define: { 'process.env': {} },
  plugins: [react()],
});
