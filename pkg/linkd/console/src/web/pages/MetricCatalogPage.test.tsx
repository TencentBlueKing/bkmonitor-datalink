import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import {
  cleanup,
  fireEvent,
  render,
  screen,
  within,
} from "@testing-library/react";
import { MemoryRouter } from "react-router-dom";
import { afterEach, expect, it, vi } from "vitest";

import { metricCatalogFixture } from "../../../tests/fixtures/metric-catalog";
import { MetricCatalogPage } from "./MetricCatalogPage";

afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
});

function setup(path = "/metrics/catalog") {
  render(
    <QueryClientProvider
      client={
        new QueryClient({ defaultOptions: { queries: { retry: false } } })
      }
    >
      <MemoryRouter initialEntries={[path]}>
        <MetricCatalogPage />
      </MemoryRouter>
    </QueryClientProvider>,
  );
}

it("searches names and dimensions, filters categories and shows actual query series", async () => {
  const fetcher = vi.fn(
    async () => new Response(JSON.stringify(metricCatalogFixture)),
  );
  vi.stubGlobal("fetch", fetcher);
  setup();
  await screen.findByRole("button", { name: "查看告警丰富总耗时详情" });
  expect(fetcher.mock.calls).toHaveLength(1);
  fireEvent.click(screen.getByRole("button", { name: "告警丰富" }));
  expect(
    screen.queryByRole("button", { name: "查看存储逻辑操作数详情" }),
  ).not.toBeInTheDocument();
  fireEvent.change(screen.getByLabelText("指标类型"), {
    target: { value: "histogram" },
  });
  expect(
    screen.queryByRole("button", { name: "查看告警丰富尝试结果详情" }),
  ).not.toBeInTheDocument();
  fireEvent.change(screen.getByLabelText("指标用途"), {
    target: { value: "throughput" },
  });
  expect(screen.getByText(/没有匹配的指标/)).toBeInTheDocument();
  fireEvent.click(screen.getByRole("button", { name: "清除筛选" }));
  for (const query of [
    "丰富 耗时",
    "linkd.enrich.attempt.duration",
    "linkd_enrich_attempt_duration_seconds_bucket",
    "linkd_event_source_id",
  ]) {
    fireEvent.change(screen.getByLabelText("搜索指标"), {
      target: { value: query },
    });
    expect(
      screen.getByRole("button", { name: "查看告警丰富总耗时详情" }),
    ).toBeInTheDocument();
    expect(
      screen.queryByRole("button", { name: "查看存储逻辑操作数详情" }),
    ).not.toBeInTheDocument();
  }
  fireEvent.click(
    screen.getByRole("button", { name: "查看告警丰富总耗时详情" }),
  );
  const detail = screen.getByRole("region", { name: "告警丰富总耗时详情" });
  expect(
    within(detail).getByText("linkd_enrich_attempt_duration_seconds_bucket"),
  ).toBeInTheDocument();
  expect(within(detail).getByText("告警源 ID")).toBeInTheDocument();
  expect(within(detail).getByText("OTel 注册名")).toBeInTheDocument();
});

it("restores URL filters and clamps malformed pagination", async () => {
  vi.stubGlobal(
    "fetch",
    vi.fn(async () => new Response(JSON.stringify(metricCatalogFixture))),
  );
  setup("/metrics/catalog?module=store&q=operations&page=Infinity");
  await screen.findByRole("button", { name: "查看存储逻辑操作数详情" });
  expect(screen.getByLabelText("搜索指标")).toHaveValue("operations");
  expect(
    screen.queryByRole("button", { name: "查看告警丰富总耗时详情" }),
  ).not.toBeInTheDocument();
  expect(screen.getByRole("button", { name: "下一页" })).toBeDisabled();
});

it("shows an actionable error and can retry", async () => {
  const fetcher = vi
    .fn()
    .mockResolvedValueOnce(
      new Response(JSON.stringify({ error: { message: "控制面不可用" } }), {
        status: 502,
      }),
    )
    .mockImplementation(
      async () => new Response(JSON.stringify(metricCatalogFixture)),
    );
  vi.stubGlobal("fetch", fetcher);
  setup();
  expect(await screen.findByRole("alert")).toHaveTextContent("控制面不可用");
  fireEvent.click(screen.getByRole("button", { name: "刷新目录" }));
  await screen.findByRole("button", { name: "查看告警丰富总耗时详情" });
  expect(screen.queryByRole("alert")).not.toBeInTheDocument();
});
