import type {
  ControlPlaneRuntime,
  ControlPlaneTaskDefinition,
} from "../shared/contracts.js";
export function controlPlaneFixture(): ControlPlaneRuntime {
  const at = "2026-09-24T08:25:00Z";
  const definitions = [
    ["scheduler", "调度协调", "调度与来源", "periodic"],
    ["source-providers", "来源 Provider 同步", "调度与来源", "periodic"],
    [
      "elasticsearch-schema-and-active-reconciler",
      "Schema & Active",
      "消息与存储维护",
      "periodic",
    ],
    [
      "elasticsearch-bucket-manager",
      "Bucket Manager",
      "消息与存储维护",
      "periodic",
    ],
    [
      "elasticsearch-alert-archiver",
      "Alert Archiver",
      "消息与存储维护",
      "continuous",
    ],
    [
      "redis-stream-manager",
      "Redis Stream Manager",
      "消息与存储维护",
      "periodic",
    ],
    ["active-alert-indexes", "策略活跃索引维护", "投影与配置", "periodic"],
    ["dynamic-config", "动态配置同步", "投影与配置", "notification"],
  ];
  const tasks: ControlPlaneTaskDefinition[] = definitions.map(
    ([id, name, group, kind]) => ({
      id,
      name,
      group,
      kind,
      description: `${name}：由控制面直接记录实际轮次和处理结果。`,
      enabled: id !== "source-providers",
      disabledReason:
        id === "source-providers" ? "未注入来源 Provider" : undefined,
      intervalSeconds: 10,
      deadlineSeconds: 3,
      configSource: "default",
      dependsOn: [],
      settings: { batchSize: 16 },
      active: id !== "source-providers",
      state:
        id === "source-providers"
          ? "disabled"
          : id === "active-alert-indexes"
            ? "idle"
            : "healthy",
      execution: {
        id,
        name,
        outcome: "succeeded",
        running: false,
        startedAt: at,
        finishedAt: at,
        lastSuccess: at,
        durationSeconds: 0.025,
        succeeded: 120,
        failed: 0,
        canceled: 0,
        work: 1,
        failures: 0,
      },
      steps: [],
      detailsTruncated: false,
    }),
  );
  tasks[0].steps = [
    { ...tasks[0].execution, id: "assignment", name: "任务分配", work: 12 },
  ];
  return {
    status: "available",
    snapshotAt: at,
    startedAt: "2026-09-24T00:00:00Z",
    owner: "control-plane-a",
    tasks,
    services: [
      { ...tasks[0], id: "source-api", name: "管理 API", kind: "service" },
    ],
    processes: {
      status: "available",
      items: [
        {
          instance: "control-plane-a:9464",
          job: "linkd",
          serviceInstanceId: "control-plane-a",
          role: "control-plane",
          version: "test",
          up: true,
        },
      ],
    },
    metrics: {
      status: "available",
      series: {
        active: tasks
          .filter((t) => t.enabled)
          .map((t) => ({
            labels: { linkd_task: t.id, instance: "control-plane-a:9464" },
            value: 1,
            timestamp: Date.parse(at) / 1000,
          })),
        runCount: [],
      },
    },
    archive: { status: "available", backlog: 2450 },
  };
}
