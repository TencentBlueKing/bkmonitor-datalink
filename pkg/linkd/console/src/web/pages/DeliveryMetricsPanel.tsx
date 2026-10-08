import { useState, type FormEvent } from "react";
import { useQuery } from "@tanstack/react-query";
import {
  deliveryMetricIDs,
  type DeliveryMetricKind,
} from "../../shared/delivery-metrics";
import { getDeliveryMetrics } from "../api";
import { MetricSection } from "../components/MetricSection";
import { MetricQueryControls } from "../components/MetricQueryControls";
import { formatTime, useTimeMode } from "../time";
import {
  defaultMetricRangeSeconds,
  defaultMetricCalculationWindowSeconds,
  metricStep,
} from "../metricRange";
import "./delivery-metrics.css";

// 此组件独立于任务列表的租户条件，只有显式展开才开始读指标，收起时取消请求。
export function DeliveryMetricsPanel({ kind }: { kind: DeliveryMetricKind }) {
  const subject = kind === "action" ? "动作" : "投影";
  const [range, setRange] = useState(defaultMetricRangeSeconds),
    [window, setWindow] = useState(defaultMetricCalculationWindowSeconds),
    [instance, setInstance] = useState("");
  const mode = useTimeMode();
  const metrics = useQuery({
    queryKey: [kind + "-metrics", range, window, instance],
    refetchOnWindowFocus: false,
    refetchOnReconnect: false,
    queryFn: ({ signal }) => {
      const to = new Date(),
        from = new Date(to.getTime() - range * 1000);
      return getDeliveryMetrics(
        kind,
        {
          from: from.toISOString(),
          to: to.toISOString(),
          step: metricStep(range),
          calculation_window_seconds: window,
          ...(instance ? { instance } : {}),
        },
        signal,
      );
    },
  });
  function search(e: FormEvent<HTMLFormElement>) {
    e.preventDefault();
    const value = String(
      new FormData(e.currentTarget).get("instance") ?? "",
    ).trim();
    if (value === instance) void metrics.refetch();
    else setInstance(value);
  }
  return (
    <section className="delivery-metrics" aria-label={`${subject}运行观测`}>
      <div className="delivery-metrics-heading">
        <div>
          <h2>{subject}运行观测</h2>
          <p>
            按 Prometheus 进程和阶段观察，不受上方租户、Alert
            或任务筛选影响。仅按需读取，刷新不推进任务。
          </p>
        </div>
        <button
          type="button"
          disabled={metrics.isFetching}
          onClick={() => void metrics.refetch()}
        >
          刷新{subject}指标
        </button>
      </div>
      <form className="delivery-metrics-filter" onSubmit={search}>
        <label>
          进程 instance
          <input name="instance" defaultValue={instance} maxLength={512} />
        </label>
        <button>应用进程筛选</button>
      </form>
      <MetricQueryControls
        rangeSeconds={range}
        calculationWindowSeconds={window}
        onRangeChange={setRange}
        onCalculationWindowChange={setWindow}
      />
      <p className="merge-note">
        最近页最多 16
        项，不代表全局积压；页面年龄与观察距今需要一起查看。工作观察可重复统计同一任务。
        {kind === "action"
          ? "queued 只表示已投递 Celery。"
          : "advanced 在生产阶段表示建或复用任务，在投递阶段表示完成本地 ACK；扫描成功不等于已同步。"}
        没有时序不等于零，也不证明任务已完成。
      </p>
      {metrics.isPending && <p role="status">正在读取{subject}指标…</p>}
      {metrics.isError && (
        <p role="alert">
          {subject}指标读取失败。{metrics.error.message}
        </p>
      )}
      {metrics.data && !metrics.isError && (
        <>
          <p className="delivery-metrics-time">
            查询区间：{formatTime(metrics.data.from, mode)} 至{" "}
            {formatTime(metrics.data.to, mode)}
          </p>
          <MetricSection
            title="补扫与投递"
            description="运行器、工作观察和最近页事实；未启用自动发送的部署可能没有这些时序。"
            panels={metrics.data.panels}
            range={{ from: metrics.data.from, to: metrics.data.to }}
            ids={[...deliveryMetricIDs[kind]]}
          />
        </>
      )}
    </section>
  );
}
