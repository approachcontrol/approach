import { fileURLToPath } from 'node:url'

import { defineConfig } from 'vitest/config'

export default defineConfig({
  resolve: {
    alias: {
      'server-only': fileURLToPath(new URL('./src/test/server-only-stub.ts', import.meta.url)),
    },
  },
  test: {
    environment: 'node',
    // `.tsx` is included deliberately: with a `.ts`-only glob a component test
    // is collected by nobody and the suite still reports green.
    include: ['src/**/*.test.{ts,tsx}'],
  },
})
