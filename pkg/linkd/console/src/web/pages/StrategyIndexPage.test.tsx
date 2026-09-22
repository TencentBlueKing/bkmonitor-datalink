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
import type { StrategyResult } from "../../shared/strategy-index";

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
function show(response = result, empty = false) {
  const fetcher = vi.fn(
    async (url: RequestInfo | URL) =>
      new Response(
        JSON.stringify(
          String(url).includes("/targets") ? (empty ? [] : [target]) : response,
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
  await screen.findByRole("option", { name: "source / active" });
  fireEvent.change(screen.getByLabelText("租户 ID"), {
    target: { value: "tenant" },
  });
  fireEvent.change(screen.getByLabelText("策略 ID"), {
    target: { value: "123" },
  });
  fireEvent.click(screen.getByRole("button", { name: "查询并对账" }));
}
it("queries only on submit and links matched alerts with tenant isolation", async () => {
  const fetcher = show();
  expect(screen.getByRole("button", { name: "查询并对账" })).toBeDisabled();
  await submit();
  expect(await screen.findByText("已读完当前查询范围")).toBeVisible();
  expect(screen.getByText(/通知 Channel：open:changes/)).toBeVisible();
  expect(fetcher).toHaveBeenCalledTimes(2);
  expect(String(fetcher.mock.calls[1][0])).toContain(
    "bk_tenant_id=tenant&strategy_id=123",
  );
  expect(
    screen.getByRole("link", { name: "source / alert-1" }),
  ).toHaveAttribute("href", "/explore/alerts?bk_tenant_id=tenant&id=alert-1");
  fireEvent.change(screen.getByLabelText("对账结果"), {
    target: { value: "missing_redis" },
  });
  const table = within(screen.getByRole("table"));
  expect(table.getByText("missing")).toBeVisible();
  expect(table.queryByText("extra")).toBeNull();
  fireEvent.change(screen.getByLabelText("租户 ID"), {
    target: { value: "another" },
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
it("handles no configured hooks", async () => {
  show(result, true);
  expect(await screen.findByText(/当前来源未配置/)).toBeVisible();
  expect(screen.getByRole("button", { name: "查询并对账" })).toBeDisabled();
});
