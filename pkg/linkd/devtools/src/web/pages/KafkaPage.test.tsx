import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import {
  cleanup,
  fireEvent,
  render,
  screen,
  within,
} from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import { MemoryRouter, useLocation, useNavigationType } from "react-router-dom";

import type {
  KafkaInfrastructure,
  KafkaPartition,
  KafkaResource,
} from "../../shared/contracts";
import { filterKafkaResources } from "./KafkaPage.logic";
import { KafkaPage } from "./KafkaPage";

afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
});

describe("KafkaPage", () => {
  it("shows partial lag, unknown offsets, assignments and numeric partition order", async () => {
    stubSnapshot(snapshot());
    const { container } = renderPage();

    expect(
      await screen.findByRole("heading", {
        name: "Input Topic 详情 · source-a",
      }),
    ).toBeInTheDocument();
    expect(screen.getByRole("heading", { name: "Kafka" })).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "自动 30s" })).toHaveAttribute(
      "aria-pressed",
      "true",
    );
    expect(screen.getByText("1/2 partitions 可计算")).toBeInTheDocument();
    expect(
      screen.getByRole("button", { name: /LAG 3 · PARTIAL/ }),
    ).toBeInTheDocument();
    expect(screen.getAllByText("member-a").length).toBeGreaterThan(0);
    expect(screen.getByText("900719925474099312345")).toBeInTheDocument();
    expect(screen.getAllByText("— 未知").length).toBeGreaterThan(0);
    expect(
      screen.getByText(/committed 的增量不等于入库 Event 数/),
    ).toBeInTheDocument();

    const partitionSection = container.querySelector(
      ".kafka-partition-section",
    );
    expect(partitionSection).not.toBeNull();
    const rows = within(partitionSection as HTMLElement).getAllByRole("row");
    expect(within(rows[1]).getAllByRole("cell")[0]).toHaveTextContent("2");
    expect(within(rows[2]).getAllByRole("cell")[0]).toHaveTextContent("10");
  });

  it("separates Input and Output tabs, exposes detail buttons and switches detail", async () => {
    const value = snapshot();
    value.resources.splice(
      1,
      0,
      resource({
        kind: "input",
        eventSourceId: "source-b",
        status: "partial",
        topic: "raw-topic-b",
        consumerGroup: "linkd-cleaner-b",
        group: { state: "Empty", protocol: "range", members: [] },
        issues: [
          {
            code: "group_empty",
            message: "Consumer group 当前没有活跃成员。",
          },
        ],
      }),
    );
    stubSnapshot(value);
    const { container } = renderPage();
    await screen.findByRole("heading", {
      name: "Input Topic 详情 · source-a",
    });

    const inputNavigator = container.querySelector(".kafka-topic-navigator");
    expect(inputNavigator).not.toBeNull();
    expect(screen.queryByText("alert-output")).not.toBeInTheDocument();
    const inputTopicButtons = within(
      inputNavigator as HTMLElement,
    ).getAllByRole("button", { name: /raw-topic/ });
    expect(inputTopicButtons[0]).toBeVisible();
    expect(inputTopicButtons[0]).toHaveAttribute("type", "button");
    expect(inputTopicButtons[0]).toHaveAttribute("aria-current", "true");
    expect(
      within(inputNavigator as HTMLElement).queryByRole("table"),
    ).not.toBeInTheDocument();
    expect(
      within(inputNavigator as HTMLElement).queryByRole("row"),
    ).not.toBeInTheDocument();
    fireEvent.click(inputTopicButtons[1]);
    expect(
      screen.getByRole("heading", { name: "Input Topic 详情 · source-b" }),
    ).toBeInTheDocument();
    expect(inputTopicButtons[1]).toHaveAttribute("aria-current", "true");

    fireEvent.change(screen.getByLabelText("KEYWORD"), {
      target: { value: "source-a" },
    });
    expect(
      screen.getByRole("heading", { name: "Input Topic 详情 · source-a" }),
    ).toBeInTheDocument();
    expect(
      screen.queryByRole("button", { name: /raw-topic-b/ }),
    ).not.toBeInTheDocument();

    fireEvent.click(screen.getByRole("tab", { name: /Output Topics/ }));
    expect(
      await screen.findByRole("complementary", {
        name: "Output Topic 导航",
      }),
    ).toBeInTheDocument();
    expect(screen.queryByText("raw-topic")).not.toBeInTheDocument();
    const outputTopicButton = screen.getByRole("button", {
      name: /alert-output/,
    });
    expect(outputTopicButton).toBeVisible();
    expect(outputTopicButton).toHaveAttribute("aria-current", "true");
    expect(
      screen.getByRole("heading", {
        name: "Output Topic 详情 · alert-output",
      }),
    ).toBeInTheDocument();

    const detail = container.querySelector(".kafka-detail-panel");
    expect(detail).not.toBeNull();
    const detailView = within(detail as HTMLElement);
    expect(
      detailView.getByText("Output 没有 consumer lag"),
    ).toBeInTheDocument();
    expect(
      detailView.getByText(/LEO 是 topic 的全局日志末端/),
    ).toBeInTheDocument();
    expect(
      detailView.queryByRole("columnheader", { name: "Owner" }),
    ).not.toBeInTheDocument();
    expect(
      detailView.queryByRole("columnheader", { name: "Committed Next" }),
    ).not.toBeInTheDocument();
    expect(
      detailView.queryByRole("columnheader", { name: "Lag" }),
    ).not.toBeInTheDocument();
    expect(
      screen.getByText(/无法可靠归因到这个 Kafka Output/),
    ).toBeInTheDocument();

    fireEvent.click(screen.getByRole("tab", { name: /Input Topics/ }));
    expect(
      screen.getByRole("heading", { name: "Input Topic 详情 · source-a" }),
    ).toBeInTheDocument();
    expect(screen.queryByText("alert-output")).not.toBeInTheDocument();
    expect(container.querySelector(".kafka-master-detail")).not.toBeNull();
  });

  it("restores the tab from URL, preserves query params and replaces on switch", async () => {
    stubSnapshot(snapshot());
    renderPage("/infrastructure/kafka?tab=output&scope=kept");

    expect(
      await screen.findByRole("heading", {
        name: "Output Topic 详情 · alert-output",
      }),
    ).toBeInTheDocument();
    expect(screen.getByRole("tab", { name: /Output Topics/ })).toHaveAttribute(
      "aria-selected",
      "true",
    );
    fireEvent.click(screen.getByRole("tab", { name: /Input Topics/ }));
    expect(
      screen.getByRole("heading", { name: "Input Topic 详情 · source-a" }),
    ).toBeInTheDocument();
    expect(screen.getByTestId("location-state")).toHaveTextContent(
      "REPLACE|?tab=input&scope=kept",
    );
  });

  it("falls back to Input for an invalid tab parameter", async () => {
    stubSnapshot(snapshot());
    renderPage("/infrastructure/kafka?tab=invalid");
    expect(
      await screen.findByRole("heading", {
        name: "Input Topic 详情 · source-a",
      }),
    ).toBeInTheDocument();
    expect(screen.getByRole("tab", { name: /Input Topics/ })).toHaveAttribute(
      "aria-selected",
      "true",
    );
  });

  it("paginates a large topic set and converges selection to the visible page", async () => {
    const value = snapshot();
    value.resources = Array.from({ length: 25 }, (_, index) =>
      resource({
        kind: "input",
        eventSourceId: `bulk-source-${index}`,
        topic: `bulk-topic-${index}`,
        consumerGroup: `bulk-group-${index}`,
        group: { state: "Stable", protocol: "range", members: [] },
        status: "available",
      }),
    );
    stubSnapshot(value);
    renderPage();

    expect(
      await screen.findByRole("heading", {
        name: "Input Topic 详情 · bulk-source-0",
      }),
    ).toBeInTheDocument();
    expect(screen.getAllByRole("button", { name: /bulk-topic-/ })).toHaveLength(
      20,
    );
    fireEvent.click(screen.getByRole("button", { name: "下一页" }));
    expect(
      screen.getByRole("heading", {
        name: "Input Topic 详情 · bulk-source-20",
      }),
    ).toBeInTheDocument();
    expect(screen.getAllByRole("button", { name: /bulk-topic-/ })).toHaveLength(
      5,
    );
    expect(screen.getByText("第 2 / 2 页")).toBeInTheDocument();
  });

  it("shows the empty-resource state without turning unknown lag into zero", async () => {
    stubSnapshot({ status: "unavailable", resources: [] });
    renderPage();

    expect(
      await screen.findByText("当前配置没有 EventSource Input Topic。"),
    ).toBeInTheDocument();
    expect(screen.getByText("没有 Input partition")).toBeInTheDocument();
    expect(screen.getByText("不适用")).toBeInTheDocument();
  });

  it("retains and marks the previous snapshot when a refresh fails", async () => {
    let calls = 0;
    vi.stubGlobal(
      "fetch",
      vi.fn(async (input: RequestInfo | URL) => {
        const url = String(input);
        if (url.includes("/local-api/metrics")) return response(metrics());
        calls += 1;
        if (calls === 1) return response(snapshot());
        return response(
          {
            error: {
              code: "data_source_error",
              message: "snapshot refresh failed",
              requestId: "request-a",
            },
          },
          502,
        );
      }),
    );
    renderPage();
    await screen.findByRole("heading", {
      name: "Input Topic 详情 · source-a",
    });

    fireEvent.click(await screen.findByRole("button", { name: "立即刷新" }));
    expect(
      await screen.findByText(/最近一次刷新失败，当前保留的是/),
    ).toBeInTheDocument();
    expect(screen.getAllByText("raw-topic").length).toBeGreaterThan(0);
  });
});

describe("filterKafkaResources", () => {
  it("combines keyword and status filters within one topic type", () => {
    const resources = snapshot().resources.filter(
      (resource) => resource.kind === "input",
    );
    expect(
      filterKafkaResources(resources, {
        keyword: "raw-topic",
        status: "partial",
      }).map((resource) => resource.eventSourceId),
    ).toEqual(["source-a"]);
    expect(
      filterKafkaResources(resources, {
        keyword: "missing-topic",
        status: "all",
      }),
    ).toHaveLength(0);
  });
});

function renderPage(path = "/infrastructure/kafka") {
  return render(
    <MemoryRouter initialEntries={[path]}>
      <QueryClientProvider
        client={
          new QueryClient({
            defaultOptions: { queries: { retry: false, gcTime: 0 } },
          })
        }
      >
        <KafkaPage />
        <LocationProbe />
      </QueryClientProvider>
    </MemoryRouter>,
  );
}

function LocationProbe() {
  const location = useLocation();
  const navigationType = useNavigationType();
  return (
    <output data-testid="location-state">
      {navigationType}|{location.search}
    </output>
  );
}

function stubSnapshot(value: KafkaInfrastructure): void {
  vi.stubGlobal(
    "fetch",
    vi.fn(async (input: RequestInfo | URL) =>
      String(input).includes("/local-api/metrics")
        ? response(metrics())
        : response(value),
    ),
  );
}

function snapshot(): KafkaInfrastructure {
  return {
    status: "partial",
    resources: [
      resource({
        kind: "input",
        eventSourceId: "source-a",
        status: "partial",
        topic: "raw-topic",
        consumerGroup: "linkd-cleaner",
        group: {
          state: "Stable",
          protocol: "range",
          members: [
            {
              memberId: "member-a",
              clientId: "linkd-a",
              clientHost: "/127.0.0.1",
              partitions: [2, 10],
            },
          ],
        },
        partitions: [
          partition({
            partition: 10,
            highOffset: "900719925474099312345",
            committedOffset: "900719925474099312342",
            lag: "3",
          }),
          partition({
            partition: 2,
            committedOffset: undefined,
            lag: undefined,
            status: "partial",
            issues: [
              {
                code: "committed_missing",
                message: "Partition 2 没有可用 committed next offset。",
                partition: 2,
              },
            ],
          }),
        ],
        issues: [
          {
            code: "committed_missing",
            message: "Partition 2 没有可用 committed next offset。",
            partition: 2,
          },
        ],
      }),
      resource({
        kind: "output",
        status: "available",
        clientId: "linkd-final-hook",
        topic: "alert-output",
        partitions: [
          partition({
            partition: 0,
            committedOffset: undefined,
            lag: undefined,
            members: undefined,
          }),
        ],
      }),
    ],
  };
}

function resource(override: Partial<KafkaResource>): KafkaResource {
  return {
    kind: "input",
    status: "available",
    brokers: ["127.0.0.1:9092"],
    topic: "topic-a",
    cluster: {
      id: "cluster-a",
      controller: 1,
      brokers: [{ nodeId: 1, host: "127.0.0.1", port: 9092 }],
    },
    partitions: [],
    issues: [],
    ...override,
  };
}

function partition(override: Partial<KafkaPartition>): KafkaPartition {
  return {
    partition: 0,
    leader: 1,
    replicas: [1],
    isr: [1],
    lowOffset: "0",
    highOffset: "10",
    committedOffset: "8",
    lag: "2",
    members: ["member-a"],
    status: "available",
    issues: [],
    ...override,
  };
}

function metrics() {
  return {
    from: "2026-09-02T00:00:00.000Z",
    to: "2026-09-02T01:00:00.000Z",
    step: 15,
    panels: [],
  };
}

function response(value: unknown, status = 200): Response {
  return new Response(JSON.stringify(value), {
    status,
    headers: { "content-type": "application/json" },
  });
}
