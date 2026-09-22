import type { MetricCatalog } from "../../src/shared/metric-catalog";

export const metricCatalogFixture: MetricCatalog = {
  schema_version: 1,
  modules: [
    { id: "enrich", name: "告警丰富" },
    { id: "store", name: "存储访问" },
  ],
  purposes: [
    { id: "latency", name: "耗时与时效" },
    { id: "throughput", name: "吞吐与结果" },
  ],
  common_dimensions: [
    {
      name: "otel_scope_name",
      prometheus_name: "otel_scope_name",
      description: "采集 scope",
    },
  ],
  notes: ["目录存在不代表已经产生样本。"],
  metrics: [
    {
      name: "linkd.enrich.attempt.duration",
      prometheus_name: "linkd_enrich_attempt_duration_seconds",
      display_name: "告警丰富总耗时",
      module: "enrich",
      purpose: "latency",
      type: "histogram",
      prometheus_type: "histogram",
      unit: "s",
      unit_label: "秒",
      description: "新 Alert 同步丰富总耗时",
      origin: "linkd",
      dimensions: [
        {
          name: "linkd.event_source_id",
          prometheus_name: "linkd_event_source_id",
          description: "告警源 ID",
        },
      ],
      series: [
        "linkd_enrich_attempt_duration_seconds_bucket",
        "linkd_enrich_attempt_duration_seconds_sum",
        "linkd_enrich_attempt_duration_seconds_count",
      ],
    },
    {
      name: "linkd.enrich.attempts",
      prometheus_name: "linkd_enrich_attempts_total",
      display_name: "告警丰富尝试结果",
      module: "enrich",
      purpose: "throughput",
      type: "counter",
      prometheus_type: "counter",
      unit: "{attempt}",
      unit_label: "次尝试",
      description: "新 Alert 同步丰富尝试结果",
      origin: "linkd",
      dimensions: [],
      series: ["linkd_enrich_attempts_total"],
    },
    {
      name: "linkd.store.operations",
      prometheus_name: "linkd_store_operations_total",
      display_name: "存储逻辑操作数",
      module: "store",
      purpose: "throughput",
      type: "counter",
      prometheus_type: "counter",
      unit: "{operation}",
      unit_label: "次操作",
      description: "Repository 逻辑操作次数",
      origin: "linkd",
      dimensions: [],
      series: ["linkd_store_operations_total"],
    },
  ],
};
