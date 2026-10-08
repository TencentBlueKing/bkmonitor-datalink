import { actionMetricsFixture } from "./action-metrics.js";
import { deliveryMetricIDs } from "../shared/delivery-metrics.js";

export function projectionMetricsFixture(q: URLSearchParams, empty = false) {
  const result = actionMetricsFixture(q, empty);
  result.panels = deliveryMetricIDs.projection.map((id, index) => ({
    ...result.panels[index],
    id,
    message: empty
      ? "查询范围内没有投影运行时序；不能据此判定任务已完成"
      : undefined,
    description:
      "advanced 在生产阶段表示创建或复用任务，在投递阶段表示本地 ACK 已完成；不是 HTTP 请求数。",
    series: result.panels[index].series.map((series) => ({
      ...series,
      name: `control · ${q.get("instance") ?? "worker-a"} · 投影投递`,
      labels: { ...series.labels, linkd_task: "projection-delivery" },
    })),
  }));
  return result;
}
