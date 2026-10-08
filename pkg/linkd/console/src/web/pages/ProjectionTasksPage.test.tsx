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
import { ProjectionTasksPage } from "./ProjectionTasksPage";
import {
  projectionFixture,
  projectionID,
  projectionTenant,
  recoveredProjection,
} from "../../test-fixtures/projection-tasks";
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
          "/projection-tasks?bk_tenant_id=" +
            projectionTenant +
            "&id=" +
            projectionID,
        ]}
      >
        <ProjectionTasksPage />
      </MemoryRouter>
    </QueryClientProvider>,
  );
}
it("keeps uncertain retry command across remount and never calls accepted work synchronized", async () => {
  let row = projectionFixture(),
    fail = true;
  const writes: string[] = [];
  vi.stubGlobal(
    "fetch",
    vi.fn(async (input: string, options?: RequestInit) => {
      if (options?.method === "POST") {
        writes.push(String(options.body));
        if (fail) throw new Error("网络中断");
        row = recoveredProjection(
          JSON.parse(String(options.body)) as Record<string, unknown>,
        );
        return Response.json(row, { status: 202 });
      }
      return Response.json(
        input.includes("/" + projectionID)
          ? row
          : { bk_tenant_id: projectionTenant, items: [row], next: "" },
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
    bk_tenant_id: projectionTenant,
  });
  expect(
    screen.queryByText("本任务已同步", { selector: "strong" }),
  ).not.toBeInTheDocument();
  await waitFor(() => expect(sessionStorage.length).toBe(0));
  expect(screen.getByText("最近一次人工恢复")).toBeVisible();
});
it("separates durable receipt from local ACK and loads only immutable snapshot on demand", async () => {
  const row = projectionFixture("delivered"),
    fetcher = vi.fn(async (input: string) => {
      if (input.includes("/snapshot"))
        return Response.json({
          bk_tenant_id: projectionTenant,
          id: projectionID,
          revision: row.revision,
          content_hash: row.content_hash,
          alert: {
            bk_tenant_id: projectionTenant,
            alert_id: row.alert_id,
            revision: row.revision,
            title: "冻结标题",
          },
        });
      return Response.json(
        input.includes("/" + projectionID)
          ? row
          : { bk_tenant_id: projectionTenant, items: [row], next: "" },
      );
    });
  vi.stubGlobal("fetch", fetcher);
  view();
  await screen.findByText("远端已确认，本地水位尚待完成；后续执行只补 ACK。");
  expect(
    screen.queryByRole("button", { name: "恢复原任务" }),
  ).not.toBeInTheDocument();
  expect(fetcher.mock.calls.some(([u]) => u.includes("/snapshot"))).toBe(false);
  fireEvent.click(screen.getByRole("button", { name: "查看冻结快照" }));
  await screen.findByText(/冻结标题/);
  expect(screen.getByRole("link", { name: "查看当前 Alert" })).toHaveAttribute(
    "href",
    expect.stringContaining("detail_tenant=tenant-a"),
  );
});
