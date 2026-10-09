// @vitest-environment node
import { afterEach, expect, it, vi } from "vitest";
import type { ConsoleConfig } from "./config.js";
import { createApp } from "./app.js";
import { createKACAlarmConnector } from "./elasticsearch.js";

const config: ConsoleConfig = {
  server: { host: "127.0.0.1", port: 4399 },
  query: {
    defaultRangeSeconds: 3600,
    maxRangeSeconds: 604800,
    defaultLimit: 50,
    maxLimit: 200,
    timeoutMilliseconds: 5000,
  },
  entities: { events: "mysql", alerts: "mysql", alertLogs: "mysql" },
  plugins: {
    kac: {
      enabled: false,
      alarm_event_index: "kac_alarm_event",
      elasticsearch: {
        addresses: ["https://kac-es.example"],
        basic_auth: { username: "reader", password: "private-password" },
      },
    },
  },
};
const payload = {
  bk_tenant_id: "tenant-a",
  alarm_id: "legacy-id",
  name: "KAC CPU 告警",
  alarm_time: "2026-10-09 09:00:00",
  storage_time: "2026-10-09 09:00:02",
  status: "executing",
  level: "critical",
  source_id: "source-a",
  action: "firing",
};
afterEach(() => vi.unstubAllGlobals());

it("queries the independent KAC alias, preserves raw fields and converts time for list and statistics", async () => {
  const bodies: Record<string, unknown>[] = [];
  vi.stubGlobal(
    "fetch",
    vi.fn(async (input: string, init?: RequestInit) => {
      const url = new URL(input);
      expect(url.origin).toBe("https://kac-es.example");
      expect(new Headers(init?.headers).get("authorization")).toBe(
        `Basic ${Buffer.from("reader:private-password").toString("base64")}`,
      );
      expect(url.pathname).not.toContain("linkd");
      if (url.pathname.startsWith("/_resolve/index/")) {
        expect(url.pathname).toBe("/_resolve/index/kac_alarm_event");
        return Response.json({
          aliases: [
            { name: "kac_alarm_event", indices: ["kac_alarm_event_20261009"] },
          ],
        });
      }
      if (url.pathname.endsWith("/_pit") && init?.method === "POST")
        return Response.json({ id: "kac-pit" });
      if (url.pathname === "/_pit") return Response.json({ succeeded: true });
      const body = JSON.parse(String(init?.body));
      bodies.push(body);
      if (JSON.stringify(body.query).includes("tenant-b"))
        return Response.json({ hits: { hits: [] } });
      if (body.size === 0)
        return Response.json({
          hits: { total: { value: 1 } },
          aggregations: {
            timeline: {
              buckets: [
                {
                  key: Date.parse("2026-10-09T09:00:00Z"),
                  key_as_string: "2026-10-09 09:00:00",
                  doc_count: 1,
                },
              ],
            },
            facet_status: { buckets: [{ key: "executing", doc_count: 1 }] },
          },
        });
      return Response.json({
        hits: {
          hits: [
            {
              _source: payload,
              _index: "kac_alarm_event_20261009",
              sort: [
                Date.parse("2026-10-09T09:00:00Z"),
                "tenant-a",
                "legacy-id",
                "kac_alarm_event_20261009",
              ],
            },
          ],
        },
      });
    }),
  );
  const app = await createApp(config);
  try {
    const cap = await app.inject("/local-api/capabilities");
    expect(cap.json().entities["kac-alarms"].source).toBe("elasticsearch");
    expect(cap.body).not.toContain("private-password");
    const query =
      "?bk_tenant_id=tenant-a&from=2026-10-09T00:00:00Z&to=2026-10-09T02:00:00Z&source_id=source-a&status=executing&level=critical&action=firing&event_id=event-a";
    const list = await app.inject("/local-api/kac-alarms" + query);
    expect(list.statusCode).toBe(200);
    expect(list.json().items[0]).toMatchObject({
      id: "legacy-id",
      timestamp: "2026-10-09T01:00:00.000Z",
      payload,
    });
    expect(bodies[0].sort).toEqual([
      { alarm_time: "desc" },
      { bk_tenant_id: "asc" },
      { alarm_id: "asc" },
      { _index: "asc" },
    ]);
    expect(bodies[0].query).toEqual({
      bool: {
        filter: [
          { term: { bk_tenant_id: "tenant-a" } },
          {
            range: {
              alarm_time: {
                format: "epoch_millis",
                gte: Date.parse("2026-10-09T08:00:00Z"),
                lte: Date.parse("2026-10-09T10:00:00Z"),
              },
            },
          },
          { term: { source_id: "source-a" } },
          { term: { status: "executing" } },
          { term: { level: "critical" } },
          { term: { action: "firing" } },
          { term: { event_id: "event-a" } },
        ],
      },
    });
    const stats = await app.inject("/local-api/kac-alarms/stats" + query);
    expect(stats.statusCode).toBe(200);
    expect(stats.json().timeline).toEqual([
      { timestamp: "2026-10-09T01:00:00.000Z", count: 1 },
    ]);
    expect(stats.json().facets[0]).toEqual({
      name: "status",
      values: [{ value: "executing", count: 1 }],
    });
    expect(bodies[1].query).toEqual(bodies[0].query);
    expect(JSON.stringify(bodies[1].aggs)).toContain(
      String(Date.parse("2026-10-09T08:00:00Z")),
    );
    const detail = await app.inject(
      "/local-api/kac-alarms/legacy-id?bk_tenant_id=tenant-b",
    );
    expect(detail.statusCode).toBe(404);
    expect(bodies[2].query).toEqual({
      bool: {
        filter: [
          { term: { bk_tenant_id: "tenant-b" } },
          { term: { alarm_id: "legacy-id" } },
        ],
      },
    });
    expect(bodies[2].query).not.toHaveProperty("range");
  } finally {
    await app.close();
  }
});

it("requires tenant scope, enforces limits, reports missing configuration and does not issue an ES request", async () => {
  const fetcher = vi.fn();
  vi.stubGlobal("fetch", fetcher);
  const app = await createApp(config);
  const missing = await createApp({ ...config, plugins: undefined });
  try {
    for (const url of [
      "/local-api/kac-alarms",
      "/local-api/kac-alarms/stats",
      "/local-api/kac-alarms/legacy-id",
      "/local-api/kac-alarms?bk_tenant_id=%20",
      "/local-api/kac-alarms?bk_tenant_id=tenant-a&limit=201",
      "/local-api/kac-alarms?bk_tenant_id=tenant-a&from=2026-09-01T00:00:00Z&to=2026-10-09T00:00:00Z",
    ]) {
      expect((await app.inject(url)).statusCode).toBe(400);
    }
    const response = await missing.inject(
      "/local-api/kac-alarms?bk_tenant_id=tenant-a",
    );
    expect(response.statusCode).toBe(502);
    expect(
      (await missing.inject("/local-api/capabilities")).json().entities,
    ).not.toHaveProperty("kac-alarms");
    expect(fetcher).not.toHaveBeenCalled();
  } finally {
    await app.close();
    await missing.close();
  }
});

it("paginates KAC dates without changing the ES sort value, and rejects another tenant's cursor", async () => {
  const bodies: Record<string, unknown>[] = [];
  const closed: string[] = [];
  vi.stubGlobal(
    "fetch",
    vi.fn(async (input: string, init?: RequestInit) => {
      const path = new URL(input).pathname;
      if (path.startsWith("/_resolve/"))
        return Response.json({ indices: [{ name: "kac_alarm_event" }] });
      if (path.endsWith("/_pit") && init?.method === "POST")
        return Response.json({ id: "pit-first" });
      if (path === "/_pit") {
        closed.push(JSON.parse(String(init?.body)).id);
        return Response.json({ succeeded: true });
      }
      const body = JSON.parse(String(init?.body));
      bodies.push(body);
      const hit = (id: string) => ({
        _source: { ...payload, alarm_id: id },
        sort: [1791536400000, "tenant-a", id, "kac_alarm_event"],
      });
      return Response.json({
        pit_id: "pit-next",
        hits: { hits: bodies.length === 1 ? [hit("a"), hit("b")] : [hit("b")] },
      });
    }),
  );
  const connector = createKACAlarmConnector(config)!;
  const first = await connector.search("kac-alarms", {
    tenantId: "tenant-a",
    limit: 1,
  });
  expect(first.nextCursor).toBeDefined();
  await expect(
    connector.search("kac-alarms", {
      tenantId: "tenant-b",
      limit: 1,
      cursor: first.nextCursor,
    }),
  ).rejects.toThrow("cursor does not match query");
  const second = await connector.search("kac-alarms", {
    tenantId: "tenant-a",
    limit: 1,
    cursor: first.nextCursor,
  });
  expect(second.items.map((item) => item.id)).toEqual(["b"]);
  expect(bodies[1].search_after).toEqual([
    1791536400000,
    "tenant-a",
    "a",
    "kac_alarm_event",
  ]);
  expect(closed).toEqual(["pit-next"]);
});

it.each([403, 500])(
  "reports ES failure %s without exposing credentials",
  async (status) => {
    vi.stubGlobal(
      "fetch",
      vi.fn(async () => new Response("private-password", { status })),
    );
    const app = await createApp(config);
    try {
      const response = await app.inject(
        "/local-api/kac-alarms?bk_tenant_id=tenant-a&id=missing",
      );
      expect(response.statusCode).toBe(502);
      expect(response.json().error.code).toBe("data_source_error");
      expect(response.body).not.toContain("private-password");
    } finally {
      await app.close();
    }
  },
);
