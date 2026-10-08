import { cleanup, render, screen } from "@testing-library/react";
import { afterEach, expect, it, vi } from "vitest";
import { init } from "echarts/core";
import type { MetricPanel } from "../../shared/contracts";
import { MetricChart } from "./MetricChart";
const captured = vi.hoisted(() => ({ setOption: vi.fn() }));
vi.mock("echarts/core", () => ({
  use: vi.fn(),
  init: vi.fn((root: HTMLDivElement) => ({
    setOption: captured.setOption,
    resize: vi.fn(),
    dispose: () => root.replaceChildren(),
  })),
}));
afterEach(() => {
  cleanup();
  vi.clearAllMocks();
});

it("shows isolated finite samples including zero without connecting missing data", () => {
  const points: Array<[number, number | null]> = [
    [1, 0],
    [2, null],
    [3, 4],
    [4, 5],
    [5, null],
    [6, 6],
    [7, null],
    [8, NaN],
  ];
  render(
    <MetricChart
      panel={{
        id: "sparse",
        title: "稀疏采样",
        unit: "项",
        kind: "line",
        status: "available",
        series: [{ name: "actual", labels: {}, points }],
      }}
    />,
  );
  const option = captured.setOption.mock.calls[0][0];
  expect(option.series[0].connectNulls).toBe(false);
  expect(option.series[0].smooth).toBe(false);
  expect(option.xAxis.axisLabel.hideOverlap).toBe(true);
  expect(
    option.series[0].data.map((point: { symbol: string }) => point.symbol),
  ).toEqual([
    "circle",
    "none",
    "none",
    "none",
    "none",
    "circle",
    "none",
    "none",
  ]);
  expect(
    option.series[0].data.map((point: { value: unknown }) => point.value),
  ).toEqual(points.map(([at, value]) => [at * 1000, value]));
});
it("keeps no-data text when disposing a previously rendered chart", () => {
  const panel: MetricPanel = {
    id: "metric",
    title: "指标",
    unit: "项",
    kind: "line",
    status: "available",
    series: [{ name: "one", labels: {}, points: [[1, 1]] }],
  };
  const rendered = render(<MetricChart panel={panel} />);
  expect(init).toHaveBeenCalledTimes(1);
  rendered.rerender(
    <MetricChart
      panel={{
        ...panel,
        status: "unavailable",
        message: "本次查询无数据",
        series: [],
      }}
    />,
  );
  expect(screen.getByText("本次查询无数据")).toBeVisible();
  rendered.rerender(<MetricChart panel={panel} />);
  expect(init).toHaveBeenCalledTimes(2);
  rendered.rerender(
    <MetricChart
      panel={{
        ...panel,
        series: [{ name: "none", labels: {}, points: [[1, null]] }],
      }}
    />,
  );
  expect(screen.getByText(/窗口内没有可计算样本/)).toBeVisible();
  expect(init).toHaveBeenCalledTimes(2);
});

it("keeps sparse charts within the explicit query range and updates a changed range", () => {
  const panel: MetricPanel = {
    id: "one",
    title: "单点",
    unit: "项",
    kind: "line",
    status: "available",
    series: [
      {
        name: "one",
        labels: {},
        points: [[Date.parse("2026-10-06T00:10:00Z") / 1000, 0]],
      },
    ],
  };
  const first = { from: "2026-10-06T00:00:00Z", to: "2026-10-06T00:15:00Z" };
  const rendered = render(<MetricChart panel={panel} range={first} />);
  expect(captured.setOption.mock.calls.at(-1)![0].xAxis).toMatchObject({
    min: Date.parse(first.from),
    max: Date.parse(first.to),
  });
  const second = { from: "2026-10-06T00:00:00Z", to: "2026-10-06T01:00:00Z" };
  rendered.rerender(<MetricChart panel={panel} range={second} />);
  expect(captured.setOption.mock.calls.at(-1)![0].xAxis).toMatchObject({
    min: Date.parse(second.from),
    max: Date.parse(second.to),
  });
  rendered.rerender(
    <MetricChart panel={panel} range={{ from: "invalid", to: second.to }} />,
  );
  expect(captured.setOption.mock.calls.at(-1)![0].xAxis.min).toBeUndefined();
});
