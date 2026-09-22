import { createClient, createSentinel } from "redis";
import type { ConsoleConfig } from "./config.js";
import { parseRedisNodeAddress } from "./redis.js";

export interface StrategyMembers {
  members: string[];
  total: number | null;
  complete: boolean;
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

export async function readStrategyMembers(
  config: NonNullable<ConsoleConfig["redis"]>,
  key: string,
  signal: AbortSignal,
  timeout: number,
): Promise<StrategyMembers> {
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
        return scanStrategyMembers(async ([operation, ...args]) => {
          signal.throwIfAborted();
          const command = [operation, key, ...args];
          if (config.mode === "sentinel")
            return (client as ReturnType<typeof createSentinel>).sendCommand(
              true,
              command,
            );
          return (client as ReturnType<typeof createClient>).sendCommand(
            command,
          );
        });
      })(),
    ]);
  } finally {
    signal.removeEventListener("abort", abort);
    if (client.isOpen) client.destroy();
  }
}
