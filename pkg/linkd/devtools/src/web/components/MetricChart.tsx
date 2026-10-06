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

export function MetricChart({ panel }: { panel: MetricPanel }) {
  const root = useRef<HTMLDivElement>(null);

  useEffect(() => {
    if (!root.current || panel.status !== "available") return;
    const chart = init(root.current, undefined, { renderer: "canvas" });
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
        axisLabel: { color: "#607187", fontSize: 10 },
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
        smooth: 0.25,
        symbol: "none",
        areaStyle: panel.kind === "area" ? { opacity: 0.08 } : undefined,
        data: series.points.map(([timestamp, value]) => [
          timestamp * 1000,
          value,
        ]),
      })),
    });
    const observer = new ResizeObserver(() => chart.resize());
    observer.observe(root.current);
    return () => {
      observer.disconnect();
      chart.dispose();
    };
  }, [panel]);

  if (panel.status === "unavailable") {
    return <div className="chart-empty">{panel.message ?? "未接入"}</div>;
  }
  return <div ref={root} className="metric-chart" />;
}
