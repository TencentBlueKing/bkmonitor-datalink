import type { MetricPanel } from "../../shared/contracts";
import { HelpTip } from "./HelpTip";
import { MetricChart } from "./MetricChart";

const metricHelp: Record<string, string> = {
  "received-rate":
    "从 Kafka 拉取到 Cleaner 的消息速率，按 EventSource 和 partition 分组。",
  "settled-rate":
    "已到达可提交终态的消息速率；成功处理和确定性丢弃都会推进确认。",
  "cleaner-steps": "Cleaner 各处理步骤的执行速率，并按结果拆分。",
  "cleaner-step-average":
    "Cleaner 各可靠性步骤的平均耗时；批处理步骤表示单次批次耗时。",
  "cleaner-step-p95": "所选时间窗内 Cleaner 各步骤耗时的近似 95 分位值。",
  "cleaner-step-p99":
    "所选时间窗内 Cleaner 各步骤耗时的近似 99 分位值，用于观察长尾。",
  "cleaner-step-duration":
    "当前步骤的平均、P95 和 P99 耗时合并对比；分位数是所选时间窗内的近似值。",
  "lifecycle-results": "Lifecycle 对 Event 作出接受、抑制、孤立等裁决的速率。",
  "lifecycle-mailbox": "Mailbox peek、ack 等操作的执行速率，并按结果拆分。",
  "lifecycle-drain-p95":
    "所选时间窗内，95% 的单次 Mailbox drain 数量不超过该值。",
  "lifecycle-lease": "分布式 lease 获取、续租和释放的操作速率。",
  "final-hook": "Lifecycle 将结果投递到 FinalHook 的尝试速率，并按结果拆分。",
  "final-hook-p95": "所选时间窗内 FinalHook 耗时的近似 95 分位值。",
  "pipeline-throughput": "各处理阶段每秒执行的尝试数；失败重试也会计入。",
  "pipeline-average": "各阶段尝试耗时的平均值，由总耗时除以尝试次数得到。",
  "pipeline-p95": "所选时间窗内各阶段耗时的近似 95 分位值。",
  "pipeline-p99": "所选时间窗内各阶段耗时的近似 99 分位值，用于观察尾延迟。",
  "messaging-inflight": "已被读取、但尚未到达确认或确定性丢弃终态的消息数。",
  "retry-rate": "因可恢复失败而进入重试的速率，并按阶段和原因拆分。",
  "settlement-gap": "已完成但被更早未完成消息挡住、暂不能连续提交的消息数。",
  "store-errors":
    "Repository 真正异常结果的速率；正常查询未命中 not_found 已从该面板排除。",
  "control-plane-task-runs":
    "四个固定管理任务在所选时间窗内的单轮执行次数，按任务和 succeeded/failed 结果拆分。",
  "control-plane-task-average":
    "控制面管理任务在所选时间窗内的平均单轮执行耗时。",
  "control-plane-task-p95":
    "控制面管理任务在所选时间窗内单轮执行耗时的近似 95 分位值。",
  "control-plane-archive-rate":
    "Alert Archiver 成功搬迁至 History 并从 Active 删除的 Alert 速率。",
  "control-plane-redis-trim-rate":
    "Redis Stream Manager 从所有 Group 已确认前缀中安全裁剪 entry 的速率。",
  goroutines: "Linkd 进程当前运行的 goroutine 数量。",
  rss: "Linkd 进程当前占用的常驻物理内存。",
};

export function MetricPanelCard({
  panel,
  className = "",
}: {
  panel: MetricPanel;
  className?: string;
}) {
  const help =
    metricHelp[panel.id] ??
    "展示所选时间范围内的 Prometheus 时序；没有时序不等同于数值为 0。";
  return (
    <article className={`panel ${className}`.trim()}>
      <header>
        <div>
          <div className="metric-panel-title">
            <h2>{panel.title}</h2>
            <HelpTip label={panel.title}>{help}</HelpTip>
          </div>
          <span className="metric-panel-unit">{panel.unit}</span>
        </div>
        <span className={`panel-state ${panel.status}`}>
          {panel.status === "available" ? "LIVE" : "NO DATA"}
        </span>
      </header>
      <MetricChart panel={panel} />
    </article>
  );
}
