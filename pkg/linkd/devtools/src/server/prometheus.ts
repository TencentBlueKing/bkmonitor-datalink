import type { MetricPanel, MetricsResponse } from "../shared/contracts.js";
import type { DevtoolsConfig } from "./config.js";

interface PrometheusResponse {
  status: "success" | "error";
  error?: string;
  data?: {
    result: Array<{
      metric: Record<string, string>;
      values: Array<[number, string]>;
    }>;
  };
}

interface PrometheusInstantResponse {
  status: "success" | "error";
  error?: string;
  data?: {
    result: Array<{
      metric: Record<string, string>;
      value: [number, string];
    }>;
  };
}

interface PanelDefinition {
  id: string;
  title: string;
  unit: string;
  kind: MetricPanel["kind"];
  query: (selector: string, window: string) => string;
  partitioned?: boolean;
}

const panelDefinitions: PanelDefinition[] = [
  {
    id: "received-rate",
    title: "消息拉取速率",
    unit: "message/s",
    kind: "area",
    partitioned: true,
    query: (selector, window) =>
      `sum(rate(linkd_messaging_received_messages_total${selector}[${window}])) by (linkd_stage, linkd_event_source_id, messaging_kafka_partition)`,
  },
  {
    id: "settled-rate",
    title: "消息确认速率",
    unit: "message/s",
    kind: "area",
    partitioned: true,
    query: (selector, window) =>
      `sum(rate(linkd_messaging_settled_messages_total${selector}[${window}])) by (linkd_stage, linkd_event_source_id, messaging_kafka_partition, linkd_outcome)`,
  },
  {
    id: "cleaner-steps",
    title: "Cleaner 步骤速率",
    unit: "item/s",
    kind: "area",
    query: (selector, window) =>
      `sum(rate(linkd_cleaner_step_items_total${selector}[${window}])) by (linkd_event_source_id, linkd_step, linkd_outcome)`,
  },
  {
    id: "cleaner-step-average",
    title: "Cleaner 步骤平均耗时",
    unit: "s",
    kind: "line",
    query: (selector, window) =>
      `sum(rate(linkd_cleaner_step_duration_seconds_sum${selector}[${window}])) by (linkd_event_source_id, linkd_step, linkd_outcome) / sum(rate(linkd_cleaner_step_duration_seconds_count${selector}[${window}])) by (linkd_event_source_id, linkd_step, linkd_outcome)`,
  },
  {
    id: "cleaner-step-p95",
    title: "Cleaner 步骤 P95",
    unit: "s",
    kind: "line",
    query: (selector, window) =>
      `histogram_quantile(0.95, sum(rate(linkd_cleaner_step_duration_seconds_bucket${selector}[${window}])) by (le, linkd_event_source_id, linkd_step, linkd_outcome))`,
  },
  {
    id: "cleaner-step-p99",
    title: "Cleaner 步骤 P99",
    unit: "s",
    kind: "line",
    query: (selector, window) =>
      `histogram_quantile(0.99, sum(rate(linkd_cleaner_step_duration_seconds_bucket${selector}[${window}])) by (le, linkd_event_source_id, linkd_step, linkd_outcome))`,
  },
  {
    id: "lifecycle-results",
    title: "Lifecycle 裁决速率",
    unit: "event/s",
    kind: "area",
    query: (selector, window) =>
      `sum(rate(linkd_lifecycle_result_items_total${selector}[${window}])) by (linkd_event_source_id, linkd_outcome, linkd_reason_code)`,
  },
  {
    id: "lifecycle-mailbox",
    title: "Mailbox 操作速率",
    unit: "operation/s",
    kind: "area",
    query: (selector, window) =>
      `sum(rate(linkd_lifecycle_mailbox_operations_total${selector}[${window}])) by (linkd_event_source_id, linkd_operation, linkd_outcome)`,
  },
  {
    id: "lifecycle-drain-p95",
    title: "Mailbox 单次 Drain P95",
    unit: "event",
    kind: "line",
    query: (selector, window) =>
      `histogram_quantile(0.95, sum(rate(linkd_lifecycle_mailbox_drained_events_bucket${selector}[${window}])) by (le, linkd_event_source_id, linkd_outcome))`,
  },
  {
    id: "lifecycle-lease",
    title: "Lease 操作速率",
    unit: "operation/s",
    kind: "area",
    query: (selector, window) =>
      `sum(rate(linkd_lifecycle_lease_operations_total${selector}[${window}])) by (linkd_operation, linkd_outcome)`,
  },
  {
    id: "lifecycle-recent-alert-cache",
    title: "Recent Alert 缓存操作速率",
    unit: "operation/s",
    kind: "area",
    query: (selector, window) =>
      `sum(rate(linkd_lifecycle_recent_alert_cache_operations_total${selector}[${window}])) by (linkd_operation, linkd_outcome)`,
  },
  {
    id: "lifecycle-recent-alert-hit-ratio",
    title: "Recent Alert 缓存命中率",
    unit: "%",
    kind: "line",
    query: (selector, window) =>
      `100 * sum(rate(linkd_lifecycle_recent_alert_cache_operations_total${mergeSelector(selector, 'linkd_operation=~"get_.*",linkd_outcome="hit"')}[${window}])) / clamp_min(sum(rate(linkd_lifecycle_recent_alert_cache_operations_total${mergeSelector(selector, 'linkd_operation=~"get_.*",linkd_outcome=~"hit|miss"')}[${window}])), 0.000000001)`,
  },
  {
    id: "final-hook",
    title: "FinalHook 速率",
    unit: "operation/s",
    kind: "line",
    query: (selector, window) =>
      `sum(rate(linkd_final_hook_operations_total${selector}[${window}])) by (linkd_event_source_id, messaging_system, linkd_outcome)`,
  },
  {
    id: "final-hook-p95",
    title: "FinalHook P95",
    unit: "s",
    kind: "line",
    query: (selector, window) =>
      `histogram_quantile(0.95, sum(rate(linkd_final_hook_duration_seconds_bucket${selector}[${window}])) by (le, linkd_event_source_id, messaging_system, linkd_outcome))`,
  },
  {
    id: "pipeline-throughput",
    title: "阶段处理速率",
    unit: "attempt/s",
    kind: "area",
    query: (selector, window) =>
      `sum(rate(linkd_pipeline_attempts_total${selector}[${window}])) by (linkd_stage, linkd_outcome)`,
  },
  {
    id: "pipeline-average",
    title: "阶段处理平均耗时",
    unit: "s",
    kind: "line",
    query: (selector, window) =>
      `sum(rate(linkd_pipeline_attempt_duration_seconds_sum${selector}[${window}])) by (linkd_stage) / sum(rate(linkd_pipeline_attempt_duration_seconds_count${selector}[${window}])) by (linkd_stage)`,
  },
  {
    id: "pipeline-p95",
    title: "阶段处理 P95",
    unit: "s",
    kind: "line",
    query: (selector, window) =>
      `histogram_quantile(0.95, sum(rate(linkd_pipeline_attempt_duration_seconds_bucket${selector}[${window}])) by (le, linkd_stage))`,
  },
  {
    id: "pipeline-p99",
    title: "阶段处理 P99",
    unit: "s",
    kind: "line",
    query: (selector, window) =>
      `histogram_quantile(0.99, sum(rate(linkd_pipeline_attempt_duration_seconds_bucket${selector}[${window}])) by (le, linkd_stage))`,
  },
  {
    id: "messaging-inflight",
    title: "分阶段在途消息",
    unit: "message",
    kind: "area",
    query: (selector) =>
      `sum(linkd_messaging_inflight${selector}) by (linkd_stage, messaging_system)`,
  },
  {
    id: "retry-rate",
    title: "重试速率",
    unit: "retry/s",
    kind: "line",
    query: (selector, window) =>
      `sum(rate(linkd_pipeline_retries_total${selector}[${window}])) by (linkd_stage, linkd_reason_code)`,
  },
  {
    id: "settlement-gap",
    title: "确认阻塞消息",
    unit: "message",
    kind: "area",
    query: (selector) =>
      `sum(linkd_messaging_settlement_gap${selector}) by (messaging_system)`,
  },
  {
    id: "store-errors",
    title: "存储异常速率",
    unit: "operation/s",
    kind: "line",
    query: (selector, window) =>
      `sum(rate(linkd_store_operations_total${mergeSelector(selector, 'linkd_outcome!~"succeeded|not_found"')}[${window}])) by (linkd_object_type, linkd_operation, linkd_outcome)`,
  },
  {
    id: "control-plane-task-runs",
    title: "控制面任务执行次数",
    unit: "次",
    kind: "area",
    query: (selector, window) =>
      `round(sum(increase(linkd_control_plane_task_runs_total${selector}[${window}])) by (linkd_task, linkd_outcome))`,
  },
  {
    id: "control-plane-task-average",
    title: "控制面任务平均耗时",
    unit: "s",
    kind: "line",
    query: (selector, window) =>
      `sum(rate(linkd_control_plane_task_run_duration_seconds_sum${selector}[${window}])) by (linkd_task) / sum(rate(linkd_control_plane_task_run_duration_seconds_count${selector}[${window}])) by (linkd_task)`,
  },
  {
    id: "control-plane-task-p95",
    title: "控制面任务 P95",
    unit: "s",
    kind: "line",
    query: (selector, window) =>
      `histogram_quantile(0.95, sum(rate(linkd_control_plane_task_run_duration_seconds_bucket${selector}[${window}])) by (le, linkd_task))`,
  },
  {
    id: "control-plane-archive-rate",
    title: "Alert 归档速率",
    unit: "alert/s",
    kind: "area",
    query: (selector, window) =>
      `sum(rate(linkd_elasticsearch_alert_archiver_archived_alerts_total${selector}[${window}])) by (instance)`,
  },
  {
    id: "control-plane-redis-trim-rate",
    title: "Redis Stream 裁剪速率",
    unit: "entry/s",
    kind: "area",
    query: (selector, window) =>
      `sum(rate(linkd_redis_stream_trimmed_entries_total${selector}[${window}])) by (instance)`,
  },
  {
    id: "goroutines",
    title: "Goroutine",
    unit: "goroutine",
    kind: "line",
    query: (selector) => `sum(go_goroutines${selector}) by (instance)`,
  },
  {
    id: "rss",
    title: "进程 RSS",
    unit: "bytes",
    kind: "line",
    query: (selector) =>
      `sum(process_resident_memory_bytes${selector}) by (instance)`,
  },
];

export class PrometheusConnector {
  private readonly baseUrl?: string;
  private readonly timeoutMilliseconds: number;
  private readonly headers: Record<string, string>;

  constructor(config: DevtoolsConfig) {
    this.baseUrl = config.prometheus?.baseUrl.replace(/\/$/, "");
    this.timeoutMilliseconds = config.query.timeoutMilliseconds;
    this.headers = config.prometheus ? authHeaders(config.prometheus.auth) : {};
  }

  async panels(
    from: Date,
    to: Date,
    step: number,
    scope?:
      | string
      | { instance?: string; eventSourceId?: string; partition?: number },
  ): Promise<MetricsResponse> {
    if (to <= from) throw new Error("metrics to must be later than from");
    const normalizedScope =
      typeof scope === "string" ? { instance: scope } : (scope ?? {});
    const baseMatchers = [
      normalizedScope.instance
        ? `instance=${JSON.stringify(normalizedScope.instance)}`
        : undefined,
      normalizedScope.eventSourceId
        ? `linkd_event_source_id=${JSON.stringify(normalizedScope.eventSourceId)}`
        : undefined,
    ].filter((value): value is string => Boolean(value));
    const laneMatchers = [
      ...baseMatchers,
      normalizedScope.partition !== undefined
        ? `messaging_kafka_partition=${JSON.stringify(String(normalizedScope.partition))}`
        : undefined,
    ].filter((value): value is string => Boolean(value));
    const rateWindow = `${Math.max(300, step * 4)}s`;
    const panels = await Promise.all(
      panelDefinitions.map(async (definition): Promise<MetricPanel> => {
        if (!this.baseUrl) return unavailable(definition, "Prometheus 未配置");
        try {
          const selector = matcherSelector(
            definition.partitioned ? laneMatchers : baseMatchers,
          );
          const query = definition.query(selector, rateWindow);
          const response = await this.queryRange(query, from, to, step);
          const series = (response.data?.result ?? []).map((item, index) => ({
            name: seriesName(item.metric, index),
            labels: item.metric,
            points: item.values.map(
              ([timestamp, value]) =>
                [timestamp, finiteNumber(value)] as [number, number | null],
            ),
          }));
          if (series.length === 0)
            return unavailable(definition, "查询范围内没有对应时序");
          return { ...definition, status: "available", series };
        } catch {
          return unavailable(definition, "Prometheus 查询失败");
        }
      }),
    );
    return { from: from.toISOString(), to: to.toISOString(), step, panels };
  }

  async processes(): Promise<Record<string, unknown>> {
    if (!this.baseUrl)
      return { status: "unavailable", message: "Prometheus 未配置", items: [] };
    try {
      const [targets, up] = await Promise.all([
        this.queryInstant('target_info{service_name="linkd"}'),
        this.queryInstant("up"),
      ]);
      const upByTarget = new Map(
        (up.data?.result ?? []).map((item) => [
          targetKey(item.metric),
          finiteNumber(item.value[1]),
        ]),
      );
      const items = (targets.data?.result ?? []).map((item) => ({
        instance: item.metric.instance ?? "unknown",
        job: item.metric.job ?? "unknown",
        serviceInstanceId:
          item.metric.service_instance_id ?? item.metric.instance ?? "unknown",
        role: item.metric.linkd_role ?? "unknown",
        version: item.metric.service_version ?? "unknown",
        up: upByTarget.get(targetKey(item.metric)) === 1,
      }));
      return { status: "available", items };
    } catch {
      return {
        status: "unavailable",
        message: "Prometheus 进程查询失败",
        items: [],
      };
    }
  }

  async cleanerSnapshot(): Promise<Record<string, unknown>> {
    return this.runtimeSnapshot({
      flows: "linkd_cleaner_flow_active",
      received:
        'sum(rate(linkd_messaging_received_messages_total{linkd_stage="clean"}[5m])) by (instance,linkd_event_source_id,messaging_kafka_partition)',
      receivedBytes:
        'sum(rate(linkd_messaging_received_bytes_total{linkd_stage="clean"}[5m])) by (instance,linkd_event_source_id,messaging_kafka_partition)',
      settled:
        'sum(rate(linkd_messaging_settled_messages_total{linkd_stage="clean",linkd_outcome="succeeded"}[5m])) by (instance,linkd_event_source_id,messaging_kafka_partition)',
      inflight: 'linkd_messaging_lane_inflight{linkd_stage="clean"}',
      paused: 'linkd_messaging_lane_paused{linkd_stage="clean"}',
      owned: 'linkd_messaging_lane_owned{linkd_stage="clean"}',
      steps:
        "sum(rate(linkd_cleaner_step_items_total[5m])) by (instance,linkd_event_source_id,linkd_step,linkd_outcome)",
    });
  }

  async lifecycleSnapshot(): Promise<Record<string, unknown>> {
    return this.runtimeSnapshot({
      inflight: 'linkd_messaging_inflight{linkd_stage="lifecycle"}',
      retry: 'linkd_messaging_retry_items{linkd_stage="lifecycle"}',
      received:
        'sum(rate(linkd_messaging_received_messages_total{linkd_stage="lifecycle"}[5m])) by (instance)',
      settled:
        'sum(rate(linkd_messaging_settled_messages_total{linkd_stage="lifecycle",linkd_outcome="succeeded"}[5m])) by (instance)',
      results:
        "sum(rate(linkd_lifecycle_result_items_total[5m])) by (instance,linkd_event_source_id,linkd_event_action,linkd_event_state,linkd_outcome,linkd_reason_code)",
      mailbox:
        "sum(rate(linkd_lifecycle_mailbox_operations_total[5m])) by (instance,linkd_event_source_id,linkd_operation,linkd_outcome)",
      lease:
        "sum(rate(linkd_lifecycle_lease_operations_total[5m])) by (instance,linkd_operation,linkd_outcome)",
      recentAlertCache:
        "sum(rate(linkd_lifecycle_recent_alert_cache_operations_total[5m])) by (instance,linkd_operation,linkd_outcome)",
      finalHook:
        "sum(rate(linkd_final_hook_operations_total[5m])) by (instance,linkd_event_source_id,linkd_hook_name,messaging_system,linkd_outcome)",
    });
  }

  async controlPlaneSnapshot(
    rangeSeconds: number,
    instance?: string,
  ): Promise<Record<string, unknown>> {
    const selector = matcherSelector(
      instance ? [`instance=${JSON.stringify(instance)}`] : [],
    );
    const window = `${Math.max(60, Math.trunc(rangeSeconds))}s`;
    return this.runtimeSnapshot({
      active: `max(linkd_control_plane_task_active_ratio${selector}) by (instance, linkd_task)`,
      lastSuccess: `max(linkd_control_plane_task_last_success_seconds${selector}) by (instance, linkd_task)`,
      runCount: `round(sum(increase(linkd_control_plane_task_runs_total${selector}[${window}])) by (instance, linkd_task, linkd_outcome))`,
      averageDuration: `sum(increase(linkd_control_plane_task_run_duration_seconds_sum${selector}[${window}])) by (instance, linkd_task) / sum(increase(linkd_control_plane_task_run_duration_seconds_count${selector}[${window}])) by (instance, linkd_task)`,
      p95Duration: `histogram_quantile(0.95, sum(increase(linkd_control_plane_task_run_duration_seconds_bucket${selector}[${window}])) by (le, instance, linkd_task))`,
      archiveLastScanned: `max(linkd_elasticsearch_alert_archiver_last_batch_scanned${selector}) by (instance)`,
      archiveLastBatch: `max(linkd_elasticsearch_alert_archiver_last_batch_items${selector}) by (instance)`,
      archiveLastFailed: `max(linkd_elasticsearch_alert_archiver_last_batch_failed${selector}) by (instance)`,
      trimRequired: `max(linkd_redis_stream_trim_required_ratio${selector}) by (instance)`,
      trimSafe: `max(linkd_redis_stream_trim_safe_ratio${selector}) by (instance)`,
      trimLastEntries: `max(linkd_redis_stream_trim_last_entries${selector}) by (instance)`,
      oldestPendingAge: `max(linkd_redis_stream_oldest_pending_age_seconds${selector}) by (instance)`,
    });
  }

  private async runtimeSnapshot(
    queries: Record<string, string>,
  ): Promise<Record<string, unknown>> {
    if (!this.baseUrl)
      return {
        status: "unavailable",
        message: "Prometheus 未配置",
        series: {},
      };
    const entries = await Promise.all(
      Object.entries(queries).map(async ([name, query]) => {
        try {
          const response = await this.queryInstant(query);
          return [
            name,
            (response.data?.result ?? []).map((item) => ({
              labels: item.metric,
              value: finiteNumber(item.value[1]),
              timestamp: item.value[0],
            })),
            true,
          ] as const;
        } catch {
          return [name, [], false] as const;
        }
      }),
    );
    const succeeded = entries.filter((entry) => entry[2]).length;
    return {
      status:
        succeeded === entries.length
          ? "available"
          : succeeded === 0
            ? "unavailable"
            : "partial",
      series: Object.fromEntries(
        entries.map(([name, values]) => [name, values]),
      ),
    };
  }

  private async queryRange(
    query: string,
    from: Date,
    to: Date,
    step: number,
  ): Promise<PrometheusResponse> {
    const url = new URL(`${this.baseUrl}/api/v1/query_range`);
    url.searchParams.set("query", query);
    url.searchParams.set("start", String(from.getTime() / 1000));
    url.searchParams.set("end", String(to.getTime() / 1000));
    url.searchParams.set("step", String(step));
    const response = await fetch(url, {
      headers: { accept: "application/json", ...this.headers },
      signal: AbortSignal.timeout(this.timeoutMilliseconds),
    });
    if (!response.ok)
      throw new Error(
        `Prometheus request failed with status ${response.status}`,
      );
    const decoded = (await response.json()) as PrometheusResponse;
    if (decoded.status !== "success")
      throw new Error("Prometheus query failed");
    return decoded;
  }

  private async queryInstant(
    query: string,
  ): Promise<PrometheusInstantResponse> {
    if (!this.baseUrl) throw new Error("Prometheus is not configured");
    const url = new URL(`${this.baseUrl}/api/v1/query`);
    url.searchParams.set("query", query);
    const response = await fetch(url, {
      headers: { accept: "application/json", ...this.headers },
      signal: AbortSignal.timeout(this.timeoutMilliseconds),
    });
    if (!response.ok)
      throw new Error(
        `Prometheus request failed with status ${response.status}`,
      );
    const decoded = (await response.json()) as PrometheusInstantResponse;
    if (decoded.status !== "success")
      throw new Error("Prometheus query failed");
    return decoded;
  }
}

function targetKey(labels: Record<string, string>): string {
  return `${labels.job ?? ""}\u0000${labels.instance ?? ""}`;
}

function unavailable(
  definition: PanelDefinition,
  message: string,
): MetricPanel {
  return { ...definition, status: "unavailable", message, series: [] };
}

function seriesName(labels: Record<string, string>, index: number): string {
  const preferred = [
    labels.linkd_stage,
    labels.linkd_event_source_id,
    labels.messaging_kafka_partition,
    labels.linkd_step,
    labels.linkd_outcome,
    labels.messaging_system,
    labels.linkd_object_type,
    labels.linkd_operation,
    labels.linkd_task,
    labels.__name__,
    labels.instance,
  ].filter(Boolean);
  return preferred.length ? preferred.join(" · ") : `series-${index + 1}`;
}

function finiteNumber(value: string): number | null {
  const parsed = Number(value);
  return Number.isFinite(parsed) ? parsed : null;
}

function mergeSelector(selector: string, matcher: string): string {
  if (!selector) return `{${matcher}}`;
  return `${selector.slice(0, -1)},${matcher}}`;
}

function matcherSelector(matchers: string[]): string {
  return matchers.length ? `{${matchers.join(",")}}` : "";
}

function authHeaders(auth: {
  apiKey?: string;
  username?: string;
  password?: string;
}): Record<string, string> {
  if (auth.apiKey) return { authorization: `Bearer ${auth.apiKey}` };
  if (auth.username)
    return {
      authorization: `Basic ${Buffer.from(`${auth.username}:${auth.password ?? ""}`).toString("base64")}`,
    };
  return {};
}
