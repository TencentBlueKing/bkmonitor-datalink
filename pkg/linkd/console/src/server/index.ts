import { formatBuildInfo, readBuildInfo } from "./version.js";

const args = process.argv.slice(2);
if (args.length === 1 && ["version", "--version"].includes(args[0])) {
  // 版本查询不读取连接配置、认证凭据，也不启动 HTTP 服务。
  process.stdout.write(formatBuildInfo(readBuildInfo()));
} else {
  const { createApp } = await import("./app.js");
  const { loadConfig } = await import("./config.js");
  const config = await loadConfig();
  const app = await createApp(config);

  const shutdown = async () => {
    await app.close();
    process.exitCode = 0;
  };

  process.once("SIGINT", () => void shutdown());
  process.once("SIGTERM", () => void shutdown());

  try {
    await app.listen({ host: config.server.host, port: config.server.port });
  } catch (error) {
    app.log.error({ err: error }, "Linkd Console failed to start");
    process.exitCode = 1;
  }
}
