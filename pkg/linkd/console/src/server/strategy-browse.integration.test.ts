// @vitest-environment node
import { createHash, randomUUID } from "node:crypto";
import { createClient } from "redis";
import { expect, it } from "vitest";
import type { ConsoleConfig } from "./config.js";
import { readStrategyPage } from "./strategy-browse.js";

const address = process.env.LINKD_TEST_REDIS_ADDRESS;

it.skipIf(!address)(
  "paginates hundreds of real Redis strategies and uncached pending entries without loading member sets",
  async () => {
    const prefix = `linkd-browse-${randomUUID()}`;
    const root = `linkd:active-index:${createHash("sha256").update(prefix).digest("hex")}`;
    const redis = {
      mode: "standalone" as const,
      address: address!,
      database: 8,
      username: process.env.LINKD_TEST_REDIS_USERNAME,
      password: process.env.LINKD_TEST_REDIS_PASSWORD,
    } satisfies NonNullable<ConsoleConfig["redis"]>;
    const client = createClient({
      url: `redis://${address}`,
      database: 8,
      username: redis.username,
      password: redis.password,
    });
    client.on("error", () => undefined);
    const expected = Array.from(
      { length: 250 },
      (_, i) => `${prefix}:tenant-${i % 3}:${i}`,
    );
    const pending = Array.from(
      { length: 25 },
      (_, i) => `${prefix}:waiting:${i}`,
    );
    const foreign = `${prefix}-other:tenant:123`;
    const keys = [...expected, foreign, `${root}:pending`, `${root}:health`];
    try {
      await client.connect();
      const batch = client.multi();
      for (const key of expected) batch.sAdd(key, ["fp-a", "fp-b"]);
      batch.sAdd(foreign, "foreign");
      batch.zAdd(
        `${root}:pending`,
        [...pending, ...expected.slice(0, 5)].map((value) => ({
          value,
          score: Date.now(),
        })),
      );
      batch.hSet(`${root}:health`, {
        last_success: "2026-09-22T00:00:00Z",
        last_attempt: "2026-09-22T00:00:00Z",
        error: "",
      });
      await batch.exec();
      const found = new Map<string, number | null>();
      const signal = AbortSignal.timeout(20000);
      let cursor: string | undefined,
        pages = 0;
      do {
        const page = await readStrategyPage(
          redis,
          prefix,
          { target: "test" },
          cursor,
          10,
          signal,
          3000,
        );
        for (const row of page.rows) found.set(row.key, row.members);
        expect(page.health.pendingCount).toBe(30);
        expect(page.rows.length).toBeLessThanOrEqual(1000);
        cursor = page.nextCursor ?? undefined;
        pages++;
        if (pages > 200) throw new Error("scan did not terminate");
      } while (cursor);
      expect(pages).toBeGreaterThan(1);
      expect([...found.keys()].sort()).toEqual(
        [...expected, ...pending].sort(),
      );
      for (const key of expected) expect(found.get(key)).toBe(2);
      for (const key of pending) expect(found.get(key)).toBe(0);
      expect(found.has(foreign)).toBe(false);
    } finally {
      if (client.isOpen) {
        await client.unlink(keys);
        client.destroy();
      }
    }
  },
  30000,
);
