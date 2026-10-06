import react from "@vitejs/plugin-react";
import { defineConfig } from "vitest/config";

export default defineConfig({
  plugins: [react()],
  root: ".",
  build: {
    outDir: "dist",
    emptyOutDir: true,
    chunkSizeWarningLimit: 600,
  },
  server: {
    host: "127.0.0.1",
    port: 5173,
    proxy: {
      "/local-api": "http://127.0.0.1:4399",
    },
  },
  test: {
    environment: "jsdom",
    setupFiles: ["./src/web/test/setup.ts"],
    include: ["src/**/*.test.{ts,tsx}"],
  },
});
