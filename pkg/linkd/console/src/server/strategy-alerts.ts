// 对账只读取 Active Alert 的身份和策略标签，不读取 payload 中的其他业务数据。
export interface StrategyAlertRow {
  bk_tenant_id: string;
  alert_id: string;
  event_source_id: string;
  fingerprint: string;
  status: string;
  labels?: { strategy_id?: unknown };
}
export interface StrategyAlertReader {
  readStrategyAlerts(
    tenant: string,
    sources: string[],
    strategy: string,
    limit: number,
  ): Promise<StrategyAlertRow[]>;
}

// 与 Go FormatFloat(value, 'f', -1, 64) 一致：字符串不裁剪，数字不用指数形式。
export function strategyLabel(value: unknown): string | undefined {
  if (typeof value === "string") return value || undefined;
  if (typeof value !== "number" || !Number.isFinite(value)) return undefined;
  if (Object.is(value, -0)) return "-0";
  const text = String(value);
  if (!/[eE]/.test(text)) return text;
  const [mantissa, exponent] = text.toLowerCase().split("e");
  const sign = mantissa.startsWith("-") ? "-" : "";
  const parts = mantissa.replace("-", "").split(".");
  const digits = parts.join("");
  const position = parts[0].length + Number(exponent);
  return (
    sign +
    (position <= 0
      ? "0." + "0".repeat(-position) + digits
      : position >= digits.length
        ? digits + "0".repeat(position - digits.length)
        : digits.slice(0, position) + "." + digits.slice(position))
  );
}

// 只有规范数字字符串才允许匹配数值标签；例如 "00123" 不能误命中数字 123。
export function numericStrategy(strategy: string): number | undefined {
  const number = Number(strategy);
  return Number.isFinite(number) && strategyLabel(number) === strategy
    ? number
    : undefined;
}
