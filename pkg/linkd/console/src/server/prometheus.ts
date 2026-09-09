import type { MetricPanel, MetricsResponse } from "../shared/contracts.js";
import type { ConsoleConfig } from "./config.js";

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

function namedSeries(
  query: string,
  value: string,
  label = "linkd_statistic",
): string {
  return `label_replace((${query}), "${label}", "${value}", "__name__", ".*")`;
}

// 历史数据也排除 Lifecycle Handler complete/retry 等 Signal 样本。
function eventDurationSeries(
  suffix: string,
  selector: string,
  window: string,
): string {
  const by = suffix === "bucket" ? "le, linkd_stage" : "linkd_stage";
  const metric = `linkd_pipeline_attempt_duration_seconds_${suffix}`;
  return `(sum(rate(${metric}${mergeSelector(selector, 'linkd_stage!="lifecycle"')}[${window}])) by (${by}) or sum(rate(${metric}${mergeSelector(selector, 'linkd_stage="lifecycle",linkd_outcome=~"accepted|rejected|replayed|failed"')}[${window}])) by (${by}))`;
}

function batchPanels(): PanelDefinition[] {
  const prefix = "linkd_elasticsearch_write_batch_";
  const select = (selector: string) =>
    mergeSelector(selector, 'linkd_batch_kind="write"');
  const histogram = (
    metric: string,
    selector: string,
    window: string,
    scale = 1,
  ) => {
    const selected = metric.endsWith("_seconds")
      ? mergeSelector(select(selector), 'linkd_metric_schema="2"')
      : select(selector);
    const average = `${scale} * sum(rate(${prefix}${metric}_sum${selected}[${window}])) / sum(rate(${prefix}${metric}_count${selected}[${window}]))`;
    const quantile = (q: number) =>
      `${scale} * histogram_quantile(${q}, sum(rate(${prefix}${metric}_bucket${selected}[${window}])) by (le))`;
    return `${namedSeries(average, "平均")} or ${namedSeries(quantile(0.95), "P95")} or ${namedSeries(quantile(0.99), "P99")}`;
  };
  const common = { processWide: true };
  return [
    {
      ...common,
      id: "lifecycle-batch-server-client",
      title: "同批写请求：服务端与客户端",
      unit: "ms",
      kind: "line",
      description:
        "仅比较同时取得合法 server_took 与 paired_execution 的同批写请求均值。ES took 不是纯写盘时间，两者差值也不是纯网络耗时；无 took 的响应不补零。",
      query: (s, w) => {
        const selected = mergeSelector(
          select(s),
          'linkd_batch_phase=~"server_took|paired_execution"',
        );
        return `1000 * sum(rate(${prefix}phase_duration_seconds_sum${selected}[${w}])) by (linkd_batch_phase) / sum(rate(${prefix}phase_duration_seconds_count${selected}[${w}])) by (linkd_batch_phase)`;
      },
    },
    {
      ...common,
      id: "lifecycle-batch-phases",
      title: "读写请求分段耗时",
      unit: "ms",
      kind: "line",
      description:
        "按 read/write 和阶段分别计算均值。server_took 与 paired_execution 仅包含同一批有合法 took 的写响应；took 不是纯写盘时间，差值也不只是网络。response_items 是逐项解析映射；collect 是首项等待，operation_queue 按操作计数，各阶段不可直接相加。",
      query: (s, w) =>
        `1000 * sum(rate(${prefix}phase_duration_seconds_sum${s}[${w}])) by (linkd_batch_kind, linkd_batch_phase) / sum(rate(${prefix}phase_duration_seconds_count${s}[${w}])) by (linkd_batch_kind, linkd_batch_phase)`,
    },
    {
      ...common,
      id: "lifecycle-batch-triggers",
      title: "读写聚合触发原因",
      unit: "group/s",
      kind: "line",
      description:
        "operations 数量、bytes 原始字节、deadline 等待到期、ready 不主动等待。聚合提交可能再按编码字节切片，所以不是物理请求次数。",
      query: (s, w) =>
        `sum(rate(${prefix}triggers_total${s}[${w}])) by (linkd_batch_kind, linkd_batch_trigger)`,
    },
    {
      ...common,
      id: "lifecycle-batch-executing",
      title: "读写平均执行中批次数",
      unit: "batch",
      kind: "line",
      description:
        "执行耗时总量的每秒速率，分别展示读写平均占用；不含凑批和编码，不是瞬时峰值，低均值不能排除短时槽位耗尽。",
      query: (s, w) =>
        `sum(rate(${prefix}duration_seconds_sum${mergeSelector(s, 'linkd_metric_schema="2"')}[${w}])) by (linkd_batch_kind)`,
    },
    {
      ...common,
      id: "lifecycle-batch-executions",
      title: "范围内批次执行次数",
      unit: "batch",
      kind: "stat",
      rangeTotal: true,
      description:
        "按选定时间范围 increase 估算，包含失败尝试；不是瞬时速率，也不是审计精确计数。",
      query: (s, w) =>
        `round(sum(increase(${prefix}batches_total${select(s)}[${w}])) by (linkd_outcome))`,
    },
    {
      ...common,
      id: "lifecycle-batch-items",
      title: "范围内写操作数量",
      unit: "operation",
      kind: "stat",
      rangeTotal: true,
      description:
        "逐项结果计数。失败包含结果未知，不代表 ES 一定未写入；操作数不是 Event 数。",
      query: (s, w) =>
        `round(sum(increase(${prefix}items_total${select(s)}[${w}])) by (linkd_outcome))`,
    },
    {
      ...common,
      id: "lifecycle-batch-rate",
      title: "Bulk 执行速率",
      unit: "batch/s",
      kind: "line",
      description:
        "仅 Lifecycle 写 Bulk，按全部成功、部分失败和全部失败拆分；不含 _mget、Cleaner 和 Archiver。",
      query: (s, w) =>
        `sum(rate(${prefix}batches_total${select(s)}[${w}])) by (linkd_outcome)`,
    },
    {
      ...common,
      id: "lifecycle-batch-write-rate",
      title: "写操作结果速率",
      unit: "operation/s",
      kind: "line",
      description:
        "每秒 Bulk 子操作结果，失败包含传输失败时的结果未知，不是每秒处理的 Event 数。",
      query: (s, w) =>
        `sum(rate(${prefix}items_total${select(s)}[${w}])) by (linkd_outcome)`,
    },
    {
      ...common,
      id: "lifecycle-batch-size",
      title: "实际每批操作数",
      unit: "operation/batch",
      kind: "line",
      description:
        "平均值为操作总数/批次数；P95/P99 是桶近似值。观察实际合批效果，不把配置上限当作实际大小。",
      query: (s, w) => histogram("operations", s, w),
    },
    {
      ...common,
      id: "lifecycle-batch-queue",
      title: "批次首项排队耗时",
      unit: "ms",
      kind: "line",
      description:
        "包括聚合等待和执行槽位等待，每个物理批次记录首项等待；不是所有子操作等待的平均值。",
      query: (s, w) => histogram("queue_duration_seconds", s, w, 1000),
    },
    {
      ...common,
      id: "lifecycle-batch-duration",
      title: "Bulk 请求执行耗时",
      unit: "ms",
      kind: "line",
      description:
        "包含网络、请求与响应解码，不含入队等待，也不等同于 ES 服务端 took。毫秒级直方图只适用于新版采样。",
      query: (s, w) => histogram("duration_seconds", s, w, 1000),
    },
    {
      ...common,
      id: "lifecycle-batch-bytes",
      title: "平均批次字节数",
      unit: "MiB",
      kind: "line",
      description:
        "编码后的物理 Bulk 请求大小；用于确认是否被字节预算提前切批。",
      query: (s, w) =>
        `sum(rate(${prefix}size_bytes_sum${select(s)}[${w}])) / sum(rate(${prefix}size_bytes_count${select(s)}[${w}])) / 1048576`,
    },
  ];
}

interface PanelDefinition {
  id: string;
  title: string;
  unit: string;
  kind: MetricPanel["kind"];
  query: (selector: string, window: string) => string;
  partitioned?: boolean;
  processWide?: boolean;
  rangeTotal?: boolean;
  description?: string;
}

const panelDefinitions: PanelDefinition[] = [
  {
    id: "pipeline-completed",
    title: "阶段完成速率",
    unit: "event/s",
    kind: "line",
    description:
      "Cleaner 为成功规范化（含重复投递），Lifecycle 为成功移出 Mailbox 的 Event。处理单元不同，不能取最小值作为端到端吞吐。",
    query: (selector, window) =>
      `${namedSeries(`sum(rate(linkd_pipeline_attempts_total${mergeSelector(selector, 'linkd_stage="clean",linkd_outcome="normalized"')}[${window}]))`, "clean", "linkd_stage")} or ${namedSeries(`sum(rate(linkd_lifecycle_mailbox_operations_total${mergeSelector(selector, 'linkd_operation="ack",linkd_outcome="succeeded"')}[${window}]))`, "lifecycle", "linkd_stage")}`,
  },
  {
    id: "signal-backlog",
    title: "Signal 近似积压",
    unit: "signal",
    kind: "line",
    processWide: true,
    description:
      "目标 Consumer Group 的 lag + pending，不是 Event 数，不能与 Mailbox 相加；负值表示未知，不绘制为零。",
    query: (selector) =>
      `max(linkd_cleaner_backpressure_unresolved${selector} >= 0) by (instance)`,
  },
  {
    id: "cleaner-backpressure",
    title: "接入背压状态",
    unit: "%",
    kind: "line",
    processWide: true,
    description:
      "100% 表示接入因 Signal 积压暂停拉取，0% 表示未暂停；这是进程级控制状态。",
    query: (selector) =>
      `100 * max(linkd_cleaner_backpressure_paused_ratio${selector}) by (instance)`,
  },
  {
    id: "kafka-lane-paused",
    title: "Kafka 分区暂停状态",
    unit: "%",
    kind: "line",
    partitioned: true,
    description:
      "每个分区的采样暂停状态。短暂 pause/resume 可能落在采样间隔内，图中没有高点不能证明从未暂停。",
    query: (selector) =>
      `100 * max(linkd_messaging_lane_paused_ratio${mergeSelector(selector, 'linkd_stage="clean"')}) by (linkd_event_source_id, messaging_kafka_partition)`,
  },
  {
    id: "signal-handler-duration",
    title: "Signal Handler P95",
    unit: "ms",
    kind: "line",
    description:
      "一次 Signal 调度可能处理多个 Event；此耗时不可混入单 Event 的延迟或与其相加。",
    query: (selector, window) =>
      `1000 * histogram_quantile(0.95, sum(rate(linkd_messaging_handler_duration_seconds_bucket${mergeSelector(selector, 'linkd_stage="lifecycle"')}[${window}])) by (le))`,
  },
  {
    id: "store-latency",
    title: "Repository 逻辑操作平均耗时",
    unit: "ms",
    kind: "line",
    processWide: true,
    description:
      "包含逻辑操作的读取校验、合批排队和请求执行，不等同于 ES 服务端执行时间。用于与物理批次耗时对照。",
    query: (selector, window) =>
      `1000 * sum(rate(linkd_store_operation_duration_seconds_sum${selector}[${window}])) by (linkd_object_type, linkd_operation) / sum(rate(linkd_store_operation_duration_seconds_count${selector}[${window}])) by (linkd_object_type, linkd_operation)`,
  },
  ...batchPanels(),
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
    id: "enrich-attempt-rate",
    title: "Enrich 状态速率",
    unit: "attempt/s",
    kind: "area",
    query: (selector, window) =>
      `sum(rate(linkd_enrich_attempts_total${selector}[${window}])) by (linkd_event_source_id, linkd_status, linkd_outcome, linkd_chain_kind)`,
  },
  {
    id: "enrich-duration",
    title: "Enrich 总体耗时",
    unit: "s",
    kind: "line",
    query: (selector, window) =>
      `${namedSeries(`sum(rate(linkd_enrich_attempt_duration_seconds_sum${selector}[${window}])) / sum(rate(linkd_enrich_attempt_duration_seconds_count${selector}[${window}]))`, "平均")} or ${namedSeries(`histogram_quantile(0.95, sum(rate(linkd_enrich_attempt_duration_seconds_bucket${selector}[${window}])) by (le))`, "P95")} or ${namedSeries(`histogram_quantile(0.99, sum(rate(linkd_enrich_attempt_duration_seconds_bucket${selector}[${window}])) by (le))`, "P99")}`,
  },
  {
    id: "enrich-inflight",
    title: "Enrich 在途调用",
    unit: "attempt",
    kind: "area",
    query: (selector) =>
      `sum(linkd_enrich_inflight${selector}) by (linkd_event_source_id)`,
  },
  {
    id: "enrich-processor-rate",
    title: "Enrich Processor 状态速率",
    unit: "attempt/s",
    kind: "area",
    query: (selector, window) =>
      `sum(rate(linkd_enrich_processor_attempts_total${selector}[${window}])) by (linkd_processor, linkd_status, linkd_outcome)`,
  },
  {
    id: "enrich-processor-p99",
    title: "Enrich Processor P99",
    unit: "s",
    kind: "line",
    query: (selector, window) =>
      `histogram_quantile(0.99, sum(rate(linkd_enrich_processor_duration_seconds_bucket${selector}[${window}])) by (le, linkd_processor))`,
  },
  {
    id: "enrich-diagnostics",
    title: "Enrich 诊断速率",
    unit: "diagnostic/s",
    kind: "area",
    query: (selector, window) =>
      `sum(rate(linkd_enrich_processor_diagnostics_total${selector}[${window}])) by (linkd_processor, linkd_diagnostic_code, linkd_dependency)`,
  },
  {
    id: "enrich-datasource-rate",
    title: "Enrich DataSource 调用速率",
    unit: "operation/s",
    kind: "area",
    query: (selector, window) =>
      `sum(rate(linkd_enrich_datasource_operations_total${selector}[${window}])) by (linkd_datasource, linkd_operation, linkd_outcome)`,
  },
  {
    id: "enrich-datasource-p99",
    title: "Enrich DataSource P99",
    unit: "s",
    kind: "line",
    query: (selector, window) =>
      `histogram_quantile(0.99, sum(rate(linkd_enrich_datasource_duration_seconds_bucket${selector}[${window}])) by (le, linkd_datasource, linkd_operation))`,
  },
  {
    id: "enrich-lifecycle-duration-ratio",
    title: "Enrich / Lifecycle 平均耗时",
    unit: "%",
    kind: "line",
    query: (selector, window) =>
      `100 * (sum(rate(linkd_enrich_attempt_duration_seconds_sum${selector}[${window}])) / sum(rate(linkd_enrich_attempt_duration_seconds_count${selector}[${window}]))) / (sum(rate(linkd_pipeline_attempt_duration_seconds_sum${mergeSelector(selector, 'linkd_stage="lifecycle",linkd_outcome=~"accepted|rejected|replayed|failed"')}[${window}])) / sum(rate(linkd_pipeline_attempt_duration_seconds_count${mergeSelector(selector, 'linkd_stage="lifecycle",linkd_outcome=~"accepted|rejected|replayed|failed"')}[${window}])))`,
  },
  {
    id: "enrich-payload-size",
    title: "Enrich Payload 大小",
    unit: "bytes",
    kind: "line",
    query: (selector, window) =>
      `${namedSeries(`sum(rate(linkd_enrich_payload_size_bytes_sum${selector}[${window}])) / sum(rate(linkd_enrich_payload_size_bytes_count${selector}[${window}]))`, "平均")} or ${namedSeries(`histogram_quantile(0.95, sum(rate(linkd_enrich_payload_size_bytes_bucket${selector}[${window}])) by (le))`, "P95")} or ${namedSeries(`histogram_quantile(0.99, sum(rate(linkd_enrich_payload_size_bytes_bucket${selector}[${window}])) by (le))`, "P99")}`,
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
      `${eventDurationSeries("sum", selector, window)} / ${eventDurationSeries("count", selector, window)}`,
  },
  {
    id: "pipeline-p95",
    title: "阶段处理 P95",
    unit: "s",
    kind: "line",
    query: (selector, window) =>
      `histogram_quantile(0.95, ${eventDurationSeries("bucket", selector, window)})`,
  },
  {
    id: "pipeline-p99",
    title: "阶段处理 P99",
    unit: "s",
    kind: "line",
    query: (selector, window) =>
      `histogram_quantile(0.99, ${eventDurationSeries("bucket", selector, window)})`,
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

  constructor(config: ConsoleConfig) {
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
      | {
          instance?: string;
          eventSourceId?: string;
          partition?: number;
          calculationWindowSeconds?: number;
        },
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
    const calculationWindowSeconds =
      normalizedScope.calculationWindowSeconds ?? 60;
    if (
      !Number.isInteger(calculationWindowSeconds) ||
      calculationWindowSeconds < 15 ||
      calculationWindowSeconds > 3600
    ) {
      throw new Error(
        "metrics calculation window must be an integer between 15 and 3600 seconds",
      );
    }
    const rateWindow = `${calculationWindowSeconds}s`;
    const panels = await Promise.all(
      panelDefinitions.map(async (definition): Promise<MetricPanel> => {
        if (!this.baseUrl) return unavailable(definition, "Prometheus 未配置");
        if (definition.processWide && normalizedScope.eventSourceId)
          return unavailable(
            definition,
            "跨来源共享指标不能按 EventSource 拆分，请在总览或 Lifecycle 查看",
          );
        try {
          const selector = matcherSelector(
            definition.partitioned ? laneMatchers : baseMatchers,
          );
          const query = definition.query(
            selector,
            definition.rangeTotal
              ? `${Math.ceil((to.getTime() - from.getTime()) / 1000)}s`
              : rateWindow,
          );
          const response = definition.rangeTotal
            ? await this.queryInstant(query, to).then((r) => ({
                data: {
                  result: (r.data?.result ?? []).map((item) => ({
                    metric: item.metric,
                    values: [item.value],
                  })),
                },
              }))
            : await this.queryRange(query, from, to, step);
          const series = (response.data?.result ?? []).map((item, index) => ({
            name: seriesName(item.metric, index),
            labels: item.metric,
            points: item.values.map(
              ([timestamp, value]) =>
                [timestamp, finiteNumber(value)] as [number, number | null],
            ),
          }));
          if (!definition.rangeTotal) {
            const lastEvaluation =
              from.getTime() / 1000 +
              Math.floor((to.getTime() - from.getTime()) / 1000 / step) * step;
            for (const item of series) {
              const last = item.points.at(-1);
              // 不把已经停止上报的历史末值冒充“当前”值。
              if (last && last[0] < lastEvaluation - 0.001)
                item.points.push([lastEvaluation, null]);
            }
          }
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
      paused: 'linkd_messaging_lane_paused_ratio{linkd_stage="clean"}',
      owned: 'linkd_messaging_lane_owned_ratio{linkd_stage="clean"}',
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
      enrichAttempts:
        "sum(rate(linkd_enrich_attempts_total[5m])) by (instance,linkd_event_source_id,linkd_status,linkd_outcome,linkd_chain_kind)",
      enrichInflight:
        "sum(linkd_enrich_inflight) by (instance,linkd_event_source_id)",
      enrichProcessors:
        "sum(rate(linkd_enrich_processor_attempts_total[5m])) by (instance,linkd_processor,linkd_status,linkd_outcome)",
      enrichDataSources:
        "sum(rate(linkd_enrich_datasource_operations_total[5m])) by (instance,linkd_datasource,linkd_operation,linkd_outcome)",
      enrichDiagnostics:
        "sum(rate(linkd_enrich_processor_diagnostics_total[5m])) by (instance,linkd_processor,linkd_diagnostic_code,linkd_dependency)",
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
    at?: Date,
  ): Promise<PrometheusInstantResponse> {
    if (!this.baseUrl) throw new Error("Prometheus is not configured");
    const url = new URL(`${this.baseUrl}/api/v1/query`);
    url.searchParams.set("query", query);
    if (at) url.searchParams.set("time", String(at.getTime() / 1000));
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
    labels.linkd_processor,
    labels.linkd_datasource,
    labels.linkd_status,
    labels.linkd_outcome,
    labels.linkd_event_source_id,
    labels.messaging_kafka_partition,
    labels.linkd_step,
    labels.linkd_outcome,
    labels.messaging_system,
    labels.linkd_object_type,
    labels.linkd_operation,
    labels.linkd_task,
    labels.linkd_batch_kind,
    labels.linkd_batch_phase,
    labels.linkd_batch_trigger,
    labels.linkd_statistic,
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
