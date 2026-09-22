import { createHash } from "node:crypto";
import { z } from "zod";
import type { ConsoleConfig } from "./config.js";
import type { StrategyBrowseResult } from "../shared/strategy-index.js";
import { queryHash } from "./cursor.js";
import {
  withStrategyRedis,
  type StrategyRedisCommand,
} from "./strategy-redis.js";

const scanCursor = z
  .string()
  .regex(/^\d{1,20}$/)
  .refine((v) => BigInt(v) <= 18446744073709551615n);
const cursorSchema = z
  .object({
    version: z.literal(1),
    target: z.string(),
    phase: z.enum(["sets", "pending"]),
    position: scanCursor,
  })
  .strict();
type BrowseCursor = z.infer<typeof cursorSchema>;
type ScanResult = Pick<
  StrategyBrowseResult,
  "rows" | "phase" | "nextCursor" | "health" | "warnings"
>;

function decodeCursor(value: string | undefined, target: string): BrowseCursor {
  if (!value) return { version: 1, target, phase: "sets", position: "0" };
  try {
    const cursor = cursorSchema.parse(
      JSON.parse(Buffer.from(value, "base64url").toString("utf8")),
    );
    if (cursor.target !== target) throw new Error("target changed");
    return cursor;
  } catch {
    throw new Error(
      "cursor does not match current strategy target; restart scan",
    );
  }
}

function parseKey(prefix: string, key: string) {
  if (!key.startsWith(prefix + ":")) return undefined;
  const suffix = key.slice(prefix.length + 1),
    split = suffix.indexOf(":");
  if (split < 0) return undefined;
  const tenantId = suffix.slice(0, split),
    strategyId = suffix.slice(split + 1);
  if (
    !/^[a-zA-Z0-9_-]{1,64}$/.test(tenantId) ||
    !strategyId ||
    Buffer.byteLength(strategyId) > 1024
  )
    return undefined;
  return { tenantId, strategyId, key };
}

function strings(value: unknown, length: number): Array<string | null> {
  if (
    !Array.isArray(value) ||
    value.length !== length ||
    value.some((v) => v !== null && (typeof v !== "string" || v.length > 128))
  )
    throw new Error("invalid projection metadata");
  return value as Array<string | null>;
}
function count(value: unknown): number {
  if (
    (typeof value !== "string" && typeof value !== "number") ||
    !/^\d+$/.test(String(value))
  )
    throw new Error("invalid projection count");
  const n = Number(value);
  if (!Number.isSafeInteger(n) || n < 0)
    throw new Error("invalid projection count");
  return n;
}
function timestamp(value: string | null): string | null {
  if (value === null || value === "") return null;
  if (!Number.isFinite(Date.parse(value)))
    throw new Error("invalid projection time");
  return value;
}

// SCAN 的 COUNT 是工作量提示，不是严格页长。整批保留返回的 key，绝不截断后推进游标。
// 一页最多执行八次空扫描；页内去重，跨页可能重复，前端不承诺快照或全局总数。
export async function scanStrategyPage(
  command: StrategyRedisCommand,
  prefix: string,
  target: string,
  value: string | undefined,
  scanCount: number,
  signal: AbortSignal,
  auditKeys = false,
): Promise<ScanResult> {
  const cursor = decodeCursor(value, target);
  const root = `linkd:active-index:${createHash("sha256").update(prefix).digest("hex")}`;
  const pattern = prefix.replace(/[\\*?[\]]/g, "\\$&") + ":*";
  const found = new Map<string, NonNullable<ReturnType<typeof parseKey>>>();
  let nextCursor: string | null = null;
  let bytes = 0;
  for (let round = 0; round < 8; round++) {
    signal.throwIfAborted();
    const raw =
      cursor.phase === "sets"
        ? await command([
            "SCAN",
            cursor.position,
            "MATCH",
            pattern,
            "COUNT",
            String(scanCount),
            ...(auditKeys ? [] : ["TYPE", "set"]),
          ])
        : await command([
            "ZSCAN",
            `${root}:pending`,
            cursor.position,
            "COUNT",
            String(scanCount),
          ]);
    if (
      !Array.isArray(raw) ||
      raw.length !== 2 ||
      !Array.isArray(raw[1]) ||
      !scanCursor.safeParse(String(raw[0])).success
    )
      throw new Error("invalid strategy scan response");
    const values: unknown[] = raw[1];
    if (
      values.length > (cursor.phase === "sets" ? 1000 : 2000) ||
      (cursor.phase === "pending" && values.length % 2 !== 0)
    )
      throw new Error("strategy scan page exceeds limits; reduce count");
    for (let i = 0; i < values.length; i += cursor.phase === "sets" ? 1 : 2) {
      const key = values[i];
      if (typeof key !== "string") throw new Error("invalid strategy key");
      bytes += Buffer.byteLength(key);
      if (bytes > 2 * 1024 * 1024)
        throw new Error("strategy scan page exceeds byte limit");
      const scope = parseKey(prefix, key);
      if (scope) found.set(key, scope);
    }
    cursor.position = String(raw[0]);
    const next =
      cursor.position === "0"
        ? cursor.phase === "sets"
          ? { ...cursor, phase: "pending" as const }
          : null
        : cursor;
    nextCursor = next
      ? Buffer.from(JSON.stringify(next)).toString("base64url")
      : null;
    if (found.size || cursor.position === "0") break;
  }
  const rows: StrategyBrowseResult["rows"] = [];
  const scopes = [...found.values()];
  // 整体对账只需发现正式 key，成员与稳定性由对账器读取；不让观测元数据损坏
  // 阻止其他策略比较，也不能隐藏类型损坏的正式 key。待刷新但无集合的策略由数据库侧覆盖。
  if (auditKeys)
    return {
      rows: scopes.map((scope) => ({
        ...scope,
        members: null,
        pending: null,
        lastSuccess: null,
        lastAttempt: null,
        error: null,
      })),
      phase: "sets",
      nextCursor: cursor.position === "0" ? null : nextCursor,
      health: {
        lastSuccess: null,
        lastAttempt: null,
        error: null,
        pendingCount: 0,
        oldestDueAt: null,
      },
      warnings: [],
    };
  // 限制并发元数据读取，避免单页放大为上千个同时在途请求。
  for (let start = 0; start < scopes.length; start += 8) {
    signal.throwIfAborted();
    const chunk = await Promise.all(
      scopes.slice(start, start + 8).map(async (scope) => {
        try {
          const [cardinality, rawStatus, rawPending] = await Promise.all([
            command(["SCARD", scope.key]),
            command([
              "HMGET",
              `${root}:status:${scope.tenantId}:${scope.strategyId}`,
              "last_success",
              "last_attempt",
              "error",
            ]),
            command(["ZSCORE", `${root}:pending`, scope.key]),
          ]);
          const status = strings(rawStatus, 3),
            members = count(cardinality),
            pending = rawPending !== null;
          // pending 阶段只补充尚未建好集合的组合，减少与前一阶段的重复。
          if (cursor.phase === "pending" && members > 0) return undefined;
          if (members === 0 && !pending) return undefined;
          return {
            ...scope,
            members,
            pending,
            lastSuccess: timestamp(status[0]),
            lastAttempt: timestamp(status[1]),
            error: status[2] || null,
          };
        } catch {
          signal.throwIfAborted();
          return {
            ...scope,
            members: null,
            pending: null,
            lastSuccess: null,
            lastAttempt: null,
            error: "metadata_read_failed",
          };
        }
      }),
    );
    for (const row of chunk) if (row) rows.push(row);
  }
  const [rawHealth, pendingCount, oldest] = await Promise.all([
    command([
      "HMGET",
      `${root}:health`,
      "last_success",
      "last_attempt",
      "error",
    ]),
    command(["ZCARD", `${root}:pending`]),
    command(["ZRANGE", `${root}:pending`, "0", "0", "WITHSCORES"]),
  ]);
  const health = strings(rawHealth, 3);
  const queued = count(pendingCount);
  if (cursor.phase === "sets" && cursor.position === "0" && queued === 0)
    nextCursor = null;
  // 原始命令在 RESP2 返回平铺 member/score，RESP3 返回嵌套二元组。
  const oldestPair =
    Array.isArray(oldest) && oldest.length === 1 && Array.isArray(oldest[0])
      ? oldest[0]
      : oldest;
  if (
    !Array.isArray(oldestPair) ||
    (oldestPair.length !== 0 && oldestPair.length !== 2)
  )
    throw new Error("invalid pending summary");
  let oldestDueAt: string | null = null;
  if (oldestPair.length === 2) {
    const due = Number(oldestPair[1]);
    if (!Number.isSafeInteger(due) || due < 0 || due > 8640000000000000)
      throw new Error("invalid pending due time");
    oldestDueAt = new Date(due).toISOString();
  }
  return {
    rows,
    phase: cursor.phase,
    nextCursor,
    health: {
      lastSuccess: timestamp(health[0]),
      lastAttempt: timestamp(health[1]),
      error: health[2] || null,
      pendingCount: queued,
      oldestDueAt,
    },
    warnings: [
      "列表按扫描批次展示，不是固定快照；并发变更可能导致跨批重复，扫描数量不代表总数。",
      "仅列出已缓存或待刷新的组合；上游异常或重建期间可能暂缺部分策略。",
    ],
  };
}

export async function readStrategyPage(
  config: NonNullable<ConsoleConfig["redis"]>,
  prefix: string,
  identity: unknown,
  cursor: string | undefined,
  scanCount: number,
  signal: AbortSignal,
  timeout: number,
  auditKeys = false,
): Promise<ScanResult> {
  const target = queryHash(identity);
  decodeCursor(cursor, target);
  const bounded = AbortSignal.any([signal, AbortSignal.timeout(timeout)]);
  return withStrategyRedis(config, bounded, timeout, (command) =>
    scanStrategyPage(
      command,
      prefix,
      target,
      cursor,
      scanCount,
      bounded,
      auditKeys,
    ),
  );
}
