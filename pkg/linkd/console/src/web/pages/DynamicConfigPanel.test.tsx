import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import {
  render,
  screen,
  fireEvent,
  waitFor,
  cleanup,
} from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import { DynamicConfigPanel } from "./DynamicConfigPanel";

afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
});
function show(value: unknown) {
  vi.stubGlobal(
    "fetch",
    vi.fn(async () => new Response(JSON.stringify(value))),
  );
  return render(
    <QueryClientProvider
      client={
        new QueryClient({ defaultOptions: { queries: { retry: false } } })
      }
    >
      <DynamicConfigPanel autoRefresh={false} />
    </QueryClientProvider>,
  );
}
describe("DynamicConfigPanel", () => {
  it("renders disabled without alarming", async () => {
    show({
      config: { enabled: false, origin: "yaml", sync_state: "disabled" },
      workers: {},
    });
    expect(await screen.findByText("未启用")).toBeInTheDocument();
    expect(screen.queryByRole("alert")).not.toBeInTheDocument();
  });
  it("shows persisted fallback, failure and actual worker application", async () => {
    show({
      config: {
        enabled: true,
        origin: "persisted",
        sync_state: "source_error",
        source: "kingeye",
        source_type: "kingeye_alarmlevel",
        bk_tenant_id: "system",
        persisted_at: "2026-09-21T00:00:00Z",
        current: {
          digest: "abc",
          severity: {
            default_severity: "warning",
            levels: [
              { name: "fatal", priority: 0 },
              { name: "warning", priority: 1 },
            ],
          },
        },
      },
      workers: {
        a: { id: "worker-a", config_digest: "abc" },
        b: {
          id: "worker-b",
          config_digest: "old",
          config_error: "fetch_failed",
        },
      },
    });
    expect(await screen.findByText(/上游读取失败/)).toBeInTheDocument();
    expect(screen.getByText("fatal")).toBeInTheDocument();
    expect(screen.getByText("已应用")).toBeInTheDocument();
    expect(screen.getByText(/应用失败：fetch_failed/)).toBeInTheDocument();
    expect(screen.getByText(/快照年龄/)).toBeInTheDocument();
  });
});

it("disables duplicate refreshes while fetching and enables retry after failure", async () => {
  const value = {
    config: { enabled: false, origin: "yaml", sync_state: "disabled" },
    workers: {},
  };
  show(value);
  await screen.findByText("未启用");
  const fetcher = vi.mocked(fetch);
  let reject: ((error: Error) => void) | undefined;
  fetcher.mockImplementationOnce(
    () =>
      new Promise<Response>((_resolve, fail) => {
        reject = fail;
      }),
  );
  fireEvent.click(screen.getByRole("button", { name: "刷新配置状态" }));
  const refreshing = await screen.findByRole("button", { name: "正在刷新…" });
  expect(refreshing).toBeDisabled();
  expect(refreshing).toHaveAttribute("aria-busy", "true");
  reject?.(new Error("failed"));
  await screen.findByRole("alert");
  await waitFor(() =>
    expect(screen.getByRole("button", { name: "刷新配置状态" })).toBeEnabled(),
  );
});
