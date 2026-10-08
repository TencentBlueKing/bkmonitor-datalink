import type { Definition } from "./delivery-metrics.js";
const by = "job, instance, linkd_task";
export const actionDefinitions: Definition[] = [
  {
    id: "action-runners",
    title: "运行与执行中实例",
    unit: "实例",
    description:
      "按进程和阶段累计运行器实例。执行中表示正在扫描或处理一页，不是 HTTP 并发数；无时序不能当作零。",
    query: (s) =>
      `label_replace(sum by (${by}) (linkd_action_runner_active${s}), "linkd_statistic", "运行", "__name__", ".*") or label_replace(sum by (${by}) (linkd_action_runner_inflight${s}), "linkd_statistic", "执行中", "__name__", ".*")`,
  },
  {
    id: "action-rounds",
    title: "扫描页结束速率",
    unit: "页/s",
    description:
      "按成功、失败和取消拆分；成功页也可能包含正常等待或锁忙，不等于处置成功。",
    query: (s, w) =>
      `sum by (${by}, linkd_outcome) (rate(linkd_action_runner_rounds_total${s}[${w}]))`,
  },
  {
    id: "action-duration",
    title: "扫描页耗时 P95",
    unit: "s",
    description:
      "含扫描、逐项执行和有界退出清理的近似 P95，包含成功和失败页，不是纯 HTTP 耗时。",
    query: (s, w) =>
      `histogram_quantile(0.95, sum by (${by}, le) (rate(linkd_action_runner_duration_seconds_bucket${s}[${w}])))`,
  },
  {
    id: "action-work",
    title: "工作观察结果速率",
    unit: "观察项/s",
    description:
      "每页互斥分类，允许重复观察同一任务。accepted 表示已有持久受理确认，不是唯一动作数、HTTP 次数或处置完成数。",
    query: (s, w) =>
      `sum by (${by}, linkd_outcome) (rate(linkd_action_work_observations_total${s}[${w}]))`,
  },
  {
    id: "action-page-items",
    title: "最近页项目数",
    unit: "项",
    description:
      "每个进程、阶段最近合法扫描页的 0..16 项；不是全局积压数，不能跨进程相加。请同时核对观察距今。",
    query: (s) => `max by (${by}) (linkd_action_last_page_items${s})`,
  },
  {
    id: "action-page-age",
    title: "最近页最大待办年龄",
    unit: "s",
    description:
      "观察当时本页待办的最大年龄。入队以 Alert 更新时间为起点，发送以任务首次创建时间为起点；不是全局最老积压。",
    query: (s) =>
      `max by (${by}) (linkd_action_last_page_oldest_age_seconds${s})`,
  },
  {
    id: "action-page-freshness",
    title: "最近页观察距今",
    unit: "s",
    description:
      "查询采样时间减去最后合法扫描页的观察时间；失败扫描不刷新。持续增长表示观察过旧，负数提示时钟偏差。",
    query: (s) =>
      `time() - max by (${by}) (linkd_action_last_page_observed_at_seconds${s})`,
  },
  {
    id: "action-unconfirmed",
    title: "此前结果未确认观察速率",
    unit: "观察项/s",
    description:
      "返回任务仍保留 previous_unconfirmed 的观察速率；同任务可重复计数，后续跳过不证明此前从未受理。",
    query: (s, w) =>
      `sum by (${by}) (rate(linkd_action_work_unconfirmed_total${s}[${w}]))`,
  },
];
export const actionOutcomeNames: Record<string, string> = {
  enqueued: "已确认入队",
  accepted: "已有受理确认",
  skipped: "旧触发已跳过",
  waiting_projection: "等待投影",
  blocked: "前序失败阻塞",
  retrying: "安排重试",
  deferred: "延后",
  capacity: "容量满",
  failed: "失败",
  unstarted: "尚未开始",
  succeeded: "页面成功",
  cancelled: "已取消",
};
