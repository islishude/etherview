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
  server: {
    proxy: {
      "/api": {
        target: "http://localhost:8080",
        changeOrigin: false,
      },
    },
  },
  preview: {
    proxy: {
      "/api": {
        target: "http://localhost:8080",
        changeOrigin: false,
      },
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
              name: 'codemirror-vendor',
              test:
                /node_modules[\\/](?:@codemirror|@lezer|@replit[\\/]codemirror-lang-solidity)(?:[\\/]|$)/,
              priority: 45,
            },

            {
              name: 'echarts-vendor',
              test:
                /node_modules[\\/](?:echarts|zrender)(?:[\\/]|$)/,
              priority: 40,
            },

            {
              name: 'web3-vendor',
              test:
                /node_modules[\\/](?:viem|ox|abitype|@noble|@scure)(?:[\\/]|$)/,
              priority: 40,
            },

            {
              name: 'x402-vendor',
              test: /node_modules[\\/]@x402[\\/]/,
              priority: 45,
              includeDependenciesRecursively: false,
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
