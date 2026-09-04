export const defaultMetricRangeSeconds = 3600;

export const metricRanges = [
  { label: "15m", seconds: 900 },
  { label: "1h", seconds: 3600 },
  { label: "6h", seconds: 21600 },
  { label: "24h", seconds: 86400 },
  { label: "7d", seconds: 604800 },
];

export const defaultMetricCalculationWindowSeconds = 60;

export const metricCalculationWindows = [
  { label: "30 秒", seconds: 30 },
  { label: "1 分钟", seconds: 60 },
  { label: "2 分钟", seconds: 120 },
  { label: "5 分钟", seconds: 300 },
  { label: "15 分钟", seconds: 900 },
];

export function metricStep(rangeSeconds: number): number {
  return Math.max(15, Math.ceil(rangeSeconds / 240));
}
