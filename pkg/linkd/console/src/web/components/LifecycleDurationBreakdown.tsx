import { useState } from "react";
import type { MetricPanel } from "../../shared/contracts";
import { LifecyclePhaseDetails } from "./LifecyclePhaseDetails";
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
    detail: "保存策略快照、Event 执行计划、最终处理结果及冲突时的计划撤销。",
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
  const [ignoreBatch, setIgnoreBatch] = useState(false);
  const panel = panels.find(
    (p) => p.id === "lifecycle-phase-execution-average",
  );
  // 后端先按耗时和次数跨 Worker 加权；前端不平均 Worker 均值，也不回退到历史末值。
  const values = phases.map((phase) => {
    const series =
      panel?.series.filter((s) => s.labels.linkd_phase === phase.id) ?? [];
    const point = series.length === 1 ? series[0].points.at(-1) : undefined;
    return { ...phase, value: point?.[1], timestamp: point?.[0] };
  });
  const waitPanel = panels.find((p) => p.id === "lifecycle-phase-wait-average");
  const waits = phases.flatMap((phase) =>
    ["batch_wait", "storage_queue"].map((kind) => {
      const series =
        waitPanel?.series.filter(
          (s) =>
            s.labels.linkd_phase === phase.id &&
            s.labels.linkd_wait_kind === kind,
        ) ?? [];
      const point = series.length === 1 ? series[0].points.at(-1) : undefined;
      return {
        phase: phase.id,
        kind,
        value: point?.[1],
        timestamp: point?.[0],
      };
    }),
  );
  const complete =
    panel?.status === "available" &&
    waitPanel?.status === "available" &&
    waits.every(
      (v) =>
        typeof v.value === "number" &&
        Number.isFinite(v.value) &&
        v.value >= 0 &&
        v.timestamp === values[0].timestamp,
    ) &&
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
  const waitTotal = (kind: string) =>
    complete
      ? waits
          .filter((v) => v.kind === kind)
          .reduce((sum, v) => sum + (v.value ?? 0), 0)
      : 0;
  const phaseWait = (phase: string, kind: string) =>
    waits.find((v) => v.phase === phase && v.kind === kind)?.value ?? 0;
  const batchWait = waitTotal("batch_wait");
  const storageQueue = waitTotal("storage_queue");
  const available =
    complete && Number.isFinite(total) && total + batchWait + storageQueue > 0;
  const displayed = values.map((v) => ({
    ...v,
    shown:
      (v.value ?? 0) +
      phaseWait(v.id, "storage_queue") +
      (ignoreBatch ? 0 : phaseWait(v.id, "batch_wait")),
  }));
  const shownTotal = total + storageQueue + (ignoreBatch ? 0 : batchWait);
  const largest = displayed.reduce((a, b) => (b.shown > a.shown ? b : a));
  const active = displayed.find((v) => v.id === selected) ?? largest;
  const percent = (value: number) =>
    `${(shownTotal > 0 ? (value / shownTotal) * 100 : 0).toFixed(1)}%`;

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
            所选范围内每次 Event
            尝试的均值，包含失败与重放。点击阶段查看子步骤；执行耗时包含实际
            I/O，并非 CPU 时间。
          </p>
        </div>
        <button
          type="button"
          className="lifecycle-duration-toggle"
          aria-pressed={ignoreBatch}
          onClick={() => setIgnoreBatch(!ignoreBatch)}
        >
          {ignoreBatch ? "已忽略合批等待" : "忽略合批等待"}
        </button>
      </header>
      {available ? (
        <>
          <div className="lifecycle-duration-summary">
            <div>
              <span>{ignoreBatch ? "扣除合批后耗时" : "当前图表耗时"}</span>
              <strong>{duration(shownTotal)}</strong>
            </div>
            <div>
              <span>平均执行耗时</span>
              <strong>{duration(total)}</strong>
            </div>
            <div>
              <span>主动合批等待</span>
              <strong>{duration(batchWait)}</strong>
            </div>
            <div>
              <span>存储排队</span>
              <strong>{duration(storageQueue)}</strong>
            </div>
            <div>
              <span>平均总耗时</span>
              <strong>{duration(total + batchWait + storageQueue)}</strong>
            </div>
            <p>
              当前口径耗时最多 <b>{largest.label}</b>
              <strong>{percent(largest.shown)}</strong>
            </p>
          </div>
          <div
            className="lifecycle-duration-bar"
            role="group"
            aria-label="各环节耗时占比"
          >
            {displayed
              .filter((v) => v.shown > 0)
              .map((v) => (
                <button
                  key={v.id}
                  type="button"
                  style={{
                    width: `${(v.shown / shownTotal) * 100}%`,
                    backgroundColor: v.color,
                  }}
                  title={`${v.label} · ${duration(v.shown)} · ${percent(v.shown)}`}
                  aria-label={`${v.label} ${duration(v.shown)} ${percent(v.shown)}`}
                  aria-pressed={active.id === v.id}
                  onClick={() => setSelected(v.id)}
                />
              ))}
          </div>
          <ul className="lifecycle-duration-legend">
            {displayed.map((v) => (
              <li key={v.id}>
                <button
                  type="button"
                  aria-pressed={active.id === v.id}
                  onClick={() => setSelected(v.id)}
                >
                  <span
                    className="lifecycle-duration-dot"
                    style={{ backgroundColor: v.color }}
                  />
                  <span>
                    {v.label}
                    <small>
                      <span>执行 {duration(v.value ?? 0)}</span>
                      <span>
                        合批 {duration(phaseWait(v.id, "batch_wait"))}
                      </span>
                      <span>
                        排队 {duration(phaseWait(v.id, "storage_queue"))}
                      </span>
                    </small>
                  </span>
                  <b>{percent(v.shown)}</b>
                </button>
              </li>
            ))}
          </ul>
          <p className="lifecycle-duration-detail">
            <b>{active.label}</b> · {active.detail}
          </p>
          <LifecyclePhaseDetails
            panels={panels}
            phase={active.id}
            label={active.label}
            color={active.color}
            timestamp={active.timestamp}
            execution={active.value ?? 0}
            batch={phaseWait(active.id, "batch_wait")}
            queue={phaseWait(active.id, "storage_queue")}
            ignoreBatch={ignoreBatch}
          />
        </>
      ) : (
        <div className="lifecycle-duration-empty" role="status">
          {loading
            ? "正在加载环节耗时…"
            : error
              ? "环节耗时加载失败，请刷新重试。"
              : complete
                ? "当前范围内没有可计算的处理耗时。"
                : "暂无完整执行与等待耗时数据，请确认 Worker 已上报新分段指标且所选范围内有处理样本。"}
        </div>
      )}
      <p className="lifecycle-duration-note">
        {ignoreBatch
          ? "图表与明细已扣除主动合批等待，存储排队仍计入。"
          : "图表与明细包含主动合批等待和存储排队。"}
        平均总耗时 = 执行耗时 + 主动合批等待 + 存储排队。统计范围为 ProcessEvent
        开始至返回；不包含 Cleaner、Mailbox 排队、Signal 调度及异步下游处理。
      </p>
    </section>
  );
}

function duration(milliseconds: number): string {
  return milliseconds >= 1000
    ? `${(milliseconds / 1000).toFixed(2)} s`
    : `${milliseconds.toFixed(2)} ms`;
}
