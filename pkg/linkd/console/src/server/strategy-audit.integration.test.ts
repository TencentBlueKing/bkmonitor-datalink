// @vitest-environment node
import { randomUUID } from "node:crypto";
import { createClient } from "redis";
import { expect, it } from "vitest";
import { StrategyAuditJobs, type AuditTarget } from "./strategy-audit.js";
import type {
  StrategyAlertReader,
  StrategyAlertRow,
} from "./strategy-alerts.js";

const address = process.env.LINKD_TEST_REDIS_ADDRESS;
it.skipIf(!address)(
  "audits a Redis strategy beyond the interactive 5000-member cap and finds entirely missing sets",
  async () => {
    const prefix = `linkd-audit-${randomUUID()}`,
      key = `${prefix}:tenant:123`;
    const redis = {
      mode: "standalone" as const,
      address: address!,
      database: 8,
      username: process.env.LINKD_TEST_REDIS_USERNAME,
      password: process.env.LINKD_TEST_REDIS_PASSWORD,
    };
    const c = createClient({
      url: `redis://${address}`,
      database: 8,
      username: redis.username,
      password: redis.password,
    });
    c.on("error", () => undefined);
    const scope: AuditTarget = {
      target: {
        eventSourceId: "source",
        hookName: "active",
        keyPrefix: prefix,
        address: address!,
        database: 8,
        sources: ["source"],
      },
      redis,
      identity: "test",
    };
    const rows: StrategyAlertRow[] = Array.from({ length: 6000 }, (_, i) => ({
      bk_tenant_id: "tenant",
      alert_id: `a-${i}`,
      event_source_id: "source",
      fingerprint: `fp-${i}`,
      status: "active",
      labels: { strategy_id: "123" },
    }));
    rows.push({
      ...rows[0],
      alert_id: "db-only",
      fingerprint: "missing",
      labels: { strategy_id: "db-only" },
    });
    const reader: StrategyAlertReader = {
      readStrategyAlerts: async () => [],
      async *scanActiveStrategyAlerts() {
        for (let start = 0; start < rows.length; start += 1000)
          yield rows.slice(start, start + 1000);
      },
    };
    const jobs = new StrategyAuditJobs(async () => scope, reader, 3000);
    const corruptKey = `${prefix}:tenant:corrupt`;
    try {
      await c.connect();
      await c.sAdd(key, [
        ...rows.slice(0, 6000).map((r) => r.fingerprint),
        "orphan",
      ]);
      const started = await jobs.start({
        event_source_id: "source",
        hook_name: "active",
      });
      let result = jobs.get(started.id);
      const deadline = Date.now() + 20000;
      while (result.status === "running" && Date.now() < deadline) {
        await new Promise((r) => setTimeout(r, 10));
        result = jobs.get(started.id);
      }
      expect(result).toMatchObject({
        status: "completed",
        scannedAlerts: 6001,
        checkedStrategies: 2,
        redisMembers: 6001,
        matched: 6000,
        missing: 1,
        extra: 1,
        incompleteStrategies: 0,
      });
      expect(result.differences).toContainEqual({
        tenantId: "tenant",
        strategyId: "db-only",
        fingerprint: "missing",
        status: "missing_redis",
      });
      expect(await c.sCard(key)).toBe(6001);
      await c.set(corruptKey, "wrong-type");
      const next = await jobs.start({
        event_source_id: "source",
        hook_name: "active",
      });
      let partial = jobs.get(next.id);
      while (partial.status === "running" && Date.now() < deadline) {
        await new Promise((r) => setTimeout(r, 10));
        partial = jobs.get(next.id);
      }
      expect(partial.status).toBe("incomplete");
      expect(partial.differences).toContainEqual({
        tenantId: "tenant",
        strategyId: "corrupt",
        fingerprint: null,
        status: "unknown",
      });
    } finally {
      await jobs.close();
      if (c.isOpen) {
        await c.unlink([key, corruptKey]);
        c.destroy();
      }
    }
  },
  30000,
);
