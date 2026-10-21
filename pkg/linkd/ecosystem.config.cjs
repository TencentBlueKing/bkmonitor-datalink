const path = require("node:path");

const root = __dirname;
const configuredLinkdConfig = process.env.LINKD_CONFIG?.trim();
const linkdConfig = configuredLinkdConfig
  ? path.resolve(root, configuredLinkdConfig)
  : path.join(root, "configs", "linkd.pm2.yaml");
const linkdBinary = path.join(root, "bin", "linkd");
const eventgenBinary = path.join(root, "bin", "linkd-eventgen");

const processDefaults = {
  cwd: root,
  interpreter: "none",
  exec_mode: "fork",
  instances: 1,
  autorestart: true,
  restart_delay: 3000,
  max_restarts: 20,
  min_uptime: "10s",
  kill_timeout: 45000,
  time: true,
  env: {
    TZ: "Asia/Shanghai",
  },
};

module.exports = {
  apps: [
    {
      ...processDefaults,
      name: "linkd-all-in-one",
      script: linkdBinary,
      args: ["run", "all-in-one", "--config", linkdConfig],
      max_memory_restart: "768M",
    },
    ...(process.env.LINKD_EVENTGEN_ENABLED === "true" ? [{
      ...processDefaults,
      name: "linkd-eventgen",
      script: eventgenBinary,
      args: ["serve", "--control-plane", process.env.LINKD_CONTROL_PLANE_URL || "http://127.0.0.1:8090"],
      // Worker Token 从 LINKD_EVENTGEN_WORKER_TOKEN 环境变量读取；启动后等待 Admin 下发任务。
      kill_timeout: 60000,
      max_memory_restart: "2G",
    }] : []),
  ],
};
