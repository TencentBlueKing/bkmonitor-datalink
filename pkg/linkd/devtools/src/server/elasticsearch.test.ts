import { afterEach, describe, expect, it, vi } from "vitest";

import type { DevtoolsConfig } from "./config.js";
import { ElasticsearchConnector } from "./elasticsearch.js";

const config = {
  server: { host: "127.0.0.1", port: 4399 },
  query: {
    defaultRangeSeconds: 3600,
    maxRangeSeconds: 604800,
    defaultLimit: 50,
    maxLimit: 200,
    timeoutMilliseconds: 5000,
  },
  elasticsearch: {
    baseUrl: "http://elasticsearch:9200",
    auth: {},
    eventTargets: ["linkd-events-*"],
    alertTargets: ["linkd-alerts-*"],
    alertLogTargets: ["linkd-alert-logs-*"],
  },
  entities: {
    alerts: "elasticsearch",
    events: "elasticsearch",
    alertLogs: "elasticsearch",
  },
} satisfies DevtoolsConfig;

afterEach(() => vi.unstubAllGlobals());

describe("ElasticsearchConnector", () => {
  it("counts only terminal Alerts through the fixed Active alias", async () => {
    let requestedPath = "";
    let requestedBody = "";
    vi.stubGlobal(
      "fetch",
      vi.fn(async (input: Parameters<typeof fetch>[0], init?: RequestInit) => {
        const url = new URL(String(input));
        requestedPath = url.pathname;
        requestedBody = String(init?.body ?? "");
        return jsonResponse({ count: 17 });
      }),
    );

    const result = await new ElasticsearchConnector({
      ...config,
      elasticsearch: { ...config.elasticsearch, indexPrefix: "linkd-prod" },
    }).archiveBacklog();

    expect(result).toEqual({ status: "available", backlog: 17 });
    expect(requestedPath).toBe("/linkd-prod-alerts-active/_count");
    expect(requestedBody).toContain('"must_not"');
    expect(requestedBody).toContain('"status":"active"');
  });

  it("degrades archive backlog independently when the fixed query fails", async () => {
    vi.stubGlobal(
      "fetch",
      vi.fn(async () => jsonResponse({}, 503)),
    );
    const result = await new ElasticsearchConnector({
      ...config,
      elasticsearch: { ...config.elasticsearch, indexPrefix: "linkd" },
    }).archiveBacklog();
    expect(result).toMatchObject({ status: "unavailable", backlog: null });
  });

  it("queries aliases backed by more than 128 physical indices", async () => {
    vi.stubGlobal(
      "fetch",
      vi.fn(async (input: Parameters<typeof fetch>[0], init?: RequestInit) => {
        const url = new URL(String(input));
        if (url.pathname.startsWith("/_resolve/index/"))
          return jsonResponse({
            indices: Array.from({ length: 129 }, (_, index) => ({
              name: `linkd-events-${index}`,
            })),
          });
        if (url.pathname.endsWith("/_pit") && init?.method === "POST")
          return jsonResponse({ id: "pit-events" });
        if (url.pathname === "/_search")
          return jsonResponse({ hits: { hits: [] } });
        if (url.pathname === "/_pit" && init?.method === "DELETE")
          return jsonResponse({ succeeded: true });
        throw new Error(`unexpected request ${init?.method ?? "GET"} ${url}`);
      }),
    );
    const connector = new ElasticsearchConnector(config);
    await expect(
      connector.search("events", { limit: 50 }),
    ).resolves.toMatchObject({
      items: [],
      source: "elasticsearch",
    });
  });

  it("queries Alert using indexed metadata and returns its payload", async () => {
    let searchBody: Record<string, unknown> | undefined;
    vi.stubGlobal(
      "fetch",
      vi.fn(async (input: Parameters<typeof fetch>[0], init?: RequestInit) => {
        const url = new URL(String(input));
        if (url.pathname.startsWith("/_resolve/index/")) {
          return jsonResponse({ indices: [{ name: "linkd-alerts" }] });
        }
        if (url.pathname.endsWith("/_pit") && init?.method === "POST") {
          return jsonResponse({ id: "pit-alerts" });
        }
        if (url.pathname === "/_search") {
          searchBody = JSON.parse(String(init?.body)) as Record<
            string,
            unknown
          >;
          return jsonResponse({
            pit_id: "pit-alerts-updated",
            hits: {
              hits: [
                {
                  _index: "linkd-alerts-active-000001",
                  _source: {
                    bk_tenant_id: "tenant-a",
                    alert_id: "alert-a",
                    update_at: "2026-08-30T01:00:00Z",
                    status: "active",
                    event_source_id: "source-a",
                    severity: "critical",
                    title: "CPU high",
                  },
                  sort: ["2026-08-30T01:00:00Z", "tenant-a", "alert-a"],
                },
                {
                  _index: "linkd-alert-history-20260825",
                  _source: {
                    bk_tenant_id: "tenant-a",
                    alert_id: "alert-a",
                    update_at: "2026-08-30T01:00:00Z",
                    status: "active",
                    event_source_id: "source-a",
                    severity: "critical",
                    title: "CPU high",
                  },
                  sort: ["2026-08-30T01:00:00Z", "tenant-a", "alert-a", 2],
                },
              ],
            },
          });
        }
        if (url.pathname === "/_pit" && init?.method === "DELETE") {
          return jsonResponse({ succeeded: true });
        }
        throw new Error(`unexpected request ${init?.method ?? "GET"} ${url}`);
      }),
    );

    const page = await new ElasticsearchConnector(config).search("alerts", {
      limit: 50,
      tenantId: "tenant-a",
      status: "active",
      eventSourceId: "source-a",
      fingerprint: "fingerprint-a",
      severity: "critical",
      from: "2026-08-30T00:00:00Z",
      to: "2026-08-30T02:00:00Z",
    });

    expect(page.source).toBe("elasticsearch");
    expect(page.items[0]).toMatchObject({
      tenantId: "tenant-a",
      id: "alert-a",
      timestamp: "2026-08-30T01:00:00Z",
      summary: { status: "active", severity: "critical" },
    });
    expect(JSON.stringify(searchBody)).toContain("update_at");
    expect(JSON.stringify(searchBody)).toContain("fingerprint-a");
    expect(page.items).toHaveLength(1);
    expect(page.warnings).toContain(
      "检测到归档过渡副本，已优先展示 AlertHistory。",
    );
  });

  it("queries Event processing through its object and preserves the API payload shape", async () => {
    let searchBody = "";
    vi.stubGlobal(
      "fetch",
      vi.fn(async (input: Parameters<typeof fetch>[0], init?: RequestInit) => {
        const url = new URL(String(input));
        if (url.pathname.startsWith("/_resolve/index/")) {
          return jsonResponse({ indices: [{ name: "linkd-events" }] });
        }
        if (url.pathname.endsWith("/_pit") && init?.method === "POST") {
          return jsonResponse({ id: "pit-events" });
        }
        if (url.pathname === "/_search") {
          searchBody = String(init?.body);
          return jsonResponse({
            hits: {
              hits: [
                {
                  _source: {
                    bk_tenant_id: "tenant-a",
                    event_id: "event-a",
                    event_source_id: "source-a",
                    received_at: "2026-08-30T01:00:00Z",
                    action: "triggered",
                    severity: "critical",
                    processing: { state: "unprocessed" },
                  },
                  sort: ["2026-08-30T01:00:00Z", "tenant-a", "event-a"],
                },
              ],
            },
          });
        }
        if (url.pathname === "/_pit" && init?.method === "DELETE") {
          return jsonResponse({ succeeded: true });
        }
        throw new Error(`unexpected request ${init?.method ?? "GET"} ${url}`);
      }),
    );

    const page = await new ElasticsearchConnector(config).search("events", {
      limit: 50,
      state: "unprocessed",
      relatedAlertId: "alert-a",
    });

    expect(searchBody).toContain('"processing.state":"unprocessed"');
    expect(searchBody).not.toContain("processing_state");
    expect(searchBody).toContain('"related_alert_id":"alert-a"');
    expect(page.items[0].payload).toMatchObject({
      event_id: "event-a",
      _processing: { state: "unprocessed" },
    });
    expect(page.items[0].payload).not.toHaveProperty("processing");
  });

  it("filters and aggregates AlertLog operation fields", async () => {
    const bodies: Array<Record<string, unknown>> = [];
    vi.stubGlobal(
      "fetch",
      vi.fn(async (input: Parameters<typeof fetch>[0], init?: RequestInit) => {
        const url = new URL(String(input));
        if (url.pathname.startsWith("/_resolve/index/")) {
          return jsonResponse({ indices: [{ name: "linkd-alert-logs" }] });
        }
        if (url.pathname.endsWith("/_search")) {
          bodies.push(
            JSON.parse(String(init?.body)) as Record<string, unknown>,
          );
          return jsonResponse({
            hits: { total: { value: 1 }, hits: [] },
            aggregations: {
              timeline: { buckets: [] },
              facet_operation_kind: {
                buckets: [{ key: "trigger", doc_count: 1 }],
              },
              facet_operator_kind: {
                buckets: [{ key: "source", doc_count: 1 }],
              },
            },
          });
        }
        throw new Error(`unexpected request ${init?.method ?? "GET"} ${url}`);
      }),
    );

    const connector = new ElasticsearchConnector(config);
    const result = await connector.stats("alert-logs", {
      limit: 50,
      alertId: "alert-a",
      operationKind: "trigger",
      operatorKind: "source",
    });

    expect(JSON.stringify(bodies[0])).toContain("operation_kind");
    expect(JSON.stringify(bodies[0])).toContain("operator_kind");
    expect(result.facets).toEqual([
      {
        name: "operation_kind",
        values: [{ value: "trigger", count: 1 }],
      },
      {
        name: "operator_kind",
        values: [{ value: "source", count: 1 }],
      },
    ]);
    expect(result.warnings).toEqual([]);

    const alertStats = await connector.stats("alerts", { limit: 50 });
    expect(alertStats.warnings).toEqual([
      "Alert 归档短暂重叠期间，聚合统计可能重复计数。",
    ]);
  });

  it("builds topology only from configured targets", async () => {
    vi.stubGlobal(
      "fetch",
      vi.fn(async (input: Parameters<typeof fetch>[0]) => {
        const url = new URL(String(input));
        if (url.pathname === "/") {
          return jsonResponse({
            cluster_name: "linkd-test",
            version: { number: "7.17.7" },
          });
        }
        if (url.pathname === "/_cluster/health") {
          return jsonResponse({
            status: "yellow",
            number_of_nodes: 1,
            active_shards: 3,
            unassigned_shards: 3,
          });
        }
        if (url.pathname.startsWith("/_cat/indices/")) {
          return jsonResponse([
            {
              health: "yellow",
              status: "open",
              index: "linkd-events",
              pri: "1",
              rep: "1",
              "docs.count": "7",
              "store.size": "4096",
            },
            {
              health: "yellow",
              status: "open",
              index: "linkd-alerts",
              pri: "1",
              rep: "1",
              "docs.count": "4",
              "store.size": "2048",
            },
            {
              health: "yellow",
              status: "open",
              index: "linkd-alert-logs",
              pri: "1",
              rep: "1",
              "docs.count": "14",
              "store.size": "8192",
            },
          ]);
        }
        if (url.pathname.includes("linkd-events-")) {
          return jsonResponse({
            indices: [{ name: "linkd-events", aliases: ["linkd-events-read"] }],
          });
        }
        if (url.pathname.includes("linkd-alert-logs-")) {
          return jsonResponse({
            indices: [{ name: "linkd-alert-logs", aliases: [] }],
          });
        }
        if (url.pathname.includes("linkd-alerts-")) {
          return jsonResponse({
            indices: [{ name: "linkd-alerts", aliases: [] }],
          });
        }
        throw new Error(`unexpected request ${url}`);
      }),
    );

    const topology = await new ElasticsearchConnector(config).topology();

    expect(topology.cluster).toMatchObject({
      name: "linkd-test",
      version: "7.17.7",
      status: "yellow",
      numberOfNodes: 1,
    });
    expect(topology.indices).toHaveLength(3);
    expect(
      topology.indices.find((index) => index.name === "linkd-events"),
    ).toMatchObject({
      docsCount: 7,
      storeBytes: 4096,
      aliases: ["linkd-events-read"],
      entities: ["events"],
    });
    expect(topology.aliases).toEqual([
      {
        name: "linkd-events-read",
        indices: ["linkd-events"],
        entities: ["events"],
      },
    ]);
  });

  it("keeps configured topology visible before indices are created", async () => {
    vi.stubGlobal(
      "fetch",
      vi.fn(async (input: Parameters<typeof fetch>[0]) => {
        const url = new URL(String(input));
        if (url.pathname === "/") {
          return jsonResponse({
            cluster_name: "linkd-empty",
            version: { number: "7.17.7" },
          });
        }
        if (url.pathname === "/_cluster/health") {
          return jsonResponse({ status: "green", number_of_nodes: 1 });
        }
        if (url.pathname.startsWith("/_resolve/index/")) {
          return jsonResponse({ indices: [], aliases: [] });
        }
        if (url.pathname.startsWith("/_index_template/")) {
          return jsonResponse({ index_templates: [] });
        }
        throw new Error(`unexpected request ${url}`);
      }),
    );

    const topology = await new ElasticsearchConnector(config).topology();

    expect(topology.cluster.name).toBe("linkd-empty");
    expect(topology.targets).toHaveLength(3);
    expect(topology.targets[0]).toMatchObject({
      configuredTargets: expect.any(Array),
      indices: [],
    });
    expect(topology.indices).toEqual([]);
    expect(topology.aliases).toEqual([]);
  });
});

function jsonResponse(value: unknown, status = 200): Response {
  return new Response(JSON.stringify(value), {
    status,
    headers: { "content-type": "application/json" },
  });
}
