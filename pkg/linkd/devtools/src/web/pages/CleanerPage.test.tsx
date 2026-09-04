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
import { afterEach, describe, expect, it, vi } from "vitest";

vi.mock("../components/MetricPanelCard", () => ({
  MetricPanelCard: ({
    panel,
  }: {
    panel: {
      id: string;
      title: string;
      status: string;
      series: Array<{ name: string; labels: Record<string, string> }>;
    };
  }) => (
    <article data-testid={`metric-${panel.id}`}>
      <span>{panel.title}</span>
      <span>{panel.status}</span>
      <span>{JSON.stringify(panel.series)}</span>
    </article>
  ),
}));

import { CleanerPage } from "./CleanerPage";

afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
});

describe("CleanerPage", () => {
  it("separates the processing flow from a dedicated Kafka queue panel", async () => {
    stubCleanerAPI();
    renderPage();

    const queueTitle = await screen.findByRole("heading", {
      name: "消息队列当前状态",
    });
    expect(screen.getByRole("button", { name: "自动 15s" })).toHaveAttribute(
      "aria-pressed",
      "true",
    );
    expect(
      within(screen.getByRole("group", { name: "图表时间范围" })).getByRole(
        "button",
        { name: "1h" },
      ),
    ).toHaveAttribute("aria-pressed", "true");
    expect(screen.getByRole("combobox", { name: "指标计算窗口" })).toHaveValue(
      "60",
    );
    const flowPanel = screen
      .getByRole("heading", { name: "source-a 处理链路" })
      .closest("article");
    expect(flowPanel).not.toBeNull();
    expect(within(flowPanel!).getByText("模块如何运作")).toBeVisible();
    expect(
      within(flowPanel!).getByText(/按 partition 进入独立 lane/),
    ).toBeVisible();
    expect(
      within(flowPanel!).getByText(/连续成功前缀提交源 offset/),
    ).toBeVisible();
    const queuePanel = queueTitle.closest("article");
    expect(queuePanel).not.toBeNull();
    expect(within(queuePanel!).getByText("events.raw")).toBeVisible();
    expect(within(queuePanel!).getByText("cleaner-a")).toBeVisible();
    expect(within(queuePanel!).getByText("client-a")).toBeVisible();
    expect(within(queuePanel!).getByText("未分配")).toBeVisible();
    expect(within(queuePanel!).getByText("owner_missing")).toBeVisible();

    const lagFact = within(queuePanel!)
      .getByText("Total Lag")
      .closest("article");
    const abnormalFact = within(queuePanel!)
      .getByText("Abnormal Partitions")
      .closest("article");
    expect(lagFact).not.toBeNull();
    expect(abnormalFact).not.toBeNull();
    expect(within(lagFact!).getByText("12")).toBeVisible();
    expect(within(abnormalFact!).getByText("1")).toBeVisible();

    expect(screen.queryByRole("button", { name: "复制 JSON" })).toBeNull();
    expect(
      screen.getByRole("link", { name: /打开 Kafka 工作台/ }),
    ).toHaveAttribute("href", "/infrastructure/kafka?tab=input");

    const stageMetrics = screen.getByRole("region", {
      name: "Cleaner 当前指标",
    });
    expect(await within(stageMetrics).findByText("5.00 /s")).toBeVisible();
    expect(within(stageMetrics).getByText("40.0 ms")).toBeVisible();
    expect(within(stageMetrics).getByText("20.0 ms")).toBeVisible();
    expect(within(stageMetrics).getByText("4")).toBeVisible();

    expect(screen.getByTestId("metric-pipeline-average")).toHaveTextContent(
      "Cleaner 整体平均耗时",
    );
    expect(screen.getByTestId("metric-pipeline-p95")).toHaveTextContent(
      "Cleaner 整体 P95",
    );
    const overallP99 = screen.getByTestId("metric-pipeline-p99");
    expect(overallP99).toHaveTextContent("Cleaner 整体 P99");
    expect(overallP99).toHaveTextContent('"linkd_stage":"clean"');
    expect(overallP99).not.toHaveTextContent('"linkd_stage":"lifecycle"');
    expect(screen.getByTestId("metric-cleaner-step-average")).toBeVisible();
    expect(screen.getByTestId("metric-cleaner-step-p95")).toBeVisible();
    expect(screen.getByTestId("metric-cleaner-step-p99")).toBeVisible();
  });

  it("keeps the selected EventSource config behind a dialog", async () => {
    stubCleanerAPI();
    renderPage();

    const trigger = await screen.findByRole("button", {
      name: /EventSource 配置/,
    });
    expect(screen.queryByRole("dialog")).toBeNull();

    fireEvent.click(trigger);

    const dialog = screen.getByRole("dialog", { name: "EventSource 配置" });
    expect(dialog).toBeVisible();
    expect(dialog).toHaveTextContent('"eventSourceId": "source-a"');
    expect(
      within(dialog).getByRole("button", { name: "复制 JSON" }),
    ).toBeVisible();

    fireEvent.keyDown(window, { key: "Escape" });
    expect(
      screen.queryByRole("dialog", { name: "EventSource 配置" }),
    ).toBeNull();
    expect(trigger).toHaveFocus();
  });

  it("refreshes both runtime snapshot and selected EventSource metrics", async () => {
    const fetchMock = stubCleanerAPI();
    renderPage();

    const refresh = await screen.findByRole("button", { name: "立即刷新" });
    fireEvent.click(refresh);

    await waitFor(() => {
      expect(
        fetchMock.mock.calls.filter(([input]) =>
          String(input).includes("/runtime/cleaner"),
        ),
      ).toHaveLength(2);
      expect(
        fetchMock.mock.calls.filter(([input]) =>
          String(input).includes("/metrics"),
        ),
      ).toHaveLength(2);
    });
  });

  it("uses a compact snapshot in processing node details", async () => {
    stubCleanerAPI();
    renderPage();
    await screen.findByRole("heading", { name: "消息队列当前状态" });

    fireEvent.click(screen.getByRole("button", { name: /receive/ }));

    const dialog = screen.getByRole("dialog", { name: "receive 节点详情" });
    const guide = within(dialog).getByRole("region", {
      name: "receive 步骤说明",
    });
    expect(guide).toHaveTextContent("从 EventSource 对应的 Kafka");
    expect(guide).toHaveTextContent("此时尚未清洗、持久化或确认");
    expect(within(dialog).getByText("EventSource")).toBeVisible();
    expect(within(dialog).getByText("Total Lag")).toBeVisible();
    expect(
      within(dialog).queryByRole("button", { name: "复制 JSON" }),
    ).toBeNull();
  });

  it("shows only the selected step rate and duration history", async () => {
    stubCleanerAPI();
    renderPage();
    await screen.findByRole("heading", { name: "消息队列当前状态" });

    fireEvent.click(screen.getByRole("button", { name: /transform/ }));

    const dialog = screen.getByRole("dialog", { name: "transform 节点详情" });
    const duration = within(dialog).getByTestId("metric-cleaner-step-duration");
    expect(duration).toHaveTextContent("Cleaner 步骤耗时");
    expect(duration).toHaveTextContent("平均 · succeeded");
    expect(duration).toHaveTextContent("P95 · succeeded");
    expect(duration).toHaveTextContent("P99 · succeeded");
    expect(duration).toHaveTextContent('"linkd_step":"transform"');
    expect(duration).not.toHaveTextContent('"linkd_step":"event_store"');
    expect(
      within(dialog).queryByTestId("metric-cleaner-step-average"),
    ).toBeNull();
    expect(within(dialog).queryByTestId("metric-cleaner-step-p95")).toBeNull();
    expect(within(dialog).queryByTestId("metric-cleaner-step-p99")).toBeNull();
  });
});

function renderPage() {
  return render(
    <QueryClientProvider
      client={
        new QueryClient({ defaultOptions: { queries: { retry: false } } })
      }
    >
      <MemoryRouter initialEntries={["/cleaner"]}>
        <CleanerPage />
      </MemoryRouter>
    </QueryClientProvider>,
  );
}

function stubCleanerAPI() {
  const fetchMock = vi.fn(async (input: RequestInfo | URL) => {
    const url = String(input);
    if (url.includes("/runtime/cleaner")) {
      return response({
        status: "partial",
        eventSources: [
          {
            eventSourceId: "source-a",
            enabled: true,
            cleanerType: "standard",
            runtime: {
              workerCount: 4,
              maxBatchMessages: 128,
            },
            kafka: {
              brokers: ["kafka:9092"],
              topic: "events.raw",
              consumerGroup: "cleaner-a",
              security: "plaintext",
            },
          },
        ],
        kafka: {
          status: "partial",
          resources: [
            {
              kind: "input",
              eventSourceId: "source-a",
              status: "partial",
              brokers: ["kafka:9092"],
              topic: "events.raw",
              consumerGroup: "cleaner-a",
              cluster: {
                id: "cluster-a",
                controller: 1,
                brokers: [{ nodeId: 1, host: "kafka", port: 9092 }],
              },
              group: {
                state: "Stable",
                protocol: "consumer",
                members: [
                  {
                    memberId: "member-a",
                    clientId: "client-a",
                    clientHost: "/127.0.0.1",
                    partitions: [0],
                  },
                ],
              },
              partitions: [
                {
                  partition: 0,
                  leader: 1,
                  replicas: [1, 2],
                  isr: [1, 2],
                  lowOffset: "10",
                  highOffset: "100",
                  committedOffset: "93",
                  lag: "7",
                  members: ["member-a"],
                  status: "available",
                  issues: [],
                },
                {
                  partition: 1,
                  leader: 1,
                  replicas: [1, 2],
                  isr: [1, 2],
                  lowOffset: "20",
                  highOffset: "200",
                  committedOffset: "195",
                  lag: "5",
                  members: [],
                  status: "partial",
                  issues: [
                    {
                      code: "owner_missing",
                      message: "Partition 1 没有 owner",
                      partition: 1,
                    },
                  ],
                },
              ],
              issues: [
                {
                  code: "owner_missing",
                  message: "Partition 1 没有 owner",
                  partition: 1,
                },
              ],
            },
          ],
        },
      });
    }
    if (url.includes("/metrics")) {
      return response({
        from: "2026-09-02T00:00:00.000Z",
        to: "2026-09-02T01:00:00.000Z",
        step: 15,
        panels: stageMetricPanels(),
      });
    }
    throw new Error(`unexpected fetch ${url}`);
  });
  vi.stubGlobal("fetch", fetchMock);
  return fetchMock;
}

function stageMetricPanels() {
  return [
    metricPanel("pipeline-throughput", [
      metricSeries("clean", 3, { linkd_outcome: "succeeded" }),
      metricSeries("clean", 2, { linkd_outcome: "failed" }),
      metricSeries("lifecycle", 9),
    ]),
    metricPanel("pipeline-p99", [
      metricSeries("clean", 0.04),
      metricSeries("lifecycle", 0.7),
    ]),
    metricPanel("pipeline-p95", [
      metricSeries("clean", 0.03),
      metricSeries("lifecycle", 0.6),
    ]),
    metricPanel("pipeline-average", [
      metricSeries("clean", 0.02),
      metricSeries("lifecycle", 0.3),
    ]),
    metricPanel("messaging-inflight", [
      metricSeries("clean", 4, { messaging_system: "kafka" }),
      metricSeries("lifecycle", 8, { messaging_system: "redis_streams" }),
    ]),
    metricPanel("cleaner-steps", [
      metricSeries("clean", 4, {
        linkd_step: "transform",
        linkd_outcome: "succeeded",
      }),
      metricSeries("clean", 2, {
        linkd_step: "event_store",
        linkd_outcome: "created",
      }),
    ]),
    metricPanel("cleaner-step-average", [
      metricSeries("clean", 0.012, {
        linkd_step: "transform",
        linkd_outcome: "succeeded",
      }),
      metricSeries("clean", 0.21, {
        linkd_step: "event_store",
        linkd_outcome: "created",
      }),
    ]),
    metricPanel("cleaner-step-p95", [
      metricSeries("clean", 0.02, {
        linkd_step: "transform",
        linkd_outcome: "succeeded",
      }),
    ]),
    metricPanel("cleaner-step-p99", [
      metricSeries("clean", 0.03, {
        linkd_step: "transform",
        linkd_outcome: "succeeded",
      }),
    ]),
  ];
}

function metricPanel(id: string, series: ReturnType<typeof metricSeries>[]) {
  return {
    id,
    title: id,
    unit: "value",
    kind: "line",
    status: "available",
    series,
  };
}

function metricSeries(
  stage: string,
  value: number,
  labels: Record<string, string> = {},
) {
  return {
    name: stage,
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
