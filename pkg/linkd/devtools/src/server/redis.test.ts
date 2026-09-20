import { describe, expect, it } from "vitest";

import {
  normalizeMap,
  normalizePendingEntries,
  normalizeStreamCounters,
  parseRedisInfo,
} from "./redis.js";

describe("Redis read-only response normalization", () => {
  it("accepts RESP3 objects and RESP2 alternating maps", () => {
    expect(
      normalizeMap({ name: "linkd-lifecycle", pending: 3, lag: null }),
    ).toEqual({ name: "linkd-lifecycle", pending: 3, lag: null });
    expect(normalizeMap(["name", "linkd-lifecycle", "pending", 3])).toEqual({
      name: "linkd-lifecycle",
      pending: 3,
    });
  });

  it("only parses INFO key-value rows and ignores headings", () => {
    expect(
      parseRedisInfo(
        "# Memory\r\nused_memory:4096\r\nmaxmemory_policy:noeviction\r\n\r\n",
      ),
    ).toEqual({ used_memory: "4096", maxmemory_policy: "noeviction" });
  });

  it("preserves unknown Stream counters instead of presenting zero", () => {
    expect(normalizeStreamCounters({}, 100)).toEqual({
      length: null,
      entriesAdded: null,
      groupsCount: null,
      entriesAboveMax: null,
    });
    expect(
      normalizeStreamCounters(
        ["length", 120, "entries-added", 180, "groups", 2],
        100,
      ),
    ).toEqual({
      length: 120,
      entriesAdded: 180,
      groupsCount: 2,
      entriesAboveMax: 20,
    });
  });

  it("normalizes XPENDING rows without exposing Stream payload", () => {
    expect(
      normalizePendingEntries([["1710000000000-0", "consumer-a", 4500, 2]]),
    ).toEqual([
      {
        id: "1710000000000-0",
        consumer: "consumer-a",
        idleMilliseconds: 4500,
        deliveryCount: 2,
      },
    ]);
  });
});
