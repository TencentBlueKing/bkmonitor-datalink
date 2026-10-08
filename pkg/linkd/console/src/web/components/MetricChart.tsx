import { LineChart } from "echarts/charts";
import {
  GridComponent,
  LegendComponent,
  TooltipComponent,
} from "echarts/components";
import { init, use as registerECharts } from "echarts/core";
import { CanvasRenderer } from "echarts/renderers";
import { useEffect, useRef } from "react";

import type { MetricPanel } from "../../shared/contracts";
import { formatMetricNumber, formatMetricValue } from "../metricFormat";

registerECharts([
  LineChart,
  GridComponent,
  LegendComponent,
  TooltipComponent,
  CanvasRenderer,
]);

const finiteSample = (value: number | null | undefined) =>
  typeof value === "number" && Number.isFinite(value);

export function MetricChart({
  panel,
  range,
}: {
  panel: MetricPanel;
  range?: { from: string; to: string };
}) {
  const root = useRef<HTMLDivElement>(null);

  useEffect(() => {
    if (!root.current || panel.status !== "available") return;
    const chart = init(root.current, undefined, { renderer: "canvas" });
    const from = Date.parse(range?.from ?? ""),
      to = Date.parse(range?.to ?? "");
    const boundedRange =
      Number.isFinite(from) && Number.isFinite(to) && from < to;
    chart.setOption({
      animationDuration: 240,
      backgroundColor: "transparent",
      color: ["#6fe3c1", "#68a7ff", "#f3b35b", "#d987ff", "#ff718f"],
      tooltip: {
        trigger: "axis",
        backgroundColor: "#111a25",
        borderColor: "#263447",
        textStyle: { color: "#e8f0f8" },
        valueFormatter: (value: unknown) =>
          formatMetricValue(value, panel.unit),
      },
      legend: {
        type: "scroll",
        top: 2,
        right: 0,
        textStyle: { color: "#8495a8", fontSize: 10 },
      },
      grid: { left: 52, right: 18, top: 48, bottom: 28 },
      xAxis: {
        type: "time",
        // 显式查询窗口不能由稀疏样本自动扩成更长历史或未来时间。
        min: boundedRange ? from : undefined,
        max: boundedRange ? to : undefined,
        axisLabel: { color: "#607187", fontSize: 10, hideOverlap: true },
        axisLine: { lineStyle: { color: "#263447" } },
        splitLine: { show: false },
      },
      yAxis: {
        type: "value",
        name: panel.unit,
        nameLocation: "end",
        nameGap: 12,
        nameTextStyle: {
          color: "#52667c",
          fontSize: 10,
          align: "left",
        },
        axisLabel: {
          color: "#607187",
          fontSize: 10,
          formatter: (value: number) => formatMetricNumber(value),
        },
        splitLine: { lineStyle: { color: "#182331" } },
      },
      series: panel.series.map((series) => ({
        name: series.name,
        type: "line",
        // 保留真实采样拐点，避免平滑曲线掩盖吞吐下降或虚构峰值。
        smooth: false,
        connectNulls: false,
        symbol: "none",
        areaStyle: panel.kind === "area" ? { opacity: 0.08 } : undefined,
        data: series.points.map(([timestamp, value], index) => ({
          value: [timestamp * 1000, value],
          // 启动后或缺采样两侧的孤立点没有可画的线段，必须显示点，不能让真实零值看起来像无数据。
          symbol:
            finiteSample(value) &&
            !finiteSample(series.points[index - 1]?.[1]) &&
            !finiteSample(series.points[index + 1]?.[1])
              ? "circle"
              : "none",
          symbolSize: 6,
        })),
      })),
    });
    const observer = new ResizeObserver(() => chart.resize());
    observer.observe(root.current);
    return () => {
      observer.disconnect();
      chart.dispose();
    };
  }, [panel, range?.from, range?.to]);

  if (panel.status === "unavailable") {
    // ECharts.dispose 会清空原容器；状态切换须换 DOM，避免清除 React 刚写入的空态提示。
    return (
      <div key="unavailable" className="chart-empty">
        {panel.message ?? "未接入"}
      </div>
    );
  }
  if (
    !panel.series.some((s) =>
      s.points.some(([, value]) => value !== null && Number.isFinite(value)),
    )
  ) {
    return (
      <div key="empty" className="chart-empty">
        窗口内没有可计算样本；无调用时，成功率、均值和分位数均不补零。
      </div>
    );
  }
  return <div key="chart" ref={root} className="metric-chart" />;
}
