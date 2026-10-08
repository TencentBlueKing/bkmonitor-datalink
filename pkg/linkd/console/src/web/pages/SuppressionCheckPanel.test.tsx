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
import { SuppressionCheckPanel } from "./SuppressionCheckPanel";
import { runtimeClip } from "../../test-fixtures/suppression-runtime";
import { suppressionRequestFixture } from "../../test-fixtures/suppression-checks";
afterEach(() => {
  cleanup();
  sessionStorage.clear();
  vi.unstubAllGlobals();
});
function view(missing = false) {
  const w = runtimeClip();
  return render(
    <QueryClientProvider
      client={
        new QueryClient({ defaultOptions: { queries: { retry: false } } })
      }
    >
      <MemoryRouter>
        <SuppressionCheckPanel
          tenant={w.bk_tenant_id}
          kind={w.kind}
          id={w.id}
          observed={missing ? undefined : w}
        />
      </MemoryRouter>
    </QueryClientProvider>,
  );
}
it("preserves original owner/epoch across uncertain response and reload, with historical result after window loss", async () => {
  let failed = true;
  const bodies: string[] = [];
  let row = suppressionRequestFixture();
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
      if (input.includes("/requests/" + row.id)) {
        const done = suppressionRequestFixture(true);
        done.command = row.command;
        return Response.json(done);
      }
      return Response.json({
        bk_tenant_id: "tenant-a",
        kind: "clip",
        window_id: row.command.window_id,
        items: [row],
        next: "",
      });
    }),
  );
  const first = view();
  fireEvent.change(screen.getByLabelText("对账原因"), {
    target: { value: "核对 owner 状态" },
  });
  fireEvent.click(screen.getByRole("button", { name: "提交受控对账" }));
  await screen.findByText("网络中断");
  expect(screen.getByLabelText("对账原因")).toBeDisabled();
  first.unmount();
  failed = false;
  view(true);
  fireEvent.click(screen.getByRole("button", { name: "重试同一对账操作" }));
  await screen.findByText("窗口保留");
  expect(bodies).toHaveLength(2);
  expect(bodies[1]).toBe(bodies[0]);
  expect(JSON.parse(bodies[0])).toMatchObject({
    expected_epoch: "opening-event",
    expected_owner_alert_id: "owner-alert",
  });
  expect(screen.getByRole("button", { name: "提交受控对账" })).toBeDisabled();
  await waitFor(() =>
    expect(
      sessionStorage.getItem(
        "linkd:suppression-check:tenant-a:clip:" + runtimeClip().id,
      ),
    ).toBeNull(),
  );
});
