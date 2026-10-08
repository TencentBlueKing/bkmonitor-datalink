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
import { ShieldCheckPanel } from "./ShieldCheckPanel";
import {
  runtimeCheck,
  runtimeCheckRequest,
} from "../../test-fixtures/shield-checks";
import {
  runtimeShield,
  childID,
  shieldTenant,
} from "../../test-fixtures/shield-runtime";
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
      <MemoryRouter>
        <ShieldCheckPanel row={runtimeShield()} />
      </MemoryRouter>
    </QueryClientProvider>,
  );
}
it("preserves uncertain command across remount and displays final result without admitting Alert", async () => {
  const bodies: string[] = [];
  let fail = true;
  const accepted = runtimeCheckRequest();
  const fetcher = vi.fn(async (input: string, options?: RequestInit) => {
    const u = new URL(input, "http://local");
    if (options?.method === "POST") {
      bodies.push(String(options.body));
      if (fail) throw new Error("网络中断");
      Object.assign(accepted.command, JSON.parse(String(options.body)));
      return Response.json(accepted, { status: 202 });
    }
    if (u.pathname.endsWith("/requests/" + accepted.id)) {
      const final = runtimeCheckRequest(true);
      final.command = accepted.command;
      return Response.json(final);
    }
    if (u.pathname.endsWith("/check"))
      return Response.json({
        bk_tenant_id: shieldTenant,
        alert_id: childID,
        check: runtimeCheck(),
      });
    return Response.json({
      bk_tenant_id: shieldTenant,
      alert_id: childID,
      items: [],
      next: "",
    });
  });
  vi.stubGlobal("fetch", fetcher);
  const first = view();
  await screen.findByText("部分条件未能检查");
  expect(screen.getByText("dependency_child_unavailable")).toBeVisible();
  fireEvent.change(screen.getByLabelText("复查原因"), {
    target: { value: "依赖服务恢复后复查" },
  });
  fireEvent.click(screen.getByRole("button", { name: "提交复查" }));
  await screen.findByText("网络中断");
  expect(screen.getByLabelText("复查原因")).toBeDisabled();
  first.unmount();
  fail = false;
  view();
  fireEvent.click(screen.getByRole("button", { name: "重试同一复查操作" }));
  await screen.findByText("请求已完成");
  expect(bodies).toHaveLength(2);
  expect(bodies[1]).toBe(bodies[0]);
  expect(JSON.parse(bodies[0])).toMatchObject({
    expected_revision: 2,
    bk_tenant_id: shieldTenant,
  });
  await waitFor(() => expect(sessionStorage.length).toBe(0));
  expect(screen.getByRole("button", { name: "准备新的复查" })).toBeVisible();
});
it("shows absent diagnosis separately from successful checks", async () => {
  vi.stubGlobal(
    "fetch",
    vi.fn(async (input: string) =>
      Response.json(
        input.includes("/check?")
          ? { bk_tenant_id: shieldTenant, alert_id: childID, check: null }
          : {
              bk_tenant_id: shieldTenant,
              alert_id: childID,
              items: [],
              next: "",
            },
      ),
    ),
  );
  view();
  await screen.findByText("尚无复查记录。");
  expect(screen.getByRole("button", { name: "提交复查" })).toBeDisabled();
});

it("labels automatic event-hint checks separately from manual requests", async () => {
  const check = runtimeCheck();
  check.trigger = "hint";
  vi.stubGlobal(
    "fetch",
    vi.fn(async (input: string) =>
      Response.json(
        input.includes("/check?")
          ? { bk_tenant_id: shieldTenant, alert_id: childID, check }
          : {
              bk_tenant_id: shieldTenant,
              alert_id: childID,
              items: [],
              next: "",
            },
      ),
    ),
  );
  view();
  expect(await screen.findByText(/事件提示复查/)).toBeVisible();
  expect(screen.queryByText(/手动复查 · 观察版本/)).not.toBeInTheDocument();
});

it("pages candidate diagnostics and does not describe skipped candidates as retained bindings", async () => {
  const check = runtimeCheck();
  check.trigger = "hint";
  const steps = check.report.decision!.steps,
    policy = steps[0].policy;
  for (let i = 0; i < 17; i++)
    steps.push({ policy, outcome: "skipped", reason_code: `candidate-${i}` });
  vi.stubGlobal(
    "fetch",
    vi.fn(async (input: string) =>
      Response.json(
        input.includes("/check?")
          ? { bk_tenant_id: shieldTenant, alert_id: childID, check }
          : {
              bk_tenant_id: shieldTenant,
              alert_id: childID,
              items: [],
              next: "",
            },
      ),
    ),
  );
  view();
  expect(await screen.findByText("1 / 2 · 共 18 条步骤")).toBeVisible();
  expect(screen.getAllByText(/未能评估，跳过候选/)).toHaveLength(15);
  expect(screen.queryByText("candidate-16")).not.toBeInTheDocument();
  fireEvent.click(screen.getByRole("button", { name: "检查步骤下一页" }));
  expect(screen.getByText("candidate-16")).toBeVisible();
  expect(screen.getAllByText(/未能评估，跳过候选/)).toHaveLength(2);
  expect(screen.getByRole("button", { name: "检查步骤下一页" })).toBeDisabled();
});
