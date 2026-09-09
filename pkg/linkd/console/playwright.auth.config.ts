import { defineConfig } from "@playwright/test";

const port = Number(process.env.LINKD_CONSOLE_AUTH_TEST_PORT ?? 14399);
if (!Number.isInteger(port) || port < 1 || port > 65535)
  throw new Error("invalid auth test port");
const baseURL = `http://127.0.0.1:${port}`;
export default defineConfig({
  testDir: "./tests/auth",
  use: { baseURL, channel: "chrome" },
  webServer: {
    command: "node dist-server/server/index.js",
    url: baseURL,
    // Playwright 将 401 视为服务器已启动；不复用其他进程。
    reuseExistingServer: false,
    env: {
      NODE_ENV: "production",
      LINKD_CONFIG: "tests/fixtures/server.yaml",
      LINKD_CONSOLE_MODE: "server",
      LINKD_CONSOLE_HOST: "127.0.0.1",
      LINKD_CONSOLE_PORT: String(port),
      LINKD_CONSOLE_BASIC_AUTH_ENABLED: "true",
      LINKD_CONSOLE_BASIC_AUTH_USERNAME: "browser-test",
      LINKD_CONSOLE_BASIC_AUTH_PASSWORD: "test-only-password",
      LINKD_CONSOLE_TIMEOUT_MILLISECONDS: "100",
    },
  },
});
