import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import {
  cleanup,
  fireEvent,
  render,
  screen,
  within,
  waitFor,
} from "@testing-library/react";
import { MemoryRouter } from "react-router-dom";
import { afterEach, describe, expect, it, vi } from "vitest";
import { controlPlaneFixture } from "../../test-fixtures/control-plane";
import { ControlPlanePage } from "./ControlPlanePage";
afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
});
function renderPage() {
  return render(
    <QueryClientProvider
      client={
        new QueryClient({ defaultOptions: { queries: { retry: false } } })
      }
    >
      <MemoryRouter>
        <ControlPlanePage />
      </MemoryRouter>
    </QueryClientProvider>,
  );
}
const dynamic = {
  config: { enabled: false, origin: "yaml", sync_state: "disabled" },
  workers: {},
};
function response(value: unknown, status = 200) {
  return new Response(JSON.stringify(value), {
    status,
    headers: { "content-type": "application/json" },
  });
}
function stub(data = controlPlaneFixture()) {
  const mock = vi.fn(async (input: RequestInfo | URL) =>
    response(String(input).includes("dynamic-config") ? dynamic : data),
  );
  vi.stubGlobal("fetch", mock);
  return mock;
}
describe("Control Plane task workspace", () => {
  it("cancels the old range request and ignores a late response", async () => {
    let resolveOld: ((r: Response) => void) | undefined;
    let oldSignal: AbortSignal | undefined;
    let count = 0;
    vi.stubGlobal(
      "fetch",
      vi.fn((input: RequestInfo | URL, init?: RequestInit) => {
        const url = String(input);
        if (url.includes("dynamic-config"))
          return Promise.resolve(response(dynamic));
        if (url.includes("range_seconds=900")) {
          const next = controlPlaneFixture();
          next.owner = "new-owner";
          return Promise.resolve(response(next));
        }
        count++;
        if (count === 1)
          return Promise.resolve(response(controlPlaneFixture()));
        oldSignal = init?.signal ?? undefined;
        return new Promise<Response>((resolve) => {
          resolveOld = resolve;
        });
      }),
    );
    renderPage();
    await screen.findByText("管理 API");
    fireEvent.click(screen.getByRole("tab", { name: "执行情况" }));
    fireEvent.click(screen.getByRole("button", { name: "立即刷新" }));
    await waitFor(() => expect(resolveOld).toBeDefined());
    fireEvent.change(screen.getByLabelText("历史时间范围"), {
      target: { value: "900" },
    });
    await screen.findByText("new-owner");
    expect(oldSignal?.aborted).toBe(true);
    const late = controlPlaneFixture();
    late.owner = "old-response";
    resolveOld!(response(late));
    await waitFor(() =>
      expect(screen.queryByText("old-response")).not.toBeInTheDocument(),
    );
    expect(screen.getByText("new-owner")).toBeInTheDocument();
  });

  it("renders every backend registration, independent service, filters and effective configuration", async () => {
    const data = controlPlaneFixture();
    data.tasks.push({
      ...data.tasks[0],
      id: "new-task",
      name: "新注册任务",
      group: "新职责",
    });
    stub(data);
    renderPage();
    expect(await screen.findByText("新注册任务")).toBeInTheDocument();
    expect(screen.getByText("管理 API")).toBeInTheDocument();
    fireEvent.change(screen.getByLabelText("搜索任务"), {
      target: { value: "Provider" },
    });
    expect(screen.getAllByText(/未注入来源 Provider/).length).toBeGreaterThan(
      0,
    );
    fireEvent.click(screen.getByRole("tab", { name: "生效配置" }));
    expect(screen.getByText("默认配置")).toBeInTheDocument();
    fireEvent.click(screen.getByRole("button", { name: "已启用" }));
    expect(screen.getByText("没有匹配的任务。")).toBeInTheDocument();
  });
  it("keeps current health while missing owner metrics are explicitly incomplete, never zero", async () => {
    const data = controlPlaneFixture();
    data.metrics.series.active = [];
    data.metrics.status = "unavailable";
    stub(data);
    renderPage();
    await screen.findByText("管理 API");
    const detail = screen.getByRole("article", { name: "任务详情" });
    expect(within(detail).getByText("正常")).toBeInTheDocument();
    expect(
      within(detail).getByText(/当前运行状态以控制面快照为准/),
    ).toBeInTheDocument();
    fireEvent.click(screen.getByRole("tab", { name: "执行情况" }));
    expect(within(detail).getAllByText("—")).toHaveLength(4);
  });
  it("shows isolated target failures and duplicate owner evidence", async () => {
    const data = controlPlaneFixture();
    data.tasks[0].state = "failed";
    data.tasks[0].steps[0].errorCode = "metadata_unavailable";
    data.tasks[0].steps[0].outcome = "failed";
    data.metrics.series.active.push({
      labels: { linkd_task: "scheduler", instance: "other" },
      value: 1,
      timestamp: 1,
    });
    stub(data);
    renderPage();
    expect(await screen.findByText("metadata_unavailable")).toBeInTheDocument();
    expect(screen.getByText(/2 个活跃 Owner/)).toBeInTheDocument();
  });
  it("one refresh includes dynamic state and retains old snapshot when current read fails", async () => {
    let fail = false;
    const mock = vi.fn(async (input: RequestInfo | URL) =>
      String(input).includes("dynamic-config")
        ? response(dynamic)
        : fail
          ? response({ error: { message: "控制面离线" } }, 502)
          : response(controlPlaneFixture()),
    );
    vi.stubGlobal("fetch", mock);
    renderPage();
    await screen.findByText("管理 API");
    fail = true;
    fireEvent.click(screen.getByRole("button", { name: "立即刷新" }));
    expect(await screen.findByRole("alert")).toHaveTextContent("旧快照");
    expect(screen.getByText("管理 API")).toBeInTheDocument();
    await waitFor(() =>
      expect(
        mock.mock.calls.filter(([url]) =>
          String(url).includes("dynamic-config"),
        ),
      ).toHaveLength(2),
    );
  });
});
