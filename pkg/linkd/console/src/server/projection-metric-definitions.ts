import type { Definition } from "./delivery-metrics.js";
const by = "job, instance, linkd_stage, linkd_outcome";
export const projectionDefinitions: Definition[] = [
  {
    id: "projection-work",
    title: "KAC 处理吞吐",
    unit: "条/s",
    description: "按入队、投影、刷新及动作阶段区分成功和失败。",
    query: (s, w) => `sum by (${by}) (rate(linkd_kac_items_total${s}[${w}]))`,
  },
  {
    id: "projection-duration",
    title: "处理耗时 P95",
    unit: "s",
    description: "投影及动作耗时，动作接口不占用投影通道。",
    query: (s, w) =>
      `histogram_quantile(0.95, sum by (${by}, le) (rate(linkd_kac_duration_seconds_bucket${s}[${w}])))`,
  },
  {
    id: "projection-page-items",
    title: "队列积压",
    unit: "条",
    description: "Stream 中尚未结束的消息，包含重启待恢复消息。",
    query: (s) =>
      `max by (job, instance) (linkd_kac_queue_messages${s.replace(/linkd_stage=~"[^"]*",?/, "")})`,
  },
  {
    id: "projection-page-age",
    title: "最早消息等待",
    unit: "s",
    description: "队列中最早未结束消息的等待时间。",
    query: (s) =>
      `max by (job, instance) (linkd_kac_queue_oldest_age_seconds${s.replace(/linkd_stage=~"[^"]*",?/, "")})`,
  },
];
