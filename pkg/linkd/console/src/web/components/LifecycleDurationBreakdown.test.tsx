import {
  cleanup,
  fireEvent,
  render,
  screen,
  within,
} from "@testing-library/react";
import { afterEach, expect, it } from "vitest";
import type { MetricPanel } from "../../shared/contracts";
import { detailPanels } from "../../../tests/fixtures/lifecycle-duration";
import { LifecycleDurationBreakdown } from "./LifecycleDurationBreakdown";

afterEach(cleanup);
function panel(
  values: (number | null)[] = [40, 5, 10, 15, 20, 8, 2],
): MetricPanel {
  return {
    id: "lifecycle-phase-execution-average",
    title: "分段",
    kind: "stat",
    unit: "ms",
    status: "available",
    series: [
      "enrich",
      "policy_snapshot",
      "plan",
      "event_commit",
      "apply",
      "final_hook",
      "other",
    ].map((phase, i) => ({
      name: phase,
      labels: { linkd_phase: phase },
      points: [[10, values[i]]],
    })),
  };
}
function waitPanel(batch = 0, queue = 0): MetricPanel {
  const p = panel();
  return {
    ...p,
    id: "lifecycle-phase-wait-average",
    series: p.series.flatMap((s) =>
      ["batch_wait", "storage_queue"].map((kind) => ({
        ...s,
        labels: { ...s.labels, linkd_wait_kind: kind },
        points: [[10, kind === "batch_wait" ? batch : queue]],
      })),
    ),
  };
}
it("separates execution, intentional batching and storage queues with the same Event denominator", () => {
  render(
    <LifecycleDurationBreakdown
      panels={[panel(), waitPanel(10, 2)]}
      loading={false}
      error={false}
    />,
  );
  expect(screen.getByText("100.00 ms")).toBeVisible();
  expect(screen.getByText("70.00 ms")).toBeVisible();
  expect(screen.getByText("14.00 ms")).toBeVisible();
  expect(screen.getAllByText("184.00 ms")).toHaveLength(2);
  expect(
    within(screen.getByRole("group")).getByRole("button", {
      name: "Event 丰富 52.00 ms 28.3%",
    }),
  ).toBeVisible();
});
it.each(["missing", "stale", "duplicate", "negative"])(
  "does not subtract incomplete %s wait samples",
  (scenario) => {
    const w = waitPanel();
    if (scenario === "missing") w.series.pop();
    if (scenario === "stale") w.series[0].points[0][0] = 9;
    if (scenario === "duplicate") w.series.push(w.series[0]);
    if (scenario === "negative") w.series[0].points[0][1] = -1;
    render(
      <LifecycleDurationBreakdown
        panels={[panel(), w]}
        loading={false}
        error={false}
      />,
    );
    expect(screen.queryByRole("group")).toBeNull();
    expect(screen.getByRole("status")).toHaveTextContent("暂无完整");
  },
);
it("shows one weighted total, proportional segments and interactive phase details", () => {
  render(
    <LifecycleDurationBreakdown
      panels={[panel(), waitPanel()]}
      loading={false}
      error={false}
    />,
  );
  expect(screen.getAllByText("100.00 ms")).toHaveLength(3);
  const chart = screen.getByRole("group", { name: "各环节耗时占比" });
  const enrich = within(chart).getByRole("button", {
    name: "Event 丰富 40.00 ms 40.0%",
  });
  expect(enrich).toHaveStyle({ width: "40%" });
  expect(within(chart).getAllByRole("button")).toHaveLength(7);
  fireEvent.click(within(chart).getByRole("button", { name: /Hook 输出/ }));
  expect(screen.getByText(/按顺序调用 Hook 并构造输出流水/)).toBeVisible();
  expect(enrich).toHaveAttribute("aria-pressed", "false");
});
it("keeps an unexecuted phase at zero without inflating its bar", () => {
  render(
    <LifecycleDurationBreakdown
      panels={[panel([100, 0, 0, 0, 0, 0, 0]), waitPanel()]}
      loading={false}
      error={false}
    />,
  );
  expect(within(screen.getByRole("group")).getAllByRole("button")).toHaveLength(
    1,
  );
  expect(screen.getAllByText("0.0%")).toHaveLength(6);
});
it.each([
  "missing",
  "null",
  "negative",
  "nan",
  "stale",
  "duplicate",
  "unavailable",
  "zero",
])("does not fabricate proportions for %s samples", (scenario) => {
  const p = panel();
  if (scenario === "missing") p.series.pop();
  if (scenario === "null") p.series[0].points.push([11, null]);
  if (scenario === "negative") p.series[0].points[0][1] = -1;
  if (scenario === "nan") p.series[0].points[0][1] = NaN;
  if (scenario === "stale") p.series[0].points[0][0] = 9;
  if (scenario === "duplicate") p.series.push(p.series[0]);
  if (scenario === "unavailable") p.status = "unavailable";
  if (scenario === "zero") p.series.forEach((s) => (s.points[0][1] = 0));
  render(
    <LifecycleDurationBreakdown
      panels={[p, waitPanel()]}
      loading={false}
      error={false}
    />,
  );
  expect(screen.queryByRole("group")).toBeNull();
  expect(screen.getByRole("status")).toHaveTextContent(
    scenario === "zero" ? "没有可计算" : "暂无完整",
  );
});
it("distinguishes query failure from loading", () => {
  const { rerender } = render(
    <LifecycleDurationBreakdown panels={[]} loading error={false} />,
  );
  expect(screen.getByRole("status")).toHaveTextContent("正在加载");
  rerender(<LifecycleDurationBreakdown panels={[]} loading={false} error />);
  expect(screen.getByRole("status")).toHaveTextContent("加载失败");
});

it("excludes only batch waits and preserves queue time and full latency", () => {
  render(
    <LifecycleDurationBreakdown
      panels={[panel(), waitPanel(10, 2)]}
      loading={false}
      error={false}
    />,
  );
  fireEvent.click(screen.getByRole("button", { name: "忽略合批等待" }));
  expect(
    screen.getByRole("button", { name: "已忽略合批等待" }),
  ).toHaveAttribute("aria-pressed", "true");
  expect(screen.getByText("114.00 ms")).toBeVisible();
  expect(screen.getByText("184.00 ms")).toBeVisible();
  expect(
    within(screen.getByRole("group")).getByRole("button", {
      name: "Event 丰富 42.00 ms 36.8%",
    }),
  ).toBeVisible();
  fireEvent.click(screen.getByRole("button", { name: "已忽略合批等待" }));
  expect(screen.getAllByText("184.00 ms")).toHaveLength(2);
});

it("drills into processors, checkpoints and alert writes with the same toggle", () => {
  render(
    <LifecycleDurationBreakdown
      panels={[panel(), waitPanel(5, 1), ...detailPanels(10)]}
      loading={false}
      error={false}
    />,
  );
  const enrich = screen.getByRole("region", { name: "Event 丰富耗时明细" });
  expect(within(enrich).getByText("策略 processor")).toBeVisible();
  expect(within(enrich).getByText("26.00 ms")).toBeVisible();
  fireEvent.click(screen.getByRole("button", { name: "忽略合批等待" }));
  expect(within(enrich).getByText("21.00 ms")).toBeVisible();
  const chart = screen.getByRole("group");
  fireEvent.click(
    within(chart).getByRole("button", { name: /计划与结果写入/ }),
  );
  const commits = screen.getByRole("region", {
    name: "计划与结果写入耗时明细",
  });
  expect(within(commits).getByText("保存执行计划")).toBeVisible();
  expect(within(commits).getByText("提交最终处理结果")).toBeVisible();
  fireEvent.mouseEnter(
    within(chart).getByRole("button", { name: /Event 丰富/ }),
  );
  expect(commits).toBeVisible();
  fireEvent.click(
    within(chart).getByRole("button", { name: /告警变更与落库/ }),
  );
  const apply = screen.getByRole("region", { name: "告警变更与落库耗时明细" });
  expect(within(apply).getByText("读取当前告警")).toBeVisible();
  expect(
    within(apply).getByText("条件更新告警（校验 / 编码 / ES）"),
  ).toBeVisible();
  expect(within(apply).getByText("告警流水校验与落库")).toBeVisible();
});
it.each(["missing", "null", "stale", "duplicate", "partial", "mismatch"])(
  "keeps parent chart when %s child data cannot be trusted",
  (scenario) => {
    const details: MetricPanel[] = detailPanels(10);
    // 用 Enrich 首个 processor 验证各种无效样本，避免影响无关父阶段。
    if (scenario === "missing") details[0].series.shift();
    if (scenario === "stale") details[0].series[0].points[0][0] = 9;
    if (scenario === "null") details[0].series[0].points[0][1] = null;
    if (scenario === "duplicate") details[0].series.push(details[0].series[0]);
    if (scenario === "partial") details[1].series[0].points[0][1] = 0.5;
    if (scenario === "mismatch") details[0].series[0].points[0][1] = 99;
    render(
      <LifecycleDurationBreakdown
        panels={[panel(), waitPanel(5, 1), ...details]}
        loading={false}
        error={false}
      />,
    );
    expect(screen.getByRole("group")).toBeVisible();
    expect(screen.getByRole("status")).toHaveTextContent("暂无完整子步骤样本");
  },
);
