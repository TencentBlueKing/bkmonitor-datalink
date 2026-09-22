// 对账只读取 Active Alert 的身份和策略标签，不读取 payload 中的其他业务数据。
export interface StrategyAlertRow {
  bk_tenant_id: string;
  alert_id: string;
  event_source_id: string;
  fingerprint: string;
  status: string;
  labels?: { strategy_id?: unknown };
  enrich?: unknown;
}
export interface StrategyAlertReader {
  // 整体对账逐批扫描完整 active 范围，不使用 Explorer 的时间窗口。
  scanActiveStrategyAlerts?(
    sources: string[],
    signal: AbortSignal,
  ): AsyncIterable<StrategyAlertRow[]>;
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

// effectiveStrategyRow 仅投影策略标签，按处理器和补丁顺序计算，不持久化最终值。
export function effectiveStrategyRow(row: StrategyAlertRow): StrategyAlertRow {
  let value = row.labels?.strategy_id;
  const payload = row.enrich as
    | {
        processors?: Array<
          Record<
            string,
            {
              status?: string;
              patches?: Array<{ op?: string; path?: string; value?: unknown }>;
              value?: Record<string, unknown>;
            }
          >
        >;
      }
    | undefined;
  for (const entry of payload?.processors ?? []) {
    for (const result of Object.values(entry)) {
      if (result.status !== "succeeded" && result.status !== "partial")
        continue;
      if (result.patches) {
        for (const patch of result.patches) {
          if (
            patch.op === "set" &&
            (patch.path === '$["labels"]["strategy_id"]' ||
              patch.path === "$.labels.strategy_id")
          )
            value = patch.value;
        }
      } else if (result.value?.strategy_id !== undefined)
        value = result.value.strategy_id;
    }
  }
  return { ...row, labels: { ...row.labels, strategy_id: value } };
}
// readEffectiveStrategyAlerts 必须扫描后再匹配，原始标签过滤会漏掉补丁新增或覆盖的策略。
export async function readEffectiveStrategyAlerts(
  pages: AsyncIterable<StrategyAlertRow[]>,
  tenant: string,
  strategy: string,
  limit: number,
): Promise<StrategyAlertRow[]> {
  const matches: StrategyAlertRow[] = [];
  let scanned = 0;
  let bytes = 0;
  for await (const page of pages) {
    scanned += page.length;
    bytes += Buffer.byteLength(JSON.stringify(page));
    if (scanned > 100000 || bytes > 64 * 1024 * 1024)
      throw new Error("策略对账扫描超过上限");
    for (const raw of page) {
      const row = effectiveStrategyRow(raw);
      if (
        row.bk_tenant_id === tenant &&
        strategyLabel(row.labels?.strategy_id) === strategy
      ) {
        matches.push(row);
        if (matches.length === limit) return matches;
      }
    }
  }
  return matches;
}
