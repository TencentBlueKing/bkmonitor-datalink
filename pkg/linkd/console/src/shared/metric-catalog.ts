import { z } from "zod";

const dimensionSchema = z.object({
  name: z.string(),
  prometheus_name: z.string(),
  description: z.string(),
});

export const metricDefinitionSchema = z.object({
  name: z.string(),
  prometheus_name: z.string(),
  display_name: z.string(),
  module: z.string(),
  purpose: z.string(),
  type: z.enum(["counter", "up_down_counter", "gauge", "histogram", "summary"]),
  prometheus_type: z.enum(["counter", "gauge", "histogram", "summary"]),
  unit: z.string(),
  unit_label: z.string(),
  description: z.string(),
  dimensions: z.array(dimensionSchema),
  series: z.array(z.string()),
  origin: z.enum(["linkd", "runtime", "exporter"]),
});

export const metricCatalogSchema = z.object({
  schema_version: z.literal(1),
  metrics: z.array(metricDefinitionSchema),
  modules: z.array(z.object({ id: z.string(), name: z.string() })),
  purposes: z.array(z.object({ id: z.string(), name: z.string() })),
  common_dimensions: z.array(dimensionSchema),
  notes: z.array(z.string()),
});

export type MetricDefinition = z.infer<typeof metricDefinitionSchema>;
export type MetricCatalog = z.infer<typeof metricCatalogSchema>;

export const metricTypeLabels: Record<MetricDefinition["type"], string> = {
  counter: "Counter · 累计计数",
  up_down_counter: "UpDownCounter · 增减计数",
  gauge: "Gauge · 瞬时值",
  histogram: "Histogram · 分布",
  summary: "Summary · 摘要",
};

// 中文、OTel 名、Prometheus 名、说明和维度名均可查询；词项之间取交集。
export function matchesMetric(
  metric: MetricDefinition,
  query: string,
): boolean {
  const searchable = [
    metric.name,
    metric.prometheus_name,
    metric.display_name,
    metric.description,
    ...metric.series,
    ...metric.dimensions.flatMap((d) => [
      d.name,
      d.prometheus_name,
      d.description,
    ]),
  ]
    .join(" ")
    .toLocaleLowerCase();
  return query
    .trim()
    .toLocaleLowerCase()
    .split(/\s+/)
    .every((word) => searchable.includes(word));
}
