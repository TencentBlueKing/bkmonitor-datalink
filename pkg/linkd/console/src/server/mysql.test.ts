import type { Pool } from "mysql2/promise";
import { describe, expect, it, vi } from "vitest";

import type { ConsoleConfig } from "./config.js";
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
} satisfies ConsoleConfig;

describe("MysqlConnector", () => {
  it.each(["events", "alerts", "alert-logs"] as const)(
    "keeps %s list and every statistic on identical filters",
    async (entity) => {
      const query = vi.fn<
        (sql: string, values?: unknown[]) => Promise<unknown>
      >(async () => [[], []]);
      const connector = new MysqlConnector(config, {
        query,
      } as unknown as Pool);
      const params = {
        limit: 20,
        tenantId: "tenant",
        id: "entity",
        eventSourceId: "source",
        state: "accepted",
        status: "active",
        fingerprint: "fp",
        subjectId: "host",
        sourceEventId: "original",
        sourceAlertId: "external",
        enrichStatus: "failed",
        outcome: "alert_updated",
        severity: "critical",
        alertId: "alert",
        operationKind: "close",
        operatorKind: "user",
        from: "2026-09-23T00:00:00.000Z",
        to: "2026-09-23T01:00:00.000Z",
      };
      await connector.search(entity, params);
      const [listSql, listValues] = query.mock.calls[0];
      const listWhere = listSql.split("WHERE ")[1].split("ORDER BY")[0].trim();
      query.mockClear();
      await connector.stats(entity, params);
      for (const [sql, values] of query.mock.calls) {
        const clauses = sql
          .split("WHERE ")[1]
          .split(/GROUP BY|ORDER BY/)[0]
          .trim()
          .split(" AND ");
        const args = sql.includes(" AS bucket") ? values?.slice(1) : values;
        const expected = listWhere
          .split(" AND ")
          .map((clause, index) => [clause, listValues?.[index]]);
        expect(clauses.map((clause, index) => [clause, args?.[index]])).toEqual(
          expect.arrayContaining(expected),
        );
        expect(clauses).toHaveLength(expected.length);
      }
    },
  );
  it("paginates ascending timestamps with stable tenant and ID tie-breaks", async () => {
    const query = vi.fn<(sql: string, values?: unknown[]) => Promise<unknown>>(
      async () => [
        [1, 2].map((n) => ({
          tenant_id: "tenant",
          entity_id: `event-${n}`,
          sort_time: "1788105600000000000",
          payload: {},
        })),
        [],
      ],
    );
    const connector = new MysqlConnector(config, { query } as unknown as Pool);
    const first = await connector.search("events", { limit: 1, order: "asc" });
    await connector.search("events", {
      limit: 1,
      order: "asc",
      cursor: first.nextCursor,
    });
    expect(query.mock.calls[1][0]).toContain("received_at_ns > ?");
    expect(query.mock.calls[1][0]).toContain(
      "ORDER BY received_at_ns ASC, bk_tenant_id ASC, event_id ASC",
    );
    await expect(
      connector.search("events", {
        limit: 1,
        order: "desc",
        cursor: first.nextCursor,
      }),
    ).rejects.toThrow("cursor does not match query");
  });
  it("reconciles only tenant-scoped active alerts without a time cutoff or full payload", async () => {
    const query = vi.fn(async () => [
      [
        {
          bk_tenant_id: "tenant",
          alert_id: "a",
          event_source_id: "source",
          fingerprint: "fp",
          status: "active",
          strategy_json: '"00123"',
        },
      ],
      [],
    ]);
    const connector = new MysqlConnector(config, { query } as unknown as Pool);
    expect(
      await connector.readStrategyAlerts(
        "tenant",
        ["source", "shared"],
        "00123",
        5001,
      ),
    ).toEqual([
      {
        bk_tenant_id: "tenant",
        alert_id: "a",
        event_source_id: "source",
        fingerprint: "fp",
        status: "active",
        labels: { strategy_id: "00123" },
      },
    ]);
    const [options, values] = query.mock.calls[0] as unknown as [
      { sql: string; timeout: number },
      unknown[],
    ];
    expect(options.sql).toContain("bk_tenant_id=?");
    expect(options.sql).toContain("status='active'");
    expect(options.sql).toContain("enrich_json");
    expect(options.sql).toContain("event_source_id IN (?,?)");
    expect(options.sql).not.toMatch(/update_at|SELECT.*payload FROM/);
    expect(options.timeout).toBe(5000);
    expect(values).toEqual(["source", "shared", "tenant"]);
  });
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
