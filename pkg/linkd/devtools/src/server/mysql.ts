import mysql, { type Pool, type RowDataPacket } from "mysql2/promise";

import type {
  EntityItem,
  EntityKind,
  EntityPage,
  SearchParams,
} from "../shared/contracts.js";
import type { DevtoolsConfig } from "./config.js";
import { decodeCursor, encodeCursor, queryHash } from "./cursor.js";

interface EntitySpec {
  table: string;
  idColumn: string;
  timeExpression: string;
  directTimeColumn?: string;
  payloadExpression: string;
}

const specs: Record<EntityKind, EntitySpec> = {
  events: {
    table: "linkd_events",
    idColumn: "event_id",
    timeExpression: "received_at_ns",
    directTimeColumn: "received_at_ns",
    payloadExpression: "JSON_SET(payload, '$._processing', processing)",
  },
  alerts: {
    table: "linkd_alerts",
    idColumn: "alert_id",
    timeExpression: "JSON_UNQUOTE(JSON_EXTRACT(payload, '$.update_at'))",
    payloadExpression: "payload",
  },
  "alert-logs": {
    table: "linkd_alert_logs",
    idColumn: "log_id",
    timeExpression: "created_time_ns",
    directTimeColumn: "created_time_ns",
    payloadExpression: "payload",
  },
};

interface EntityRow extends RowDataPacket {
  tenant_id: Buffer | string;
  entity_id: Buffer | string;
  sort_time: bigint | number | string;
  payload: Buffer | string | Record<string, unknown>;
}

interface CountRow extends RowDataPacket {
  key_value?: Buffer | string;
  bucket?: bigint | number | string;
  count: bigint | number | string;
}

export class MysqlConnector {
  private readonly pool: Pool;
  private readonly timeoutMilliseconds: number;

  constructor(config: DevtoolsConfig, pool?: Pool) {
    if (!config.mysql) throw new Error("mysql is not configured");
    this.timeoutMilliseconds = config.query.timeoutMilliseconds;
    this.pool =
      pool ??
      mysql.createPool({
        host: config.mysql.host,
        port: config.mysql.port,
        database: config.mysql.database,
        user: config.mysql.username,
        password: config.mysql.password,
        connectionLimit: config.mysql.connectionLimit,
        multipleStatements: false,
        timezone: "Z",
        supportBigNumbers: true,
        bigNumberStrings: true,
        enableKeepAlive: true,
      });
  }

  async close(): Promise<void> {
    await this.pool.end();
  }

  async search(entity: EntityKind, params: SearchParams): Promise<EntityPage> {
    const spec = specs[entity];
    const queryIdentity = { ...params, cursor: undefined };
    const where: string[] = [];
    const values: unknown[] = [];
    addEqual(where, values, "bk_tenant_id", params.tenantId);
    addEqual(where, values, spec.idColumn, params.id);
    this.addTimeRange(where, values, spec, params);
    this.addEntityFilters(entity, where, values, params);

    if (params.cursor) {
      const cursor = decodeCursor(params.cursor, entity, queryIdentity);
      if (cursor.kind !== "mysql" || cursor.values.length !== 3)
        throw new Error("invalid mysql cursor");
      const [sortTime, tenantID, entityID] = cursor.values;
      where.push(`(
        ${spec.timeExpression} < ? OR
        (${spec.timeExpression} = ? AND (bk_tenant_id > ? OR (bk_tenant_id = ? AND ${spec.idColumn} > ?)))
      )`);
      values.push(sortTime, sortTime, tenantID, tenantID, entityID);
    }

    const limit = params.limit + 1;
    values.push(limit);
    const sql = `SELECT /*+ MAX_EXECUTION_TIME(${this.timeoutMilliseconds}) */
        bk_tenant_id AS tenant_id,
        ${spec.idColumn} AS entity_id,
        ${spec.timeExpression} AS sort_time,
        ${spec.payloadExpression} AS payload
      FROM ${spec.table}
      ${where.length ? `WHERE ${where.join(" AND ")}` : ""}
      ORDER BY ${spec.timeExpression} DESC, bk_tenant_id ASC, ${spec.idColumn} ASC
      LIMIT ?`;
    const [rows] = await this.pool.query<EntityRow[]>(sql, values);
    const visible = rows.slice(0, params.limit);
    const items = visible.map((row) => rowToItem(entity, row));
    let nextCursor: string | undefined;
    if (rows.length > params.limit && visible.length > 0) {
      const last = visible.at(-1)!;
      nextCursor = encodeCursor({
        version: 1,
        kind: "mysql",
        entity,
        queryHash: queryHash(queryIdentity),
        values: [
          String(last.sort_time),
          text(last.tenant_id),
          text(last.entity_id),
        ],
        from: params.from,
        to: params.to,
      });
    }
    const warnings: string[] = [];
    if (!params.tenantId)
      warnings.push(
        "跨租户查询可能无法使用租户前缀索引，已应用时间和超时限制。",
      );
    if (entity === "alerts")
      warnings.push("Alert 时间过滤读取 JSON 字段，适合调试而非大规模报表。");
    return { items, nextCursor, source: "mysql", warnings };
  }

  async detail(
    entity: EntityKind,
    tenantId: string,
    id: string,
  ): Promise<EntityItem | undefined> {
    const spec = specs[entity];
    const sql = `SELECT /*+ MAX_EXECUTION_TIME(${this.timeoutMilliseconds}) */
        bk_tenant_id AS tenant_id,
        ${spec.idColumn} AS entity_id,
        ${spec.timeExpression} AS sort_time,
        ${spec.payloadExpression} AS payload
      FROM ${spec.table}
      WHERE bk_tenant_id = ? AND ${spec.idColumn} = ?
      LIMIT 1`;
    const [rows] = await this.pool.query<EntityRow[]>(sql, [tenantId, id]);
    return rows[0] ? rowToItem(entity, rows[0]) : undefined;
  }

  async stats(entity: EntityKind, params: SearchParams) {
    if (entity === "alerts") return this.alertStats(params);
    const spec = specs[entity];
    const where: string[] = [];
    const values: unknown[] = [];
    addEqual(where, values, "bk_tenant_id", params.tenantId);
    this.addTimeRange(where, values, spec, params);
    if (entity === "events") {
      addEqual(where, values, "processing_state", params.state);
      addEqual(where, values, "related_alert_id", params.relatedAlertId);
    }
    if (entity === "alert-logs") {
      addEqual(where, values, "alert_id", params.alertId);
      addJSONEqual(where, values, "operation_kind", params.operationKind);
      addJSONEqual(where, values, "operator_kind", params.operatorKind);
    }
    const bucketSeconds = statsBucketSeconds(params);
    const bucketNanos = BigInt(bucketSeconds) * 1_000_000_000n;
    const condition = where.length ? `WHERE ${where.join(" AND ")}` : "";
    const totalSQL = `SELECT /*+ MAX_EXECUTION_TIME(${this.timeoutMilliseconds}) */ COUNT(*) AS count FROM ${spec.table} ${condition}`;
    const timelineSQL = `SELECT /*+ MAX_EXECUTION_TIME(${this.timeoutMilliseconds}) */ FLOOR(${spec.directTimeColumn} / ?) AS bucket, COUNT(*) AS count FROM ${spec.table} ${condition} GROUP BY bucket ORDER BY bucket`;
    const [totalRows, timelineRows] = await Promise.all([
      this.pool.query<CountRow[]>(totalSQL, values).then(([rows]) => rows),
      this.pool
        .query<CountRow[]>(timelineSQL, [bucketNanos, ...values])
        .then(([rows]) => rows),
    ]);
    const facets = [];
    if (entity === "events") {
      const facetSQL = `SELECT /*+ MAX_EXECUTION_TIME(${this.timeoutMilliseconds}) */ processing_state AS key_value, COUNT(*) AS count FROM ${spec.table} ${condition} GROUP BY processing_state ORDER BY count DESC`;
      const [rows] = await this.pool.query<CountRow[]>(facetSQL, values);
      facets.push({ name: "processing_state", values: facetValues(rows) });
    }
    if (entity === "alert-logs") {
      for (const field of ["operation_kind", "operator_kind"] as const) {
        const expression = `JSON_UNQUOTE(JSON_EXTRACT(payload, '$.${field}'))`;
        const facetSQL = `SELECT /*+ MAX_EXECUTION_TIME(${this.timeoutMilliseconds}) */ ${expression} AS key_value, COUNT(*) AS count FROM ${spec.table} ${condition} GROUP BY ${expression} ORDER BY count DESC LIMIT 100`;
        const [rows] = await this.pool.query<CountRow[]>(facetSQL, values);
        facets.push({ name: field, values: facetValues(rows) });
      }
    }
    return {
      entity,
      source: "mysql" as const,
      total: numberValue(totalRows[0]?.count),
      timeline: timelineRows.map((row) => ({
        timestamp: new Date(
          Number((BigInt(String(row.bucket)) * bucketNanos) / 1_000_000n),
        ).toISOString(),
        count: numberValue(row.count),
      })),
      facets,
      warnings: [],
    };
  }

  private async alertStats(params: SearchParams) {
    const where: string[] = [];
    const values: unknown[] = [];
    addEqual(where, values, "bk_tenant_id", params.tenantId);
    addEqual(where, values, "status", params.status);
    addEqual(where, values, "event_source_id", params.eventSourceId);
    addEqual(where, values, "severity", params.severity);
    const condition = where.length ? `WHERE ${where.join(" AND ")}` : "";
    const totalSQL = `SELECT /*+ MAX_EXECUTION_TIME(${this.timeoutMilliseconds}) */ COUNT(*) AS count FROM linkd_alerts ${condition}`;
    const facet = async (column: "status" | "event_source_id" | "severity") => {
      const sql = `SELECT /*+ MAX_EXECUTION_TIME(${this.timeoutMilliseconds}) */ ${column} AS key_value, COUNT(*) AS count FROM linkd_alerts ${condition} GROUP BY ${column} ORDER BY count DESC LIMIT 100`;
      const [rows] = await this.pool.query<CountRow[]>(sql, values);
      return { name: column, values: facetValues(rows) };
    };
    const [[totalRows], facets] = await Promise.all([
      this.pool.query<CountRow[]>(totalSQL, values),
      Promise.all([
        facet("status"),
        facet("event_source_id"),
        facet("severity"),
      ]),
    ]);
    return {
      entity: "alerts",
      source: "mysql" as const,
      total: numberValue(totalRows[0]?.count),
      timeline: [],
      facets,
      warnings: [
        "当前 MySQL schema 没有独立 update_at 列，因此只展示 Alert 当前快照分布。",
      ],
    };
  }

  private addTimeRange(
    where: string[],
    values: unknown[],
    spec: EntitySpec,
    params: SearchParams,
  ): void {
    if (spec.directTimeColumn) {
      if (params.from) {
        where.push(`${spec.directTimeColumn} >= ?`);
        values.push(BigInt(new Date(params.from).getTime()) * 1_000_000n);
      }
      if (params.to) {
        where.push(`${spec.directTimeColumn} <= ?`);
        values.push(BigInt(new Date(params.to).getTime()) * 1_000_000n);
      }
      return;
    }
    if (params.from) {
      where.push(`${spec.timeExpression} >= ?`);
      values.push(params.from);
    }
    if (params.to) {
      where.push(`${spec.timeExpression} <= ?`);
      values.push(params.to);
    }
  }

  private addEntityFilters(
    entity: EntityKind,
    where: string[],
    values: unknown[],
    params: SearchParams,
  ): void {
    if (entity === "events") {
      addEqual(where, values, "processing_state", params.state);
      addJSONEqual(where, values, "event_source_id", params.eventSourceId);
      addEqual(where, values, "related_alert_id", params.relatedAlertId);
      return;
    }
    if (entity === "alerts") {
      addEqual(where, values, "status", params.status);
      addEqual(where, values, "event_source_id", params.eventSourceId);
      addEqual(where, values, "fingerprint", params.fingerprint);
      addEqual(where, values, "severity", params.severity);
      return;
    }
    addEqual(where, values, "alert_id", params.alertId);
    addJSONEqual(where, values, "operation_kind", params.operationKind);
    addJSONEqual(where, values, "operator_kind", params.operatorKind);
  }
}

function statsBucketSeconds(params: SearchParams): number {
  const from = params.from
    ? new Date(params.from).getTime()
    : Date.now() - 3600_000;
  const to = params.to ? new Date(params.to).getTime() : Date.now();
  return Math.max(60, Math.ceil((to - from) / 1000 / 240));
}

function facetValues(rows: CountRow[]) {
  return rows.map((row) => ({
    value: text(row.key_value ?? ""),
    count: numberValue(row.count),
  }));
}

function numberValue(value: bigint | number | string | undefined): number {
  const parsed = Number(value ?? 0);
  return Number.isFinite(parsed) ? parsed : 0;
}

function addEqual(
  where: string[],
  values: unknown[],
  column: string,
  value: string | undefined,
): void {
  if (!value) return;
  where.push(`${column} = ?`);
  values.push(value);
}

function addJSONEqual(
  where: string[],
  values: unknown[],
  field: string,
  value: string | undefined,
): void {
  if (!value) return;
  where.push(`JSON_UNQUOTE(JSON_EXTRACT(payload, '$.${field}')) = ?`);
  values.push(value);
}

function rowToItem(entity: EntityKind, row: EntityRow): EntityItem {
  const payload = decodePayload(row.payload);
  const id = text(row.entity_id);
  const tenantId = text(row.tenant_id);
  const timestamp = timestampFromRow(entity, row.sort_time, payload);
  return {
    tenantId,
    id,
    timestamp,
    summary: summary(entity, payload),
    payload,
  };
}

function decodePayload(payload: EntityRow["payload"]): Record<string, unknown> {
  const value = Buffer.isBuffer(payload) ? payload.toString("utf8") : payload;
  if (typeof value === "string")
    return JSON.parse(value) as Record<string, unknown>;
  return value;
}

function text(value: Buffer | string): string {
  return Buffer.isBuffer(value) ? value.toString("utf8") : value;
}

function timestampFromRow(
  entity: EntityKind,
  value: EntityRow["sort_time"],
  payload: Record<string, unknown>,
): string {
  if (entity === "alerts") return String(payload.update_at ?? value);
  const nanos = BigInt(String(value));
  return new Date(Number(nanos / 1_000_000n)).toISOString();
}

function summary(
  entity: EntityKind,
  payload: Record<string, unknown>,
): Record<string, unknown> {
  if (entity === "events") {
    const selected = pick(payload, [
      "action",
      "event_source_id",
      "severity",
      "related_alert_id",
      "title",
    ]);
    const processing = payload._processing;
    if (processing && typeof processing === "object")
      return { ...selected, ...(processing as Record<string, unknown>) };
    return selected;
  }
  if (entity === "alerts") {
    return pick(payload, [
      "status",
      "event_source_id",
      "severity",
      "enrich_status",
      "title",
      "latest_event_id",
    ]);
  }
  return pick(payload, ["alert_id", "operation_kind", "operator_kind"]);
}

function pick(
  payload: Record<string, unknown>,
  keys: string[],
): Record<string, unknown> {
  return Object.fromEntries(
    keys
      .filter((key) => payload[key] !== undefined)
      .map((key) => [key, payload[key]]),
  );
}
