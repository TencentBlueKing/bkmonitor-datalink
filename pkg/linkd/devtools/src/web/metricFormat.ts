const metricNumberFormatter = new Intl.NumberFormat("en-US", {
  maximumFractionDigits: 4,
  minimumFractionDigits: 0,
  useGrouping: true,
});

// formatMetricNumber 只约束展示精度，不修改 Prometheus 返回的原始采样值。
export function formatMetricNumber(value: number): string {
  return Number.isFinite(value) ? metricNumberFormatter.format(value) : "—";
}

export function formatMetricValue(value: unknown, unit: string): string {
  const number = metricValueNumber(value);
  if (number === undefined) return "—";
  const formatted = formatMetricNumber(number);
  return unit ? `${formatted} ${unit}` : formatted;
}

function metricValueNumber(value: unknown): number | undefined {
  if (typeof value === "number")
    return Number.isFinite(value) ? value : undefined;
  if (typeof value === "string" && value.trim() !== "") {
    const parsed = Number(value);
    return Number.isFinite(parsed) ? parsed : undefined;
  }
  if (Array.isArray(value)) {
    for (let index = value.length - 1; index >= 0; index -= 1) {
      const parsed = metricValueNumber(value[index]);
      if (parsed !== undefined) return parsed;
    }
  }
  return undefined;
}
