import { createApp } from "./app.js";
import { loadConfig } from "./config.js";

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
  app.log.error({ err: error }, "Linkd DevTools failed to start");
  process.exitCode = 1;
}
