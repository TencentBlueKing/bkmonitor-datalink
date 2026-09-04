import { useQuery } from "@tanstack/react-query";
import {
  useCallback,
  useEffect,
  useRef,
  useState,
  type ReactNode,
  type RefObject,
} from "react";
import { createPortal } from "react-dom";
import { Link } from "react-router-dom";

import type {
  KafkaInfrastructure,
  KafkaPartition,
  KafkaResource,
  MetricPanel,
} from "../../shared/contracts";
import { getCleanerRuntime, getMetrics } from "../api";
import { HelpLabel, HelpTableHeader } from "../components/HelpTip";
import { JsonViewer } from "../components/JsonViewer";
import { MetricPanelCard } from "../components/MetricPanelCard";
import { MetricQueryControls } from "../components/MetricQueryControls";
import { RefreshControls } from "../components/RefreshControls";
import { StageMetricCards } from "../components/StageMetricCards";
import {
  ProcessingFlowHeader,
  StepGuideCard,
  type StepGuide,
} from "../components/StepGuide";
import { StatusBadge } from "../components/StatusBadge";
import {
  defaultMetricCalculationWindowSeconds,
  defaultMetricRangeSeconds,
  metricStep,
} from "../metricRange";

interface SourceSummary {
  eventSourceId: string;
  enabled: boolean;
  cleanerType: string;
  runtime: Record<string, number>;
  kafka: {
    topic: string;
    consumerGroup: string;
    brokers: string[];
    security?: string;
  };
}

interface SnapshotItem {
  label: string;
  value: ReactNode;
}

const cleanerStepDurationPanelIDs = [
  "cleaner-step-average",
  "cleaner-step-p95",
  "cleaner-step-p99",
];
const cleanerPipelineDurationPanelIDs = [
  "pipeline-average",
  "pipeline-p95",
  "pipeline-p99",
];

const cleanerStepGuides: Record<string, StepGuide> = {
  receive: {
    summary:
      "从 EventSource 对应的 Kafka consumer group 有界拉取消息，并按 partition 建立独立 lane。",
    success: "消息进入受容量限制的 inflight 队列，等待 Cleaner worker 处理。",
    boundary: "此时尚未清洗、持久化或确认；接收失败不会产生业务副作用。",
  },
  transform: {
    summary:
      "SourceCleaner 解释来源 payload，EventFactory 再补齐受控身份、时间、severity 与 fingerprint。",
    success: "得到通过校验的 Event，或得到允许确认的确定性 Discard。",
    boundary:
      "该步骤只做纯计算，可跨 lane 并行；超时和临时错误必须重试，不能当作 Discard。",
  },
  event_store: {
    summary:
      "按 lane 的连续完成前缀批量创建 Event，并保留逐项创建、幂等重放或失败结果。",
    success: "Event 已创建，或相同稳定 Event ID 已存在且内容一致。",
    boundary: "临时存储错误会形成顺序缺口；后续消息不能越过缺口进入 Mailbox。",
  },
  mailbox_enqueue: {
    summary:
      "把已持久化 Event 的稳定 ID 写入按关联键划分的 Redis Mailbox List 与去重 Set。",
    success: "Event ID 已新增或被幂等去重，后续可由 Lifecycle 恢复处理。",
    boundary:
      "只有已持久化 Event 才能入队；入队失败时上游 Kafka 消息仍不能确认。",
  },
  mailbox_signal: {
    summary:
      "把仍为 unprocessed 的 Event ID 追加到 Mailbox；空到非空时写入 Lifecycle Signal。",
    success: "首次信号已发出，或已有信号时本次唤醒被安全合并。",
    boundary: "Signal 只负责唤醒并指向 Mailbox，不承载具体 Event 列表。",
  },
  source_ack: {
    summary: "向 Kafka 提交该 partition 已经连续完成的 Receipt 前缀。",
    success: "提交位置已经推进，重启或 rebalance 后不会再从更早位置重放。",
    boundary:
      "只有 Event 已存储且 Mailbox 已入队，或确定性 Discard 才能推进；不得跨过失败缺口。",
  },
};

const cleanerSnapshotHelp: Record<string, string> = {
  EventSource: "当前选择的接入源稳定标识。",
  Cleaner: "该 EventSource 使用的清洗器类型。",
  Topic: "Cleaner 消费的 Kafka topic。",
  "Consumer Group": "Cleaner 用于消费该 topic 的 Kafka group。",
  Partitions: "该 topic 当前可见的 partition 数。",
  "Total Lag": "各 partition 的 LEO 减 committed next offset 后求和。",
};

export function CleanerPage() {
  const [rangeSeconds, setRangeSeconds] = useState(defaultMetricRangeSeconds);
  const [calculationWindowSeconds, setCalculationWindowSeconds] = useState(
    defaultMetricCalculationWindowSeconds,
  );
  const [selected, setSelected] = useState<string>();
  const [selectedStep, setSelectedStep] = useState<string>();
  const [configOpen, setConfigOpen] = useState(false);
  const [autoRefresh, setAutoRefresh] = useState(true);
  const configButtonRef = useRef<HTMLButtonElement>(null);
  const closeConfig = useCallback(() => setConfigOpen(false), []);
  const runtime = useQuery({
    queryKey: ["runtime-cleaner"],
    queryFn: getCleanerRuntime,
    refetchInterval: autoRefresh ? 15_000 : false,
  });
  const sources =
    (runtime.data?.eventSources as SourceSummary[] | undefined) ?? [];
  const selectedSource = selected ?? sources[0]?.eventSourceId;
  const selectedConfig = sources.find(
    (source) => source.eventSourceId === selectedSource,
  );
  const resources = (
    (runtime.data?.kafka as KafkaInfrastructure | undefined)?.resources ?? []
  ).filter((resource) => resource.kind === "input");
  const selectedResource = resources.find(
    (resource) => resource.eventSourceId === selectedSource,
  );
  const metrics = useQuery({
    queryKey: [
      "cleaner-metrics",
      selectedSource,
      rangeSeconds,
      calculationWindowSeconds,
    ],
    enabled: Boolean(selectedSource),
    queryFn: () => {
      const to = new Date();
      return getMetrics({
        from: new Date(to.getTime() - rangeSeconds * 1000),
        to,
        step: metricStep(rangeSeconds),
        calculationWindowSeconds,
        eventSourceId: selectedSource,
      });
    },
    refetchInterval: autoRefresh ? 15_000 : false,
  });
  const refreshing = runtime.isFetching || metrics.isFetching;
  const lastSuccessfulAt = selectedSource
    ? runtime.dataUpdatedAt && metrics.dataUpdatedAt
      ? Math.min(runtime.dataUpdatedAt, metrics.dataUpdatedAt)
      : undefined
    : runtime.dataUpdatedAt || undefined;

  function refresh(): void {
    void Promise.all([
      runtime.refetch(),
      selectedSource ? metrics.refetch() : Promise.resolve(),
    ]);
  }

  return (
    <section>
      <div className="page-heading cleaner-page-heading">
        <div>
          <p className="eyebrow">EVENTSOURCE CLEANER</p>
          <h1>Cleaner Runtime</h1>
          <p>
            按进程与 EventSource 查看 Kafka lane、transform、入库、Mailbox
            和确认进度。
          </p>
        </div>
        <div className="metric-page-control-stack">
          <MetricQueryControls
            rangeSeconds={rangeSeconds}
            calculationWindowSeconds={calculationWindowSeconds}
            onRangeChange={setRangeSeconds}
            onCalculationWindowChange={setCalculationWindowSeconds}
          />
          <RefreshControls
            status={runtime.data?.status}
            lastSuccessfulAt={lastSuccessfulAt}
            isFetching={refreshing}
            autoRefresh={autoRefresh}
            intervalSeconds={15}
            onRefresh={refresh}
            onToggleAutoRefresh={() => setAutoRefresh((value) => !value)}
          >
            {selectedConfig && (
              <button
                ref={configButtonRef}
                className="cleaner-config-button"
                type="button"
                aria-haspopup="dialog"
                aria-expanded={configOpen}
                onClick={() => setConfigOpen(true)}
              >
                EventSource 配置 <span aria-hidden="true">↗</span>
              </button>
            )}
          </RefreshControls>
        </div>
      </div>

      {runtime.isError && (
        <div className="warning-banner">
          Cleaner 当前状态加载失败：{errorMessage(runtime.error)}
        </div>
      )}

      <div className="source-card-grid">
        {sources.map((source) => {
          const resource = resources.find(
            (item) => item.eventSourceId === source.eventSourceId,
          );
          return (
            <button
              type="button"
              className={`source-runtime-card ${selectedSource === source.eventSourceId ? "selected" : ""}`}
              key={source.eventSourceId}
              title="选择 EventSource，查看对应的处理链路、Kafka 快照与指标。"
              onClick={() => {
                setSelected(source.eventSourceId);
                setSelectedStep(undefined);
              }}
            >
              <span>{source.enabled ? "ENABLED" : "DISABLED"}</span>
              <strong>{source.eventSourceId}</strong>
              <small>
                {source.kafka.topic} · {source.kafka.consumerGroup}
              </small>
              <em>
                {resource?.partitions.length ?? 0} partitions ·{" "}
                {resource?.status ?? "unknown"}
              </em>
            </button>
          );
        })}
      </div>

      {sources.length === 0 && !runtime.isLoading && (
        <div className="warning-banner">当前配置没有 EventSource。</div>
      )}

      {selectedSource && (
        <>
          <article className="runtime-flow-panel cleaner-flow-panel">
            <ProcessingFlowHeader
              title={`${selectedSource} 处理链路`}
              description="Kafka 消息按 partition 进入独立 lane，清洗与 Event 构建可并行；只有 Event 入库、Mailbox 入队并发出调度信号后，才按 lane 的连续成功前缀提交源 offset。失败只保留在所属 lane 等待重试。"
            />
            <div className="runtime-flow">
              {[
                "receive",
                "transform",
                "event_store",
                "mailbox_enqueue",
                "mailbox_signal",
                "source_ack",
              ].map((step) => (
                <button
                  type="button"
                  className="runtime-node"
                  key={step}
                  title={cleanerStepGuides[step]?.summary}
                  onClick={() => setSelectedStep(step)}
                >
                  <span>→</span>
                  <strong>{step}</strong>
                </button>
              ))}
            </div>
          </article>

          <StageMetricCards
            panels={metrics.data?.panels ?? []}
            stage="clean"
            stageLabel="Cleaner"
          />

          <CleanerQueuePanel
            sourceId={selectedSource}
            source={selectedConfig}
            resource={selectedResource}
          />
        </>
      )}

      <MetricPanels panels={metrics.data?.panels ?? []} />

      {configOpen && selectedConfig && (
        <CleanerConfigDialog
          source={selectedConfig}
          returnFocusRef={configButtonRef}
          onClose={closeConfig}
        />
      )}
      {selectedStep && (
        <NodeHistoryDrawer
          step={selectedStep}
          guide={cleanerStepGuides[selectedStep]}
          panels={cleanerNodePanels(metrics.data?.panels ?? [], selectedStep)}
          snapshot={cleanerNodeSnapshot(selectedConfig, selectedResource)}
          onClose={() => setSelectedStep(undefined)}
        />
      )}
    </section>
  );
}

function CleanerQueuePanel({
  sourceId,
  source,
  resource,
}: {
  sourceId: string;
  source?: SourceSummary;
  resource?: KafkaResource;
}) {
  const lag = kafkaLag(resource);
  const abnormalPartitions =
    resource?.partitions.filter(
      (partition) =>
        partition.status !== "available" || partition.issues.length > 0,
    ).length ?? 0;

  return (
    <article className="cleaner-queue-panel">
      <header>
        <div>
          <p className="eyebrow">CURRENT KAFKA SNAPSHOT</p>
          <h2>消息队列当前状态</h2>
          <p>
            <code>
              {resource?.topic ?? source?.kafka.topic ?? "Topic 未知"}
            </code>
            <span> · </span>
            <code>
              {resource?.consumerGroup ??
                source?.kafka.consumerGroup ??
                "Consumer Group 未知"}
            </code>
          </p>
        </div>
        <div className="cleaner-queue-actions">
          <StatusBadge value={resource?.status ?? "unknown"} />
          <Link to="/infrastructure/kafka?tab=input">打开 Kafka 工作台 →</Link>
        </div>
      </header>

      {resource?.message && (
        <div className="warning-banner cleaner-queue-warning">
          {resource.message}
        </div>
      )}

      <div className="cleaner-queue-facts">
        <QueueFact
          label="Partitions"
          value={displayNumber(resource?.partitions.length)}
          detail={`EventSource ${sourceId}`}
          help="当前 topic 的 partition 总数。"
        />
        <QueueFact
          label="Group Members"
          value={displayNumber(resource?.group?.members.length)}
          detail={`State ${resource?.group?.state ?? "未知"}`}
          help="当前加入 consumer group 的成员数量。"
          tone={resource?.group?.state === "Stable" ? undefined : "amber"}
        />
        <QueueFact
          label="Total Lag"
          value={lag.value}
          detail={lag.detail}
          help="各 partition 的 LEO 减 committed next offset 后求和；缺失值不会按 0 补齐。"
          tone={lag.partial ? "amber" : undefined}
        />
        <QueueFact
          label="Abnormal Partitions"
          value={displayNumber(resource ? abnormalPartitions : undefined)}
          detail="leader、ISR、owner 或 committed 异常"
          help="存在 leader、ISR、owner、committed 或查询可用性问题的 partition 数。"
          tone={abnormalPartitions > 0 ? "red" : undefined}
        />
      </div>

      {resource && resource.issues.length > 0 && (
        <div className="kafka-issue-list" aria-label="消息队列状态问题">
          {resource.issues.map((issue, index) => (
            <span key={`${issue.code}:${issue.partition ?? "group"}:${index}`}>
              <code>{issue.code}</code>
              {issue.message}
            </span>
          ))}
        </div>
      )}

      <PartitionTable resource={resource} />
    </article>
  );
}

function QueueFact({
  label,
  value,
  detail,
  help,
  tone,
}: {
  label: string;
  value: string;
  detail: string;
  help: string;
  tone?: "amber" | "red";
}) {
  return (
    <article className={`stat-card ${tone ?? ""}`}>
      <HelpLabel label={label} help={help} />
      <strong>{value}</strong>
      <small>{detail}</small>
    </article>
  );
}

function PartitionTable({ resource }: { resource?: KafkaResource }) {
  const partitions = resource?.partitions ?? [];
  return (
    <section className="kafka-partition-section cleaner-partition-section">
      <header>
        <div>
          <h3>Partition Ownership &amp; Offset</h3>
          <span>缺失 offset 保持未知，不补成 0</span>
        </div>
        <span>{partitions.length} partitions</span>
      </header>
      {partitions.length === 0 ? (
        <div className="cleaner-queue-empty">
          {resource?.message ?? "当前没有可展示的 Partition 快照"}
        </div>
      ) : (
        <div className="table-scroll kafka-partition-table cleaner-partition-table">
          <table>
            <thead>
              <tr>
                <HelpTableHeader
                  label="Partition"
                  help="Kafka topic 内的有序分片编号。"
                />
                <HelpTableHeader
                  label="Owner"
                  help="当前被分配处理该 partition 的 consumer。"
                />
                <HelpTableHeader
                  label="Leader"
                  help="负责该 partition 读写请求的 broker 节点 ID。"
                />
                <HelpTableHeader
                  label="ISR"
                  help="同步副本数 / 配置副本数；前者变少表示副本未完全同步。"
                />
                <HelpTableHeader
                  label="Committed"
                  help="Consumer group 已提交的下一条消息 offset。"
                />
                <HelpTableHeader
                  label="High"
                  help="Log End Offset，即下一条将写入的 offset。"
                />
                <HelpTableHeader
                  label="Lag"
                  help="High 减 Committed，表示尚未被 group 确认的消息量。"
                />
                <HelpTableHeader
                  label="Status"
                  help="综合 leader、ISR、owner、offset 和查询结果得到的状态。"
                />
              </tr>
            </thead>
            <tbody>
              {partitions.map((partition) => (
                <tr key={partition.partition}>
                  <td className="mono">{partition.partition}</td>
                  <td title={partition.members?.join(", ")}>
                    {partitionOwners(resource, partition)}
                  </td>
                  <td>{partition.leader ?? "—"}</td>
                  <td>
                    {partition.isr.length}/{partition.replicas.length}
                  </td>
                  <td className="mono">
                    {partition.committedOffset ?? "未知"}
                  </td>
                  <td className="mono">{partition.highOffset ?? "未知"}</td>
                  <td className="mono">{partition.lag ?? "未知"}</td>
                  <td>
                    <StatusBadge value={partition.status} />
                    {partition.issues.length > 0 && (
                      <small>{partition.issues[0]?.message}</small>
                    )}
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}
    </section>
  );
}

function cleanerNodePanels(panels: MetricPanel[], step: string) {
  const ratePanelID =
    step === "receive"
      ? "received-rate"
      : step === "source_ack"
        ? "settled-rate"
        : "cleaner-steps";
  const ratePanels = panels
    .filter((panel) => panel.id === ratePanelID)
    .map((panel) =>
      panel.id === "cleaner-steps"
        ? filterMetricPanel(
            panel,
            (series) => series.labels.linkd_step === step,
            `查询范围内没有 ${step} 步骤时序`,
          )
        : panel,
    );
  if (step === "receive") return ratePanels;
  return [...ratePanels, mergeCleanerStepDurationPanel(panels, step)];
}

function mergeCleanerStepDurationPanel(
  panels: MetricPanel[],
  step: string,
): MetricPanel {
  const statisticLabels: Record<string, string> = {
    "cleaner-step-average": "平均",
    "cleaner-step-p95": "P95",
    "cleaner-step-p99": "P99",
  };
  const durationPanels = panels.filter((panel) =>
    cleanerStepDurationPanelIDs.includes(panel.id),
  );
  const series = durationPanels.flatMap((panel) => {
    if (panel.status !== "available") return [];
    const statistic = statisticLabels[panel.id] ?? panel.title;
    return panel.series
      .filter((item) => item.labels.linkd_step === step)
      .map((item) => ({
        ...item,
        name: item.labels.linkd_outcome
          ? `${statistic} · ${item.labels.linkd_outcome}`
          : statistic,
        labels: { ...item.labels, linkd_statistic: statistic },
      }));
  });
  return {
    id: "cleaner-step-duration",
    title: "Cleaner 步骤耗时",
    unit: durationPanels[0]?.unit ?? "s",
    kind: "line",
    status: series.length > 0 ? "available" : "unavailable",
    message:
      series.length > 0 ? undefined : `查询范围内没有 ${step} 步骤耗时时序`,
    series,
  };
}

function cleanerNodeSnapshot(
  source: SourceSummary | undefined,
  resource: KafkaResource | undefined,
): SnapshotItem[] {
  const lag = kafkaLag(resource);
  return [
    { label: "EventSource", value: source?.eventSourceId ?? "—" },
    { label: "Cleaner", value: source?.cleanerType ?? "—" },
    { label: "Topic", value: resource?.topic ?? source?.kafka.topic ?? "—" },
    {
      label: "Consumer Group",
      value: resource?.consumerGroup ?? source?.kafka.consumerGroup ?? "—",
    },
    {
      label: "Partitions",
      value: displayNumber(resource?.partitions.length),
    },
    { label: "Total Lag", value: lag.value },
  ];
}

function NodeHistoryDrawer({
  step,
  guide,
  panels,
  snapshot,
  onClose,
}: {
  step: string;
  guide: StepGuide;
  panels: MetricPanel[];
  snapshot: SnapshotItem[];
  onClose: () => void;
}) {
  return (
    <div className="drawer-backdrop" role="presentation" onClick={onClose}>
      <aside
        className="detail-drawer"
        role="dialog"
        aria-modal="true"
        aria-label={`${step} 节点详情`}
        onClick={(event) => event.stopPropagation()}
      >
        <header>
          <div>
            <h2>{step}</h2>
            <span>当前关键状态与固定历史指标</span>
          </div>
          <button type="button" aria-label="关闭详情" onClick={onClose}>
            ×
          </button>
        </header>
        <div className="node-drawer-body">
          <StepGuideCard step={step} guide={guide} />
          <h3>当前快照</h3>
          <div className="lifecycle-node-snapshot">
            {snapshot.map((item) => (
              <div key={item.label}>
                <HelpLabel
                  label={item.label}
                  help={
                    cleanerSnapshotHelp[item.label] ??
                    "当前节点对应的只读运行快照。"
                  }
                />
                <strong>{item.value}</strong>
              </div>
            ))}
          </div>
          <h3>历史趋势</h3>
          <MetricPanels panels={panels} />
        </div>
      </aside>
    </div>
  );
}

function CleanerConfigDialog({
  source,
  returnFocusRef,
  onClose,
}: {
  source: SourceSummary;
  returnFocusRef: RefObject<HTMLButtonElement | null>;
  onClose: () => void;
}) {
  const closeButtonRef = useRef<HTMLButtonElement>(null);

  useEffect(() => {
    const returnFocus = returnFocusRef.current;
    const previousOverflow = document.body.style.overflow;
    const closeOnEscape = (event: KeyboardEvent) => {
      if (event.key === "Escape") onClose();
    };
    document.body.style.overflow = "hidden";
    window.addEventListener("keydown", closeOnEscape);
    closeButtonRef.current?.focus();
    return () => {
      document.body.style.overflow = previousOverflow;
      window.removeEventListener("keydown", closeOnEscape);
      returnFocus?.focus();
    };
  }, [onClose, returnFocusRef]);

  return createPortal(
    <div className="lifecycle-dialog-layer cleaner-dialog-layer">
      <div
        className="lifecycle-dialog-backdrop"
        aria-hidden="true"
        onMouseDown={onClose}
      />
      <section
        className="lifecycle-config-dialog cleaner-config-dialog"
        role="dialog"
        aria-modal="true"
        aria-labelledby="cleaner-config-title"
      >
        <header>
          <div>
            <p className="eyebrow">EFFECTIVE CONFIG</p>
            <h2 id="cleaner-config-title">EventSource 配置</h2>
            <span>{source.eventSourceId} 的脱敏有效配置</span>
          </div>
          <button
            ref={closeButtonRef}
            type="button"
            aria-label="关闭 EventSource 配置"
            onClick={onClose}
          >
            ×
          </button>
        </header>
        <div className="lifecycle-config-dialog-content">
          <JsonViewer
            value={source}
            description="EventSource 的脱敏有效配置；runtime 是生效的并发与批次参数，kafka 是输入连接配置。"
          />
        </div>
      </section>
    </div>,
    document.body,
  );
}

function MetricPanels({ panels }: { panels: MetricPanel[] }) {
  const panelIDs = [
    "received-rate",
    "settled-rate",
    ...cleanerPipelineDurationPanelIDs,
    "cleaner-steps",
    "cleaner-step-duration",
    ...cleanerStepDurationPanelIDs,
    "retry-rate",
    "store-errors",
  ];
  const byID = new Map(panels.map((panel) => [panel.id, panel]));
  const visible = panelIDs.flatMap((id) => {
    const panel = byID.get(id);
    if (!panel) return [];
    if (!cleanerPipelineDurationPanelIDs.includes(id)) return [panel];
    const title: Record<string, string> = {
      "pipeline-average": "Cleaner 整体平均耗时",
      "pipeline-p95": "Cleaner 整体 P95",
      "pipeline-p99": "Cleaner 整体 P99",
    };
    return [
      {
        ...filterMetricPanel(
          panel,
          (series) => series.labels.linkd_stage === "clean",
          "查询范围内没有 Cleaner 整体耗时时序",
        ),
        title: title[id] ?? panel.title,
      },
    ];
  });
  return (
    <div className="chart-grid runtime-charts">
      {visible.map((panel) => (
        <MetricPanelCard key={panel.id} panel={panel} />
      ))}
    </div>
  );
}

function filterMetricPanel(
  panel: MetricPanel,
  predicate: (series: MetricPanel["series"][number]) => boolean,
  emptyMessage: string,
): MetricPanel {
  if (panel.status !== "available") return panel;
  const series = panel.series.filter(predicate);
  return series.length > 0
    ? { ...panel, series }
    : { ...panel, status: "unavailable", message: emptyMessage, series: [] };
}

function kafkaLag(resource: KafkaResource | undefined): {
  value: string;
  detail: string;
  partial: boolean;
} {
  const partitions = resource?.partitions ?? [];
  const known = partitions
    .map((partition) => partition.lag)
    .filter((value): value is string => Boolean(value && /^\d+$/.test(value)));
  if (known.length === 0) {
    return {
      value: "未知",
      detail:
        partitions.length === 0
          ? "尚无 partition offset"
          : `0/${partitions.length} partitions known`,
      partial: partitions.length > 0,
    };
  }
  const total = known.reduce((sum, value) => sum + BigInt(value), 0n);
  return {
    value: total.toLocaleString(),
    detail: `${known.length}/${partitions.length} partitions known`,
    partial: known.length !== partitions.length,
  };
}

function partitionOwners(
  resource: KafkaResource | undefined,
  partition: KafkaPartition,
): string {
  if (!partition.members || partition.members.length === 0) return "未分配";
  return partition.members
    .map(
      (memberId) =>
        resource?.group?.members.find((member) => member.memberId === memberId)
          ?.clientId ?? shortenMemberID(memberId),
    )
    .join(", ");
}

function shortenMemberID(value: string): string {
  return value.length <= 22 ? value : `${value.slice(0, 19)}…`;
}

function displayNumber(value: number | undefined): string {
  return value === undefined ? "未知" : value.toLocaleString();
}

function errorMessage(error: unknown): string {
  return error instanceof Error ? error.message : "未知错误";
}
