import type { MetricPanel } from "../../shared/contracts";

// 与 Worker 的固定子步骤词表对应，不将动态 processor / Hook 身份变成指标标签。
const steps: Record<string, Record<string, string>> = {
  enrich: {
    strategy: "策略 processor",
    resource: "资源 processor",
    display: "展示 processor",
    metric: "指标 processor",
    source: "来源 processor",
    log: "日志 processor",
    k8s: "K8s processor",
    apm: "APM processor",
    test: "测试 processor",
    custom: "自定义 processor（合计）",
    "cmdb-access": "CMDB 权限 processor",
    cloud_resource: "云资源 processor",
    chain: "链编排 / 补丁校验与编码",
    persist: "丰富结果校验与保存",
    other: "输入准备 / 诊断与其他",
  },
  policy_snapshot: { read: "读取策略快照", other: "快照校验与编排" },
  plan: {
    active_lookup: "查找活动告警（缓存 / ES）",
    content: "生成新告警内容",
    suppression: "抑制与候选选择",
    admission: "屏蔽与准入裁决",
    merge: "合并入窗裁决",
    pending_changes: "补齐历史待提交变更",
    other: "状态裁决 / 计划构造与校验",
  },
  event_commit: {
    policy_save: "保存策略快照",
    plan_save: "保存执行计划",
    result_save: "提交最终处理结果",
    plan_clear: "冲突后撤销计划",
    other: "其他 Event 提交",
  },
  apply: {
    alert_read: "读取当前告警",
    alert_create: "创建告警（校验 / 编码 / ES）",
    alert_cas: "条件更新告警（校验 / 编码 / ES）",
    action: "动作意图投递与编排",
    bindings: "抑制 / 合并 / 依赖绑定与清理",
    cache: "更新最近告警缓存",
    logs: "告警流水校验与落库",
    other: "计划校验 / 状态比对与其他",
  },
  final_hook: {
    projection: "投影 Hook",
    action: "动作 Hook",
    custom: "其他 Hook（合计）",
    other: "Hook 编排",
  },
  other: { event_reload: "CAS 重试读取 Event", other: "输入校验与重试编排" },
};
const kinds = ["execution", "batch_wait", "storage_queue"] as const;
function value(
  panel: MetricPanel | undefined,
  labels: Record<string, string>,
  timestamp: number | undefined,
): number | undefined {
  if (panel?.status !== "available") return;
  const series = panel.series.filter((s) =>
    Object.entries(labels).every(([k, v]) => s.labels[k] === v),
  );
  if (series.length !== 1) return;
  const point = series[0].points.at(-1);
  if (
    !point ||
    point[0] !== timestamp ||
    typeof point[1] !== "number" ||
    !Number.isFinite(point[1]) ||
    point[1] < 0
  )
    return;
  return point[1];
}
function duration(ms: number) {
  return ms >= 1000 ? `${(ms / 1000).toFixed(2)} s` : `${ms.toFixed(2)} ms`;
}

export function LifecyclePhaseDetails({
  panels,
  phase,
  label,
  color,
  timestamp,
  execution,
  batch,
  queue,
  ignoreBatch,
}: {
  panels: MetricPanel[];
  phase: string;
  label: string;
  color: string;
  timestamp: number | undefined;
  execution: number;
  batch: number;
  queue: number;
  ignoreBatch: boolean;
}) {
  const panel = panels.find((p) => p.id === "lifecycle-phase-step-average");
  const coverage = value(
    panels.find((p) => p.id === "lifecycle-phase-step-coverage"),
    { linkd_phase: phase },
    timestamp,
  );
  const rows = Object.entries(steps[phase]).map(([step, name]) => {
    const parts = kinds.map((kind) =>
      value(
        panel,
        { linkd_phase: phase, linkd_step: step, linkd_wait_kind: kind },
        timestamp,
      ),
    );
    return {
      step,
      name,
      parts,
      shown:
        (parts[0] ?? 0) + (parts[2] ?? 0) + (ignoreBatch ? 0 : (parts[1] ?? 0)),
    };
  });
  const parents = [execution, batch, queue];
  const complete =
    coverage !== undefined &&
    Math.abs(coverage - 1) < 0.000001 &&
    rows.every((r) => r.parts.every((v) => v !== undefined)) &&
    kinds.every((_, i) => {
      const sum = rows.reduce((s, r) => s + (r.parts[i] ?? 0), 0);
      return (
        Math.abs(sum - parents[i]) <= Math.max(0.000001, parents[i] * 0.000001)
      );
    });
  const total = execution + queue + (ignoreBatch ? 0 : batch);
  return (
    <section
      className="lifecycle-phase-details"
      aria-label={`${label}耗时明细`}
    >
      <div className="lifecycle-phase-details-heading">
        <h3>{label} · 耗时明细</h3>
        <span>{ignoreBatch ? "已扣除合批等待" : "含合批等待"}</span>
      </div>
      <p>
        与大阶段使用相同的 Event
        尝试分母；同一步骤多次执行会累加，未执行记零。子步骤互不重叠。
      </p>
      {complete ? (
        <>
          {rows
            .filter((r) => r.shown > 0)
            .sort((a, b) => b.shown - a.shown)
            .map((row) => (
              <div className="lifecycle-step-row" key={row.step}>
                <div className="lifecycle-step-title">
                  <b>{row.name}</b>
                  <span>
                    <span>{duration(row.shown)}</span>{" "}
                    <strong>
                      {(total > 0 ? (row.shown / total) * 100 : 0).toFixed(1)}%
                    </strong>
                  </span>
                </div>
                <div className="lifecycle-step-track">
                  <span
                    style={{
                      width: `${total > 0 ? (row.shown / total) * 100 : 0}%`,
                      backgroundColor: color,
                    }}
                  />
                </div>
                <div className="lifecycle-step-parts">
                  <span>执行 {duration(row.parts[0] ?? 0)}</span>
                  <span className={ignoreBatch ? "ignored" : undefined}>
                    合批 {duration(row.parts[1] ?? 0)}
                    {ignoreBatch ? "（已扣除）" : ""}
                  </span>
                  <span>排队 {duration(row.parts[2] ?? 0)}</span>
                </div>
              </div>
            ))}
          {total === 0 && <p>本阶段未执行，或当前口径耗时为零。</p>}
        </>
      ) : (
        <p role="status">
          暂无完整子步骤样本。请在 Worker
          上报明细后选择完整覆盖的时间范围；大阶段数据仍可查看。
        </p>
      )}
    </section>
  );
}
