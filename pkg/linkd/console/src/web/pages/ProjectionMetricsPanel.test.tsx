import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import {
  cleanup,
  fireEvent,
  render,
  screen,
  waitFor,
} from "@testing-library/react";
import { MemoryRouter } from "react-router-dom";
import { afterEach, expect, it, vi } from "vitest";
import type { MetricPanel } from "../../shared/contracts";
import {
  projectionFixture,
  projectionID,
  projectionTenant,
} from "../../test-fixtures/projection-tasks";
import { projectionMetricsFixture } from "../../test-fixtures/projection-metrics";
import { ProjectionTasksPage } from "./ProjectionTasksPage";
vi.mock("../components/MetricPanelCard", () => ({
  MetricPanelCard: ({ panel }: { panel: MetricPanel }) => (
    <article>
      {panel.title} {panel.message}
      <span>{panel.status}</span>
    </article>
  ),
}));
afterEach(() => {
  cleanup();
  sessionStorage.clear();
  vi.unstubAllGlobals();
});
function view(metrics: (url: URL, init?: RequestInit) => Promise<Response>) {
  const row = projectionFixture("succeeded");
  const fetcher = vi.fn(async (input: string, init?: RequestInit) => {
    const url = new URL(input, "http://console");
    expect(init?.method ?? "GET").toBe("GET");
    if (url.pathname.endsWith("projection-metrics")) return metrics(url, init);
    return Response.json(
      url.pathname.endsWith(projectionID)
        ? row
        : { bk_tenant_id: projectionTenant, items: [row], next: "" },
    );
  });
  vi.stubGlobal("fetch", fetcher);
  render(
    <QueryClientProvider
      client={
        new QueryClient({ defaultOptions: { queries: { retry: false } } })
      }
    >
      <MemoryRouter
        initialEntries={[
          `/projection-tasks?bk_tenant_id=${projectionTenant}&id=${projectionID}`,
        ]}
      >
        <ProjectionTasksPage />
      </MemoryRouter>
    </QueryClientProvider>,
  );
  return fetcher;
}
it("loads seven projection charts on demand and keeps process filters separate from tenant and ACK state", async () => {
  const calls: URL[] = [];
  view(async (url) => {
    calls.push(url);
    return Response.json(projectionMetricsFixture(url.searchParams));
  });
  await screen.findByText("远端确认");
  expect(calls).toHaveLength(0);
  fireEvent.click(screen.getByRole("button", { name: "查看投影运行观测" }));
  await screen.findByText("最近页观察距今");
  expect(screen.getAllByText("available", { exact: true })).toHaveLength(7);
  expect([...calls[0].searchParams.keys()].sort()).toEqual([
    "calculation_window_seconds",
    "from",
    "step",
    "to",
  ]);
  expect(screen.getByText(/advanced 在生产阶段表示建或复用任务/)).toBeVisible();
  fireEvent.change(screen.getByLabelText("进程 instance"), {
    target: { value: "worker-b" },
  });
  fireEvent.click(screen.getByRole("button", { name: "应用进程筛选" }));
  await waitFor(() =>
    expect(calls.at(-1)?.searchParams.get("instance")).toBe("worker-b"),
  );
  fireEvent.change(screen.getByLabelText("指标计算窗口"), {
    target: { value: "300" },
  });
  await waitFor(() =>
    expect(calls.at(-1)?.searchParams.get("calculation_window_seconds")).toBe(
      "300",
    ),
  );
  fireEvent.click(screen.getByRole("button", { name: "15m" }));
  await waitFor(() =>
    expect(
      Date.parse(calls.at(-1)!.searchParams.get("to")!) -
        Date.parse(calls.at(-1)!.searchParams.get("from")!),
    ).toBe(900000),
  );
  expect(screen.getByText(/本任务完成不等于最新版本也已同步/)).toBeVisible();
});
it("keeps missing metrics explicit, rejects foreign panels and cancels pending reads on collapse", async () => {
  let mode = "empty",
    signal: AbortSignal | undefined;
  view(async (url, init) => {
    if (mode === "pending")
      return new Promise((_resolve, reject) => {
        signal = init?.signal as AbortSignal;
        signal.addEventListener("abort", () => reject(new Error("aborted")), {
          once: true,
        });
      });
    const result = projectionMetricsFixture(url.searchParams, true);
    if (mode === "foreign") result.panels[0].id = "action-runners";
    return Response.json(result);
  });
  fireEvent.click(screen.getByRole("button", { name: "查看投影运行观测" }));
  await screen.findByText(
    "最近页观察距今 查询范围内没有投影运行时序；不能据此判定任务已完成",
  );
  mode = "foreign";
  fireEvent.click(screen.getByRole("button", { name: "刷新投影指标" }));
  await screen.findByRole("alert");
  expect(screen.getByRole("alert")).toHaveTextContent(
    "投影指标响应与查询范围不一致",
  );
  expect(
    screen.queryByRole("region", { name: "补扫与投递" }),
  ).not.toBeInTheDocument();
  mode = "pending";
  fireEvent.click(screen.getByRole("button", { name: "刷新投影指标" }));
  await waitFor(() => expect(signal).toBeDefined());
  fireEvent.click(screen.getByRole("button", { name: "收起投影运行观测" }));
  await waitFor(() => expect(signal?.aborted).toBe(true));
  expect(
    screen.queryByRole("region", { name: "投影运行观测" }),
  ).not.toBeInTheDocument();
  expect(screen.getByText("远端确认")).toBeVisible();
});
