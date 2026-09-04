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
});
