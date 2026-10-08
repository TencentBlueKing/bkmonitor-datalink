import type { Definition } from "./delivery-metrics.js";
const by = "job, instance, linkd_task";
export const projectionDefinitions: Definition[] = [
  {
    id: "projection-runners",
    title: "运行与执行中实例",
    unit: "实例",
    description:
      "按进程和阶段累计运行器实例。执行中表示正在扫描或处理一页，不是 HTTP 并发数；无时序不能当作零。",
    query: (s) =>
      `label_replace(sum by (${by}) (linkd_projection_runner_active${s}), "linkd_statistic", "运行", "__name__", ".*") or label_replace(sum by (${by}) (linkd_projection_runner_inflight${s}), "linkd_statistic", "执行中", "__name__", ".*")`,
  },
  {
    id: "projection-rounds",
    title: "扫描页结束速率",
    unit: "页/s",
    description:
      "按成功、失败和取消拆分；成功页也可能包含正常等待或锁忙，不等于投影已确认。",
    query: (s, w) =>
      `sum by (${by}, linkd_outcome) (rate(linkd_projection_runner_rounds_total${s}[${w}]))`,
  },
  {
    id: "projection-duration",
    title: "扫描页耗时 P95",
    unit: "s",
    description:
      "含扫描、逐项执行和有界退出清理的近似 P95，包含成功和失败页，不是纯 HTTP 耗时。",
    query: (s, w) =>
      `histogram_quantile(0.95, sum by (${by}, le) (rate(linkd_projection_runner_duration_seconds_bucket${s}[${w}])))`,
  },
  {
    id: "projection-work",
    title: "工作观察结果速率",
    unit: "观察项/s",
    description:
      "每页互斥分类，可重复观察。生产的 advanced 表示创建或复用任务，投递的 advanced 表示本地 ACK 已完成；不是 HTTP 请求数。failed 包含取消时未开始的项。",
    query: (s, w) =>
      `sum by (${by}, linkd_outcome) (rate(linkd_projection_work_observations_total${s}[${w}]))`,
  },
  {
    id: "projection-page-items",
    title: "最近页项目数",
    unit: "项",
    description:
      "每个进程、阶段最近合法扫描页的 0..16 项；不是全局积压数，不能跨进程相加。请同时核对观察距今。",
    query: (s) => `max by (${by}) (linkd_projection_last_page_items${s})`,
  },
  {
    id: "projection-page-age",
    title: "最近页最大待办年龄",
    unit: "s",
    description:
      "观察当时本页待办的最大年龄。生产以当前业务 update_at 为起点，投递以任务首次创建时间为起点；不是全局最老积压或首次未同步以来的等待时间。",
    query: (s) =>
      `max by (${by}) (linkd_projection_last_page_oldest_age_seconds${s})`,
  },
  {
    id: "projection-page-freshness",
    title: "最近页观察距今",
    unit: "s",
    description:
      "查询采样时间减去最后合法扫描页的观察时间；失败扫描不刷新。持续增长表示观察过旧，负数提示时钟偏差。",
    query: (s) =>
      `time() - max by (${by}) (linkd_projection_last_page_observed_at_seconds${s})`,
  },
];
