export function StatusBadge({ value }: { value: unknown }) {
  const text = String(value ?? "—");
  const tone = /failed|rejected|closed|error|block|red/i.test(text)
    ? "danger"
    : /active|accepted|complete|succeeded|normalized|green/i.test(text)
      ? "success"
      : /retry|pending|unprocessed|partial|yellow/i.test(text)
        ? "warning"
        : "neutral";
  return (
    <span className={`status-badge ${tone}`} title={statusHelp(text)}>
      {text}
    </span>
  );
}

function statusHelp(value: string): string {
  if (/available|green|stable|up|succeeded|accepted/i.test(value))
    return "当前快照可用或运行状态正常。";
  if (/partial|yellow|retry|pending|unprocessed|rebalance/i.test(value))
    return "当前状态需要结合详情判断，部分数据可能缺失或仍在处理中。";
  if (/unavailable|red|failed|error|dead|down/i.test(value))
    return "当前数据不可用或检测到明确异常，请查看同区域的错误详情。";
  if (/empty/i.test(value)) return "查询成功，但当前没有匹配数据。";
  return "来源系统返回的当前状态值。";
}
