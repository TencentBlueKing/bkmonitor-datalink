import { defineConfig } from "@playwright/test";
if (
  !process.env.LINKD_CONSOLE_DELIVERY_FIXTURE ||
  !process.env.LINKD_CONSOLE_REAL_OUTPUT
)
  throw new Error(
    "Run TestAllInOneKACDeliveryE2E with LINKD_E2E_CONSOLE_DELIVERY=1",
  );
export default defineConfig({
  testDir: "./tests/real",
  testMatch: "delivery.spec.ts",
  timeout: 180000,
  expect: { timeout: 20000 },
  workers: 1,
  retries: 0,
  reporter: "list",
  outputDir: process.env.LINKD_CONSOLE_REAL_OUTPUT,
  use: {
    channel: "chrome",
    viewport: { width: 1440, height: 1050 },
    screenshot: "only-on-failure",
  },
});
