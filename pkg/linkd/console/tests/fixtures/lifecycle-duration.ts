// 固定测试输入，与生产元数据独立维护；所有子步骤按同一次 Event 的样本贡献记账。
export const detailFixture: Record<string, Record<string, number>> = {
  enrich: {
    strategy: 20,
    resource: 10,
    display: 0,
    metric: 0,
    source: 0,
    log: 0,
    k8s: 0,
    apm: 0,
    test: 0,
    custom: 0,
    "cmdb-access": 0,
    cloud_resource: 0,
    chain: 0,
    persist: 10,
    other: 0,
  },
  policy_snapshot: { read: 5, other: 0 },
  plan: {
    active_lookup: 2,
    content: 3,
    suppression: 0,
    admission: 0,
    merge: 0,
    pending_changes: 0,
    other: 5,
  },
  event_commit: {
    policy_save: 3,
    plan_save: 7,
    result_save: 5,
    plan_clear: 0,
    other: 0,
  },
  apply: {
    alert_read: 4,
    alert_create: 1,
    alert_cas: 10,
    action: 1,
    bindings: 1,
    cache: 1,
    logs: 2,
    other: 0,
  },
  final_hook: { projection: 8, action: 0, custom: 0, other: 0 },
  other: { event_reload: 0, other: 2 },
};
export function detailPanels(timestamp: number, batch = 5, queue = 1) {
  return [
    {
      id: "lifecycle-phase-step-average",
      title: "子步骤",
      unit: "ms",
      kind: "stat" as const,
      status: "available" as const,
      series: Object.entries(detailFixture).flatMap(([phase, steps]) =>
        Object.entries(steps).flatMap(([step, execution], i) =>
          ["execution", "batch_wait", "storage_queue"].map((kind) => ({
            name: `${phase} ${step} ${kind}`,
            labels: {
              linkd_phase: phase,
              linkd_step: step,
              linkd_wait_kind: kind,
            },
            points: [
              [
                timestamp,
                kind === "execution"
                  ? execution
                  : i === 0
                    ? kind === "batch_wait"
                      ? batch
                      : queue
                    : 0,
              ] as [number, number],
            ],
          })),
        ),
      ),
    },
    {
      id: "lifecycle-phase-step-coverage",
      title: "样本覆盖",
      unit: "ratio",
      kind: "stat" as const,
      status: "available" as const,
      series: Object.keys(detailFixture).map((phase) => ({
        name: phase,
        labels: { linkd_phase: phase },
        points: [[timestamp, 1] as [number, number]],
      })),
    },
  ];
}
