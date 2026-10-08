import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import {
  cleanup,
  fireEvent,
  render,
  screen,
  waitFor,
  within,
} from "@testing-library/react";
import { MemoryRouter } from "react-router-dom";
import { afterEach, expect, it, vi } from "vitest";
import {
  policyRecord,
  policyRelease,
  policyPreview,
} from "../../test-fixtures/policies";
import type { PolicyKind } from "../../shared/policies";
import { TimeContext } from "../time";
import { PoliciesPage } from "./PoliciesPage";

afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
});
function setup(
  query = "?bk_tenant_id=tenant-a&type=suppression&id=db-noise",
  kind: PolicyKind = "suppression",
) {
  const fetcher = vi.fn(
    async (input: string, init?: RequestInit): Promise<Response> => {
      const url = new URL(input, "http://local");
      if (url.pathname === "/local-api/policies/statistics")
        return Response.json({
          bk_tenant_id: "tenant-a",
          type: kind,
          hours: 24,
          mode: "execution_observations",
          from: "2026-10-07T01:00:00Z",
          to: "2026-10-08T00:00:00Z",
          items: [
            {
              id: "db-noise",
              matched: 0,
              not_matched: 0,
              unavailable: 0,
              execution_skipped: 0,
            },
          ],
        });
      if (url.pathname === "/local-api/policy-links")
        return Response.json({
          bk_tenant_id: "tenant-a",
          type: kind,
          url: null,
          reason: "not_configured",
        });
      if (url.pathname.endsWith("/preview")) {
        const request = JSON.parse(String(init?.body));
        const result = policyPreview(request.version ?? 2);
        if (request.spec) {
          result.id = "preview";
          result.version = 0;
        }
        return Response.json(result);
      }
      const version = /releases\/(\d+)/.exec(url.pathname);
      if (version)
        return Response.json(policyRelease(Number(version[1]), kind));
      if (url.pathname.endsWith("/db-noise"))
        return Response.json(policyRecord(kind));
      return Response.json({ items: [policyRecord(kind)], next: "" });
    },
  );
  vi.stubGlobal("fetch", fetcher);
  render(
    <QueryClientProvider
      client={
        new QueryClient({ defaultOptions: { queries: { retry: false } } })
      }
    >
      <MemoryRouter initialEntries={["/policies" + query]}>
        <TimeContext.Provider value="utc">
          <PoliciesPage />
        </TimeContext.Provider>
      </MemoryRouter>
    </QueryClientProvider>,
  );
  return fetcher;
}
it("keeps published and pending versions separate, compares config and previews exact immutable releases", async () => {
  const fetcher = setup();
  const detail = screen.getByRole("region", { name: "策略详情" });
  await within(detail).findByRole("heading", { name: "发布 v2" });
  expect(
    within(detail).getByText("待完成", { exact: true }),
  ).toBeInTheDocument();
  expect(
    within(detail).getByText(/不能据此确认每个 Worker 已应用/),
  ).toBeInTheDocument();
  fireEvent.click(screen.getByRole("button", { name: "对比配置" }));
  await screen.findByText("变化字段：scheme");
  fireEvent.change(screen.getByLabelText("预览输入 ID"), {
    target: { value: "event-a" },
  });
  fireEvent.click(screen.getByRole("button", { name: "执行只读匹配" }));
  const result = await screen.findByRole("region", { name: "匹配结果" });
  expect(
    within(result).getByRole("heading", { name: /critical.*无法判定/ }),
  ).toBeInTheDocument();
  expect(
    within(result).getByRole("heading", { name: /warning.*匹配/ }),
  ).toBeInTheDocument();
  expect(within(result).getByText("field_unavailable")).toBeInTheDocument();
  const request = JSON.parse(
    String(
      fetcher.mock.calls.find(([url]) => url.endsWith("/preview"))?.[1]?.body,
    ),
  );
  expect(request).toMatchObject({
    bk_tenant_id: "tenant-a",
    type: "suppression",
    id: "db-noise",
    version: 2,
    event_id: "event-a",
  });
  fireEvent.change(screen.getByLabelText("查看版本"), {
    target: { value: "1" },
  });
  fireEvent.click(screen.getByRole("button", { name: "读取版本" }));
  await within(detail).findByRole("heading", { name: "发布 v1" });
  expect(
    screen.queryByRole("region", { name: "匹配结果" }),
  ).not.toBeInTheDocument();
  expect(screen.getByLabelText("预览输入 ID")).toHaveValue("");
  const writes = fetcher.mock.calls.filter(
    ([, init]) => init?.method && init.method !== "GET",
  );
  expect(writes).toHaveLength(1);
  expect(
    screen.queryByRole("button", { name: /保存|删除策略|新建策略/ }),
  ).not.toBeInTheDocument();
});
it("does not query without a tenant and preserves next cursor on an empty filtered page", async () => {
  const fetcher = setup("");
  expect(fetcher).not.toHaveBeenCalled();
  fetcher.mockImplementation(async (input) => {
    const url = new URL(input, "http://local");
    return Response.json(
      url.searchParams.get("after") === "a-before"
        ? { items: [policyRecord()], next: "" }
        : { items: [], next: "a-before" },
    );
  });
  fireEvent.change(screen.getByLabelText("策略租户"), {
    target: { value: "tenant-a" },
  });
  fireEvent.change(screen.getByLabelText("配置启用"), {
    target: { value: "true" },
  });
  fireEvent.click(screen.getByRole("button", { name: "查询策略" }));
  await screen.findByText("当前扫描页没有符合筛选的策略，可继续下一页。");
  fireEvent.click(screen.getByRole("button", { name: "下一页" }));
  await screen.findByRole("button", { name: /数据库告警降噪/ });
  const urls = fetcher.mock.calls
    .map(([url]) => new URL(url, "http://local"))
    .filter((url) => url.pathname === "/local-api/policies");
  expect(urls.at(-1)?.searchParams.get("after")).toBe("a-before");
  expect(
    urls.every(
      (url) =>
        url.searchParams.get("bk_tenant_id") === "tenant-a" &&
        url.searchParams.get("is_enable") === "true",
    ),
  ).toBe(true);
});
it("rejects cross-tenant and duplicate-key JSON before preview, while temporary config stays read-only", async () => {
  const fetcher = setup();
  await screen.findByRole("heading", { name: "发布 v2" });
  fireEvent.change(screen.getByLabelText("策略预览输入方式"), {
    target: { value: "event" },
  });
  for (const sample of [
    '{"bk_tenant_id":"tenant-b"}',
    '{"bk_tenant_id":"tenant-a","title":"one","title":"two"}',
  ]) {
    fireEvent.change(screen.getByLabelText("预览输入 JSON"), {
      target: { value: sample },
    });
    fireEvent.click(screen.getByRole("button", { name: "执行只读匹配" }));
    await screen.findByRole("alert");
  }
  expect(
    fetcher.mock.calls.filter(([url]) => url.endsWith("/preview")),
  ).toHaveLength(0);
  fireEvent.change(screen.getByLabelText("策略预览输入方式"), {
    target: { value: "event_id" },
  });
  fireEvent.change(screen.getByLabelText("预览输入 ID"), {
    target: { value: "event-a" },
  });
  fireEvent.click(
    screen.getByRole("checkbox", { name: "使用临时策略配置（不保存）" }),
  );
  fireEvent.click(screen.getByRole("button", { name: "执行只读匹配" }));
  await screen.findByRole("heading", { name: "临时配置 · 仅匹配判定" });
  const request = JSON.parse(
    String(
      fetcher.mock.calls.find(([url]) => url.endsWith("/preview"))?.[1]?.body,
    ),
  );
  expect(request.spec.name).toBe("数据库告警降噪");
  expect(request).not.toHaveProperty("id");
  expect(request).not.toHaveProperty("version");
});
it("cancels stale previews when input changes and never attaches a late response to new input", async () => {
  const fetcher = setup();
  await screen.findByRole("heading", { name: "发布 v2" });
  const original = fetcher.getMockImplementation()!;
  let finish: (response: Response) => void = () => undefined;
  let signal: AbortSignal | undefined;
  fetcher.mockImplementation(async (url, init) => {
    if (!url.endsWith("/preview")) return original(url, init);
    signal = init?.signal as AbortSignal;
    return new Promise<Response>((resolve) => {
      finish = resolve;
    });
  });
  fireEvent.change(screen.getByLabelText("预览输入 ID"), {
    target: { value: "old-event" },
  });
  fireEvent.click(screen.getByRole("button", { name: "执行只读匹配" }));
  await waitFor(() => expect(signal).toBeDefined());
  fireEvent.change(screen.getByLabelText("预览输入 ID"), {
    target: { value: "new-event" },
  });
  expect(signal?.aborted).toBe(true);
  finish(Response.json(policyPreview()));
  await waitFor(() =>
    expect(screen.getByRole("button", { name: "执行只读匹配" })).toBeEnabled(),
  );
  expect(
    screen.queryByRole("region", { name: "匹配结果" }),
  ).not.toBeInTheDocument();
});
it("sends dependent shield role and explicit main Alert without adding another preview input", async () => {
  const fetcher = setup(
    "?bk_tenant_id=tenant-a&type=shield&id=db-noise",
    "shield",
  );
  await screen.findByRole("heading", { name: "发布 v2" });
  fireEvent.change(screen.getByLabelText("预览输入 ID"), {
    target: { value: "child-event" },
  });
  fireEvent.click(
    screen.getByRole("checkbox", { name: "匹配被屏蔽条件（rely_policy）" }),
  );
  fireEvent.change(screen.getByLabelText("依赖主 Alert ID"), {
    target: { value: "main-alert" },
  });
  fireEvent.click(screen.getByRole("button", { name: "执行只读匹配" }));
  await screen.findByRole("region", { name: "匹配结果" });
  const request = JSON.parse(
    String(
      fetcher.mock.calls.find(([url]) => url.endsWith("/preview"))?.[1]?.body,
    ),
  );
  expect(request).toMatchObject({
    rely: true,
    origin_alert_id: "main-alert",
    event_id: "child-event",
    type: "shield",
  });
  expect(request).not.toHaveProperty("alert_id");
});
