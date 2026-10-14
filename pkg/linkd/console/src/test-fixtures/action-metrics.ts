import type { MetricsResponse } from "../shared/contracts.js";
import { deliveryMetricIDs } from "../shared/delivery-metrics.js";
const actionMetricIDs = deliveryMetricIDs.action;
const titles = ["KAC 处理吞吐", "处理耗时 P95", "队列积压", "最早消息等待"];
export function actionMetricsFixture(
  q: URLSearchParams,
  empty = false,
): MetricsResponse {
  const from = q.get("from")!,
    to = q.get("to")!,
    step = Number(q.get("step"));
  const start = Date.parse(from) / 1000,
    end = start + Math.floor((Date.parse(to) / 1000 - start) / step) * step;
  return {
    from,
    to,
    step,
    panels: actionMetricIDs.map((id, i) => ({
      id,
      title: titles[i],
      unit: ["条/s", "s", "条", "s"][i],
      kind: "line",
      status: empty ? "unavailable" : "available",
      message: empty
        ? "查询范围内没有动作运行时序；不能据此判定任务已完成"
        : undefined,
      description: "按阶段区分成功和失败，队列包含执行中消息。",
      series: empty
        ? []
        : [
            {
              name: `control · ${q.get("instance") ?? "worker-a"} · 动作发送`,
              labels: {
                job: "control",
                instance: q.get("instance") ?? "worker-a",
                linkd_stage: "action",
              },
              points: [
                [start, i + 1],
                [start + step, i + 2],
                [end, i + 1],
              ],
            },
          ],
    })),
  };
}
