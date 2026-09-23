import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import {
  cleanup,
  fireEvent,
  render,
  screen,
  waitFor,
  within,
} from "@testing-library/react";
import { MemoryRouter } from "react-router-dom";
import { afterEach, beforeEach, expect, it, vi } from "vitest";
import type { EntityKind } from "../../shared/contracts";
import {
  alertFixture,
  eventFixture,
  explorerCapabilities,
  explorerStats,
  logFixture,
} from "../../../tests/fixtures/explorer";
import { TimeContext } from "../time";
import { ExplorerPage } from "./ExplorerPage";

beforeEach(() => {
  HTMLDialogElement.prototype.showModal = function () {
    this.setAttribute("open", "");
  };
  HTMLDialogElement.prototype.close = function () {
    this.removeAttribute("open");
  };
  sessionStorage.clear();
});
afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
});
function setup(entity: EntityKind, query = "") {
  const fetcher = vi.fn(async (input: string) => {
    const url = new URL(input, "http://local");
    if (url.pathname.endsWith("capabilities"))
      return Response.json(explorerCapabilities);
    const kind = url.pathname.split("/")[2] as EntityKind;
    if (url.pathname.endsWith("stats"))
      return Response.json(explorerStats(kind));
    const item =
      kind === "alerts"
        ? alertFixture
        : kind === "events"
          ? eventFixture
          : logFixture;
    if (url.pathname.split("/").length > 3) return Response.json(item);
    return Response.json({
      source: "elasticsearch",
      items: [item],
      warnings: [],
      nextCursor: url.searchParams.has("cursor") ? undefined : "next-page",
    });
  });
  vi.stubGlobal("fetch", fetcher);
  render(
    <QueryClientProvider
      client={
        new QueryClient({ defaultOptions: { queries: { retry: false } } })
      }
    >
      <TimeContext.Provider value="utc">
        <MemoryRouter initialEntries={[`/explore/${entity}${query}`]}>
          <ExplorerPage entity={entity} />
        </MemoryRouter>
      </TimeContext.Provider>
    </QueryClientProvider>,
  );
  return fetcher;
}
it("shows creation separately from updates and loads both tenant-scoped related lists inside Alert details", async () => {
  const fetcher = setup("alerts", "?bk_tenant_id=tenant-a&id=alert-cpu-001");
  await screen.findByRole("button", {
    name: alertFixture.payload.title as string,
  });
  expect(screen.getByText("创建")).toBeInTheDocument();
  expect(screen.getByText("2026-09-10 01:00:02.000 UTC")).toBeInTheDocument();
  expect(screen.getByText("2026-09-23 04:10:00.000 UTC")).toBeInTheDocument();
  fireEvent.click(
    screen.getByRole("button", { name: alertFixture.payload.title as string }),
  );
  const events = await screen.findByRole("region", { name: "关联 Event 列表" });
  expect(
    await within(events).findByRole("button", { name: "CPU 持续异常采样" }),
  ).toBeInTheDocument();
  const logs = screen.getByRole("region", { name: "关联 AlertLog 时间线" });
  expect(
    await within(logs).findByText("threshold_exceeded"),
  ).toBeInTheDocument();
  expect(screen.getByText(/并非全部历史/)).toBeInTheDocument();
  const related = fetcher.mock.calls
    .map(([u]) => new URL(u, "http://local"))
    .filter(
      (u) =>
        u.searchParams.has("related_alert_id") ||
        u.searchParams.has("alert_id"),
    );
  expect(related).toHaveLength(2);
  for (const url of related) {
    expect(url.searchParams.get("bk_tenant_id")).toBe("tenant-a");
    expect(
      Date.parse(url.searchParams.get("to")!) -
        Date.parse(url.searchParams.get("from")!),
    ).toBe(604800000);
  }
  fireEvent.click(
    within(events).getByRole("button", { name: "CPU 持续异常采样" }),
  );
  expect(
    within(events).getByText("Lifecycle 逐级处理结果"),
  ).toBeInTheDocument();
  expect(within(events).getByText("96.4")).toBeInTheDocument();
});
it("uses identical fixed time and identity filters for list/stats, resets pagination on new filters", async () => {
  const fetcher = setup("events", "?bk_tenant_id=tenant-a&id=event-update-002");
  await screen.findByRole("button", { name: "CPU 持续异常采样" });
  const initial = fetcher.mock.calls
    .map(([u]) => new URL(u, "http://local"))
    .filter((u) => u.pathname.includes("events"));
  expect(
    initial.every(
      (u) =>
        u.searchParams.get("id") === "event-update-002" &&
        !u.searchParams.has("from"),
    ),
  ).toBe(true);
  fireEvent.click(screen.getByRole("button", { name: "下一页 →" }));
  await waitFor(() =>
    expect(
      fetcher.mock.calls.some(([u]) => u.includes("cursor=next-page")),
    ).toBe(true),
  );
  fireEvent.change(screen.getByLabelText("处理状态"), {
    target: { value: "rejected" },
  });
  fireEvent.click(screen.getByRole("button", { name: "执行查询" }));
  await waitFor(() =>
    expect(
      fetcher.mock.calls.some(
        ([u]) => u.includes("state=rejected") && !u.includes("cursor="),
      ),
    ).toBe(true),
  );
  expect(screen.getByRole("button", { name: "返回首页" })).toBeDisabled();
});
it("keeps UTC custom dates, reports invalid ranges locally and labels local search", async () => {
  const fetcher = setup(
    "alerts",
    "?from=2026-09-23T00:00:00.000Z&to=2026-09-23T01:00:00.000Z",
  );
  await screen.findByRole("button", {
    name: alertFixture.payload.title as string,
  });
  expect(screen.getByLabelText("开始时间")).toHaveValue("2026-09-23T00:00");
  const calls = fetcher.mock.calls.length;
  fireEvent.change(screen.getByLabelText("开始时间"), {
    target: { value: "2026-09-24T00:00" },
  });
  fireEvent.click(screen.getByRole("button", { name: "执行查询" }));
  expect(screen.getByRole("alert")).toHaveTextContent("时间范围必须为正向");
  expect(fetcher.mock.calls).toHaveLength(calls);
  fireEvent.change(screen.getByLabelText("本页搜索"), {
    target: { value: "不存在的标题" },
  });
  expect(screen.getByText("本页没有匹配项")).toBeInTheDocument();
  expect(fetcher.mock.calls).toHaveLength(calls);
});
it("shows independent stats failure without hiding valid rows or inventing zero totals", async () => {
  const fetcher = setup("alerts");
  await screen.findByRole("button", {
    name: alertFixture.payload.title as string,
  });
  const original = fetcher.getMockImplementation()!;
  fetcher.mockImplementation(async (input) =>
    input.includes("/stats")
      ? Response.json({ error: { message: "统计超时" } }, { status: 502 })
      : original(input),
  );
  fireEvent.click(screen.getByRole("button", { name: "刷新查询" }));
  expect(await screen.findByText(/统计不可用：统计超时/)).toBeInTheDocument();
  expect(
    screen.getByRole("button", { name: alertFixture.payload.title as string }),
  ).toBeInTheDocument();
});
