import type { Pool } from "mysql2/promise";
import { describe, expect, it, vi } from "vitest";

import type { DevtoolsConfig } from "./config.js";
import { MysqlConnector } from "./mysql.js";

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

describe("MysqlConnector", () => {
  it("uses a fixed read-only query and normalizes payloads", async () => {
    const query = vi.fn(async (sqlText: string, values?: unknown[]) => {
      expect(sqlText).toContain("SELECT");
      expect(values).toBeDefined();
      return [
        [
          {
            tenant_id: Buffer.from("tenant-a"),
            entity_id: Buffer.from("event-a"),
            sort_time: "1788105600000000000",
            payload: JSON.stringify({
              event_id: "event-a",
              state: "accepted",
              action: "triggered",
            }),
          },
        ],
        [],
      ];
    });
    const pool = { query, end: vi.fn() } as unknown as Pool;
    const connector = new MysqlConnector(config, pool);
    const result = await connector.search("events", {
      limit: 50,
      eventSourceId: "source-a",
      relatedAlertId: "alert-a",
    });
    const sql = String(query.mock.calls[0][0]).toLowerCase();
    expect(sql.trimStart()).toMatch(/^select/);
    expect(sql).not.toMatch(/\b(insert|update|delete|alter|drop)\b/);
    expect(sql).toContain("event_source_id");
    expect(sql).toContain("related_alert_id");
    expect(query.mock.calls[0][1]).toContain("source-a");
    expect(query.mock.calls[0][1]).toContain("alert-a");
    expect(result.items[0]).toMatchObject({
      tenantId: "tenant-a",
      id: "event-a",
    });
    expect(result.items[0].payload.state).toBe("accepted");
  });

  it("applies and aggregates AlertLog operation filters", async () => {
    const query = vi.fn(async (sqlText: string, values?: unknown[]) => {
      if (sqlText.includes(" AS bucket")) {
        return [[{ bucket: "1", count: 1 }], []];
      }
      if (sqlText.includes(" AS key_value")) {
        const selected = sqlText.match(
          /\$\.(operation_kind|operator_kind)'\)\) AS key_value/,
        )?.[1];
        const value = selected === "operation_kind" ? "trigger" : "source";
        return [[{ key_value: value, count: 1 }], []];
      }
      expect(values).toEqual(["alert-a", "trigger", "source"]);
      return [[{ count: 1 }], []];
    });
    const pool = { query, end: vi.fn() } as unknown as Pool;

    const result = await new MysqlConnector(config, pool).stats("alert-logs", {
      limit: 50,
      alertId: "alert-a",
      operationKind: "trigger",
      operatorKind: "source",
    });

    const statements = query.mock.calls.map(([sqlText]) => String(sqlText));
    expect(statements.some((sql) => sql.includes("$.operation_kind"))).toBe(
      true,
    );
    expect(statements.some((sql) => sql.includes("$.operator_kind"))).toBe(
      true,
    );
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
  });
});
