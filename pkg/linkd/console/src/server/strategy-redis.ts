import { createClient, createSentinel } from "redis";
import { createHash } from "node:crypto";
import type { ConsoleConfig } from "./config.js";
import { parseRedisNodeAddress } from "./redis.js";

export interface StrategyMembers {
  members: string[];
  total: number | null;
  complete: boolean;
  projection?: {
    lastSuccess: string | null;
    lastAttempt: string | null;
    error: string | null;
    discoverySuccess: string | null;
    discoveryError: string | null;
    pending: boolean;
  };
}

// 只扫描服务端已解析的单个集合；COUNT 是提示，另外限制总成员、响应字节和轮数。
export async function scanStrategyMembers(
  command: (args: string[]) => Promise<unknown>,
): Promise<StrategyMembers> {
  const total = Number(await command(["SCARD"]));
  if (!Number.isSafeInteger(total) || total < 0)
    throw new Error("invalid cardinality");
  let cursor = "0",
    bytes = 0;
  const members = new Set<string>();
  for (let round = 0; round < 100; round++) {
    const response = await command(["SSCAN", cursor, "COUNT", "200"]);
    if (
      !Array.isArray(response) ||
      !Array.isArray(response[1]) ||
      !/^\d+$/.test(String(response[0]))
    )
      throw new Error("invalid set scan");
    cursor = String(response[0]);
    for (const raw of response[1]) {
      if (typeof raw !== "string" || raw.length > 4096)
        throw new Error("invalid fingerprint");
      bytes += Buffer.byteLength(raw);
      if (
        bytes > 2 * 1024 * 1024 ||
        (!members.has(raw) && members.size >= 5000)
      )
        return { members: [...members], total, complete: false };
      members.add(raw);
    }
    if (cursor === "0") {
      const after = Number(await command(["SCARD"]));
      return {
        members: [...members],
        total: after,
        complete: total === after && members.size === after,
      };
    }
  }
  return { members: [...members], total, complete: false };
}

export type StrategyRedisCommand = (args: string[]) => Promise<unknown>;

// 策略诊断共享连接、认证与取消边界；所有命令均使用服务端解析的目标。
export async function withStrategyRedis<T>(
  config: NonNullable<ConsoleConfig["redis"]>,
  signal: AbortSignal,
  timeout: number,
  run: (command: StrategyRedisCommand) => Promise<T>,
): Promise<T> {
  const socket = { connectTimeout: timeout, reconnectStrategy: false as const };
  const client =
    config.mode === "sentinel"
      ? createSentinel({
          name: config.sentinel!.masterName,
          sentinelRootNodes: config.sentinel!.addresses.map(
            parseRedisNodeAddress,
          ),
          nodeClientOptions: {
            username: config.username,
            password: config.password,
            database: config.database,
            socket,
          },
          sentinelClientOptions: {
            username: config.sentinel!.username,
            password: config.sentinel!.password,
            socket,
          },
          replicaPoolSize: 0,
          passthroughClientErrorEvents: true,
        })
      : createClient({
          socket: { ...socket, ...parseRedisNodeAddress(config.address!) },
          username: config.username,
          password: config.password,
          database: config.database,
        });
  client.on("error", () => undefined);
  let abort: () => void = () => undefined;
  try {
    signal.throwIfAborted();
    const canceled = new Promise<never>((_, reject) => {
      abort = () => {
        reject(new Error("Redis query canceled"));
        if (client.isOpen) client.destroy();
      };
      signal.addEventListener("abort", abort, { once: true });
    });
    return await Promise.race([
      canceled,
      (async () => {
        await client.connect();
        signal.throwIfAborted();
        const command: StrategyRedisCommand = async (args) => {
          signal.throwIfAborted();
          if (config.mode === "sentinel")
            return (client as ReturnType<typeof createSentinel>).sendCommand(
              true,
              args,
            );
          return (client as ReturnType<typeof createClient>).sendCommand(args);
        };
        return run(command);
      })(),
    ]);
  } finally {
    signal.removeEventListener("abort", abort);
    if (client.isOpen) client.destroy();
  }
}

export async function readStrategyMembers(
  config: NonNullable<ConsoleConfig["redis"]>,
  key: string,
  signal: AbortSignal,
  timeout: number,
  keyPrefix?: string,
): Promise<StrategyMembers> {
  return withStrategyRedis(config, signal, timeout, async (command) => {
    const root =
      keyPrefix === undefined
        ? undefined
        : `linkd:active-index:${createHash("sha256").update(keyPrefix).digest("hex")}`;
    const statusKey =
      root && `${root}:status:${key.slice(keyPrefix!.length + 1)}`;
    const readStatus = async () => {
      const raw = await command([
        "HMGET",
        statusKey!,
        "last_success",
        "last_attempt",
        "error",
        "members",
      ]);
      if (
        !Array.isArray(raw) ||
        raw.length !== 4 ||
        raw.some((v) => v !== null && (typeof v !== "string" || v.length > 128))
      )
        throw new Error("invalid projection status");
      return raw as Array<string | null>;
    };
    const before = statusKey ? await readStatus() : undefined;
    const members = await scanStrategyMembers(([operation, ...args]) =>
      command([operation, key, ...args]),
    );
    if (!root || !statusKey) return members;
    const after = await readStatus();
    const health = await command([
      "HMGET",
      `${root}:health`,
      "last_success",
      "error",
    ]);
    if (
      !Array.isArray(health) ||
      health.length !== 2 ||
      health.some(
        (v) => v !== null && (typeof v !== "string" || v.length > 128),
      )
    )
      throw new Error("invalid projection health");
    const pending = await command(["ZSCORE", `${root}:pending`, key]);
    return {
      ...members,
      complete:
        members.complete &&
        before?.[0] === after[0] &&
        (after[3] === null || Number(after[3]) === members.total),
      projection: {
        lastSuccess: after[0],
        lastAttempt: after[1],
        error: after[2] || null,
        discoverySuccess: health[0],
        discoveryError: health[1] || null,
        pending: pending !== null,
      },
    };
  });
}
