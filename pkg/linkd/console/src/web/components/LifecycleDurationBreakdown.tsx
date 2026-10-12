import { useState } from "react";
import type { MetricPanel } from "../../shared/contracts";
import "./lifecycle-duration.css";

const phases = [
  {
    id: "enrich",
    label: "Event 丰富",
    color: "#568df2",
    detail: "执行丰富并保存丰富结果。",
  },
  {
    id: "policy_snapshot",
    label: "策略快照",
    color: "#ad83eb",
    detail: "读取并冻结本次 Event 使用的策略配置。",
  },
  {
    id: "plan",
    label: "生命周期裁决",
    color: "#e9b43c",
    detail: "查找当前告警，计算抑制、屏蔽、合并及状态变更计划。",
  },
  {
    id: "event_commit",
    label: "计划与结果写入",
    color: "#4db9cf",
    detail: "保存 Event 执行计划和最终处理结果，包含写入等待。",
  },
  {
    id: "apply",
    label: "告警变更与落库",
    color: "#82bd46",
    detail:
      "执行计划、保存告警与流水、更新缓存和处置意图；扣除 Event 写入及 Hook 耗时。",
  },
  {
    id: "final_hook",
    label: "Hook 输出",
    color: "#e578ad",
    detail: "按顺序调用 Hook 并构造输出流水；从父环节中扣除，避免重复计算。",
  },
  {
    id: "other",
    label: "其他处理",
    color: "#929da9",
    detail: "输入校验、CAS 冲突重读、重试编排等未归入上述环节的处理。",
  },
] as const;

export function LifecycleDurationBreakdown({
  panels,
  loading,
  error,
}: {
  panels: MetricPanel[];
  loading: boolean;
  error: boolean;
}) {
  const [selected, setSelected] = useState<string>();
  const panel = panels.find((p) => p.id === "lifecycle-phase-average");
  // 后端先按耗时和次数跨 Worker 加权；前端不平均 Worker 均值，也不回退到历史末值。
  const values = phases.map((phase) => {
    const series =
      panel?.series.filter((s) => s.labels.linkd_phase === phase.id) ?? [];
    const point = series.length === 1 ? series[0].points.at(-1) : undefined;
    return { ...phase, value: point?.[1], timestamp: point?.[0] };
  });
  const complete =
    panel?.status === "available" &&
    values.every(
      (v) =>
        typeof v.value === "number" &&
        Number.isFinite(v.value) &&
        v.value >= 0 &&
        v.timestamp === values[0].timestamp,
    );
  const total = complete
    ? values.reduce((sum, v) => sum + (v.value ?? 0), 0)
    : 0;
  const available = complete && Number.isFinite(total) && total > 0;
  const largest = values.reduce((a, b) =>
    (b.value ?? 0) > (a.value ?? 0) ? b : a,
  );
  const active = values.find((v) => v.id === selected) ?? largest;
  const percent = (value: number) => `${((value / total) * 100).toFixed(1)}%`;

  return (
    <section
      className="lifecycle-duration-panel"
      aria-label="单 Event 平均处理耗时分布"
      aria-busy={loading}
    >
      <header>
        <div>
          <p className="eyebrow">EVENT · TIME BREAKDOWN</p>
          <h2>单 Event 平均处理耗时分布</h2>
          <p>
            所选时间范围内的每次处理尝试均值，包含失败与重放。各环节独占计时，占比合计
            100%。
          </p>
        </div>
      </header>
      {available ? (
        <>
          <div className="lifecycle-duration-summary">
            <div>
              <span>平均总耗时</span>
              <strong>{duration(total)}</strong>
            </div>
            <p>
              耗时最多 <b>{largest.label}</b>
              <strong>{percent(largest.value ?? 0)}</strong>
            </p>
          </div>
          <div
            className="lifecycle-duration-bar"
            role="group"
            aria-label="各环节耗时占比"
          >
            {values
              .filter((v) => (v.value ?? 0) > 0)
              .map((v) => (
                <button
                  key={v.id}
                  type="button"
                  style={{
                    width: `${((v.value ?? 0) / total) * 100}%`,
                    backgroundColor: v.color,
                  }}
                  title={`${v.label} · ${duration(v.value ?? 0)} · ${percent(v.value ?? 0)}`}
                  aria-label={`${v.label} ${duration(v.value ?? 0)} ${percent(v.value ?? 0)}`}
                  aria-pressed={active.id === v.id}
                  onClick={() => setSelected(v.id)}
                  onFocus={() => setSelected(v.id)}
                  onMouseEnter={() => setSelected(v.id)}
                />
              ))}
          </div>
          <ul className="lifecycle-duration-legend">
            {values.map((v) => (
              <li key={v.id}>
                <button
                  type="button"
                  aria-pressed={active.id === v.id}
                  onClick={() => setSelected(v.id)}
                  onMouseEnter={() => setSelected(v.id)}
                >
                  <span
                    className="lifecycle-duration-dot"
                    style={{ backgroundColor: v.color }}
                  />
                  <span>
                    {v.label}
                    <small>{duration(v.value ?? 0)}</small>
                  </span>
                  <b>{percent(v.value ?? 0)}</b>
                </button>
              </li>
            ))}
          </ul>
          <p className="lifecycle-duration-detail">
            <b>{active.label}</b> · {active.detail}
          </p>
        </>
      ) : (
        <div className="lifecycle-duration-empty" role="status">
          {loading
            ? "正在加载环节耗时…"
            : error
              ? "环节耗时加载失败，请刷新重试。"
              : complete
                ? "当前范围内没有可计算的处理耗时。"
                : "暂无完整环节耗时数据，请确认 Worker 已上报分段指标且所选范围内有处理样本。"}
        </div>
      )}
      <p className="lifecycle-duration-note">
        统计范围为 ProcessEvent 开始至返回；不包含 Cleaner、Mailbox 排队、Signal
        调度及异步下游处理。
      </p>
    </section>
  );
}

function duration(milliseconds: number): string {
  return milliseconds >= 1000
    ? `${(milliseconds / 1000).toFixed(2)} s`
    : `${milliseconds.toFixed(2)} ms`;
}
