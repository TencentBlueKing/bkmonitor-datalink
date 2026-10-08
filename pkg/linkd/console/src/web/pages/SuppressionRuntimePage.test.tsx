import {
  cleanup,
  fireEvent,
  render,
  screen,
  waitFor,
  within,
} from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { MemoryRouter } from "react-router-dom";
import { afterEach, expect, it, vi } from "vitest";
import { SuppressionRuntimePage } from "./SuppressionRuntimePage";
import {
  runtimeClip,
  runtimeAggregation,
  runtimeSuppressionMembers,
  clipID,
} from "../../test-fixtures/suppression-runtime";
afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
});
function setup(search = "", handler?: (u: URL) => unknown) {
  const fetcher = vi.fn(async (input: string) => {
    const u = new URL(input, "http://localhost"),
      v = u.pathname.includes("/aggregation")
        ? runtimeAggregation()
        : runtimeClip();
    if (u.pathname.endsWith("/requests"))
      return Response.json({
        bk_tenant_id: v.bk_tenant_id,
        kind: v.kind,
        window_id: v.id,
        items: [],
        next: "",
      });
    return Response.json(
      handler
        ? handler(u)
        : u.pathname.endsWith("/members")
          ? runtimeSuppressionMembers(v)
          : u.pathname.endsWith("/clip") || u.pathname.endsWith("/aggregation")
            ? {
                bk_tenant_id: v.bk_tenant_id,
                kind: v.kind,
                items: [v],
                next: "",
              }
            : v,
    );
  });
  vi.stubGlobal("fetch", fetcher);
  render(
    <MemoryRouter initialEntries={["/suppression-runtime" + search]}>
      <QueryClientProvider
        client={
          new QueryClient({
            defaultOptions: { queries: { retry: false, staleTime: 60000 } },
          })
        }
      >
        <SuppressionRuntimePage />
      </QueryClientProvider>
    </MemoryRouter>,
  );
  return fetcher;
}
it("requires scope and exposes read-only counts and tenant-preserving links", async () => {
  const fetcher = setup();
  expect(fetcher).not.toHaveBeenCalled();
  expect(screen.getByText("请填写合法租户后查询。")).toBeInTheDocument();
  fireEvent.change(screen.getByLabelText("租户"), {
    target: { value: "tenant-a" },
  });
  fireEvent.click(screen.getByRole("button", { name: "查询" }));
  await screen.findByRole("table", { name: "当前抑制窗口" });
  expect(screen.getByText("2 / 3 次")).toBeInTheDocument();
  fireEvent.click(screen.getByRole("button", { name: "查看详情" }));
  await screen.findByRole("heading", { name: "防抖详情" });
  const members = await screen.findByRole("region", { name: "当前保留成员" });
  await within(members).findByRole("link", { name: "opening-event" });
  expect(
    within(members).getByRole("link", { name: "opening-event" }),
  ).toHaveAttribute("href", expect.stringContaining("detail_tenant=tenant-a"));
  expect(
    screen.getByRole("link", { name: "策略 policy / 1 ↗" }),
  ).toHaveAttribute("href", expect.stringContaining("version=1"));
  expect(
    screen.queryByRole("button", { name: /清理|强制放行|重置计数/ }),
  ).not.toBeInTheDocument();
});
it("refresh returns list and members to their first page without reusing stale cursors", async () => {
  const v = runtimeClip();
  const fetcher = setup("?bk_tenant_id=tenant-a&kind=clip&id=" + clipID, (u) =>
    u.pathname.endsWith("/members")
      ? runtimeSuppressionMembers(
          v,
          u.searchParams.get("after") ? "" : "members-next",
        )
      : u.pathname.endsWith("/clip")
        ? {
            bk_tenant_id: v.bk_tenant_id,
            kind: "clip",
            items: [],
            next: u.searchParams.get("after") ? "" : "list-next",
          }
        : v,
  );
  await screen.findByText("本页没有符合条件的窗口。");
  await waitFor(() =>
    expect(screen.getByRole("button", { name: "下一页成员" })).toBeEnabled(),
  );
  fireEvent.click(screen.getByRole("button", { name: "下一页" }));
  fireEvent.click(screen.getByRole("button", { name: "下一页成员" }));
  await waitFor(() =>
    expect(
      fetcher.mock.calls.some(([u]) => u.includes("after=members-next")),
    ).toBe(true),
  );
  await waitFor(() =>
    expect(screen.getByRole("button", { name: "刷新" })).toBeEnabled(),
  );
  const before = fetcher.mock.calls.length;
  fireEvent.click(screen.getByRole("button", { name: "刷新" }));
  await waitFor(() =>
    expect(
      fetcher.mock.calls
        .slice(before)
        .some(
          ([u]) =>
            u.includes("/members?") &&
            new URL(u, "http://localhost").searchParams.get("after") === "",
        ),
    ).toBe(true),
  );
  await waitFor(() =>
    expect(screen.getByRole("button", { name: "回到首页" })).toBeDisabled(),
  );
});
it("shows aggregation references separately from Alert lifecycle and tolerates absent live records", async () => {
  const v = runtimeAggregation();
  setup("?bk_tenant_id=tenant-a&kind=aggregation&id=" + v.id);
  await screen.findByRole("heading", { name: "关联聚合详情" });
  expect(screen.getAllByText("主告警已登记").length).toBeGreaterThan(0);
  expect(screen.getByText(/是否仍活动、能否处置/)).toBeInTheDocument();
  cleanup();
  const fetcher = setup("?bk_tenant_id=tenant-a&kind=bad");
  expect(fetcher).not.toHaveBeenCalled();
});
