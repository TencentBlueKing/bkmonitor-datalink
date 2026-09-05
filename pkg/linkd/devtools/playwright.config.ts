import { defineConfig } from "@playwright/test";

const port = Number(process.env.LINKD_DEVTOOLS_E2E_PORT ?? 5173);
if (!Number.isInteger(port) || port < 1 || port > 65535)
  throw new Error("invalid LINKD_DEVTOOLS_E2E_PORT");
const baseURL = `http://127.0.0.1:${port}`;

export default defineConfig({
  testDir: "./tests/e2e",
  use: { baseURL, channel: "chrome" },
  webServer: {
    command: `pnpm exec vite --port ${port} --strictPort`,
    url: baseURL,
    reuseExistingServer: !process.env.LINKD_DEVTOOLS_E2E_PORT,
  },
});
