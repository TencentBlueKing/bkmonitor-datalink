import { describe, expect, it, vi } from "vitest";
import type { ConsoleConfig, EventSourceConfig } from "./config.js";
import { StrategyIndexConnector } from "./strategy-index.js";
import { strategyLabel, type StrategyAlertRow } from "./strategy-alerts.js";
import { scanStrategyMembers } from "./strategy-redis.js";
import { strategyQuerySchema } from "../shared/strategy-index.js";

const redis = {
  mode: "standalone" as const,
  address: "redis:6379",
  database: 8,
  password: "private-password",
};
function source(id: string, prefix = "open", db = 8): EventSourceConfig {
  return {
    eventSourceId: id,
    enabled: true,
    strategyHooks: [
      { name: "active", keyPrefix: prefix, redis: { ...redis, database: db } },
    ],
  } as EventSourceConfig;
}
const config = {
  query: { timeoutMilliseconds: 1000 },
  eventSources: [
    source("a"),
    source("b"),
    source("c", "other"),
    source("d", "open", 9),
  ],
} as ConsoleConfig;
const query = {
  event_source_id: "a",
  hook_name: "active",
  bk_tenant_id: "tenant",
  strategy_id: "123",
};
const alert = (
  fp: string,
  source = "a",
  strategy: unknown = 123,
): StrategyAlertRow => ({
  bk_tenant_id: "tenant",
  alert_id: `alert-${source}-${fp}`,
  event_source_id: source,
  fingerprint: fp,
  status: "active",
  labels: { strategy_id: strategy },
});

describe("strategy index reconciliation", () => {
  it("merges shared sources, deduplicates fingerprints and keeps credentials server-side", async () => {
    const read = vi.fn(async () => [
      alert("both"),
      alert("both", "b", "123"),
      alert("missing"),
      alert("other", "a", "00123"),
      alert("skip", "a", true),
    ]);
    const readRedis = vi.fn(async () => ({
      members: ["both", "extra"],
      total: 2,
      complete: true,
    }));
    const connector = new StrategyIndexConnector(
      config,
      { readStrategyAlerts: read },
      readRedis,
    );
    const targets = await connector.targets();
    expect(targets[0].sources).toEqual(["a", "b"]);
    expect(JSON.stringify(targets)).not.toContain("private-password");
    const result = await connector.inspect(query);
    expect(read).toHaveBeenCalledWith("tenant", ["a", "b"], "123", 5001);
    expect(readRedis.mock.calls[0]).toEqual([
      redis,
      "open:tenant:123",
      expect.any(AbortSignal),
      1000,
    ]);
    expect(result.complete).toBe(true);
    expect(result.alerts.matched).toBe(3);
    expect(
      result.rows.map((r) => [r.fingerprint, r.status, r.alerts.length]),
    ).toEqual([
      ["both", "matched", 2],
      ["extra", "redis_only", 0],
      ["missing", "missing_redis", 1],
    ]);
    expect(JSON.stringify(result)).not.toContain("private-password");
  });
  it("does not turn Redis failures into missing members", async () => {
    const c = new StrategyIndexConnector(
      config,
      { readStrategyAlerts: async () => [alert("a")] },
      async () => {
        throw new Error("password private-password");
      },
    );
    const result = await c.inspect(query);
    expect(result.complete).toBe(false);
    expect(result.redis.total).toBeNull();
    expect(result.rows[0].status).toBe("unknown");
    expect(JSON.stringify(result)).not.toContain("private-password");
  });
  it("does not turn failed or truncated Alert reads into Redis-only members", async () => {
    for (const reader of [
      async () => {
        throw new Error("db failed");
      },
      async () =>
        Array.from({ length: 5001 }, () => alert("other", "a", "different")),
    ]) {
      const c = new StrategyIndexConnector(
        config,
        { readStrategyAlerts: reader },
        async () => ({ members: ["extra"], total: 1, complete: true }),
      );
      const result = await c.inspect(query);
      expect(result.complete).toBe(false);
      expect(result.rows[0].status).toBe("unknown");
    }
  });
  it("only infers absence on the side whose read completed", async () => {
    const c = new StrategyIndexConnector(
      config,
      { readStrategyAlerts: async () => [alert("both"), alert("unseen")] },
      async () => ({
        members: ["both", "extra"],
        total: 6000,
        complete: false,
      }),
    );
    expect(
      (await c.inspect(query)).rows.map((r) => [r.fingerprint, r.status]),
    ).toEqual([
      ["both", "matched"],
      ["extra", "redis_only"],
      ["unseen", "unknown"],
    ]);
  });
  it("rejects a wrong tenant response and unknown binding", async () => {
    const read = vi.fn(async () => [
      { ...alert("a"), bk_tenant_id: "another" },
    ]);
    const c = new StrategyIndexConnector(
      config,
      { readStrategyAlerts: read },
      async () => ({ members: [], total: 0, complete: true }),
    );
    await expect(c.inspect({ ...query, hook_name: "unknown" })).rejects.toThrow(
      "Hook must exist",
    );
    expect(read).not.toHaveBeenCalled();
    await expect(c.inspect(query)).rejects.toThrow("scope");
  });
  it("bounds concurrent work and releases slots after cancellation", async () => {
    const c = new StrategyIndexConnector(
      config,
      undefined,
      async (_redis, _key, signal) =>
        new Promise((_, reject) =>
          signal.addEventListener(
            "abort",
            () => reject(new Error("canceled")),
            { once: true },
          ),
        ),
    );
    const abort = new AbortController();
    const running = Array.from({ length: 4 }, () =>
      c.inspect(query, abort.signal),
    );
    await expect(c.inspect(query)).rejects.toThrow("繁忙");
    await Promise.resolve();
    abort.abort();
    await Promise.all(running);
    expect(await c.targets()).toHaveLength(4);
  });
  it("never falls back to stale local bindings when the control plane fails", async () => {
    const c = new StrategyIndexConnector(
      {
        ...config,
        dispatch: {
          url: "http://control",
          apiToken: "private",
          deployment: "test",
        },
      },
      undefined,
      undefined,
      async () => {
        throw new Error("unavailable");
      },
    );
    await expect(c.targets()).rejects.toThrow("unavailable");
  });
  it("requires explicit tenant, strategy and configured binding without arbitrary Redis parameters", () => {
    for (const q of [
      { ...query, bk_tenant_id: "" },
      { ...query, strategy_id: "" },
      { ...query, address: "other:6379" },
    ])
      expect(strategyQuerySchema.safeParse(q).success).toBe(false);
  });
});

describe("strategy Redis set scans", () => {
  it("deduplicates SSCAN pages and detects changes during the read", async () => {
    for (const after of [2, 3]) {
      const replies = [2, ["1", ["a", "a"]], ["0", ["a", "b"]], after];
      const command = vi.fn(async () => replies.shift());
      expect(await scanStrategyMembers(command)).toEqual({
        members: ["a", "b"],
        total: after,
        complete: after === 2,
      });
      expect(command.mock.calls).toHaveLength(4);
    }
  });
  it("bounds members even when Redis ignores COUNT", async () => {
    const replies = [
      6000,
      ["0", Array.from({ length: 6000 }, (_, i) => `fp-${i}`)],
    ];
    const result = await scanStrategyMembers(async () => replies.shift());
    expect(result.members).toHaveLength(5000);
    expect(result.complete).toBe(false);
  });
  it("bounds empty repeated cursors and rejects wrong types", async () => {
    let calls = 0;
    const result = await scanStrategyMembers(async ([op]) => {
      calls++;
      return op === "SCARD" ? 1 : ["1", []];
    });
    expect(calls).toBe(101);
    expect(result.complete).toBe(false);
    await expect(
      scanStrategyMembers(async () => {
        throw new Error("WRONGTYPE");
      }),
    ).rejects.toThrow("WRONGTYPE");
  });
});

it("normalizes strategy labels like the Hook while preserving string identities", () => {
  expect(strategyLabel(-0)).toBe("-0");
  for (const [value, expected] of [
    [123, "123"],
    ["00123", "00123"],
    [" 123 ", " 123 "],
    [1e21, "1000000000000000000000"],
    [1e-7, "0.0000001"],
    [true, undefined],
    [null, undefined],
    ["", undefined],
    [Infinity, undefined],
  ])
    expect(strategyLabel(value)).toBe(expected);
});
