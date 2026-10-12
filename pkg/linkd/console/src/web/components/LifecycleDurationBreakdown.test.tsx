import {
  cleanup,
  fireEvent,
  render,
  screen,
  within,
} from "@testing-library/react";
import { afterEach, expect, it } from "vitest";
import type { MetricPanel } from "../../shared/contracts";
import { LifecycleDurationBreakdown } from "./LifecycleDurationBreakdown";

afterEach(cleanup);
function panel(
  values: (number | null)[] = [40, 5, 10, 15, 20, 8, 2],
): MetricPanel {
  return {
    id: "lifecycle-phase-average",
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
it("shows one weighted total, proportional segments and interactive phase details", () => {
  render(
    <LifecycleDurationBreakdown
      panels={[panel()]}
      loading={false}
      error={false}
    />,
  );
  expect(screen.getByText("100.00 ms")).toBeVisible();
  const chart = screen.getByRole("group", { name: "各环节耗时占比" });
  const enrich = within(chart).getByRole("button", {
    name: "Event 丰富 40.00 ms 40.0%",
  });
  expect(enrich).toHaveStyle({ width: "40%" });
  expect(within(chart).getAllByRole("button")).toHaveLength(7);
  fireEvent.focus(within(chart).getByRole("button", { name: /Hook 输出/ }));
  expect(screen.getByText(/按顺序调用 Hook 并构造输出流水/)).toBeVisible();
  expect(enrich).toHaveAttribute("aria-pressed", "false");
});
it("keeps an unexecuted phase at zero without inflating its bar", () => {
  render(
    <LifecycleDurationBreakdown
      panels={[panel([100, 0, 0, 0, 0, 0, 0])]}
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
    <LifecycleDurationBreakdown panels={[p]} loading={false} error={false} />,
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
