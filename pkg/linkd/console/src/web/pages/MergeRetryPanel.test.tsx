import {
  cleanup,
  render,
  screen,
  fireEvent,
  waitFor,
} from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { MemoryRouter } from "react-router-dom";
import { afterEach, expect, it, vi } from "vitest";
import { MergeRetryPanel } from "./MergeRetryPanel";
import { mergePointFixture } from "../../test-fixtures/merge-retries";
import { mergeRetryFixture } from "../../test-fixtures/merge-retries";
afterEach(() => {
  cleanup();
  sessionStorage.clear();
  vi.unstubAllGlobals();
});
function view() {
  const w = mergePointFixture();
  return render(
    <QueryClientProvider
      client={
        new QueryClient({ defaultOptions: { queries: { retry: false } } })
      }
    >
      <MemoryRouter>
        <MergeRetryPanel
          tenant={w.bk_tenant_id}
          kind={w.kind}
          id={w.target_id}
        />
      </MemoryRouter>
    </QueryClientProvider>,
  );
}
it("preserves original progress token across uncertain response and reload, with history despite point read failure", async () => {
  let failed = true;
  let missing = false;
  const bodies: string[] = [];
  let row = mergeRetryFixture();
  vi.stubGlobal(
    "fetch",
    vi.fn(async (input: string, options?: RequestInit) => {
      if (options?.method === "POST") {
        bodies.push(String(options.body));
        if (failed) throw new Error("网络中断");
        row = {
          ...row,
          command: { ...row.command, ...JSON.parse(String(options.body)) },
        };
        return Response.json(row, { status: 202 });
      }
      if (input.includes("/control?"))
        return missing
          ? Response.json(
              { error: { message: "控制点暂不可用" } },
              { status: 503 },
            )
          : Response.json(mergePointFixture());
      if (input.includes("/requests/" + row.id)) {
        const done = mergeRetryFixture(true);
        done.command = row.command;
        return Response.json(done);
      }
      return Response.json({
        bk_tenant_id: "tenant-a",
        kind: "decisions",
        target_id: row.command.target_id,
        items: [row],
        next: "",
      });
    }),
  );
  const first = view();
  fireEvent.change(screen.getByLabelText("操作原因"), {
    target: { value: "核对 owner 状态" },
  });
  await waitFor(() =>
    expect(screen.getByRole("button", { name: "请求接续裁决" })).toBeEnabled(),
  );
  fireEvent.click(screen.getByRole("button", { name: "请求接续裁决" }));
  await screen.findByText("网络中断");
  expect(screen.getByLabelText("操作原因")).toBeDisabled();
  first.unmount();
  failed = false;
  missing = true;
  view();
  fireEvent.click(screen.getByRole("button", { name: "重试同一操作" }));
  await screen.findByText("原进度已推进");
  expect(bodies).toHaveLength(2);
  expect(bodies[1]).toBe(bodies[0]);
  expect(JSON.parse(bodies[0])).toMatchObject({
    expected_token: mergePointFixture().token,
  });
  expect(screen.getByRole("button", { name: "请求接续裁决" })).toBeDisabled();
  await waitFor(() =>
    expect(
      sessionStorage.getItem(
        "linkd:merge-request:tenant-a:decisions:" +
          mergePointFixture().target_id,
      ),
    ).toBeNull(),
  );
});
