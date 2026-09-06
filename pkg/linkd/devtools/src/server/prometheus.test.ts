import { afterEach, describe, expect, it, vi } from "vitest";

import type { DevtoolsConfig } from "./config.js";
import { PrometheusConnector } from "./prometheus.js";

const config = {
  server: { host: "127.0.0.1", port: 4399 },
  query: {
    defaultRangeSeconds: 3600,
    maxRangeSeconds: 604800,
    defaultLimit: 50,
    maxLimit: 200,
    timeoutMilliseconds: 5000,
  },
  prometheus: { baseUrl: "http://prometheus:9090", auth: {} },
  mysql: {
    host: "mysql",
    port: 3306,
    database: "linkd",
    username: "reader",
    password: "",
    connectionLimit: 5,
  },
  entities: { alerts: "mysql", events: "mysql", alertLogs: "mysql" },
} satisfies DevtoolsConfig;

afterEach(() => vi.unstubAllGlobals());

describe("PrometheusConnector", () => {
  it("uses range totals at the selected endpoint, precise timing schema, and Event-only latency", async () => {
    const urls: URL[] = [];
    const from = new Date("2026-09-04T00:00:00Z"),
      to = new Date("2026-09-04T01:00:00Z");
    vi.stubGlobal(
      "fetch",
      vi.fn(async (input: URL | Request | string) => {
        const u = new URL(String(input));
        urls.push(u);
        return new Response(
          JSON.stringify({
            status: "success",
            data: {
              result: [
                {
                  metric: u.searchParams
                    .get("query")
                    ?.includes("phase_duration")
                    ? {
                        linkd_batch_kind: "write",
                        linkd_batch_phase: "connection",
                      }
                    : { linkd_stage: "lifecycle" },
                  values: [[from.getTime() / 1000, "NaN"]],
                  value: [to.getTime() / 1000, "3"],
                },
              ],
            },
          }),
        );
      }),
    );
    const result = await new PrometheusConnector(config).panels(from, to, 15, {
      instance: "worker-a",
      calculationWindowSeconds: 30,
    });
    const totals = urls.filter((u) => u.pathname === "/api/v1/query");
    expect(
      result.panels.find((p) => p.id === "lifecycle-batch-phases")?.series[0]
        .name,
    ).toBe("write · connection");
    expect(totals).toHaveLength(2);
    expect(
      totals.every(
        (u) =>
          u.searchParams.get("query")?.includes("[3600s]") &&
          u.searchParams.get("time") === String(to.getTime() / 1000),
      ),
    ).toBe(true);
    const queries = urls.map((u) => u.searchParams.get("query") ?? "");
    const paired = queries.find((q) =>
      q.includes("server_took|paired_execution"),
    );
    expect(paired).toContain('linkd_batch_kind="write"');
    expect(paired).toContain("phase_duration_seconds_sum");
    expect(paired).toContain("phase_duration_seconds_count");
    expect(paired).not.toContain("linkd_metric_schema");
    expect(
      queries.find((q) => q.includes("write_batch_duration_seconds_bucket")),
    ).toContain('linkd_metric_schema="2"');
    expect(
      queries.find((q) => q.includes("pipeline_attempt_duration_seconds_sum")),
    ).toContain('linkd_outcome=~"accepted|rejected|replayed|failed"');
    expect(
      queries
        .filter(
          (q) =>
            q.includes("write_batch") &&
            !q.includes("phase_duration") &&
            !q.includes("triggers_total") &&
            !q.includes("by (linkd_batch_kind)"),
        )
        .every(
          (q) =>
            q.includes('instance="worker-a"') &&
            q.includes('linkd_batch_kind="write"'),
        ),
    ).toBe(true);
    expect(queries.find((q) => q.includes("phase_duration"))).toContain(
      'instance="worker-a"',
    );
    expect(
      queries.find(
        (q) =>
          q.includes("phase_duration") &&
          !q.includes("server_took|paired_execution"),
      ),
    ).not.toContain('linkd_batch_kind="write"');
    expect(
      result.panels
        .find((p) => p.id === "pipeline-average")
        ?.series[0].points.at(-1),
    ).toEqual([to.getTime() / 1000, null]);
    expect(
      result.panels.find((p) => p.id === "lifecycle-batch-executions")
        ?.series[0].points,
    ).toEqual([[to.getTime() / 1000, 3]]);
  });

  it("does not attribute cross-source Bulk requests to a selected EventSource", async () => {
    const queries: string[] = [];
    vi.stubGlobal(
      "fetch",
      vi.fn(async (input: URL | Request | string) => {
        queries.push(new URL(String(input)).searchParams.get("query") ?? "");
        return new Response(
          JSON.stringify({ status: "success", data: { result: [] } }),
        );
      }),
    );
    const result = await new PrometheusConnector(config).panels(
      new Date("2026-09-04T00:00:00Z"),
      new Date("2026-09-04T01:00:00Z"),
      15,
      { eventSourceId: "source-a", partition: 1 },
    );
    expect(queries.some((q) => q.includes("write_batch"))).toBe(false);
    expect(
      result.panels
        .filter((p) => p.id.startsWith("lifecycle-batch-"))
        .every((p) => p.status === "unavailable"),
    ).toBe(true);
    expect(queries.find((q) => q.includes("lane_paused_ratio"))).toContain(
      'messaging_kafka_partition="1"',
    );
  });

  it("queries the four fixed control-plane tasks without dynamic labels", async () => {
    const queries: string[] = [];
    vi.stubGlobal(
      "fetch",
      vi.fn(async (input: URL | Request | string) => {
        queries.push(new URL(String(input)).searchParams.get("query") ?? "");
        return new Response(
          JSON.stringify({ status: "success", data: { result: [] } }),
          { status: 200, headers: { "content-type": "application/json" } },
        );
      }),
    );

    await new PrometheusConnector(config).controlPlaneSnapshot(
      3600,
      "linkd-control-plane:9464",
    );

    expect(queries).toHaveLength(12);
    expect(queries).toContain(
      'max(linkd_control_plane_task_active_ratio{instance="linkd-control-plane:9464"}) by (instance, linkd_task)',
    );
    expect(
      queries.some(
        (query) =>
          query.includes("linkd_control_plane_task_runs_total") &&
          query.includes("[3600s]") &&
          query.includes("linkd_outcome"),
      ),
    ).toBe(true);
    expect(
      queries.some((query) =>
        query.includes("linkd_elasticsearch_alert_archiver_last_batch_scanned"),
      ),
    ).toBe(true);
    expect(
      queries.some((query) =>
        query.includes("linkd_elasticsearch_alert_archiver_last_batch_failed"),
      ),
    ).toBe(true);
    expect(queries.some((query) => query.includes("bk_tenant_id"))).toBe(false);
  });

  it("uses fixed PromQL templates and normalizes series", async () => {
    const requests: string[] = [];
    vi.stubGlobal(
      "fetch",
      vi.fn(async (input: URL | Request | string) => {
        requests.push(String(input));
        return new Response(
          JSON.stringify({
            status: "success",
            data: {
              result: [
                {
                  metric: { linkd_stage: "clean" },
                  value: [1_788_000_000, "2.5"],
                  values: [[1_788_000_000, "2.5"]],
                },
              ],
            },
          }),
          { status: 200, headers: { "content-type": "application/json" } },
        );
      }),
    );
    const result = await new PrometheusConnector(config).panels(
      new Date("2026-08-30T00:00:00Z"),
      new Date("2026-08-30T01:00:00Z"),
      15,
    );
    expect(result.panels.every((panel) => panel.status === "available")).toBe(
      true,
    );
    expect(result.panels[0].series[0].points[0][1]).toBe(2.5);
    expect(requests[0]).toContain("/api/v1/query_range");
    expect(
      requests.some((request) =>
        decodeURIComponent(request).includes("linkd_pipeline_attempts_total"),
      ),
    ).toBe(true);
    const averageRequest = requests
      .map((request) => decodeURIComponent(request))
      .find((request) =>
        request.includes("linkd_pipeline_attempt_duration_seconds_sum"),
      );
    expect(averageRequest).toContain(
      "linkd_pipeline_attempt_duration_seconds_count",
    );
    const inflightRequest = requests.find((request) =>
      request.includes("linkd_messaging_inflight"),
    );
    expect(new URL(inflightRequest!).searchParams.get("query")).toContain(
      "by (linkd_stage, messaging_system)",
    );
    const storeErrorsRequest = requests.find((request) =>
      request.includes("linkd_store_operations_total"),
    );
    expect(new URL(storeErrorsRequest!).searchParams.get("query")).toContain(
      'linkd_outcome!~"succeeded|not_found"',
    );
    const controlPlaneRunsRequest = requests.find((request) =>
      request.includes("linkd_control_plane_task_runs_total"),
    );
    expect(
      new URL(controlPlaneRunsRequest!).searchParams.get("query"),
    ).toContain("round(sum(increase(");
    const cleanerStepAverageRequest = requests.find((request) =>
      request.includes("linkd_cleaner_step_duration_seconds_sum"),
    );
    expect(
      new URL(cleanerStepAverageRequest!).searchParams.get("query"),
    ).toContain("linkd_cleaner_step_duration_seconds_count");
    const cleanerStepP99Request = requests
      .map((request) => new URL(request).searchParams.get("query") ?? "")
      .find(
        (query) =>
          query.includes("linkd_cleaner_step_duration_seconds_bucket") &&
          query.includes("histogram_quantile(0.99"),
      );
    expect(cleanerStepP99Request).toContain(
      "by (le, linkd_event_source_id, linkd_step, linkd_outcome)",
    );
    expect(result.panels).toContainEqual(
      expect.objectContaining({
        id: "pipeline-average",
        title: "阶段处理平均耗时",
        unit: "s",
      }),
    );
    expect(result.panels).toContainEqual(
      expect.objectContaining({
        id: "cleaner-step-p95",
        title: "Cleaner 步骤 P95",
        unit: "s",
      }),
    );
    expect(result.panels).toContainEqual(
      expect.objectContaining({
        id: "control-plane-task-runs",
        title: "控制面任务执行次数",
        unit: "次",
      }),
    );
    expect(result.panels).toContainEqual(
      expect.objectContaining({
        id: "lifecycle-recent-alert-hit-ratio",
        title: "Recent Alert 缓存命中率",
        unit: "%",
      }),
    );
    expect(
      requests.some((request) =>
        decodeURIComponent(request).includes(
          "linkd_lifecycle_recent_alert_cache_operations_total",
        ),
      ),
    ).toBe(true);
  });

  it("uses the configured calculation window independently of chart range", async () => {
    const queries: string[] = [];
    vi.stubGlobal(
      "fetch",
      vi.fn(async (input: URL | Request | string) => {
        queries.push(new URL(String(input)).searchParams.get("query") ?? "");
        return new Response(
          JSON.stringify({ status: "success", data: { result: [] } }),
          { status: 200, headers: { "content-type": "application/json" } },
        );
      }),
    );

    await new PrometheusConnector(config).panels(
      new Date("2026-09-04T00:00:00Z"),
      new Date("2026-09-04T01:00:00Z"),
      15,
      { calculationWindowSeconds: 60 },
    );

    const rollingQueries = queries.filter(
      (query) => query.includes("rate(") || query.includes("increase("),
    );
    expect(rollingQueries.length).toBeGreaterThan(0);
    expect(
      rollingQueries
        .filter(
          (query) =>
            !query.includes("linkd_elasticsearch_write_batch_batches_total") &&
            !query.includes("linkd_elasticsearch_write_batch_items_total"),
        )
        .every((query) => query.includes("[60s]")),
    ).toBe(true);
    expect(
      queries.some(
        (q) =>
          q.includes(
            "increase(linkd_elasticsearch_write_batch_batches_total",
          ) && q.includes("[3600s]"),
      ),
    ).toBe(true);
  });
});
