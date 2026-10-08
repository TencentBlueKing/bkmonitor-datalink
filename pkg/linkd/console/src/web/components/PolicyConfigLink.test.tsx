import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { cleanup, fireEvent, render, screen } from "@testing-library/react";
import { afterEach, expect, it, vi } from "vitest";
import { policyRecord } from "../../test-fixtures/policies";
import { PolicyConfigLink } from "./PolicyConfigLink";

afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
});
function setup() {
  const fetcher = vi.fn(async (input: string) => {
    const url = new URL(input, "http://local");
    const tenant = url.searchParams.get("bk_tenant_id");
    return Response.json({
      bk_tenant_id: tenant,
      type: "shield",
      url:
        tenant === "tenant-a" ? "https://kac.example/#/kac/alarmShield" : null,
      ...(tenant === "tenant-a" ? {} : { reason: "not_configured" }),
    });
  });
  vi.stubGlobal("fetch", fetcher);
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  });
  const record = policyRecord("shield");
  const tree = (value = record) => (
    <QueryClientProvider client={client}>
      <PolicyConfigLink record={value} />
    </QueryClientProvider>
  );
  const view = render(tree());
  return { fetcher, view, tree, record };
}
it("opens current KAC configuration safely and removes a previous tenant link immediately", async () => {
  const { fetcher, view, tree, record } = setup();
  const link = await screen.findByRole("link", { name: /在 KAC 管理/ });
  expect(link).toHaveAttribute("target", "_blank");
  expect(link).toHaveAttribute("rel", "noopener noreferrer");
  expect(link).toHaveAttribute("referrerPolicy", "no-referrer");
  expect(screen.getByText(/使用 KAC 的登录租户与权限/)).toBeVisible();
  const params = new URL(fetcher.mock.calls[0][0], "http://local").searchParams;
  expect(Object.fromEntries(params)).toEqual({
    bk_tenant_id: "tenant-a",
    type: "shield",
    id: "db-noise",
    space_code: "bkcc__2",
  });
  view.rerender(tree({ ...record, bk_tenant_id: "tenant-b" }));
  expect(screen.queryByRole("link")).not.toBeInTheDocument();
  await screen.findByText("当前租户尚未配置此类策略的 KAC 入口。");
});
it("shows a failed lookup with explicit retry without carrying a stale destination", async () => {
  const { fetcher, view, tree, record } = setup();
  await screen.findByRole("link");
  fetcher.mockResolvedValueOnce(
    Response.json({ error: { message: "unavailable" } }, { status: 503 }),
  );
  view.rerender(tree({ ...record, id: "other" }));
  await screen.findByText(/KAC 配置入口读取失败/);
  expect(screen.queryByRole("link")).not.toBeInTheDocument();
  fetcher.mockResolvedValueOnce(
    Response.json({
      bk_tenant_id: "tenant-a",
      type: "shield",
      url: null,
      reason: "missing_context",
    }),
  );
  fireEvent.click(screen.getByRole("button", { name: "重试配置入口" }));
  await screen.findByText("缺少配置入口所需的策略或业务范围信息。");
});
