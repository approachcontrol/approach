import { fileURLToPath } from 'node:url'

import { defineConfig } from 'vitest/config'

export default defineConfig({
  resolve: {
    alias: {
      'server-only': fileURLToPath(new URL('./src/test/server-only-stub.ts', import.meta.url)),
      // The same `@/*` path mapping tsconfig gives the app. Without it a
      // component that imports the way the pages do is untestable, and the
      // alternative — relative imports in components only — is a rule nobody
      // would remember.
      '@': fileURLToPath(new URL('./src', import.meta.url)),
    },
  },
  test: {
    environment: 'node',
    // `.tsx` is included deliberately: with a `.ts`-only glob a component test
    // is collected by nobody and the suite still reports green.
    include: ['src/**/*.test.{ts,tsx}'],
  },
})
