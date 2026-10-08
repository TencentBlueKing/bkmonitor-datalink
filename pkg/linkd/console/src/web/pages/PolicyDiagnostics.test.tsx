import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { render, screen, cleanup, fireEvent } from "@testing-library/react";
import { afterEach, it, expect, vi } from "vitest";
import { PolicySimulation } from "./PolicySimulation";
import { PolicyStatistics } from "./PolicyStatistics";
import { KACAlertLink } from "../components/KACAlertLink";
import { policyRelease } from "../../test-fixtures/policies";
afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
});
function view(child: React.ReactNode) {
  return render(
    <QueryClientProvider
      client={
        new QueryClient({ defaultOptions: { queries: { retry: false } } })
      }
    >
      {child}
    </QueryClientProvider>,
  );
}
it("shows no optional KAC feature when no template exists and validates response scope", async () => {
  const alarm = "linkd-550e8400-e29b-51d4-a716-446655440000";
  const fetcher = vi.fn(async () =>
    Response.json({ bk_tenant_id: "tenant", alarm_id: alarm, url: null }),
  );
  vi.stubGlobal("fetch", fetcher);
  const first = view(<KACAlertLink tenant="tenant" alarmID={alarm} />);
  await vi.waitFor(() => expect(fetcher).toHaveBeenCalled());
  expect(screen.queryByRole("link")).toBeNull();
  first.unmount();
  fetcher.mockImplementation(async () =>
    Response.json({
      bk_tenant_id: "tenant",
      alarm_id: alarm,
      url: "https://kac.example/?id=" + alarm,
    }),
  );
  view(<KACAlertLink tenant="tenant" alarmID={alarm} />);
  expect(await screen.findByRole("link", { name: /查看 KAC/ })).toHaveAttribute(
    "rel",
    "noopener noreferrer",
  );
});
it("supports ordered event steps and explicit virtual time advancement without real side effects", async () => {
  const release = policyRelease();
  const fetcher = vi.fn(async (_url: string, init: RequestInit) => {
    const q = JSON.parse(String(init.body));
    return Response.json({
      ...q,
      mode: "state_simulation",
      compiled: release.compiled,
      steps: q.steps.map((s: { at: string; event_id?: string }) => ({
        ...s,
        outcome: s.event_id ? "alert_suppressed" : "time_advanced",
        replayed: false,
        alerts: [],
        windows: [],
      })),
    });
  });
  vi.stubGlobal("fetch", fetcher);
  view(<PolicySimulation release={release} />);
  fireEvent.change(screen.getByLabelText("模拟事件 ID"), {
    target: { value: "event-a" },
  });
  fireEvent.change(screen.getByLabelText("模拟判定时间"), {
    target: { value: "2026-10-08T00:00:00Z" },
  });
  fireEvent.click(screen.getByText("添加事件步骤"));
  fireEvent.click(screen.getByText("推进虚拟时间"));
  fireEvent.click(screen.getByText("运行隔离模拟"));
  await screen.findByLabelText("模拟轨迹");
  expect(fetcher).toHaveBeenCalledTimes(1);
  const [url, init] = fetcher.mock.calls[0];
  expect(url).toBe("/local-api/policies/simulate");
  expect(JSON.parse(String(init.body)).steps).toEqual([
    { at: "2026-10-08T00:00:00.000Z", event_id: "event-a" },
    { at: "2026-10-08T00:01:00.000Z" },
  ]);
  fireEvent.change(screen.getByLabelText("模拟步骤 JSON"), {
    target: { value: '[{"at":"bad"}]' },
  });
  expect(screen.queryByLabelText("模拟轨迹")).toBeNull();
  fireEvent.click(screen.getByText("运行隔离模拟"));
  await screen.findByRole("alert");
  expect(fetcher).toHaveBeenCalledTimes(1);
});
it("shows counts only for current scope and distinguishes unavailable statistics from zero", async () => {
  let fail = false;
  vi.stubGlobal(
    "fetch",
    vi.fn(async () =>
      fail
        ? Response.json({ error: { message: "down" } }, { status: 503 })
        : Response.json({
            bk_tenant_id: "tenant",
            type: "merge",
            hours: 24,
            mode: "execution_observations",
            from: "2026-10-07T01:00:00Z",
            to: "2026-10-08T00:00:00Z",
            items: [
              {
                id: "p",
                matched: 9,
                not_matched: 0,
                unavailable: 2,
                execution_skipped: 3,
              },
            ],
          }),
    ),
  );
  view(<PolicyStatistics tenant="tenant" kind="merge" ids={["p"]} />);
  await screen.findByText("9");
  fail = true;
  fireEvent.click(screen.getByText("刷新统计"));
  await screen.findByText("统计暂不可用，不能视为零。");
  expect(screen.queryByRole("table")).toBeNull();
});
