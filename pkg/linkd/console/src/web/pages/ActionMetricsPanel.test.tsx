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
import { ActionDeliveriesPage } from "./ActionDeliveriesPage";
import {
  actionFixture,
  actionID,
  actionTenant,
  actionOrderFixture,
} from "../../test-fixtures/action-deliveries";
import { actionMetricsFixture } from "../../test-fixtures/action-metrics";
vi.mock("../components/MetricPanelCard", () => ({
  MetricPanelCard: ({ panel }: { panel: MetricPanel }) => (
    <article>
      {panel.title} {panel.message} <span>{panel.status}</span>
    </article>
  ),
}));
afterEach(() => {
  cleanup();
  sessionStorage.clear();
  vi.unstubAllGlobals();
});
function view() {
  return render(
    <QueryClientProvider
      client={
        new QueryClient({ defaultOptions: { queries: { retry: false } } })
      }
    >
      <MemoryRouter
        initialEntries={[
          `/action-deliveries?bk_tenant_id=${actionTenant}&id=${actionID}`,
        ]}
      >
        <ActionDeliveriesPage />
      </MemoryRouter>
    </QueryClientProvider>,
  );
}
function reads(f: (url: URL, init?: RequestInit) => Promise<Response>) {
  const row = actionFixture("succeeded");
  vi.stubGlobal(
    "fetch",
    vi.fn(async (input: string, init?: RequestInit) => {
      const u = new URL(input, "http://console");
      if (u.pathname.endsWith("action-metrics")) return f(u, init);
      expect(init?.method ?? "GET").toBe("GET");
      return Response.json(
        u.pathname.endsWith("/order")
          ? actionOrderFixture(row)
          : u.pathname.endsWith(actionID)
            ? row
            : { bk_tenant_id: actionTenant, items: [row], next: "" },
      );
    }),
  );
}
it("opens metrics on demand without tenant scope and keeps query controls independent from task state", async () => {
  const calls: URL[] = [];
  reads(async (u) => {
    calls.push(u);
    return Response.json(actionMetricsFixture(u.searchParams));
  });
  view();
  await screen.findByText("Celery 投递确认");
  expect(calls).toHaveLength(0);
  fireEvent.click(screen.getByRole("button", { name: "查看动作运行观测" }));
  await screen.findByText("最近页观察距今");
  expect(calls).toHaveLength(1);
  expect([...calls[0].searchParams.keys()].sort()).toEqual([
    "calculation_window_seconds",
    "from",
    "step",
    "to",
  ]);
  expect(screen.getByText(/不受上方租户、Alert 或任务筛选影响/)).toBeVisible();
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
  fireEvent.click(screen.getByRole("button", { name: /^6h$/ }));
  await waitFor(() =>
    expect(Number(calls.at(-1)?.searchParams.get("step"))).toBe(90),
  );
  const log = screen.getByLabelText("发送日志定位字段");
  expect(JSON.parse(log.textContent!)).toEqual({
    task: "action-delivery",
    bk_tenant_id: actionTenant,
    alert_id: "opening-alert",
    action_task_id: actionID,
  });
  const link = screen.getByRole("link", {
    name: "查看 Alert 操作流水（任务更新时间前 1 小时）",
  });
  expect(link.getAttribute("href")).toContain(
    "bk_tenant_id=tenant-a&alert_id=opening-alert",
  );
  expect(link.getAttribute("href")).toContain(
    "from=2026-10-06T01%3A00%3A00.000Z",
  );
  expect(screen.getByText("这不是处置执行完成的确认。")).toBeVisible();
});
it("retains no-data meaning, rejects a foreign range, and hides old charts on failed refresh", async () => {
  let state = "empty";
  reads(async (u) => {
    if (state === "failed") throw new Error("网络不可用");
    const r = actionMetricsFixture(u.searchParams, true);
    if (state === "foreign")
      r.to = new Date(Date.parse(r.to) + 1000).toISOString();
    return Response.json(r);
  });
  view();
  fireEvent.click(screen.getByRole("button", { name: "查看动作运行观测" }));
  await screen.findByText(
    "最近页观察距今 查询范围内没有动作运行时序；不能据此判定任务已完成",
  );
  state = "foreign";
  fireEvent.click(screen.getByRole("button", { name: "刷新动作指标" }));
  await screen.findByRole("alert");
  expect(screen.getByRole("alert")).toHaveTextContent(
    "动作指标响应与查询范围不一致",
  );
  expect(
    screen.queryByRole("region", { name: "补扫与投递" }),
  ).not.toBeInTheDocument();
  state = "failed";
  fireEvent.click(screen.getByRole("button", { name: "刷新动作指标" }));
  await waitFor(() =>
    expect(screen.getByRole("alert")).toHaveTextContent("网络不可用"),
  );
  expect(screen.getByText("Celery 投递确认")).toBeVisible();
});
it("cancels in-flight metric reads when collapsed", async () => {
  let signal: AbortSignal | undefined;
  reads(
    async (_u, init) =>
      new Promise((_resolve, reject) => {
        signal = init?.signal as AbortSignal;
        signal.addEventListener("abort", () => reject(new Error("aborted")), {
          once: true,
        });
      }),
  );
  view();
  fireEvent.click(screen.getByRole("button", { name: "查看动作运行观测" }));
  await waitFor(() => expect(signal).toBeDefined());
  fireEvent.click(screen.getByRole("button", { name: "收起动作运行观测" }));
  await waitFor(() => expect(signal?.aborted).toBe(true));
  expect(
    screen.queryByRole("region", { name: "动作运行观测" }),
  ).not.toBeInTheDocument();
});
