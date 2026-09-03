import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { cleanup, render, screen } from "@testing-library/react";
import { MemoryRouter } from "react-router-dom";
import { afterEach, describe, expect, it, vi } from "vitest";

vi.mock("../components/MetricPanelCard", () => ({
  MetricPanelCard: () => null,
}));

import { OverviewPage } from "./OverviewPage";

afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
});

describe("OverviewPage", () => {
  it("calculates pipeline cards by stage and removes panel availability", async () => {
    stubOverviewAPI();
    renderPage();

    expect(await screen.findByText("7.00 /s")).toBeVisible();
    expect(screen.getByRole("button", { name: "自动 15s" })).toHaveAttribute(
      "aria-pressed",
      "true",
    );

    const throughput = statCard("吞吐");
    expect(throughput).toHaveTextContent("Cleaner12.00/s");
    expect(throughput).toHaveTextContent("Lifecycle7.00/s");
    expect(throughput).not.toHaveTextContent("瓶颈");

    const p99 = statCard("P99 耗时合计");
    expect(p99).toHaveTextContent("1.10 s");
    expect(p99).toHaveTextContent("Cleaner400ms");
    expect(p99).toHaveTextContent("Lifecycle700ms");

    const average = statCard("平均耗时合计");
    expect(average).toHaveTextContent("0.50 s");
    expect(average).toHaveTextContent("Cleaner200ms");
    expect(average).toHaveTextContent("Lifecycle300ms");

    expect(statCard("Cleaner 在途消息")).toHaveTextContent("4");
    expect(statCard("Lifecycle 在途消息")).toHaveTextContent("2");
    expect(statCard("确认阻塞消息")).toHaveTextContent("1");
    expect(screen.queryByText("可用面板")).toBeNull();
  });
});

function statCard(title: string): HTMLElement {
  const label = screen.getByText(title);
  const card = label.closest("article");
  expect(card).not.toBeNull();
  return card!;
}

function renderPage() {
  return render(
    <QueryClientProvider
      client={
        new QueryClient({ defaultOptions: { queries: { retry: false } } })
      }
    >
      <MemoryRouter initialEntries={["/overview"]}>
        <OverviewPage />
      </MemoryRouter>
    </QueryClientProvider>,
  );
}

function stubOverviewAPI() {
  vi.stubGlobal(
    "fetch",
    vi.fn(async (input: RequestInfo | URL) => {
      const url = String(input);
      if (url.includes("/local-api/metrics")) {
        return response({
          from: "2026-09-02T00:00:00.000Z",
          to: "2026-09-02T01:00:00.000Z",
          step: 15,
          panels: [
            panel("pipeline-throughput", "阶段处理速率", "attempt/s", [
              series("clean succeeded", "clean", 10, {
                linkd_outcome: "succeeded",
              }),
              series("clean failed", "clean", 2, {
                linkd_outcome: "failed",
              }),
              series("lifecycle accepted", "lifecycle", 7, {
                linkd_outcome: "accepted",
              }),
            ]),
            panel("pipeline-p99", "阶段处理 P99", "s", [
              series("clean", "clean", 0.4),
              series("lifecycle", "lifecycle", 0.7),
            ]),
            panel("pipeline-average", "阶段处理平均耗时", "s", [
              series("clean", "clean", 0.2),
              series("lifecycle", "lifecycle", 0.3),
            ]),
            panel("messaging-inflight", "分阶段在途消息", "message", [
              series("clean · kafka", "clean", 4, {
                messaging_system: "kafka",
              }),
              series("lifecycle · redis_streams", "lifecycle", 2, {
                messaging_system: "redis_streams",
              }),
            ]),
            panel("settlement-gap", "确认阻塞消息", "message", [
              {
                name: "kafka",
                labels: { messaging_system: "kafka" },
                points: [[1_788_000_000, 1]],
              },
            ]),
          ],
        });
      }
      if (url.includes("/runtime/processes")) {
        return response({ status: "available", items: [] });
      }
      if (url.includes("/runtime/cleaner")) {
        return response({ status: "available", eventSources: [], kafka: {} });
      }
      if (url.includes("/runtime/lifecycle")) {
        return response({ status: "available", redis: {} });
      }
      throw new Error(`unexpected fetch ${url}`);
    }),
  );
}

function panel(
  id: string,
  title: string,
  unit: string,
  seriesValues: Array<ReturnType<typeof series>>,
) {
  return {
    id,
    title,
    unit,
    kind: "line",
    status: "available",
    series: seriesValues,
  };
}

function series(
  name: string,
  stage: string,
  value: number,
  labels: Record<string, string> = {},
): {
  name: string;
  labels: Record<string, string>;
  points: Array<[number, number]>;
} {
  return {
    name,
    labels: { linkd_stage: stage, ...labels },
    points: [[1_788_000_000, value]],
  };
}

function response(value: unknown): Response {
  return new Response(JSON.stringify(value), {
    status: 200,
    headers: { "content-type": "application/json" },
  });
}
