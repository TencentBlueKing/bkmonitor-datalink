import { describe, expect, it } from "vitest";

import type { DevtoolsConfig } from "./config.js";
import { encodeCursor, queryHash } from "./cursor.js";
import { parseSearchQuery } from "./query.js";

const config = {
  server: { host: "127.0.0.1", port: 4399 },
  query: {
    defaultRangeSeconds: 3600,
    maxRangeSeconds: 604800,
    defaultLimit: 50,
    maxLimit: 200,
    timeoutMilliseconds: 5000,
  },
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

describe("parseSearchQuery", () => {
  it("adds a bounded default range", () => {
    const parsed = parseSearchQuery({}, "events", config);
    expect(parsed.limit).toBe(50);
    expect(
      new Date(parsed.to!).getTime() - new Date(parsed.from!).getTime(),
    ).toBe(3_600_000);
  });

  it("allows exact id lookup without a time window", () => {
    const parsed = parseSearchQuery({ id: "event-a" }, "events", config);
    expect(parsed.id).toBe("event-a");
    expect(parsed.from).toBeUndefined();
    expect(parsed.to).toBeUndefined();
  });

  it("maps the EventSource query field", () => {
    const parsed = parseSearchQuery(
      { event_source_id: "source-a" },
      "alerts",
      config,
    );
    expect(parsed.eventSourceId).toBe("source-a");
  });

  it("maps the related Alert query field", () => {
    const parsed = parseSearchQuery(
      { related_alert_id: "alert-a" },
      "events",
      config,
    );
    expect(parsed.relatedAlertId).toBe("alert-a");
  });

  it("reuses the cursor time range for subsequent pages", () => {
    const first = parseSearchQuery({}, "events", config);
    const cursor = encodeCursor({
      version: 1,
      kind: "mysql",
      entity: "events",
      queryHash: queryHash({ ...first, cursor: undefined }),
      values: ["123", "tenant-a", "event-a"],
      from: first.from,
      to: first.to,
    });
    const next = parseSearchQuery({ cursor }, "events", config);
    expect(next.from).toBe(first.from);
    expect(next.to).toBe(first.to);
  });

  it("rejects oversized ranges and limits", () => {
    expect(() =>
      parseSearchQuery(
        { from: "2026-08-01T00:00:00.000Z", to: "2026-08-20T00:00:00.000Z" },
        "events",
        config,
      ),
    ).toThrow("query range");
    expect(() => parseSearchQuery({ limit: "201" }, "events", config)).toThrow(
      "limit",
    );
  });
});
