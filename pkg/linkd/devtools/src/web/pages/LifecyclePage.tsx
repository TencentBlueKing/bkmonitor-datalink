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

import type { MetricPanel, RedisInfrastructure } from "../../shared/contracts";
import { getLifecycleRuntime, getMetrics } from "../api";
import { HelpLabel, HelpTableHeader } from "../components/HelpTip";
import { JsonViewer } from "../components/JsonViewer";
import { MetricPanelCard } from "../components/MetricPanelCard";
import { RefreshControls } from "../components/RefreshControls";
import { StageMetricCards } from "../components/StageMetricCards";
import {
  ProcessingFlowHeader,
  StepGuideCard,
  type StepGuide,
} from "../components/StepGuide";
import { StatusBadge } from "../components/StatusBadge";
import { formatTime, useTimeMode } from "../time";

type LifecycleConfig = Record<string, unknown>;

const lifecycleStepGuides: Record<string, StepGuide> = {
  redis_signal: {
    summary:
      "从 Redis Stream 读取 Mailbox 调度信号，并校验消息 ID、租户、EventSource 与关联键。",
    success: "得到一个可信的 Mailbox 唤醒请求，可以进入后续串行处理。",
    boundary:
      "Signal 只指向 Mailbox，不绑定单个 Event；未 XACK 的信号由 PEL 与 Claim 机制恢复。",
  },
  lease: {
    summary: "使用 SET NX PX 获取 Mailbox 短租约，并在处理期间按固定间隔续租。",
    success: "当前进程成为该 Mailbox 的唯一有效处理者。",
    boundary:
      "锁忙只延后处理；续租失败会取消当前工作，随机 token 防止旧 owner 误释放新 lease。",
  },
  mailbox_peek: {
    summary:
      "读取 Mailbox 队首 Event ID，并按 max_drain_events 限制一次调度的处理数量。",
    success: "得到当前应优先处理的 Event ID，或确认 Mailbox 暂时为空。",
    boundary:
      "Peek 不移除数据；读取或后续处理失败时，队首 Event 必须留给重试。",
  },
  process_event: {
    summary:
      "读取并校验 StoredEvent，依据 action、现有 Alert 和 severity 执行生命周期裁决。",
    success:
      "Event 得到 accepted、suppressed 或 orphaned 等终态，相关 Alert 与流水按规则收敛。",
    boundary:
      "依赖稳定身份和 CAS 幂等恢复；失败时 Event 仍留在 Mailbox 队首，不能提前确认。",
  },
  mailbox_ack: {
    summary:
      "仅在 ProcessEvent 成功后，按预期 Event ID 从 Mailbox List 移除队首。",
    success: "该 Event 不再占用 Mailbox，并可继续处理下一条。",
    boundary: "队首不匹配或 Redis 操作失败必须停止本次 drain，不能跳过消息。",
  },
  xack: {
    summary:
      "读取到空 Mailbox 后，确认本次 Redis Stream Signal。并发新入队会生成独立 Signal。",
    success: "Signal 从当前 consumer group 的 PEL 中移除。",
    boundary:
      "XACK 不删除 Stream entry；Mailbox 未排空或处理失败时不能提前确认。",
  },
  final_hook: {
    summary:
      "Alert 发生真实变化时，将稳定的 Alert 快照投递到配置的 Kafka FinalHook。",
    success: "下游已确认输出，或失败结果已记录为可追溯的 push AlertLog。",
    boundary:
      "它是 ProcessEvent 内的条件步骤，并非每条 Signal 都执行；失败不回滚已完成的 Alert 状态。",
  },
};

const lifecycleSnapshotHelp: Record<string, string> = {
  状态: "该运行环节当前快照的可用性。",
  Stream: "Lifecycle 使用的 Redis Signal Stream key。",
  "Stream entries": "Stream 当前保留的 entry 数量。",
  Pending: "已投递给 consumer、但尚未 XACK 的 entry 数量。",
  "Consumer groups": "Signal Stream 上当前存在的 consumer group 数。",
  "Last generated ID": "Redis 最近生成的 Stream entry ID。",
  "Active leases": "本次扫描发现的有效 Mailbox lease 数。",
  TTL: "新建 lease 的有效时长。",
  "Renew interval": "处理期间刷新 lease 的时间间隔。",
  "Key prefix": "对应 Redis key 使用的配置前缀。",
  "Approximate unresolved":
    "目标 Consumer Group 的 lag + pending，是 Signal 数而非精确 Event 数。",
  "Active mailboxes": "当前包含待处理 Event 的 Mailbox 数。",
  "Max drain events": "单次调度最多从一个 Mailbox 处理的 Event 数。",
  Concurrency: "Lifecycle 允许并行处理的 worker 数。",
  Topic: "FinalHook 写入的 Kafka output topic。",
  "Client ID": "FinalHook Kafka producer 使用的 client 标识。",
  Brokers: "FinalHook 连接的 Kafka broker 地址。",
};

export function LifecyclePage() {
  const [selectedStep, setSelectedStep] = useState<string>();
  const [configOpen, setConfigOpen] = useState(false);
  const [autoRefresh, setAutoRefresh] = useState(true);
  const configButtonRef = useRef<HTMLButtonElement>(null);
  const closeConfig = useCallback(() => setConfigOpen(false), []);
  const runtime = useQuery({
    queryKey: ["runtime-lifecycle"],
    queryFn: getLifecycleRuntime,
    refetchInterval: autoRefresh ? 15_000 : false,
  });
  const metrics = useQuery({
    queryKey: ["lifecycle-metrics"],
    queryFn: () => {
      const to = new Date();
      return getMetrics({
        from: new Date(to.getTime() - 3600_000),
        to,
        step: 15,
      });
    },
    refetchInterval: autoRefresh ? 15_000 : false,
  });
  const redis = runtime.data?.redis as RedisInfrastructure | undefined;
  const config = (runtime.data?.config as LifecycleConfig | undefined) ?? {};
  const pipelinePanelIDs = ["pipeline-average", "pipeline-p95", "pipeline-p99"];
  const panels = (metrics.data?.panels ?? [])
    .filter((panel) =>
      [
        "lifecycle-results",
        "lifecycle-mailbox",
        "lifecycle-drain-p95",
        "lifecycle-lease",
        "lifecycle-recent-alert-cache",
        "lifecycle-recent-alert-hit-ratio",
        ...pipelinePanelIDs,
        "final-hook",
        "final-hook-p95",
        "retry-rate",
        "messaging-inflight",
        "settled-rate",
        "store-errors",
      ].includes(panel.id),
    )
    .map((panel) =>
      pipelinePanelIDs.includes(panel.id)
        ? {
            ...panel,
            series: panel.series.filter(
              (series) => series.labels.linkd_stage === "lifecycle",
            ),
          }
        : panel,
    );
  const refreshing = runtime.isFetching || metrics.isFetching;
  const lastSuccessfulAt =
    runtime.dataUpdatedAt && metrics.dataUpdatedAt
      ? Math.min(runtime.dataUpdatedAt, metrics.dataUpdatedAt)
      : undefined;

  function refresh(): void {
    void Promise.all([runtime.refetch(), metrics.refetch()]);
  }

  return (
    <section>
      <div className="page-heading lifecycle-page-heading">
        <div>
          <p className="eyebrow">LIFECYCLE RUNTIME</p>
          <h1>Lifecycle</h1>
          <p>查看 Redis Stream、PEL、Mailbox drain、Event 裁决与 FinalHook。</p>
        </div>
        <RefreshControls
          status={runtime.data?.status}
          lastSuccessfulAt={lastSuccessfulAt}
          isFetching={refreshing}
          autoRefresh={autoRefresh}
          intervalSeconds={15}
          onRefresh={refresh}
          onToggleAutoRefresh={() => setAutoRefresh((value) => !value)}
        >
          <button
            ref={configButtonRef}
            className="lifecycle-config-button"
            type="button"
            aria-haspopup="dialog"
            aria-expanded={configOpen}
            onClick={() => setConfigOpen(true)}
          >
            Lifecycle 配置 <span aria-hidden="true">↗</span>
          </button>
        </RefreshControls>
      </div>

      <article className="runtime-flow-panel lifecycle-flow-panel">
        <ProcessingFlowHeader
          title="Lifecycle 处理链路"
          description="Redis Signal 只负责唤醒 Mailbox。worker 获取该 Mailbox 的 lease 后，从队首串行裁决 Event；单条成功才移出队首，排空后才 XACK Signal。Alert 真实变化时，process_event 会条件触发 FinalHook。"
        />
        <div className="runtime-flow">
          {[
            "redis_signal",
            "lease",
            "mailbox_peek",
            "process_event",
            "mailbox_ack",
            "xack",
            "final_hook",
          ].map((step) => (
            <button
              type="button"
              className="runtime-node"
              key={step}
              title={lifecycleStepGuides[step]?.summary}
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
        stage="lifecycle"
        stageLabel="Lifecycle"
      />

      {runtime.isError ? (
        <div className="warning-banner">
          Lifecycle 当前状态加载失败：{errorMessage(runtime.error)}
        </div>
      ) : (
        <LifecycleRuntimeSnapshot redis={redis} loading={runtime.isLoading} />
      )}

      <div className="chart-grid runtime-charts">
        {panels.map((panel) => (
          <MetricPanelCard key={panel.id} panel={panel} />
        ))}
      </div>

      {configOpen && (
        <LifecycleConfigDialog
          config={config}
          returnFocusRef={configButtonRef}
          onClose={closeConfig}
        />
      )}
      {selectedStep && (
        <LifecycleNodeDrawer
          step={selectedStep}
          guide={lifecycleStepGuides[selectedStep]}
          panels={lifecycleNodePanels(panels, selectedStep)}
          snapshot={lifecycleNodeSnapshot(redis, config, selectedStep)}
          onClose={() => setSelectedStep(undefined)}
        />
      )}
    </section>
  );
}

function LifecycleRuntimeSnapshot({
  redis,
  loading,
}: {
  redis?: RedisInfrastructure;
  loading: boolean;
}) {
  const timeMode = useTimeMode();
  const groups = redis?.signalQueue.groups ?? [];
  const consumers = redis
    ? groups.reduce((total, group) => total + group.consumersCount, 0)
    : undefined;
  const pending = redis
    ? groups.reduce((total, group) => total + group.pending, 0)
    : undefined;
  const knownLag = !redis
    ? undefined
    : groups.every((group) => group.lag !== null)
      ? groups.reduce((total, group) => total + (group.lag ?? 0), 0)
      : null;
  const expectedGroup = groups.find((group) => group.expected);
  const approximateUnresolved = !expectedGroup
    ? undefined
    : expectedGroup.lag === null
      ? null
      : expectedGroup.lag + expectedGroup.pending;
  const stream = redis?.signalQueue.stream;

  return (
    <article className={`lifecycle-runtime-panel ${loading ? "loading" : ""}`}>
      <header>
        <div>
          <p className="eyebrow">CURRENT SNAPSHOT</p>
          <h2>Signal / Mailbox / Lock 当前状态</h2>
          <p>
            {redis?.snapshotAt
              ? `采样于 ${formatTime(redis.snapshotAt, timeMode)}`
              : loading
                ? "正在读取 Redis 运行状态…"
                : "尚无 Redis 运行快照"}
          </p>
        </div>
        <Link to="/infrastructure/redis?tab=signal">
          打开 Redis 工作台 <span aria-hidden="true">→</span>
        </Link>
      </header>

      <div className="lifecycle-state-grid">
        <LifecycleStateCard
          label="Signal Stream"
          status={redis?.signalQueue.status}
          value={displayNumber(stream?.length)}
          detail={`${groups.length} groups · Pending ${displayNumber(pending)} · Lag ${displayNumber(knownLag)}`}
          help="Signal Stream 当前保留的 entry 数；其中 Pending 与 Lag 含义不同。"
          to="/infrastructure/redis?tab=signal"
        />
        <LifecycleStateCard
          label="近似 Signal 积压"
          status={redis?.signalQueue.status}
          value={displayNumber(approximateUnresolved)}
          detail={`${displayNumber(redis?.mailbox.activeMailboxes)} 个活跃 Mailbox${redis?.mailbox.scanTruncated ? " · 扫描已截断" : ""}`}
          help="目标 Group 的 lag + pending；它是 Signal 数，不是精确 Event 数，允许 3 秒采样误差和冗余唤醒高估。"
          to="/infrastructure/redis?tab=signal"
        />
        <LifecycleStateCard
          label="Lease / Lock"
          status={redis?.leases.status}
          value={displayNumber(redis?.leases.activeLeases)}
          detail={`TTL ${formatDuration(redis?.leases.ttlSeconds)} · 续租 ${formatDuration(redis?.leases.renewIntervalSeconds)}`}
          help="当前有效的 Mailbox 分布式 lease 数，用于保证关联键内串行。"
          to="/infrastructure/redis?tab=lease"
        />
      </div>

      <section className="lifecycle-groups-panel">
        <header>
          <div>
            <h3>Consumer Groups</h3>
            <p>
              {redis?.signalQueue.streamKey ?? "Signal Stream 未配置或不可用"}
            </p>
          </div>
          <span>
            {redis ? groups.length : "—"} groups · {displayNumber(consumers)}
            {" consumers"}
          </span>
        </header>
        {groups.length === 0 ? (
          <div className="lifecycle-empty-state">
            {redis?.signalQueue.message ?? "当前没有 Consumer Group 数据"}
          </div>
        ) : (
          <div className="table-scroll">
            <table className="lifecycle-group-table">
              <thead>
                <tr>
                  <HelpTableHeader
                    label="Group"
                    help="消费 Signal Stream 的 Redis consumer group。"
                  />
                  <HelpTableHeader
                    label="Consumers"
                    help="该 group 当前登记的 consumer 数。"
                  />
                  <HelpTableHeader
                    label="Pending"
                    help="已投递但尚未 XACK、仍在 PEL 中的 entry 数。"
                  />
                  <HelpTableHeader
                    label="Lag"
                    help="尚未投递给该 group 的 Stream entry 数。"
                  />
                  <HelpTableHeader
                    label="Last delivered"
                    help="该 group 最近投递过的 Stream ID。"
                  />
                </tr>
              </thead>
              <tbody>
                {groups.map((group) => (
                  <tr key={group.name}>
                    <td>
                      <code>{group.name}</code>
                      {group.expected && <small>EXPECTED</small>}
                    </td>
                    <td>{displayNumber(group.consumersCount)}</td>
                    <td>{displayNumber(group.pending)}</td>
                    <td>{displayNumber(group.lag)}</td>
                    <td className="mono">{group.lastDeliveredId || "—"}</td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        )}
      </section>
    </article>
  );
}

function LifecycleStateCard({
  label,
  status,
  value,
  detail,
  help,
  to,
}: {
  label: string;
  status?: string;
  value: ReactNode;
  detail: ReactNode;
  help: string;
  to: string;
}) {
  return (
    <article className="lifecycle-state-card">
      <header>
        <HelpLabel label={label} help={help} />
        <StatusBadge value={status ?? "unknown"} />
      </header>
      <strong>{value}</strong>
      <p>{detail}</p>
      <Link to={to}>查看详情 →</Link>
    </article>
  );
}

function lifecycleNodePanels(panels: MetricPanel[], step: string) {
  const mapping: Record<string, string[]> = {
    redis_signal: ["messaging-inflight", "retry-rate"],
    lease: ["lifecycle-lease"],
    mailbox_peek: ["lifecycle-mailbox", "lifecycle-drain-p95"],
    process_event: [
      "lifecycle-results",
      "lifecycle-recent-alert-cache",
      "lifecycle-recent-alert-hit-ratio",
      "pipeline-average",
      "pipeline-p95",
      "pipeline-p99",
    ],
    mailbox_ack: ["lifecycle-mailbox"],
    xack: ["settled-rate"],
    final_hook: ["final-hook", "final-hook-p95"],
  };
  const operations: Record<string, string> = {
    mailbox_peek: "peek",
    mailbox_ack: "ack",
  };
  return panels
    .filter((panel) => (mapping[step] ?? []).includes(panel.id))
    .map((panel) =>
      panel.id === "lifecycle-mailbox" && operations[step]
        ? {
            ...panel,
            series: panel.series.filter(
              (series) => series.labels.linkd_operation === operations[step],
            ),
          }
        : panel,
    );
}

interface SnapshotItem {
  label: string;
  value: ReactNode;
  detail?: ReactNode;
}

function lifecycleNodeSnapshot(
  redis: RedisInfrastructure | undefined,
  config: LifecycleConfig,
  step: string,
): SnapshotItem[] {
  const signal = redis?.signalQueue;
  const mailbox = redis?.mailbox;
  const leases = redis?.leases;
  const stream = signal?.stream;
  const groups = signal?.groups ?? [];
  const pending = groups.reduce((total, group) => total + group.pending, 0);
  const expectedGroup = groups.find((group) => group.expected);
  const approximateUnresolved =
    expectedGroup?.lag === null || expectedGroup === undefined
      ? undefined
      : expectedGroup.lag + expectedGroup.pending;
  const mailboxConfig = asRecord(config.mailbox);
  const lockConfig = asRecord(config.lock);
  const outputKafka = asRecord(config.outputKafka);

  if (step === "redis_signal" || step === "xack") {
    return [
      { label: "状态", value: signal?.status ?? "unknown" },
      { label: "Stream", value: signal?.streamKey ?? "—" },
      { label: "Stream entries", value: displayNumber(stream?.length) },
      { label: "Pending", value: displayNumber(pending) },
      { label: "Consumer groups", value: displayNumber(groups.length) },
      { label: "Last generated ID", value: stream?.lastGeneratedId ?? "—" },
    ];
  }
  if (step === "lease") {
    return [
      { label: "状态", value: leases?.status ?? "unknown" },
      { label: "Active leases", value: displayNumber(leases?.activeLeases) },
      { label: "TTL", value: formatDuration(leases?.ttlSeconds) },
      {
        label: "Renew interval",
        value: formatDuration(leases?.renewIntervalSeconds),
      },
      { label: "Key prefix", value: stringValue(lockConfig.keyPrefix) },
    ];
  }
  if (step === "mailbox_peek" || step === "mailbox_ack") {
    return [
      { label: "状态", value: mailbox?.status ?? "unknown" },
      {
        label: "Approximate unresolved",
        value: displayNumber(approximateUnresolved),
      },
      {
        label: "Active mailboxes",
        value: displayNumber(mailbox?.activeMailboxes),
      },
      {
        label: "Max drain events",
        value: displayUnknown(mailboxConfig.maxDrainEvents),
      },
      { label: "Key prefix", value: stringValue(mailboxConfig.keyPrefix) },
    ];
  }
  if (step === "process_event") {
    return [
      { label: "Concurrency", value: displayUnknown(config.concurrency) },
      {
        label: "Approximate unresolved",
        value: displayNumber(approximateUnresolved),
      },
      {
        label: "Active mailboxes",
        value: displayNumber(mailbox?.activeMailboxes),
      },
    ];
  }
  return [
    { label: "Topic", value: stringValue(outputKafka.topic) },
    { label: "Client ID", value: stringValue(outputKafka.clientId) },
    {
      label: "Brokers",
      value: Array.isArray(outputKafka.brokers)
        ? outputKafka.brokers.join(", ")
        : "—",
    },
  ];
}

function LifecycleNodeDrawer({
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
                    lifecycleSnapshotHelp[item.label] ??
                    "当前节点对应的只读运行快照。"
                  }
                />
                <strong>{item.value}</strong>
                {item.detail && <small>{item.detail}</small>}
              </div>
            ))}
          </div>
          <h3>历史趋势</h3>
          <div className="chart-grid runtime-charts">
            {panels.map((panel) => (
              <MetricPanelCard key={panel.id} panel={panel} />
            ))}
          </div>
        </div>
      </aside>
    </div>
  );
}

function LifecycleConfigDialog({
  config,
  returnFocusRef,
  onClose,
}: {
  config: LifecycleConfig;
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
    <div className="lifecycle-dialog-layer">
      <div
        className="lifecycle-dialog-backdrop"
        aria-hidden="true"
        onMouseDown={onClose}
      />
      <section
        className="lifecycle-config-dialog"
        role="dialog"
        aria-modal="true"
        aria-labelledby="lifecycle-config-title"
      >
        <header>
          <div>
            <p className="eyebrow">EFFECTIVE CONFIG</p>
            <h2 id="lifecycle-config-title">Lifecycle 配置</h2>
            <span>本地 DevTools 读取到的脱敏有效配置</span>
          </div>
          <button
            ref={closeButtonRef}
            type="button"
            aria-label="关闭 Lifecycle 配置"
            onClick={onClose}
          >
            ×
          </button>
        </header>
        <div className="lifecycle-config-dialog-content">
          <JsonViewer
            value={config}
            description="Lifecycle 的脱敏有效配置，包含并发、Mailbox、lease 与 FinalHook 输出参数。"
          />
        </div>
      </section>
    </div>,
    document.body,
  );
}

function displayNumber(value: number | null | undefined): string {
  return value === null || value === undefined
    ? "未知"
    : value.toLocaleString();
}

function formatDuration(value: number | undefined): string {
  if (value === undefined) return "—";
  if (value < 60) return `${value}s`;
  return `${(value / 60).toFixed(value % 60 === 0 ? 0 : 1)}m`;
}

function displayUnknown(value: unknown): string {
  if (value === null || value === undefined || value === "") return "—";
  return String(value);
}

function stringValue(value: unknown): string {
  return typeof value === "string" && value ? value : "—";
}

function asRecord(value: unknown): Record<string, unknown> {
  return typeof value === "object" && value !== null
    ? (value as Record<string, unknown>)
    : {};
}

function errorMessage(error: unknown): string {
  return error instanceof Error ? error.message : "未知错误";
}
