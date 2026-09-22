import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import {
  cleanup,
  fireEvent,
  render,
  screen,
  within,
} from "@testing-library/react";
import { MemoryRouter } from "react-router-dom";
import { afterEach, expect, it, vi } from "vitest";
import { StrategyIndexPage } from "./StrategyIndexPage";
import type {
  StrategyBrowseResult,
  StrategyResult,
} from "../../shared/strategy-index";

afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
});
const target = {
  eventSourceId: "source",
  hookName: "active",
  notifyChannel: "open:changes",
  keyPrefix: "open",
  address: "redis:6379",
  database: 8,
  sources: ["source", "shared"],
};
const result: StrategyResult = {
  target,
  tenantId: "tenant",
  strategyId: "123",
  key: "open:tenant:123",
  startedAt: "2026-09-22T00:00:00Z",
  finishedAt: "2026-09-22T00:00:01Z",
  complete: true,
  warnings: ["不是原子快照，请复查。"],
  redis: { complete: true, total: 2, scanned: 2 },
  alerts: { complete: true, scanned: 2, matched: 2 },
  rows: [
    {
      fingerprint: "both",
      status: "matched",
      alerts: [
        { alertId: "alert-1", eventSourceId: "source", fingerprint: "both" },
      ],
    },
    { fingerprint: "missing", status: "missing_redis", alerts: [] },
    { fingerprint: "extra", status: "redis_only", alerts: [] },
  ],
};
const catalog: StrategyBrowseResult = {
  target,
  scannedAt: "2026-09-22T00:00:01Z",
  nextCursor: null,
  phase: "sets",
  warnings: [],
  health: {
    lastSuccess: "2026-09-22T00:00:00Z",
    lastAttempt: "2026-09-22T00:00:00Z",
    error: null,
    pendingCount: 0,
    oldestDueAt: null,
  },
  rows: [
    {
      tenantId: "tenant",
      strategyId: "123",
      key: "open:tenant:123",
      members: 2,
      pending: false,
      lastSuccess: "2026-09-22T00:00:00Z",
      lastAttempt: "2026-09-22T00:00:00Z",
      error: null,
    },
  ],
};
function show(response = result, empty = false) {
  const fetcher = vi.fn(
    async (url: RequestInfo | URL) =>
      new Response(
        JSON.stringify(
          String(url).includes("/targets")
            ? empty
              ? []
              : [target]
            : String(url).includes("/browse?")
              ? catalog
              : String(url).endsWith("/audits")
                ? null
                : response,
        ),
      ),
  );
  vi.stubGlobal("fetch", fetcher);
  render(
    <QueryClientProvider
      client={
        new QueryClient({ defaultOptions: { queries: { retry: false } } })
      }
    >
      <MemoryRouter>
        <StrategyIndexPage />
      </MemoryRouter>
    </QueryClientProvider>,
  );
  return fetcher;
}
async function submit() {
  fireEvent.click(
    await screen.findByRole("button", { name: "对账租户 tenant 策略 123" }),
  );
}
it("automatically lists combinations but reads alert members only after clicking a row", async () => {
  const fetcher = show();
  await screen.findByRole("button", { name: "对账租户 tenant 策略 123" });
  expect(screen.queryByLabelText("租户 ID")).toBeNull();
  expect(
    fetcher.mock.calls.filter(([url]) => String(url).includes("/browse?")),
  ).toHaveLength(1);
  expect(
    fetcher.mock.calls.filter(([url]) => String(url).includes("/reconcile?")),
  ).toHaveLength(0);
  await submit();
  expect(await screen.findByText("已读完当前查询范围")).toBeVisible();
  expect(screen.getByText(/通知 Channel：open:changes/)).toBeVisible();
  const calls = fetcher.mock.calls.filter(([url]) =>
    String(url).includes("/reconcile?"),
  );
  expect(calls).toHaveLength(1);
  expect(String(calls[0][0])).toContain("bk_tenant_id=tenant&strategy_id=123");
  expect(
    screen.getByRole("link", { name: "source / alert-1" }),
  ).toHaveAttribute("href", "/explore/alerts?bk_tenant_id=tenant&id=alert-1");
  fireEvent.change(screen.getByLabelText("对账结果"), {
    target: { value: "missing_redis" },
  });
  const table = within(
    within(screen.getByLabelText("索引成员对账")).getByRole("table"),
  );
  expect(table.getByText("missing")).toBeVisible();
  expect(table.queryByText("extra")).toBeNull();
  fireEvent.change(screen.getByLabelText("每批扫描量"), {
    target: { value: "100" },
  });
  expect(screen.queryByText("已读完当前查询范围")).toBeNull();
});
it("shows incomplete reads without reporting empty consistency", async () => {
  show({ ...result, complete: false, rows: [], warnings: ["Redis 读取失败"] });
  await submit();
  expect(await screen.findByText("对账不完整")).toBeVisible();
  expect(screen.getByText("Redis 读取失败")).toBeVisible();
  expect(screen.getByText(/不能据此判断两侧为空或一致/)).toBeVisible();
});
it("handles no configured hooks without opening a scan", async () => {
  const fetcher = show(result, true);
  expect(await screen.findByText(/当前来源未配置/)).toBeVisible();
  expect(screen.getByRole("button", { name: "重新扫描" })).toBeDisabled();
  expect(
    fetcher.mock.calls.filter(([url]) => String(url).includes("/browse?")),
  ).toHaveLength(0);
});
