import { useQuery } from "@tanstack/react-query";
import {
  type RefObject,
  useCallback,
  useEffect,
  useRef,
  useState,
} from "react";
import { createPortal } from "react-dom";
import { Link } from "react-router-dom";

import type {
  ControlPlaneRuntime,
  ControlPlaneTaskDefinition,
  ControlPlaneTaskId,
} from "../../shared/contracts";
import { getControlPlaneRuntime, getMetrics } from "../api";
import { JsonViewer } from "../components/JsonViewer";
import { MetricPanelCard } from "../components/MetricPanelCard";
import { MetricQueryControls } from "../components/MetricQueryControls";
import { RefreshControls } from "../components/RefreshControls";
import {
  defaultMetricCalculationWindowSeconds,
  defaultMetricRangeSeconds,
  metricRanges,
  metricStep,
} from "../metricRange";
import { useReportPageQueryFailure } from "../navigation";
import { formatTime, useTimeMode } from "../time";

const taskOrder: ControlPlaneTaskId[] = [
  "elasticsearch-schema-and-active-reconciler",
  "elasticsearch-bucket-manager",
  "elasticsearch-alert-archiver",
  "redis-stream-manager",
];

const taskMeta: Record<
  ControlPlaneTaskId,
  { title: string; short: string; description: string }
> = {
  "elasticsearch-schema-and-active-reconciler": {
    title: "Schema & Active Reconciler",
    short: "Schema / Active",
    description: "对账 index template、Active Alert 索引和静态 alias。",
  },
  "elasticsearch-bucket-manager": {
    title: "Bucket Manager",
    short: "Bucket",
    description: "维护 Event、AlertHistory 和 AlertLog 的当前预创建窗口。",
  },
  "elasticsearch-alert-archiver": {
    title: "Alert Archiver",
    short: "Archiver",
    description: "连续批量归档 Active 索引中的终态 Alert，并隔离单项失败。",
  },
  "redis-stream-manager": {
    title: "Redis Stream Manager",
    short: "Redis Stream",
    description: "采集 Signal Stream 状态并裁剪所有 Group 已确认的安全前缀。",
  },
};

type TaskState =
  "healthy" | "warning" | "stale" | "unobserved" | "unavailable" | "disabled";

export function ControlPlanePage() {
  const [rangeSeconds, setRangeSeconds] = useState(defaultMetricRangeSeconds);
  const [calculationWindowSeconds, setCalculationWindowSeconds] = useState(
    defaultMetricCalculationWindowSeconds,
  );
  const [instance, setInstance] = useState("");
  const [autoRefresh, setAutoRefresh] = useState(true);
  const [configOpen, setConfigOpen] = useState(false);
  const configButtonRef = useRef<HTMLButtonElement>(null);
  const closeConfig = useCallback(() => setConfigOpen(false), []);
  const interval = autoRefresh ? 15_000 : false;
  const runtime = useQuery({
    queryKey: ["runtime-control-plane", rangeSeconds, instance],
    queryFn: () =>
      getControlPlaneRuntime({
        rangeSeconds,
        instance: instance || undefined,
      }),
    refetchInterval: interval,
  });
  const metrics = useQuery({
    queryKey: [
      "control-plane-metrics",
      rangeSeconds,
      calculationWindowSeconds,
      instance,
    ],
    queryFn: () => {
      const to = new Date();
      return getMetrics({
        from: new Date(to.getTime() - rangeSeconds * 1000),
        to,
        step: metricStep(rangeSeconds),
        calculationWindowSeconds,
        instance: instance || undefined,
      });
    },
    refetchInterval: interval,
  });
  useReportPageQueryFailure(runtime.isError || metrics.isError);

  const data = runtime.data;
  const tasks = taskOrder.flatMap((id) => {
    const task = data?.tasks.find((item) => item.id === id);
    return task ? [task] : [];
  });
  const states = tasks.map((task) => taskState(data, task));
  const attentionCount = states.filter((state) =>
    ["warning", "stale", "unobserved", "unavailable"].includes(state),
  ).length;
  const processes = data?.processes.items ?? [];
  const scopedProcesses = instance
    ? processes.filter((item) => item.instance === instance)
    : processes;
  const processInstances = [...new Set(processes.map((item) => item.instance))];
  const enabledCount = tasks.filter((task) => task.enabled).length;
  const failureCount = tasks.reduce(
    (total, task) => total + taskRunCount(data, task.id, "failed"),
    0,
  );
  const panels = (metrics.data?.panels ?? []).filter((panel) =>
    [
      "control-plane-task-runs",
      "control-plane-task-average",
      "control-plane-task-p95",
      "control-plane-archive-rate",
      "control-plane-redis-trim-rate",
    ].includes(panel.id),
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
    <section className="control-plane-page">
      <div className="page-heading control-plane-page-heading">
        <div>
          <p className="eyebrow">MANAGEMENT TASKS</p>
          <h1>Control Plane</h1>
          <p>查看单例管理任务的归属、依赖、执行新鲜度和收敛工作量。</p>
        </div>
        <div className="control-plane-controls">
          <label>
            <span>INSTANCE</span>
            <select
              aria-label="Control Plane instance"
              value={instance}
              onChange={(event) => setInstance(event.target.value)}
            >
              <option value="">全部控制面实例</option>
              {processInstances.map((value) => (
                <option key={value} value={value}>
                  {value}
                </option>
              ))}
            </select>
          </label>
          <MetricQueryControls
            rangeSeconds={rangeSeconds}
            calculationWindowSeconds={calculationWindowSeconds}
            onRangeChange={setRangeSeconds}
            onCalculationWindowChange={setCalculationWindowSeconds}
          />
          <RefreshControls
            status={data?.status}
            lastSuccessfulAt={lastSuccessfulAt}
            isFetching={refreshing}
            autoRefresh={autoRefresh}
            intervalSeconds={15}
            onRefresh={refresh}
            onToggleAutoRefresh={() => setAutoRefresh((value) => !value)}
          >
            <button
              ref={configButtonRef}
              type="button"
              onClick={() => setConfigOpen(true)}
            >
              任务配置 <span aria-hidden="true">↗</span>
            </button>
          </RefreshControls>
        </div>
      </div>

      {runtime.isError && (
        <div className="error-banner">
          控制面状态加载失败：{errorMessage(runtime.error)}
        </div>
      )}

      <div className="control-plane-summary">
        <SummaryCard
          label="控制面进程"
          value={`${scopedProcesses.filter((item) => item.up).length}/${scopedProcesses.length || "—"}`}
          detail="up / discovered"
        />
        <SummaryCard
          label="已启用任务"
          value={data ? String(enabledCount) : "—"}
          detail="3 个 ES 任务 + Redis Stream"
        />
        <SummaryCard
          label="需要关注"
          value={data ? String(attentionCount) : "—"}
          detail="stale、未采样、异常或重复 owner"
          tone={attentionCount > 0 ? "warning" : "normal"}
        />
        <SummaryCard
          label="时间窗失败"
          value={data ? formatCount(failureCount) : "—"}
          detail={`最近 ${rangeLabel(rangeSeconds)}`}
          tone={failureCount > 0 ? "danger" : "normal"}
        />
      </div>

      <div className="control-plane-fault-boundary" role="note">
        <strong>共享监督与故障域</strong>
        <span>
          当前没有 Leader Election，只允许一个控制面
          owner；任一任务失败会取消同进程其他任务。 all-in-one 模式还会使
          Cleaner 与 Lifecycle 一并退出。
        </span>
      </div>

      <TaskDependencyMap tasks={tasks} />

      <div className="control-plane-task-grid">
        {tasks.map((task) => (
          <TaskCard key={task.id} runtime={data} task={task} />
        ))}
      </div>

      {metrics.isError && (
        <div className="warning-banner">
          任务历史指标加载失败：{errorMessage(metrics.error)}
        </div>
      )}
      <div className="chart-grid control-plane-charts">
        {panels.map((panel) => (
          <MetricPanelCard key={panel.id} panel={panel} />
        ))}
      </div>

      {configOpen && data && (
        <ControlPlaneConfigDialog
          tasks={data.tasks}
          returnFocusRef={configButtonRef}
          onClose={closeConfig}
        />
      )}
    </section>
  );
}

function TaskDependencyMap({ tasks }: { tasks: ControlPlaneTaskDefinition[] }) {
  const byID = new Map(tasks.map((task) => [task.id, task]));
  return (
    <article className="control-plane-dependencies">
      <header>
        <div>
          <p className="eyebrow">TASK OWNERSHIP</p>
          <h2>任务依赖与故障边界</h2>
        </div>
        <span>同一进程监督</span>
      </header>
      <div className="control-plane-dependency-layout">
        <div className="control-plane-es-chain">
          {taskOrder.slice(0, 3).map((id, index) => (
            <div key={id} className="control-plane-chain-step">
              {index > 0 && (
                <span className="control-plane-chain-arrow" aria-hidden="true">
                  ↓
                </span>
              )}
              <div className={byID.get(id)?.enabled ? "enabled" : "disabled"}>
                <strong>{taskMeta[id].short}</strong>
                <small>
                  {index === 0
                    ? "Schema 与 Active 资源"
                    : index === 1
                      ? "当前及相邻时间桶"
                      : "消费已有 History write alias"}
                </small>
              </div>
            </div>
          ))}
        </div>
        <div
          className={`control-plane-redis-node ${byID.get("redis-stream-manager")?.enabled ? "enabled" : "disabled"}`}
        >
          <strong>Redis Stream</strong>
          <small>逻辑独立，仍共享进程退出边界</small>
        </div>
      </div>
    </article>
  );
}

function TaskCard({
  runtime,
  task,
}: {
  runtime?: ControlPlaneRuntime;
  task: ControlPlaneTaskDefinition;
}) {
  const timeMode = useTimeMode();
  const state = taskState(runtime, task);
  const owners = taskOwners(runtime, task.id);
  const lastSuccess = taskLastSuccess(runtime, task.id);
  const successCount = taskRunCount(runtime, task.id, "succeeded");
  const failureCount = taskRunCount(runtime, task.id, "failed");
  const average = taskMetric(runtime, "averageDuration", task.id);
  const p95 = taskMetric(runtime, "p95Duration", task.id);
  return (
    <article className={`control-plane-task-card state-${state}`}>
      <header>
        <div>
          <p className="eyebrow">{task.id}</p>
          <h2>{taskMeta[task.id].title}</h2>
          <p>{taskMeta[task.id].description}</p>
        </div>
        <span className={`control-plane-task-state ${state}`}>
          {taskStateLabel(state)}
        </span>
      </header>

      <dl className="control-plane-task-facts">
        <TaskFact
          label="OWNER"
          value={
            !task.enabled
              ? "未启用"
              : owners.length > 0
                ? owners.join(", ")
                : "尚未发现"
          }
          detail={
            owners.length > 1
              ? `${owners.length} 个 owner，当前不安全`
              : undefined
          }
        />
        <TaskFact
          label={
            task.id === "elasticsearch-alert-archiver"
              ? "IDLE / RETRY"
              : "INTERVAL"
          }
          value={formatDuration(task.intervalSeconds)}
        />
        <TaskFact
          label="LAST SUCCESS"
          value={
            lastSuccess
              ? formatTime(new Date(lastSuccess * 1000).toISOString(), timeMode)
              : "尚未采样"
          }
          detail={lastSuccess ? `${formatAge(lastSuccess)}前` : undefined}
        />
        <TaskFact
          label="RUNS"
          value={`${formatCount(successCount)} 成功 / ${formatCount(failureCount)} 失败`}
        />
        <TaskFact label="AVERAGE" value={formatSeconds(average)} />
        <TaskFact label="P95" value={formatSeconds(p95)} />
      </dl>

      {task.id === "elasticsearch-schema-and-active-reconciler" && (
        <div className="control-plane-task-note">
          <strong>对账范围</strong>
          <span>Schema、index template、Active Alert 索引与静态 alias。</span>
        </div>
      )}
      {task.id === "elasticsearch-bucket-manager" && (
        <BucketWorkload task={task} />
      )}
      {task.id === "elasticsearch-alert-archiver" && (
        <ArchiveWorkload runtime={runtime} task={task} />
      )}
      {task.id === "redis-stream-manager" && (
        <RedisWorkload runtime={runtime} task={task} />
      )}

      <footer>
        <span>
          配置来源：
          {task.configSource === "explicit"
            ? "显式配置"
            : task.configSource === "default"
              ? "Repository 默认启用"
              : "未启用"}
        </span>
        {task.id.startsWith("elasticsearch-") ? (
          <Link to="/storage/elasticsearch">查看 ES 资源 →</Link>
        ) : (
          <Link to="/infrastructure/redis?tab=signal">
            查看 Signal Stream →
          </Link>
        )}
      </footer>
    </article>
  );
}

function BucketWorkload({ task }: { task: ControlPlaneTaskDefinition }) {
  return (
    <div className="control-plane-task-note">
      <strong>预创建窗口</strong>
      <span>
        Event {task.settings.eventBucketDays}d · AlertHistory{" "}
        {task.settings.alertHistoryBucketDays}d · AlertLog{" "}
        {task.settings.alertLogBucketDays}d
      </span>
      <small>
        past {task.settings.precreatePastBuckets} / future{" "}
        {task.settings.precreateFutureBuckets}· 每类最多{" "}
        {task.settings.maxBucketsPerEntity} 桶
      </small>
    </div>
  );
}

function ArchiveWorkload({
  runtime,
  task,
}: {
  runtime?: ControlPlaneRuntime;
  task: ControlPlaneTaskDefinition;
}) {
  const backlog = runtime?.archive.backlog ?? null;
  const batchSize = Number(task.settings.archiveBatchSize);
  const workerCount = Number(task.settings.archiveWorkerCount);
  const lastScanned = firstMetric(runtime, "archiveLastScanned");
  const lastArchived = firstMetric(runtime, "archiveLastBatch");
  const lastFailed = firstMetric(runtime, "archiveLastFailed");
  const batches = backlog === null ? null : Math.ceil(backlog / batchSize);
  return (
    <div className="control-plane-workload">
      <TaskFact label="待归档" value={nullableCount(backlog)} />
      <TaskFact label="最近扫描" value={nullableCount(lastScanned)} />
      <TaskFact label="最近归档" value={nullableCount(lastArchived)} />
      <TaskFact label="最近失败" value={nullableCount(lastFailed)} />
      <TaskFact label="批量上限" value={formatCount(batchSize)} />
      <TaskFact label="并发 Worker" value={formatCount(workerCount)} />
      <TaskFact label="至少剩余批次" value={nullableCount(batches)} />
      {runtime?.archive.status === "unavailable" && (
        <p>{runtime.archive.message ?? "待归档工作量不可用"}</p>
      )}
      {backlog !== null && backlog >= batchSize && (
        <p>任务将连续处理后续批次，批次之间不会等待空闲间隔。</p>
      )}
    </div>
  );
}

function RedisWorkload({
  runtime,
  task,
}: {
  runtime?: ControlPlaneRuntime;
  task: ControlPlaneTaskDefinition;
}) {
  const required = firstMetric(runtime, "trimRequired");
  const safe = firstMetric(runtime, "trimSafe");
  const lastEntries = firstMetric(runtime, "trimLastEntries");
  const oldestPending = firstMetric(runtime, "oldestPendingAge");
  const redis = runtime?.redis;
  return (
    <div className="control-plane-workload redis-workload">
      <TaskFact
        label="裁剪决策"
        value={trimDecision(required, safe, redis?.expectedGroupPresent)}
      />
      <TaskFact label="最近裁剪" value={nullableCount(lastEntries)} />
      <TaskFact
        label="ENTRIES / MAX"
        value={`${nullableCount(redis?.entries)} / ${nullableCount(redis?.maxEntries ?? Number(task.settings.maxEntries))}`}
      />
      <TaskFact label="超限" value={nullableCount(redis?.entriesAboveMax)} />
      <TaskFact label="PENDING" value={nullableCount(redis?.pending)} />
      <TaskFact label="MAX LAG" value={nullableCount(redis?.maxLag)} />
      {required === 1 && safe !== 1 && (
        <p>
          {redis?.expectedGroupPresent === false
            ? "预期 Consumer Group 缺失，控制面不会裁剪。"
            : `当前未形成安全边界；最老 Pending ${formatSeconds(oldestPending)}。`}
        </p>
      )}
    </div>
  );
}

function ControlPlaneConfigDialog({
  tasks,
  returnFocusRef,
  onClose,
}: {
  tasks: ControlPlaneTaskDefinition[];
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
        className="lifecycle-config-dialog control-plane-config-dialog"
        role="dialog"
        aria-modal="true"
        aria-labelledby="control-plane-config-title"
      >
        <header>
          <div>
            <p className="eyebrow">EFFECTIVE CONFIG</p>
            <h2 id="control-plane-config-title">控制面任务配置</h2>
            <span>只展示生效周期、批次和资源边界，不包含凭据</span>
          </div>
          <button
            ref={closeButtonRef}
            type="button"
            aria-label="关闭控制面任务配置"
            onClick={onClose}
          >
            ×
          </button>
        </header>
        <div className="lifecycle-config-dialog-content">
          <JsonViewer
            value={tasks}
            description="Elasticsearch Repository 会自动启用三个任务并使用默认配置；Redis Stream 任务必须显式声明。"
          />
        </div>
      </section>
    </div>,
    document.body,
  );
}

function SummaryCard({
  label,
  value,
  detail,
  tone = "normal",
}: {
  label: string;
  value: string;
  detail: string;
  tone?: "normal" | "warning" | "danger";
}) {
  return (
    <article className={`control-plane-summary-card ${tone}`}>
      <span>{label}</span>
      <strong>{value}</strong>
      <small>{detail}</small>
    </article>
  );
}

function TaskFact({
  label,
  value,
  detail,
}: {
  label: string;
  value: string;
  detail?: string;
}) {
  return (
    <div className="control-plane-task-fact">
      <span>{label}</span>
      <strong>{value}</strong>
      {detail && <small>{detail}</small>}
    </div>
  );
}

function taskState(
  runtime: ControlPlaneRuntime | undefined,
  task: ControlPlaneTaskDefinition,
): TaskState {
  if (!task.enabled) return "disabled";
  if (!runtime || runtime.metrics.status === "unavailable")
    return "unavailable";
  const owners = taskOwners(runtime, task.id);
  const lastSuccess = taskLastSuccess(runtime, task.id);
  if (owners.length === 0 || !lastSuccess) return "unobserved";
  if (Date.now() / 1000 - lastSuccess > Math.max(task.intervalSeconds * 2, 30))
    return "stale";
  if (owners.length > 1 || taskRunCount(runtime, task.id, "failed") > 0)
    return "warning";
  return "healthy";
}

function taskOwners(
  runtime: ControlPlaneRuntime | undefined,
  task: ControlPlaneTaskId,
): string[] {
  const values = metricSeries(runtime, "active")
    .filter((sample) => sample.labels.linkd_task === task && sample.value === 1)
    .map((sample) => sample.labels.instance)
    .filter((value): value is string => Boolean(value));
  return [...new Set(values)].sort();
}

function taskLastSuccess(
  runtime: ControlPlaneRuntime | undefined,
  task: ControlPlaneTaskId,
): number | undefined {
  return maximumMetric(runtime, "lastSuccess", task);
}

function taskRunCount(
  runtime: ControlPlaneRuntime | undefined,
  task: ControlPlaneTaskId,
  outcome: "succeeded" | "failed",
): number {
  return metricSeries(runtime, "runCount")
    .filter(
      (sample) =>
        sample.labels.linkd_task === task &&
        sample.labels.linkd_outcome === outcome,
    )
    .reduce((total, sample) => total + (sample.value ?? 0), 0);
}

function taskMetric(
  runtime: ControlPlaneRuntime | undefined,
  key: string,
  task: ControlPlaneTaskId,
): number | undefined {
  return maximumMetric(runtime, key, task);
}

function maximumMetric(
  runtime: ControlPlaneRuntime | undefined,
  key: string,
  task?: ControlPlaneTaskId,
): number | undefined {
  const values = metricSeries(runtime, key)
    .filter((sample) => !task || sample.labels.linkd_task === task)
    .map((sample) => sample.value)
    .filter((value): value is number => value !== null);
  return values.length > 0 ? Math.max(...values) : undefined;
}

function firstMetric(
  runtime: ControlPlaneRuntime | undefined,
  key: string,
): number | undefined {
  return maximumMetric(runtime, key);
}

function metricSeries(runtime: ControlPlaneRuntime | undefined, key: string) {
  return runtime?.metrics.series[key] ?? [];
}

function taskStateLabel(state: TaskState): string {
  const labels: Record<TaskState, string> = {
    healthy: "正常",
    warning: "需关注",
    stale: "STALE",
    unobserved: "未采样",
    unavailable: "不可用",
    disabled: "未启用",
  };
  return labels[state];
}

function trimDecision(
  required: number | undefined,
  safe: number | undefined,
  expectedGroupPresent: boolean | null | undefined,
): string {
  if (required === undefined) return "尚未采样";
  if (required === 0) return "无需裁剪";
  if (safe === 1) return "可安全裁剪";
  if (expectedGroupPresent === false) return "Group 缺失，已阻止";
  return "安全边界不可用";
}

function formatDuration(seconds: number): string {
  if (seconds < 60) return `${seconds}s`;
  if (seconds < 3600) return `${seconds / 60}m`;
  return `${seconds / 3600}h`;
}

function formatSeconds(value: number | undefined): string {
  return value === undefined ? "—" : `${value.toFixed(value < 1 ? 3 : 2)}s`;
}

function formatAge(timestampSeconds: number): string {
  const seconds = Math.max(0, Math.round(Date.now() / 1000 - timestampSeconds));
  if (seconds < 60) return `${seconds} 秒`;
  if (seconds < 3600) return `${Math.floor(seconds / 60)} 分钟`;
  return `${Math.floor(seconds / 3600)} 小时`;
}

function nullableCount(value: number | null | undefined): string {
  return value === null || value === undefined ? "—" : formatCount(value);
}

function formatCount(value: number): string {
  return new Intl.NumberFormat("en-US", { maximumFractionDigits: 2 }).format(
    value,
  );
}

function rangeLabel(seconds: number): string {
  return (
    metricRanges.find((range) => range.seconds === seconds)?.label ??
    `${seconds}s`
  );
}

function errorMessage(error: unknown): string {
  return error instanceof Error ? error.message : "未知错误";
}
