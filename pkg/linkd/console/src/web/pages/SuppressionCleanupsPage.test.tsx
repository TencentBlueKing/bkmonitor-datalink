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
import { SuppressionCleanupsPage } from "./SuppressionCleanupsPage";
import {
  cleanupFixture,
  cleanupID,
} from "../../test-fixtures/suppression-cleanups";
afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
});
it("shows unconfirmed effects separately from zero and refreshes filtered pagination", async () => {
  const urls: string[] = [];
  const row = cleanupFixture();
  vi.stubGlobal(
    "fetch",
    vi.fn(async (input: string) => {
      urls.push(input);
      if (input.includes("/" + cleanupID + "?")) return Response.json(row);
      const later = new URL(input, "http://local").searchParams.get("after");
      return Response.json({
        bk_tenant_id: "tenant-a",
        items: later ? [row] : [],
        next: later ? "" : "later",
      });
    }),
  );
  render(
    <QueryClientProvider
      client={
        new QueryClient({ defaultOptions: { queries: { retry: false } } })
      }
    >
      <MemoryRouter
        initialEntries={[
          "/suppression-cleanups?bk_tenant_id=tenant-a&id=" + cleanupID,
        ]}
      >
        <SuppressionCleanupsPage />
      </MemoryRouter>
    </QueryClientProvider>,
  );
  await screen.findByText(
    "此前尝试的清理结果未确认。本轮零删除不代表此前没有副作用。",
  );
  expect(screen.getByText("防抖：Redis 不可用，结果未确认")).toBeVisible();
  expect(screen.queryByText("本轮未发现可清理登记")).not.toBeInTheDocument();
  fireEvent.click(screen.getByRole("button", { name: "下一页" }));
  await screen.findByRole("link", { name: "recover-event" });
  expect(screen.getByRole("link", { name: "recover-event" })).toHaveAttribute(
    "href",
    expect.stringContaining("detail_tenant=tenant-a"),
  );
  fireEvent.click(screen.getByRole("button", { name: "刷新" }));
  await screen.findByText("本页没有符合条件的记录；有后续页时可继续查询。");
  expect(screen.getByRole("button", { name: "回到首页" })).toBeDisabled();
  // 首页缓存可先呈现空结果；等待刷新请求实际发出，不能把缓存渲染当成请求完成。
  await waitFor(() =>
    expect(
      urls.filter((u) => !u.includes("/" + cleanupID + "?")).length,
    ).toBeGreaterThan(2),
  );
});

it("renders atomic generation detail and preserves a window/epoch filter", async () => {
  const row = cleanupFixture(),
    windowID = "a".repeat(64) + ":" + "b".repeat(64);
  row.result = {
    clip: {
      state: "confirmed",
      removed: 2,
      windows: [
        { id: windowID, epoch: "original-event" },
        { id: "a".repeat(64) + ":" + "c".repeat(64), missing: true },
      ],
    },
    aggregation: { state: "not_applicable" },
  };
  const urls: string[] = [];
  vi.stubGlobal(
    "fetch",
    vi.fn(async (input: string) => {
      urls.push(input);
      return Response.json(
        input.includes("/" + cleanupID + "?")
          ? row
          : { bk_tenant_id: "tenant-a", items: [row], next: "" },
      );
    }),
  );
  render(
    <QueryClientProvider
      client={
        new QueryClient({ defaultOptions: { queries: { retry: false } } })
      }
    >
      <MemoryRouter
        initialEntries={[
          "/suppression-cleanups?" +
            new URLSearchParams({
              bk_tenant_id: "tenant-a",
              id: cleanupID,
              window_kind: "clip",
              window_id: windowID,
              epoch: "original-event",
            }),
        ]}
      >
        <SuppressionCleanupsPage />
      </MemoryRouter>
    </QueryClientProvider>,
  );
  await screen.findByText("元信息已丢失，代次未知");
  expect(screen.getByText("original-event", { selector: "td" })).toBeVisible();
  expect(screen.getByLabelText("窗口方式")).toHaveValue("clip");
  expect(screen.getByLabelText("窗口 ID")).toHaveValue(windowID);
  expect(
    urls.some(
      (u) =>
        u.includes("window_kind=clip") && u.includes("epoch=original-event"),
    ),
  ).toBe(true);
});
