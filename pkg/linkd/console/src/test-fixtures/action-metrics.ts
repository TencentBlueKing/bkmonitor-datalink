import type { MetricsResponse } from "../shared/contracts.js";
import { deliveryMetricIDs } from "../shared/delivery-metrics.js";
const actionMetricIDs = deliveryMetricIDs.action;
const titles = [
  "运行与执行中实例",
  "扫描页结束速率",
  "扫描页耗时 P95",
  "工作观察结果速率",
  "最近页项目数",
  "最近页最大待办年龄",
  "最近页观察距今",
  "此前结果未确认观察速率",
];
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
      unit: ["实例", "页/s", "s", "观察项/s", "项", "s", "s", "观察项/s"][i],
      kind: "line",
      status: empty ? "unavailable" : "available",
      message: empty
        ? "查询范围内没有动作运行时序；不能据此判定任务已完成"
        : undefined,
      description: "每页工作观察可重复统计，不能作为唯一动作数或处置执行数。",
      series: empty
        ? []
        : [
            {
              name: `control · ${q.get("instance") ?? "worker-a"} · 动作发送`,
              labels: {
                job: "control",
                instance: q.get("instance") ?? "worker-a",
                linkd_task: "action-delivery",
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
