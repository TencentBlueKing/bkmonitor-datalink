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
  MetricPanelCard: () => null,
}));

import { LifecyclePage } from "./LifecyclePage";

afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
});

describe("LifecyclePage", () => {
  it("renders the signal queue and mailbox/lock dashboard instead of raw JSON", async () => {
    stubLifecycleAPI();
    renderPage();

    expect(
      await screen.findByRole("heading", {
        name: "Signal / Mailbox / Lock 当前状态",
      }),
    ).toBeVisible();
    expect(screen.getByRole("button", { name: "自动 15s" })).toHaveAttribute(
      "aria-pressed",
      "true",
    );
    const flowPanel = screen
      .getByRole("heading", { name: "Lifecycle 处理链路" })
      .closest("article");
    expect(flowPanel).not.toBeNull();
    expect(within(flowPanel!).getByText("模块如何运作")).toBeVisible();
    expect(
      within(flowPanel!).getByText(/Signal 只负责唤醒 Mailbox/),
    ).toBeVisible();
    expect(within(flowPanel!).getByText(/排空后才 XACK Signal/)).toBeVisible();
    expect(await screen.findByText("linkd-lifecycle")).toBeVisible();
    expect(screen.queryByRole("button", { name: "复制 JSON" })).toBeNull();
    expect(screen.queryByText("127.0.0.1:6379")).toBeNull();

    const signalCard = screen.getByText("Signal Stream").closest("article");
    const mailboxCard = screen.getByText("近似 Signal 积压").closest("article");
    const lockCard = screen.getByText("Lease / Lock").closest("article");
    expect(signalCard).not.toBeNull();
    expect(mailboxCard).not.toBeNull();
    expect(lockCard).not.toBeNull();
    expect(within(signalCard!).getByText("12")).toBeVisible();
    expect(within(mailboxCard!).getByText("6")).toBeVisible();
    expect(within(lockCard!).getByText("2")).toBeVisible();

    expect(screen.getByText("1788357791745-1")).toBeVisible();

    const stageMetrics = screen.getByRole("region", {
      name: "Lifecycle 当前指标",
    });
    expect(within(stageMetrics).getByText("7.00 /s")).toBeVisible();
    expect(within(stageMetrics).getByText("700 ms")).toBeVisible();
    expect(within(stageMetrics).getByText("300 ms")).toBeVisible();
    expect(within(stageMetrics).getByText("8")).toBeVisible();
  });

  it("keeps Lifecycle config behind a dialog trigger", async () => {
    stubLifecycleAPI();
    renderPage();

    const trigger = await screen.findByRole("button", {
      name: /Lifecycle 配置/,
    });
    expect(screen.queryByRole("dialog")).toBeNull();

    fireEvent.click(trigger);

    const dialog = screen.getByRole("dialog", { name: "Lifecycle 配置" });
    expect(dialog).toBeVisible();
    expect(dialog).toHaveTextContent('"concurrency": 4');
    expect(
      within(dialog).getByRole("button", { name: "复制 JSON" }),
    ).toBeVisible();

    fireEvent.keyDown(window, { key: "Escape" });
    expect(screen.queryByRole("dialog", { name: "Lifecycle 配置" })).toBeNull();
    expect(trigger).toHaveFocus();
  });

  it("refreshes both runtime snapshot and metrics", async () => {
    const fetchMock = stubLifecycleAPI();
    renderPage();
    await screen.findByRole("heading", {
      name: "Signal / Mailbox / Lock 当前状态",
    });

    fireEvent.click(await screen.findByRole("button", { name: "立即刷新" }));

    await waitFor(() => {
      expect(
        fetchMock.mock.calls.filter(([input]) =>
          String(input).includes("/runtime/lifecycle"),
        ),
      ).toHaveLength(2);
      expect(
        fetchMock.mock.calls.filter(([input]) =>
          String(input).includes("/metrics"),
        ),
      ).toHaveLength(2);
    });
  });

  it("shows a compact node-specific snapshot", async () => {
    stubLifecycleAPI();
    renderPage();
    await screen.findByRole("heading", {
      name: "Signal / Mailbox / Lock 当前状态",
    });

    fireEvent.click(screen.getByRole("button", { name: /mailbox_peek/ }));

    const dialog = screen.getByRole("dialog", {
      name: "mailbox_peek 节点详情",
    });
    const guide = within(dialog).getByRole("region", {
      name: "mailbox_peek 步骤说明",
    });
    expect(guide).toHaveTextContent("读取 Mailbox 队首 Event ID");
    expect(guide).toHaveTextContent("Peek 不移除数据");
    expect(within(dialog).getByText("Approximate unresolved")).toBeVisible();
    expect(within(dialog).getByText("Max drain events")).toBeVisible();
    expect(
      within(dialog).queryByRole("button", { name: "复制 JSON" }),
    ).toBeNull();
    expect(dialog).not.toHaveTextContent("outputKafka");
  });
});

function renderPage() {
  return render(
    <QueryClientProvider
      client={
        new QueryClient({ defaultOptions: { queries: { retry: false } } })
      }
    >
      <MemoryRouter initialEntries={["/lifecycle"]}>
        <LifecyclePage />
      </MemoryRouter>
    </QueryClientProvider>,
  );
}

function stubLifecycleAPI() {
  const fetchMock = vi.fn(async (input: RequestInfo | URL) => {
    const url = String(input);
    if (url.includes("/runtime/lifecycle")) {
      return response({
        status: "available",
        config: {
          concurrency: 4,
          signal: {
            stream: "linkd:lifecycle:signals",
            group: "linkd-lifecycle",
          },
          mailbox: {
            keyPrefix: "linkd:lifecycle:mailbox",
            maxDrainEvents: 512,
          },
          lock: {
            keyPrefix: "linkd:lifecycle:lock",
            ttlSeconds: 60,
          },
          outputKafka: {
            brokers: ["kafka:9092"],
            topic: "linkd-alerts",
            clientId: "linkd-lifecycle",
          },
        },
        redis: {
          status: "available",
          snapshotAt: "2026-09-02T01:00:00.000Z",
          connection: {
            status: "available",
            address: "127.0.0.1:6379",
            database: 0,
            ping: "PONG",
          },
          instance: { status: "available" },
          signalQueue: {
            status: "available",
            streamKey: "linkd:lifecycle:signals",
            expectedGroup: "linkd-lifecycle",
            claimMinIdleSeconds: 300,
            stream: {
              exists: true,
              length: 12,
              entriesAdded: 22,
              memoryBytes: 2048,
              firstEntryId: "1788352938006-0",
              lastGeneratedId: "1788357791745-1",
              oldestEntryAgeSeconds: 120,
              groupsCount: 1,
              maxEntries: 100_000,
              entriesAboveMax: 0,
            },
            groups: [
              {
                name: "linkd-lifecycle",
                expected: true,
                consumersCount: 2,
                pending: 3,
                lastDeliveredId: "1788357791745-1",
                entriesRead: 19,
                lag: 3,
                consumersStatus: "available",
                consumers: [],
              },
            ],
          },
          mailbox: {
            status: "available",
            activeMailboxes: 5,
            scanTruncated: false,
            maxPendingPerMailbox: 128,
            maxDrainEvents: 512,
          },
          leases: {
            status: "available",
            activeLeases: 2,
            scanTruncated: false,
            ttlSeconds: 60,
            renewIntervalSeconds: 20,
          },
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
      metricSeries("clean", 9),
      metricSeries("lifecycle", 5, { linkd_outcome: "accepted" }),
      metricSeries("lifecycle", 2, { linkd_outcome: "suppressed" }),
    ]),
    metricPanel("pipeline-p99", [
      metricSeries("clean", 0.04),
      metricSeries("lifecycle", 0.7),
    ]),
    metricPanel("pipeline-average", [
      metricSeries("clean", 0.02),
      metricSeries("lifecycle", 0.3),
    ]),
    metricPanel("messaging-inflight", [
      metricSeries("clean", 4, { messaging_system: "kafka" }),
      metricSeries("lifecycle", 8, { messaging_system: "redis_streams" }),
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
