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
import { ActionDeliveriesPage } from "./ActionDeliveriesPage";
import {
  actionFixture,
  actionSnapshotFixture,
  actionOrderFixture,
  actionID,
  actionTenant,
  recoveredAction,
} from "../../test-fixtures/action-deliveries";
afterEach(() => {
  cleanup();
  sessionStorage.clear();
  vi.unstubAllGlobals();
});
function view() {
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  });
  return render(
    <QueryClientProvider client={client}>
      <MemoryRouter
        initialEntries={[
          "/action-deliveries?bk_tenant_id=" + actionTenant + "&id=" + actionID,
        ]}
      >
        <ActionDeliveriesPage />
      </MemoryRouter>
    </QueryClientProvider>,
  );
}
it("keeps uncertain retry command across remount and never calls accepted work completed", async () => {
  let row = actionFixture(),
    fail = true;
  const writes: string[] = [];
  vi.stubGlobal(
    "fetch",
    vi.fn(async (input: string, options?: RequestInit) => {
      if (options?.method === "POST") {
        writes.push(String(options.body));
        if (fail) throw new Error("网络中断");
        row = recoveredAction(
          JSON.parse(String(options.body)) as Record<string, unknown>,
        );
        return Response.json(row, { status: 202 });
      }
      if (input.includes("/order?"))
        return Response.json(actionOrderFixture(row));
      return Response.json(
        input.includes("/" + actionID)
          ? row
          : { bk_tenant_id: actionTenant, items: [row], next: "" },
      );
    }),
  );
  const first = view();
  await screen.findByLabelText("恢复原因");
  fireEvent.change(screen.getByLabelText("恢复原因"), {
    target: { value: "鉴权恢复后重试" },
  });
  fireEvent.click(screen.getByRole("button", { name: "恢复原任务" }));
  await screen.findByText("网络中断");
  expect(screen.getByLabelText("恢复原因")).toBeDisabled();
  first.unmount();
  fail = false;
  view();
  fireEvent.click(
    await screen.findByRole("button", { name: "重试同一恢复操作" }),
  );
  await screen.findByText(/恢复操作已被接受/);
  expect(writes).toHaveLength(2);
  expect(writes[1]).toBe(writes[0]);
  expect(JSON.parse(writes[0])).toMatchObject({
    expected_version: "opaque-cas-1",
    bk_tenant_id: actionTenant,
  });
  expect(
    screen.queryByText("接收端已受理", { selector: "strong" }),
  ).not.toBeInTheDocument();
  await waitFor(() => expect(sessionStorage.length).toBe(0));
  expect(screen.getByText("最近一次人工恢复")).toBeVisible();
});
it("separates receiver acceptance from execution and loads frozen request only on demand", async () => {
  const row = actionFixture("succeeded"),
    fetcher = vi.fn(async (input: string) =>
      Response.json(
        input.includes("/snapshot")
          ? actionSnapshotFixture(row)
          : input.includes("/order?")
            ? actionOrderFixture(row)
            : input.includes("/" + actionID)
              ? row
              : { bk_tenant_id: actionTenant, items: [row], next: "" },
      ),
    );
  vi.stubGlobal("fetch", fetcher);
  view();
  await screen.findByText("接收端已持久受理 · r2 · active");
  expect(screen.getByText("这不是处置执行完成的确认。")).toBeVisible();
  expect(
    screen.queryByRole("button", { name: "恢复原任务" }),
  ).not.toBeInTheDocument();
  expect(fetcher.mock.calls.some(([u]) => u.includes("/snapshot"))).toBe(false);
  fireEvent.click(screen.getByRole("button", { name: "查看冻结快照" }));
  await screen.findByText(/冻结时的标题/);
  expect(screen.getByRole("link", { name: "查看当前 Alert" })).toHaveAttribute(
    "href",
    expect.stringContaining("detail_tenant=tenant-a"),
  );
});
it("shows previous uncertainty on local skip and links the earlier failed ordering barrier", async () => {
  const row = actionFixture("skipped");
  row.progress.previous_unconfirmed = true;
  const earlier = actionFixture("failed");
  earlier.id = "e".repeat(64);
  earlier.revision = 1;
  vi.stubGlobal(
    "fetch",
    vi.fn(async (input: string) =>
      Response.json(
        input.includes("/order?")
          ? actionOrderFixture(row, earlier)
          : input.includes("/" + actionID)
            ? row
            : { bk_tenant_id: actionTenant, items: [row], next: "" },
      ),
    ),
  );
  view();
  await screen.findByText(/此前尝试结果未确认/);
  await screen.findByRole("link", { name: "查看队首任务" });
  expect(screen.getByText(/本次依据较新终态投影在本地跳过/)).toBeVisible();
  expect(
    screen.queryByRole("heading", { name: "动作受理确认" }),
  ).not.toBeInTheDocument();
  expect(
    screen.queryByRole("button", { name: "恢复原任务" }),
  ).not.toBeInTheDocument();
});
