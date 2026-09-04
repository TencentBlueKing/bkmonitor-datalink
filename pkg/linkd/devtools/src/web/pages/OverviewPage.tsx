import { useQuery } from "@tanstack/react-query";
import { type FormEvent, type ReactNode, useState } from "react";
import { Link } from "react-router-dom";

import type { MetricPanel } from "../../shared/contracts";
import {
  getCleanerRuntime,
  getLifecycleRuntime,
  getMetrics,
  getRuntimeProcesses,
} from "../api";
import { HelpLabel, HelpTableHeader } from "../components/HelpTip";
import { MetricPanelCard } from "../components/MetricPanelCard";
import { MetricQueryControls } from "../components/MetricQueryControls";
import { RefreshControls } from "../components/RefreshControls";
import {
  defaultMetricCalculationWindowSeconds,
  defaultMetricRangeSeconds,
  metricStep,
} from "../metricRange";
import { useReportPageQueryFailure } from "../navigation";

const pipelineStages = ["clean", "lifecycle"];

export function OverviewPage() {
  const [rangeSeconds, setRangeSeconds] = useState(defaultMetricRangeSeconds);
  const [calculationWindowSeconds, setCalculationWindowSeconds] = useState(
    defaultMetricCalculationWindowSeconds,
  );
  const [autoRefresh, setAutoRefresh] = useState(true);
  const [instance, setInstance] = useState("");
  const [instanceDraft, setInstanceDraft] = useState("");
  const metrics = useQuery({
    queryKey: ["metrics", rangeSeconds, calculationWindowSeconds, instance],
    queryFn: () => {
      const to = new Date();
      return getMetrics({
        to,
        from: new Date(to.getTime() - rangeSeconds * 1000),
        step: metricStep(rangeSeconds),
        calculationWindowSeconds,
        instance: instance || undefined,
      });
    },
    refetchInterval: autoRefresh ? 15_000 : false,
  });
  const processes = useQuery({
    queryKey: ["runtime-processes"],
    queryFn: getRuntimeProcesses,
    refetchInterval: autoRefresh ? 15_000 : false,
  });
  const cleaner = useQuery({
    queryKey: ["runtime-cleaner-overview"],
    queryFn: getCleanerRuntime,
    refetchInterval: autoRefresh ? 15_000 : false,
  });
  const lifecycle = useQuery({
    queryKey: ["runtime-lifecycle-overview"],
    queryFn: getLifecycleRuntime,
    refetchInterval: autoRefresh ? 15_000 : false,
  });
  useReportPageQueryFailure(metrics.isError);
  const refreshing =
    metrics.isFetching ||
    processes.isFetching ||
    cleaner.isFetching ||
    lifecycle.isFetching;
  const updateTimes = [
    metrics.dataUpdatedAt,
    processes.dataUpdatedAt,
    cleaner.dataUpdatedAt,
    lifecycle.dataUpdatedAt,
  ];
  const lastSuccessfulAt = updateTimes.every((value) => value > 0)
    ? Math.min(...updateTimes)
    : undefined;

  function refresh(): void {
    void Promise.all([
      metrics.refetch(),
      processes.refetch(),
      cleaner.refetch(),
      lifecycle.refetch(),
    ]);
  }

  function applyInstance(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    setInstance(instanceDraft.trim());
  }

  const panels = metrics.data?.panels ?? [];
  const processItems =
    (processes.data?.items as
      | Array<{
          instance: string;
          serviceInstanceId: string;
          role: string;
          version: string;
          up: boolean;
        }>
      | undefined) ?? [];
  const throughputStages = latestStageValues(
    panels.find((panel) => panel.id === "pipeline-throughput"),
  );
  const latestThroughput = minimumStageValue(throughputStages);
  const p99Stages = latestStageValues(
    panels.find((panel) => panel.id === "pipeline-p99"),
  );
  const averageStages = latestStageValues(
    panels.find((panel) => panel.id === "pipeline-average"),
  );
  const inflightStages = overviewInflightStages(
    latestStageValues(
      panels.find((panel) => panel.id === "messaging-inflight"),
    ),
  );
  const latestGap = latestValue(
    panels.find((panel) => panel.id === "settlement-gap"),
  );

  return (
    <section>
      <div className="page-heading">
        <div>
          <p className="eyebrow">PIPELINE OBSERVABILITY</p>
          <h1>处理状态</h1>
          <p>
            从 Prometheus 聚合 Linkd
            当前已接入阶段的吞吐、延迟、积压与正确性信号。
          </p>
        </div>
        <div className="overview-control-stack">
          <form className="instance-control" onSubmit={applyInstance}>
            <label htmlFor="metrics-instance">
              <HelpLabel
                label="Instance"
                help="精确匹配 Prometheus 的 instance label，只查看一个抓取目标。"
              />
            </label>
            <input
              id="metrics-instance"
              aria-label="Instance"
              value={instanceDraft}
              placeholder="全部实例"
              onChange={(event) => setInstanceDraft(event.target.value)}
            />
            <button type="submit">应用</button>
            {instance && (
              <button
                type="button"
                onClick={() => {
                  setInstance("");
                  setInstanceDraft("");
                }}
              >
                清除
              </button>
            )}
          </form>
          <MetricQueryControls
            rangeSeconds={rangeSeconds}
            calculationWindowSeconds={calculationWindowSeconds}
            onRangeChange={setRangeSeconds}
            onCalculationWindowChange={setCalculationWindowSeconds}
          />
          <RefreshControls
            status={
              metrics.isError
                ? "unavailable"
                : metrics.data
                  ? "available"
                  : undefined
            }
            lastSuccessfulAt={lastSuccessfulAt}
            isFetching={refreshing}
            autoRefresh={autoRefresh}
            intervalSeconds={15}
            onRefresh={refresh}
            onToggleAutoRefresh={() => setAutoRefresh((value) => !value)}
          />
        </div>
      </div>

      {metrics.isError && (
        <div className="error-banner">
          无法读取指标：{metrics.error.message}
        </div>
      )}

      <div className="stat-grid">
        <Stat
          title="吞吐"
          value={formatMetric(latestThroughput, " /s")}
          detail={<StageBreakdown values={throughputStages} suffix="/s" />}
          help="当前各处理阶段尝试速率的最小值；失败重试仍会计入对应阶段。"
        />
        <Stat
          title="P99 耗时合计"
          value={formatMetric(sumStageValues(p99Stages), " s")}
          detail={<StageBreakdown values={p99Stages} suffix="s" />}
          help="当前各阶段 P99 的合计值，用作流水线尾延迟的保守诊断；分位数相加不等同于严格的端到端 P99。"
          tone="amber"
        />
        <Stat
          title="平均耗时合计"
          value={formatMetric(sumStageValues(averageStages), " s")}
          detail={<StageBreakdown values={averageStages} suffix="s" />}
          help="当前各阶段平均处理耗时之和；小字保留每个阶段的独立值。"
        />
        {inflightStages.map((stage) => (
          <Stat
            key={stage.stage}
            title={`${stageLabel(stage.stage)} 在途消息`}
            value={formatCount(stage.value)}
            detail="已接管，尚未确认完成"
            help={`${stageLabel(stage.stage)} 阶段已读取、但尚未确认或确定性丢弃的消息数。`}
          />
        ))}
        <Stat
          title="确认阻塞消息"
          value={formatCount(latestGap)}
          detail="被同分区更早消息挡住"
          help="已完成但被同分区更早未完成消息挡住、暂不能提交的数量。"
          tone={latestGap && latestGap > 0 ? "red" : undefined}
        />
      </div>

      <div className="pipeline-strip actual-pipeline">
        {[
          {
            name: "EventSource / Kafka",
            to: "/cleaner",
            status: cleaner.data?.status,
            help: "原始消息进入 EventSource 对应的 Kafka input lane。",
          },
          {
            name: "Cleaner transform",
            to: "/cleaner",
            status: cleaner.data?.status,
            help: "Cleaner 将 wire message 转换为内部 EventDraft。",
          },
          {
            name: "Event store",
            to: "/cleaner",
            status: cleaner.data?.status,
            help: "持久化 Event 与对应处理快照。",
          },
          {
            name: "Redis Mailbox",
            to: "/lifecycle",
            status: lifecycle.data?.status,
            help: "按关联键保存待处理 Event ID，并发出 Lifecycle 信号。",
          },
          {
            name: "Lifecycle",
            to: "/lifecycle",
            status: lifecycle.data?.status,
            help: "依据 Event 与当前 Alert 状态执行生命周期裁决。",
          },
          {
            name: "Alert / AlertLog",
            to: "/explore/alerts",
            status: lifecycle.data?.status,
            help: "保存 Alert 当前态与可追溯的 AlertLog 流水。",
          },
          {
            name: "FinalHook Kafka",
            to: "/lifecycle",
            status: lifecycle.data?.status,
            help: "将最终 Alert 变化写入配置的 Kafka output。",
          },
        ].map((stage, index) => (
          <Link
            className="pipeline-node"
            key={stage.name}
            to={stage.to}
            title={stage.help}
          >
            <span className={stage.status === "available" ? "connected" : ""}>
              {String(index + 1).padStart(2, "0")}
            </span>
            <strong>{stage.name}</strong>
            <small>{String(stage.status ?? "unavailable")}</small>
          </Link>
        ))}
      </div>

      <article className="runtime-flow-panel process-panel">
        <header>
          <div>
            <h2>Linkd 进程</h2>
            <p>由 Prometheus target_info 与 up 识别</p>
          </div>
          <span>{processItems.length} instances</span>
        </header>
        <div className="table-scroll">
          <table>
            <thead>
              <tr>
                <HelpTableHeader
                  label="Instance"
                  help="Prometheus 抓取目标地址，用于定位具体 endpoint。"
                />
                <HelpTableHeader
                  label="Service Instance"
                  help="OpenTelemetry Resource 中的稳定进程实例标识。"
                />
                <HelpTableHeader
                  label="Role"
                  help="该 Linkd 进程承担的 cleaner、lifecycle 等运行角色。"
                />
                <HelpTableHeader label="Version" help="进程上报的软件版本。" />
                <HelpTableHeader
                  label="Status"
                  help="Prometheus 最近一次抓取是否成功；UP 不代表业务处理一定正常。"
                />
              </tr>
            </thead>
            <tbody>
              {processItems.map((item) => (
                <tr key={`${item.instance}:${item.serviceInstanceId}`}>
                  <td>{item.instance}</td>
                  <td>{item.serviceInstanceId}</td>
                  <td>{item.role}</td>
                  <td>{item.version}</td>
                  <td>{item.up ? "UP" : "DOWN"}</td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      </article>

      <div className="chart-grid">
        {metrics.isLoading
          ? Array.from({ length: 6 }, (_, index) => (
              <div className="panel skeleton" key={index} />
            ))
          : panels
              .filter((panel) =>
                [
                  "pipeline-throughput",
                  "pipeline-average",
                  "pipeline-p99",
                  "messaging-inflight",
                  "retry-rate",
                  "settlement-gap",
                  "store-errors",
                ].includes(panel.id),
              )
              .map((panel) => <MetricPanelCard key={panel.id} panel={panel} />)}
      </div>
    </section>
  );
}

function Stat({
  title,
  value,
  detail,
  help,
  tone,
}: {
  title: string;
  value: string;
  detail: ReactNode;
  help: string;
  tone?: string;
}) {
  return (
    <article className={`stat-card ${tone ?? ""}`}>
      <HelpLabel label={title} help={help} />
      <strong>{value}</strong>
      <small className="stat-detail">{detail}</small>
    </article>
  );
}

function latestValue(panel: MetricPanel | undefined): number | undefined {
  if (!panel) return undefined;
  const values = panel.series.flatMap(
    (series) => series.points.at(-1)?.[1] ?? [],
  );
  return values.length
    ? values.reduce((sum, value) => sum + value, 0)
    : undefined;
}

interface StageValue {
  stage: string;
  value: number | undefined;
}

function latestStageValues(panel: MetricPanel | undefined): StageValue[] {
  const values = new Map<string, number>();
  for (const series of panel?.series ?? []) {
    const value = series.points.at(-1)?.[1];
    if (value === null || value === undefined) continue;
    const stage = series.labels.linkd_stage || "unknown";
    values.set(stage, (values.get(stage) ?? 0) + value);
  }
  return [...values.entries()]
    .map(([stage, value]) => ({ stage, value }))
    .sort((left, right) => stageOrder(left.stage) - stageOrder(right.stage));
}

function minimumStageValue(values: StageValue[]): number | undefined {
  const known = values.flatMap((value) =>
    value.value === undefined ? [] : [value.value],
  );
  return known.length ? Math.min(...known) : undefined;
}

function sumStageValues(values: StageValue[]): number | undefined {
  const known = values.flatMap((value) =>
    value.value === undefined ? [] : [value.value],
  );
  return known.length
    ? known.reduce((total, value) => total + value, 0)
    : undefined;
}

function overviewInflightStages(values: StageValue[]): StageValue[] {
  const byStage = new Map(values.map((value) => [value.stage, value.value]));
  return [
    ...pipelineStages.map((stage) => ({ stage, value: byStage.get(stage) })),
    ...values.filter((value) => !pipelineStages.includes(value.stage)),
  ];
}

function StageBreakdown({
  values,
  suffix,
}: {
  values: StageValue[];
  suffix: string;
}) {
  const byStage = new Map(values.map((value) => [value.stage, value.value]));
  const stages = [
    ...pipelineStages.map((stage) => ({ stage, value: byStage.get(stage) })),
    ...values.filter((value) => !pipelineStages.includes(value.stage)),
  ];
  return (
    <span className="stat-breakdown">
      {stages.map((stage) => (
        <span key={stage.stage}>
          {stageLabel(stage.stage)}
          <b>{formatCompactMetric(stage.value, suffix)}</b>
        </span>
      ))}
    </span>
  );
}

function stageLabel(stage: string): string {
  if (stage === "clean") return "Cleaner";
  if (stage === "lifecycle") return "Lifecycle";
  return stage;
}

function stageOrder(stage: string): number {
  const index = pipelineStages.indexOf(stage);
  return index < 0 ? pipelineStages.length : index;
}

function formatMetric(value: number | undefined, suffix: string): string {
  if (value === undefined) return "—";
  return `${value >= 100 ? value.toFixed(0) : value.toFixed(2)}${suffix}`;
}

function formatCompactMetric(
  value: number | undefined,
  suffix: string,
): string {
  if (value === undefined) return "—";
  if (suffix === "s" && value < 1) {
    const milliseconds = value * 1000;
    return `${milliseconds.toFixed(milliseconds >= 100 ? 0 : 1)}ms`;
  }
  const formatted = value >= 100 ? value.toFixed(0) : value.toFixed(2);
  return `${formatted}${suffix}`;
}

function formatCount(value: number | undefined): string {
  return value === undefined
    ? "—"
    : value.toLocaleString("en-US", { maximumFractionDigits: 0 });
}
