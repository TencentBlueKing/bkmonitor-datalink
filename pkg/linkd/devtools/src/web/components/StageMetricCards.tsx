import type { MetricPanel } from "../../shared/contracts";
import { HelpLabel } from "./HelpTip";

interface StageMetricCardsProps {
  panels: MetricPanel[];
  stage: string;
  stageLabel: string;
}

// StageMetricCards 只聚合指定 linkd_stage 的最新采样点，不跨阶段推导系统状态。
export function StageMetricCards({
  panels,
  stage,
  stageLabel,
}: StageMetricCardsProps) {
  const throughput = latestStageValue(panels, "pipeline-throughput", stage);
  const p99 = latestStageValue(panels, "pipeline-p99", stage);
  const average = latestStageValue(panels, "pipeline-average", stage);
  const inflight = latestStageValue(panels, "messaging-inflight", stage);

  return (
    <section
      className="stage-metric-panel"
      role="region"
      aria-label={`${stageLabel} 当前指标`}
    >
      <header>
        <div>
          <p className="eyebrow">STAGE METRICS</p>
          <h2>{stageLabel} 当前指标</h2>
          <p>最近一小时查询中各时序的最新采样点</p>
        </div>
      </header>
      <div className="stage-stat-grid">
        <StageStat
          title="阶段吞吐"
          value={formatRate(throughput)}
          detail="当前阶段各处理结果之和"
          help="当前阶段各 outcome 尝试速率的合计；失败重试仍会计入。"
        />
        <StageStat
          title="P99 耗时"
          value={formatDuration(p99)}
          detail="当前阶段尾延迟近似值"
          help="所选时间窗内当前阶段处理耗时的近似 99 分位值。"
          tone="amber"
        />
        <StageStat
          title="平均耗时"
          value={formatDuration(average)}
          detail="当前阶段单次尝试均值"
          help="当前阶段总处理耗时除以尝试次数得到的平均值。"
        />
        <StageStat
          title="在途消息"
          value={formatCount(inflight)}
          detail="已接管，尚未确认完成"
          help="当前阶段已读取、但尚未确认或确定性丢弃的消息数。"
        />
      </div>
    </section>
  );
}

function StageStat({
  title,
  value,
  detail,
  help,
  tone,
}: {
  title: string;
  value: string;
  detail: string;
  help: string;
  tone?: "amber";
}) {
  return (
    <article className={`stat-card ${tone ?? ""}`}>
      <HelpLabel label={title} help={help} />
      <strong>{value}</strong>
      <small>{detail}</small>
    </article>
  );
}

function latestStageValue(
  panels: MetricPanel[],
  panelID: string,
  stage: string,
): number | undefined {
  const panel = panels.find((candidate) => candidate.id === panelID);
  const values = (panel?.series ?? []).flatMap((series) => {
    if (series.labels.linkd_stage !== stage) return [];
    const value = series.points.at(-1)?.[1];
    return value === null || value === undefined ? [] : [value];
  });
  return values.length
    ? values.reduce((total, value) => total + value, 0)
    : undefined;
}

function formatRate(value: number | undefined): string {
  if (value === undefined) return "—";
  return `${value >= 100 ? value.toFixed(0) : value.toFixed(2)} /s`;
}

function formatDuration(value: number | undefined): string {
  if (value === undefined) return "—";
  if (value < 1) {
    const milliseconds = value * 1000;
    return `${milliseconds.toFixed(milliseconds >= 100 ? 0 : 1)} ms`;
  }
  return `${value >= 100 ? value.toFixed(0) : value.toFixed(2)} s`;
}

function formatCount(value: number | undefined): string {
  return value === undefined
    ? "—"
    : value.toLocaleString("en-US", { maximumFractionDigits: 0 });
}
