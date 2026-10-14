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

// 只查询进程级指标，不携带租户或告警身份。
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
          <p>按进程和阶段观察队列积压、吞吐、耗时及失败。</p>
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
        动作成功表示 KAC 已接受投递；业务执行结果由 KAC 记录。没有时序不等于零。
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
            title="队列与执行"
            description="队列包含执行中和重启待恢复的消息。"
            panels={metrics.data.panels}
            range={{ from: metrics.data.from, to: metrics.data.to }}
            ids={[...deliveryMetricIDs[kind]]}
          />
        </>
      )}
    </section>
  );
}
