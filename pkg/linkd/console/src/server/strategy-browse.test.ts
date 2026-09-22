import { expect, it, vi } from "vitest";
import { scanStrategyPage } from "./strategy-browse.js";
import { strategyBrowseQuerySchema } from "../shared/strategy-index.js";

function fixture(
  scans: Array<[string, string[]]>,
  cards: Record<string, number> = {},
  pending: string[] = [],
) {
  return vi.fn(async (args: string[]): Promise<unknown> => {
    switch (args[0]) {
      case "SCAN":
        return scans.shift() ?? ["0", []];
      case "ZSCAN":
        return ["0", pending.flatMap((key) => [key, "1000"])];
      case "SCARD":
        return cards[args[1]] ?? 0;
      case "HMGET":
        return args[1].endsWith(":health")
          ? ["2026-09-22T00:00:00Z", "2026-09-22T00:00:01Z", ""]
          : ["2026-09-22T00:00:00Z", "2026-09-22T00:00:00Z", ""];
      case "ZSCORE":
        return pending.includes(args[2]) ? "1000" : null;
      case "ZCARD":
        return pending.length;
      case "ZRANGE":
        return pending.length ? [pending[0], "1000"] : [];
      default:
        throw new Error("unexpected command");
    }
  });
}

it("preserves the whole SCAN batch even when COUNT is exceeded and removes page-local duplicates", async () => {
  const keys = Array.from({ length: 15 }, (_, i) => `open:tenant:${i}`);
  const cards = Object.fromEntries(keys.map((k) => [k, 1]));
  const command = fixture(
    [["123", [...keys, keys[0], "outside:tenant:123"]]],
    cards,
  );
  const result = await scanStrategyPage(
    command,
    "open",
    "target",
    undefined,
    10,
    AbortSignal.timeout(1000),
  );
  expect(result.rows).toHaveLength(15);
  expect(command).toHaveBeenCalledWith([
    "SCAN",
    "0",
    "MATCH",
    "open:*",
    "COUNT",
    "10",
    "TYPE",
    "set",
  ]);
  expect(
    JSON.parse(Buffer.from(result.nextCursor!, "base64url").toString())
      .position,
  ).toBe("123");
  expect(result.health.lastSuccess).toBe("2026-09-22T00:00:00Z");
});

it("lists uncached pending strategies in a separate ZSCAN phase and never reads all members", async () => {
  const cached = "open:tenant:123",
    waiting = "open:other:456";
  const command = fixture([["0", [cached]]], { [cached]: 2 }, [
    cached,
    waiting,
  ]);
  const first = await scanStrategyPage(
    command,
    "open",
    "target",
    undefined,
    50,
    AbortSignal.timeout(1000),
  );
  const second = await scanStrategyPage(
    command,
    "open",
    "target",
    first.nextCursor!,
    50,
    AbortSignal.timeout(1000),
  );
  expect(first.rows.map((r) => r.key)).toEqual([cached]);
  expect(second.rows).toMatchObject([
    { tenantId: "other", strategyId: "456", members: 0, pending: true },
  ]);
  expect(second.nextCursor).toBeNull();
  expect(second.health.pendingCount).toBe(2);
  expect(
    command.mock.calls.some(([args]) =>
      ["KEYS", "SMEMBERS", "ZRANGEBYSCORE"].includes(args[0]),
    ),
  ).toBe(false);
});

it("limits empty scanning work and carries the last real cursor to the next request", async () => {
  const command = fixture(
    Array.from({ length: 9 }, (_, i) => [String(i + 1), []]),
  );
  const result = await scanStrategyPage(
    command,
    "open",
    "target",
    undefined,
    50,
    AbortSignal.timeout(1000),
  );
  expect(
    command.mock.calls.filter(([args]) => args[0] === "SCAN"),
  ).toHaveLength(8);
  expect(result.rows).toEqual([]);
  expect(
    JSON.parse(Buffer.from(result.nextCursor!, "base64url").toString())
      .position,
  ).toBe("8");
});

it("rejects a cursor from a different target before Redis reads", async () => {
  const command = fixture([["17", []]]);
  const cursor = Buffer.from(
    JSON.stringify({
      version: 1,
      target: "old",
      phase: "sets",
      position: "17",
    }),
  ).toString("base64url");
  await expect(
    scanStrategyPage(
      command,
      "open",
      "new",
      cursor,
      50,
      AbortSignal.timeout(1000),
    ),
  ).rejects.toThrow("cursor");
  expect(command).not.toHaveBeenCalled();
});

it("reads RESP3 score tuples in the pending summary", async () => {
  const key = "open:tenant:123",
    base = fixture([["0", []]], {}, [key]);
  const command = async (args: string[]) =>
    args[0] === "ZRANGE" ? [[key, 1000]] : base(args);
  const result = await scanStrategyPage(
    command,
    "open",
    "target",
    undefined,
    50,
    AbortSignal.timeout(1000),
  );
  expect(result.health.oldestDueAt).toBe("1970-01-01T00:00:01.000Z");
});

it("escapes Redis glob patterns and retains a local metadata failure without claiming zero members", async () => {
  const key = "open[*]:tenant:123",
    base = fixture([["0", [key]]]);
  const command = vi.fn(async (args: string[]) => {
    if (args[0] === "SCARD") throw new Error("private-error");
    return base(args);
  });
  const result = await scanStrategyPage(
    command,
    "open[*]",
    "target",
    undefined,
    50,
    AbortSignal.timeout(1000),
  );
  expect(command.mock.calls[0][0][3]).toBe("open\\[\\*\\]:*");
  expect(result.rows[0]).toMatchObject({
    members: null,
    error: "metadata_read_failed",
  });
  expect(JSON.stringify(result)).not.toContain("private-error");
});

it("rejects an oversized response instead of truncating it and skipping keys", async () => {
  const command = fixture([
    ["17", Array.from({ length: 1001 }, (_, i) => `open:tenant:${i}`)],
  ]);
  await expect(
    scanStrategyPage(
      command,
      "open",
      "target",
      undefined,
      50,
      AbortSignal.timeout(1000),
    ),
  ).rejects.toThrow("exceeds limits");
  expect(command.mock.calls).toHaveLength(1);
});

it("accepts bounded browsing without a tenant or strategy and rejects arbitrary Redis parameters", () => {
  expect(
    strategyBrowseQuerySchema.parse({
      event_source_id: "a",
      hook_name: "active",
    }).count,
  ).toBe(50);
  for (const extra of [
    { count: 201 },
    { count: 0 },
    { cursor: "x".repeat(2049) },
    { address: "arbitrary:6379" },
    { key: "other:*" },
  ])
    expect(
      strategyBrowseQuerySchema.safeParse({
        event_source_id: "a",
        hook_name: "active",
        ...extra,
      }).success,
    ).toBe(false);
});
