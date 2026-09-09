import type { MetricPanel } from "../../shared/contracts";
import { formatMetricNumber } from "../metricFormat";
import { HelpLabel } from "./HelpTip";
import { MetricSection } from "./MetricSection";

export function LifecycleBatchPanel({
  panels,
  config,
}: {
  panels: MetricPanel[];
  config: Record<string, unknown>;
}) {
  const raw = config.elasticsearchWriteBatch;
  const batch =
    raw && typeof raw === "object" ? (raw as Record<string, unknown>) : {};
  const count = (id: string, outcome?: string) => {
    const panel = panels.find((p) => p.id === id);
    if (!panel || panel.status !== "available") return "—";
    const values = panel.series
      .filter((s) => !outcome || s.labels.linkd_outcome === outcome)
      .map((s) => s.points.at(-1)?.[1]);
    if (
      !values.length ||
      values.some((v) => v === null || v === undefined || !Number.isFinite(v))
    )
      return "—";
    return formatMetricNumber(
      values.reduce<number>((sum, v) => sum + (v ?? 0), 0),
    );
  };
  const value = (n: unknown) =>
    typeof n === "number" && Number.isFinite(n) ? formatMetricNumber(n) : "—";
  return (
    <section
      className="diagnostic-section batch-diagnostics"
      aria-label="ES 分批写入"
    >
      <header>
        <p className="eyebrow">LIFECYCLE · PHYSICAL BULK</p>
        <h2>ES 分批写入</h2>
        <p>
          先看实际每批数量，再对照排队与执行耗时。只统计 Lifecycle 写 Bulk，不含
          realtime 读取、Cleaner 或 Archiver。
        </p>
      </header>
      <div className="batch-config-summary">
        <span>
          配置
          {batch.enabled === false
            ? "关闭"
            : batch.enabled === true
              ? "启用（仅 ES 生效）"
              : "未知"}
        </span>
        <span>Lifecycle 并发 {value(config.concurrency)}</span>
        <span>单批上限 {value(batch.max_operations)} 操作</span>
        <span>写收集上限 {value(batch.wait_milliseconds)} ms</span>
        <span>读收集上限 {value(batch.read_wait_milliseconds)} ms</span>
        <span>执行上限 {value(batch.max_concurrent_batches)} 批</span>
        <span>
          字节上限{" "}
          {value(
            typeof batch.max_bytes === "number"
              ? batch.max_bytes / 1048576
              : undefined,
          )}{" "}
          MiB
        </span>
      </div>
      <p className="diagnostic-note">
        以上为当前 YAML
        推导值，非运行实例遥测；图表可能汇总多个实例。总量仅根据已采集样本估算，不补录历史。
        未启用、旧版本未上报与无样本均不按零处理。
      </p>
      <div className="stage-stat-grid">
        {[
          [
            "批次执行次数",
            count("lifecycle-batch-executions"),
            "包含成功、部分失败和失败的请求尝试",
          ],
          [
            "提交写操作",
            count("lifecycle-batch-items"),
            "成功与失败/未知的子操作总数，不是 Event 数",
          ],
          [
            "成功写操作",
            count("lifecycle-batch-items", "succeeded"),
            "收到成功逐项结果；不代表搜索已 refresh 可见",
          ],
          [
            "失败 / 未知写操作",
            count("lifecycle-batch-items", "failed"),
            "含冲突和传输错误，结果未知时可能已写入",
          ],
        ].map(([title, note, help]) => (
          <article className="stat-card" key={title}>
            <HelpLabel label={title} help={help} />
            <strong>{note}</strong>
            <small>选定时间范围内 · increase 估算</small>
          </article>
        ))}
      </div>
      <MetricSection
        title="读写等待定位"
        description="读写共享执行槽位。先看 worker_slot，再看 connection 与 first_byte；operation_queue 是逐项等待，不能与首项凑批耗时相加。"
        panels={panels}
        ids={[
          "lifecycle-batch-server-client",
          "lifecycle-batch-phases",
          "lifecycle-batch-triggers",
          "lifecycle-batch-executing",
        ]}
      />
      <MetricSection
        title="批次效率与等待"
        description="曲线使用指标计算窗口；总量卡片使用完整图表时间范围。没有请求时，均值和分位数显示为缺口。"
        panels={panels}
        ids={[
          "lifecycle-batch-rate",
          "lifecycle-batch-write-rate",
          "lifecycle-batch-size",
          "lifecycle-batch-bytes",
          "lifecycle-batch-queue",
          "lifecycle-batch-duration",
        ]}
      />
    </section>
  );
}
