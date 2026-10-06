const path = require("node:path");

const root = __dirname;
const linkdConfig = path.join(root, "configs", "linkd.pm2.yaml");
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
    {
      ...processDefaults,
      name: "linkd-eventgen-infra",
      script: eventgenBinary,
      args: [
        "--config",
        linkdConfig,
        "--event-source-id",
        "standard-infra",
        "--new-alerts-per-minute",
        "20",
        "--cycle-duration",
        "30s",
        "--mean-lifetime-cycles",
        "4",
        "--duplicate-percent",
        "20",
        "--scenarios",
        "cpu_high,memory_high,disk_full,disk_read_only,disk_io_latency_high,oom_killed,host_unreachable,network_packet_loss_high",
        "--seed",
        "1001",
        "--max-active-alerts",
        "10000",
      ],
      max_memory_restart: "256M",
    },
    {
      ...processDefaults,
      name: "linkd-eventgen-service",
      script: eventgenBinary,
      args: [
        "--config",
        linkdConfig,
        "--event-source-id",
        "standard-service",
        "--new-alerts-per-minute",
        "60",
        "--cycle-duration",
        "15s",
        "--mean-lifetime-cycles",
        "8",
        "--duplicate-percent",
        "20",
        "--scenarios",
        "process_down,service_unavailable,http_error_rate_high,database_connections_high,online_users_zero,queue_backlog_high",
        "--seed",
        "2002",
        "--max-active-alerts",
        "20000",
      ],
      max_memory_restart: "256M",
    },
  ],
};
