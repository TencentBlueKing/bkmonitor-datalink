import { mergePointFixture } from "../../test-fixtures/merge-retries";
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
  decisionID,
  mergeTenant,
  runtimeDecision,
  runtimeMembers,
  runtimeRelation,
  runtimeWindow,
  runtimeSnapshot,
  windowID,
} from "../../test-fixtures/merge-runtime";
import { MergeRuntimePage } from "./MergeRuntimePage";
afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
});
function setup(
  query = "?bk_tenant_id=tenant-a&resource=decisions&id=" + decisionID,
) {
  const fetcher = vi.fn(async (input: string) => {
    const u = new URL(input, "http://local");
    const kind = u.pathname.includes("/relations/") ? "relations" : "decisions";
    if (u.pathname.endsWith("/control"))
      return Response.json(mergePointFixture(kind, true));
    if (u.pathname.endsWith("/requests"))
      return Response.json({
        bk_tenant_id: mergeTenant,
        kind,
        target_id: decisionID,
        items: [],
        next: "",
      });
    if (u.pathname.includes("/members/"))
      return Response.json(runtimeSnapshot());
    if (u.pathname.endsWith("/members")) return Response.json(runtimeMembers());
    if (u.pathname.includes("/windows/")) return Response.json(runtimeWindow());
    if (u.pathname.includes("/relations/"))
      return Response.json(runtimeRelation());
    if (u.pathname.endsWith(decisionID))
      return Response.json(runtimeDecision());
    const resource = u.searchParams.get("resource");
    return Response.json({
      bk_tenant_id: mergeTenant,
      items: [
        resource === "windows"
          ? runtimeWindow()
          : resource === "relations"
            ? runtimeRelation()
            : runtimeDecision(),
      ],
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
      <MemoryRouter initialEntries={["/merge-runtime" + query]}>
        <MergeRuntimePage />
      </MemoryRouter>
    </QueryClientProvider>,
  );
  return fetcher;
}
it("requires explicit tenant and distinguishes durable execution from current relation/lifecycle", async () => {
  const fetcher = setup("");
  expect(screen.getByText("请填写租户后查询。")).toBeInTheDocument();
  expect(fetcher).not.toHaveBeenCalled();
  fireEvent.change(screen.getByLabelText("租户"), {
    target: { value: mergeTenant },
  });
  fireEvent.click(screen.getByRole("button", { name: "查询" }));
  await screen.findByRole("table", { name: "持久化裁决" });
  fireEvent.click(screen.getByRole("button", { name: "查看详情" }));
  await screen.findByRole("table", { name: "固定成员快照" });
  fireEvent.click(screen.getByRole("button", { name: "查看冻结快照" }));
  await screen.findByRole("heading", { name: "冻结成员 Alert 快照" });
  await screen.findByText(/opening content/);
  expect(screen.getByText(/首次捕获时的状态/)).toBeInTheDocument();
  expect(screen.getByRole("link", { name: "child-a" })).toHaveAttribute(
    "href",
    expect.stringContaining("detail_tenant=tenant-a"),
  );
  fireEvent.click(screen.getByRole("button", { name: "查看父子关系" }));
  await screen.findByRole("table", { name: "关系成员" });
  expect(screen.getAllByText("关系已结束").length).toBeGreaterThan(0);
  expect(screen.getByText(/不自动补发处置/)).toBeInTheDocument();
  expect(
    fetcher.mock.calls.every(
      ([url]) =>
        new URL(url, "http://local").searchParams.get("bk_tenant_id") ===
        mergeTenant,
    ),
  ).toBe(true);
});
it("shows committed and uncommitted window members separately and does not treat an absent window as failure", async () => {
  const fetcher = setup(
    "?bk_tenant_id=tenant-a&resource=windows&id=" + windowID,
  );
  await screen.findByRole("table", { name: "窗口成员" });
  expect(screen.getByText("尚未确认")).toBeInTheDocument();
  expect(screen.getByText("条件组 2：0 个首次命中成员")).toBeInTheDocument();
  fetcher.mockImplementation(
    async () =>
      new Response(JSON.stringify({ error: { message: "未找到记录" } }), {
        status: 404,
      }),
  );
  fireEvent.click(screen.getByRole("button", { name: "刷新" }));
  await waitFor(() =>
    expect(screen.getAllByRole("alert").length).toBeGreaterThan(0),
  );
  expect(screen.getByText(/不将窗口缺失视为合并失败/)).toBeInTheDocument();
});
