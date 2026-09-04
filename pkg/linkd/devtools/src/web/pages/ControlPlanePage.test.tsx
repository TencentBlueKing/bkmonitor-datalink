import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { cleanup, render, screen, within } from "@testing-library/react";
import { MemoryRouter } from "react-router-dom";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import type {
  ControlPlaneRuntime,
  ControlPlaneTaskDefinition,
  ControlPlaneTaskId,
} from "../../shared/contracts";
import { ControlPlanePage } from "./ControlPlanePage";

const now = new Date("2026-09-03T00:00:00Z");

beforeEach(() => {
  vi.useFakeTimers({ shouldAdvanceTime: true });
  vi.setSystemTime(now);
});

afterEach(() => {
  cleanup();
  vi.useRealTimers();
  vi.unstubAllGlobals();
});

describe("ControlPlanePage", () => {
  it("renders task dependencies and task-specific convergence work", async () => {
    stubResponses(runtimeFixture());
    renderPage();

    expect(
      await screen.findByRole("heading", { name: "Control Plane" }),
    ).toBeInTheDocument();
    expect(
      within(screen.getByRole("group", { name: "图表时间范围" })).getByRole(
        "button",
        { name: "1h" },
      ),
    ).toHaveAttribute("aria-pressed", "true");
    expect(screen.getByRole("combobox", { name: "指标计算窗口" })).toHaveValue(
      "60",
    );
    expect(
      screen.getByRole("heading", { name: "任务依赖与故障边界" }),
    ).toBeInTheDocument();
    expect(
      screen.getByRole("heading", { name: "Schema & Active Reconciler" }),
    ).toBeInTheDocument();
    expect(
      screen.getByRole("heading", { name: "Bucket Manager" }),
    ).toBeInTheDocument();
    const archiver = screen
      .getByRole("heading", { name: "Alert Archiver" })
      .closest("article");
    expect(archiver).not.toBeNull();
    expect(within(archiver!).getByText("2,450")).toBeInTheDocument();
    expect(within(archiver!).getByText("至少剩余批次")).toBeInTheDocument();
    expect(within(archiver!).getByText("3")).toBeInTheDocument();
    expect(within(archiver!).getByText("并发 Worker")).toBeInTheDocument();
    expect(within(archiver!).getByText("IDLE / RETRY")).toBeInTheDocument();
    expect(screen.getByText("可安全裁剪")).toBeInTheDocument();
    expect(screen.queryByText("Cluster Health")).not.toBeInTheDocument();
  });

  it("marks stale tasks and exposes duplicate owners", async () => {
    const runtime = runtimeFixture();
    runtime.metrics.series.lastSuccess = runtime.metrics.series.lastSuccess.map(
      (sample) =>
        sample.labels.linkd_task ===
        "elasticsearch-schema-and-active-reconciler"
          ? { ...sample, value: now.getTime() / 1000 - 8000 }
          : sample,
    );
    runtime.metrics.series.active.push({
      labels: {
        instance: "control-plane-b:9464",
        linkd_task: "elasticsearch-bucket-manager",
      },
      value: 1,
      timestamp: now.getTime() / 1000,
    });
    runtime.processes.items.push({
      instance: "control-plane-b:9464",
      job: "linkd-control-plane",
      serviceInstanceId: "control-plane-b",
      role: "control-plane",
      version: "dev",
      up: true,
    });
    stubResponses(runtime);
    renderPage();

    expect(await screen.findByText("STALE")).toBeInTheDocument();
    expect(screen.getByText("2 个 owner，当前不安全")).toBeInTheDocument();
  });
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

function stubResponses(runtime: ControlPlaneRuntime): void {
  vi.stubGlobal(
    "fetch",
    vi.fn(async (input: RequestInfo | URL) => {
      const url = String(input);
      if (url.includes("/local-api/runtime/control-plane"))
        return response(runtime);
      if (url.includes("/local-api/metrics"))
        return response({
          from: new Date(now.getTime() - 3600_000).toISOString(),
          to: now.toISOString(),
          step: 15,
          panels: [],
        });
      throw new Error(`unexpected fetch ${url}`);
    }),
  );
}

function runtimeFixture(): ControlPlaneRuntime {
  const task = (
    id: ControlPlaneTaskId,
    intervalSeconds: number,
    dependsOn: ControlPlaneTaskId[] = [],
  ): ControlPlaneTaskDefinition => {
    const settings: Record<string, string | number | boolean> =
      id === "elasticsearch-alert-archiver"
        ? {
            archiveIntervalSeconds: 30,
            archiveBatchSize: 1000,
            archiveWorkerCount: 4,
          }
        : id === "elasticsearch-bucket-manager"
          ? {
              eventBucketDays: 7,
              alertHistoryBucketDays: 7,
              alertLogBucketDays: 7,
              precreatePastBuckets: 1,
              precreateFutureBuckets: 1,
              maxBucketsPerEntity: 512,
            }
          : id === "redis-stream-manager"
            ? {
                reconcileIntervalSeconds: 60,
                operationTimeoutSeconds: 10,
                maxEntries: 100000,
                trimBatchSize: 10000,
                stream: "signals",
                group: "lifecycle",
              }
            : { schemaAndActiveReconcileIntervalSeconds: 3600 };
    return {
      id,
      enabled: true,
      dependsOn,
      intervalSeconds,
      configSource: "explicit",
      settings,
    };
  };
  const ids: ControlPlaneTaskId[] = [
    "elasticsearch-schema-and-active-reconciler",
    "elasticsearch-bucket-manager",
    "elasticsearch-alert-archiver",
    "redis-stream-manager",
  ];
  const timestamp = now.getTime() / 1000;
  return {
    status: "available",
    snapshotAt: now.toISOString(),
    tasks: [
      task(ids[0], 3600),
      task(ids[1], 21600, [ids[0]]),
      task(ids[2], 30, [ids[1]]),
      task(ids[3], 60),
    ],
    processes: {
      status: "available",
      items: [
        {
          instance: "control-plane-a:9464",
          job: "linkd-control-plane",
          serviceInstanceId: "control-plane-a",
          role: "control-plane",
          version: "dev",
          up: true,
        },
      ],
    },
    metrics: {
      status: "available",
      series: {
        active: ids.map((id) => sample(id, 1, timestamp)),
        lastSuccess: ids.map((id) => sample(id, timestamp - 5, timestamp)),
        runCount: ids.flatMap((id) => [
          sample(id, 10, timestamp, "succeeded"),
          sample(id, 0, timestamp, "failed"),
        ]),
        averageDuration: ids.map((id) => sample(id, 0.02, timestamp)),
        p95Duration: ids.map((id) => sample(id, 0.05, timestamp)),
        archiveLastScanned: [plainSample(1000, timestamp)],
        archiveLastBatch: [plainSample(998, timestamp)],
        archiveLastFailed: [plainSample(2, timestamp)],
        trimRequired: [plainSample(1, timestamp)],
        trimSafe: [plainSample(1, timestamp)],
        trimLastEntries: [plainSample(500, timestamp)],
        oldestPendingAge: [plainSample(30, timestamp)],
      },
    },
    archive: { status: "available", backlog: 2450 },
    redis: {
      status: "available",
      streamExists: true,
      expectedGroupPresent: true,
      entries: 100500,
      maxEntries: 100000,
      entriesAboveMax: 500,
      pending: 3,
      maxLag: 7,
    },
  };
}

function sample(
  task: ControlPlaneTaskId,
  value: number,
  timestamp: number,
  outcome?: string,
) {
  return {
    labels: {
      instance: "control-plane-a:9464",
      linkd_task: task,
      ...(outcome ? { linkd_outcome: outcome } : {}),
    },
    value,
    timestamp,
  };
}

function plainSample(value: number, timestamp: number) {
  return {
    labels: { instance: "control-plane-a:9464" },
    value,
    timestamp,
  };
}

function response(value: unknown): Response {
  return new Response(JSON.stringify(value), {
    status: 200,
    headers: { "content-type": "application/json" },
  });
}
