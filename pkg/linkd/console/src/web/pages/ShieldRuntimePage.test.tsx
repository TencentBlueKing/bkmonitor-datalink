import {
  cleanup,
  fireEvent,
  render,
  screen,
  waitFor,
} from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { MemoryRouter } from "react-router-dom";
import { afterEach, expect, it, vi } from "vitest";
import {
  childID,
  runtimeShield,
  runtimeShieldHistory,
  shieldTenant,
} from "../../test-fixtures/shield-runtime";
import { ShieldRuntimePage } from "./ShieldRuntimePage";
afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
});
function setup(query = "?bk_tenant_id=tenant-a&id=" + childID) {
  let released = false;
  const fetcher = vi.fn(async (input: string) => {
    const u = new URL(input, "http://local");
    if (u.pathname.endsWith("/check"))
      return Response.json({
        bk_tenant_id: shieldTenant,
        alert_id: childID,
        check: null,
      });
    if (u.pathname.endsWith("/requests"))
      return Response.json({
        bk_tenant_id: shieldTenant,
        alert_id: childID,
        items: [],
        next: "",
      });
    if (u.pathname.endsWith("/history"))
      return Response.json(
        u.searchParams.get("after")
          ? runtimeShieldHistory()
          : { ...runtimeShieldHistory(), items: [], next: "later-page" },
      );
    if (u.pathname.endsWith(childID))
      return Response.json(runtimeShield(released));
    return Response.json({
      bk_tenant_id: shieldTenant,
      items: released ? [] : [runtimeShield()],
      next: "",
    });
  });
  vi.stubGlobal("fetch", fetcher);
  render(
    <QueryClientProvider
      client={
        new QueryClient({ defaultOptions: { queries: { retry: false } } })
      }
    >
      <MemoryRouter initialEntries={["/shield-runtime" + query]}>
        <ShieldRuntimePage />
      </MemoryRouter>
    </QueryClientProvider>,
  );
  return {
    fetcher,
    release: () => {
      released = true;
    },
  };
}
it("requires tenant and preserves scoped links for fixed main and binding Event", async () => {
  const { fetcher } = setup("");
  expect(screen.getByText("请填写租户后查询。")).toBeInTheDocument();
  expect(fetcher).not.toHaveBeenCalled();
  fireEvent.change(screen.getByLabelText("租户"), {
    target: { value: shieldTenant },
  });
  fireEvent.click(screen.getByRole("button", { name: "查询" }));
  await screen.findByRole("table", { name: "当前屏蔽关系" });
  fireEvent.click(screen.getByRole("button", { name: "查看详情" }));
  await screen.findByText("CMDB 依赖", { selector: "strong" });
  expect(screen.getByRole("link", { name: "main-alert" })).toHaveAttribute(
    "href",
    expect.stringContaining("detail_tenant=tenant-a"),
  );
  expect(screen.getByRole("link", { name: "event-child" })).toHaveAttribute(
    "href",
    expect.stringContaining("/explore/events"),
  );
});
it("paginates empty history and never infers admission from automatic unshield", async () => {
  const h = setup();
  await screen.findByText(/本生命周期尚未放行处置/);
  await screen.findByText(/继续下一页查找历史/);
  fireEvent.click(screen.getByRole("button", { name: "下一页历史" }));
  await screen.findByText(/解除绑定/);
  h.release();
  fireEvent.click(screen.getByRole("button", { name: "刷新" }));
  await screen.findByText("当前没有活动屏蔽绑定。");
  expect(screen.getByText(/本生命周期尚未放行处置/)).toBeInTheDocument();
  await waitFor(() =>
    expect(
      h.fetcher.mock.calls.every(
        ([raw]) =>
          new URL(raw, "http://local").searchParams.get("bk_tenant_id") ===
          shieldTenant,
      ),
    ).toBe(true),
  );
});
