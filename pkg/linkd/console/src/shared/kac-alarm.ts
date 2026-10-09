// KAC 协议把 Asia/Shanghai 时间写成无时区字符串；保留原始 payload，
// 仅在展示时还原实际时刻，避免浏览器所在时区改变告警时间。
export function kacTimeToISO(value: string): string {
  if (!/^\d{4}-\d{2}-\d{2} \d{2}:\d{2}:\d{2}$/.test(value)) return value;
  const date = new Date(value.replace(" ", "T") + "+08:00");
  return Number.isNaN(date.getTime()) ? value : date.toISOString();
}

// 原 mapping 未指定时区，ES 将 KAC 墙上时间解析为 UTC 数值。
// 范围和直方图边界必须使用同一偏移，响应时再还原为实际时刻。
export const kacTimeOffsetMilliseconds = 8 * 3600 * 1000;
