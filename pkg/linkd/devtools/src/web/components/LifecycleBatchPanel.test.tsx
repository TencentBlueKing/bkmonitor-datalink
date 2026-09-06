import { cleanup, render, screen, within } from "@testing-library/react";
import { afterEach, expect, it, vi } from "vitest";
import type { MetricPanel } from "../../shared/contracts";
import { LifecycleBatchPanel } from "./LifecycleBatchPanel";

vi.mock("./MetricPanelCard", () => ({ MetricPanelCard: () => null }));
afterEach(cleanup);
const config = {
  concurrency: 256,
  elasticsearchWriteBatch: {
    enabled: true,
    max_operations: 100,
    max_bytes: 4194304,
    wait_milliseconds: 100,
    read_wait_milliseconds: 10,
    max_concurrent_batches: 32,
  },
};
function panel(id: string, counts: Record<string, number | null>): MetricPanel {
  return {
    id,
    title: id,
    kind: "stat",
    unit: "operation",
    status: "available",
    series: Object.entries(counts).map(([outcome, n]) => ({
      name: outcome,
      labels: { linkd_outcome: outcome },
      points: [[1, n]],
    })),
  };
}
it("separates range execution counts, submitted operations and item outcomes", () => {
  render(
    <LifecycleBatchPanel
      config={config}
      panels={[
        panel("lifecycle-batch-executions", {
          succeeded: 3,
          partial_failed: 1,
          failed: 1,
        }),
        panel("lifecycle-batch-items", { succeeded: 380, failed: 20 }),
      ]}
    />,
  );
  const card = (title: string) =>
    within(screen.getByText(title).closest("article")!);
  expect(card("批次执行次数").getByText("5")).toBeVisible();
  expect(card("提交写操作").getByText("400")).toBeVisible();
  expect(card("成功写操作").getByText("380")).toBeVisible();
  expect(card("失败 / 未知写操作").getByText("20")).toBeVisible();
  expect(screen.getByText("单批上限 100 操作")).toBeVisible();
  expect(screen.getByText("写收集上限 100 ms")).toBeVisible();
  expect(screen.getByText("读收集上限 10 ms")).toBeVisible();
  expect(screen.getByText(/非运行实例遥测/)).toBeVisible();
});
it("does not turn missing or non-finite samples into zero", () => {
  render(
    <LifecycleBatchPanel
      config={{}}
      panels={[panel("lifecycle-batch-items", { succeeded: null, failed: 0 })]}
    />,
  );
  expect(
    within(screen.getByText("提交写操作").closest("article")!).getByText("—"),
  ).toBeVisible();
  expect(
    within(screen.getByText("失败 / 未知写操作").closest("article")!).getByText(
      "0",
    ),
  ).toBeVisible();
  expect(
    within(screen.getByText("批次执行次数").closest("article")!).getByText("—"),
  ).toBeVisible();
});
