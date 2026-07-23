import { fileURLToPath, URL } from "node:url";

import tailwindcss from "@tailwindcss/vite";
import react from "@vitejs/plugin-react";
import { defineConfig } from "vitest/config";

const KiB = 1024

export default defineConfig({
  plugins: [tailwindcss(), react()],
  resolve: {
    alias: {
      "@": fileURLToPath(new URL("./src", import.meta.url)),
    },
  },
  build: {
    outDir: "dist",
    emptyOutDir: true,
    assetsInlineLimit: 0,
    sourcemap: false,
    target: "es2024",
    rollupOptions: {
      output: {
        assetFileNames: "assets/[name]-[hash][extname]",
        chunkFileNames: "assets/[name]-[hash].js",
        entryFileNames: "assets/[name]-[hash].js",
      },
    },
    rolldownOptions: {
      output: {
        codeSplitting: {
          includeDependenciesRecursively: true,
          groups: [
            {
              name: 'react-vendor',
              test:
                /node_modules[\\/](?:react|react-dom|scheduler)(?:[\\/]|$)/,
              priority: 50,
            },

            {
              name: 'tanstack-vendor',
              test: /node_modules[\\/]@tanstack[\\/]/,
              priority: 40,
            },

            {
              name: 'echarts-vendor',
              test:
                /node_modules[\\/](?:echarts|zrender)(?:[\\/]|$)/,
              priority: 40,

              maxSize: 350 * KiB,
            },

            {
              name: 'web3-vendor',
              test:
                /node_modules[\\/](?:viem|ox|abitype|@noble|@scure)(?:[\\/]|$)/,
              priority: 40,
              maxSize: 350 * KiB,
            },

            {
              name: 'support-vendor',
              test:
                /node_modules[\\/](?:i18next|react-i18next|openapi-fetch)(?:[\\/]|$)/,
              priority: 30,
            },

            {
              name: 'vendor',
              test: /node_modules/,
              priority: 10,
              minSize: 20 * KiB,
              maxSize: 350 * KiB,
            },
          ],
        },
      },
    },
  },
  test: {
    environment: "jsdom",
    include: ["src/**/*.test.{ts,tsx}"],
    setupFiles: ["./src/test/setup.ts"],
    restoreMocks: true,
    clearMocks: true,
  },
});
