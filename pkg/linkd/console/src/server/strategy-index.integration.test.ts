// @vitest-environment node
import { randomUUID } from "node:crypto";
import { createClient } from "redis";
import { expect, it } from "vitest";
import { normalizeEventSources, type ConsoleConfig } from "./config.js";
import { ElasticsearchConnector } from "./elasticsearch.js";
import { StrategyIndexConnector } from "./strategy-index.js";

const es = process.env.LINKD_TEST_ELASTICSEARCH_URL;
const redisAddress = process.env.LINKD_TEST_REDIS_ADDRESS;

// 独立 key 和索引，只有显式提供测试连接时才创建；finally 只清理本次资源。
it.skipIf(!es || !redisAddress)(
  "reconciles real Redis sets against current ES alerts",
  async () => {
    const prefix = `linkd-strategy-test-${randomUUID()}`;
    const index = prefix + "-alerts-active";
    const redis = createClient({
      url: `redis://${redisAddress}`,
      username: process.env.LINKD_TEST_REDIS_USERNAME,
      password: process.env.LINKD_TEST_REDIS_PASSWORD,
      socket: { connectTimeout: 3000, reconnectStrategy: false },
    });
    redis.on("error", () => undefined);
    const key = prefix + ":tenant:123";
    const otherKey = prefix + ":other:123";
    let created = false;
    async function request(path: string, method: string, body?: unknown) {
      const r = await fetch(`${es}/${path}`, {
        method,
        headers: { "content-type": "application/json" },
        body: body === undefined ? undefined : JSON.stringify(body),
        signal: AbortSignal.timeout(5000),
      });
      if (!r.ok) throw new Error(`ES fixture request failed: ${r.status}`);
    }
    try {
      await redis.connect();
      await request(index, "PUT", {
        settings: { number_of_shards: 1, number_of_replicas: 0 },
        mappings: {
          properties: {
            bk_tenant_id: { type: "keyword" },
            event_source_id: { type: "keyword" },
            alert_id: { type: "keyword" },
            fingerprint: { type: "keyword" },
            status: { type: "keyword" },
            labels: { type: "flattened" },
          },
        },
      });
      created = true;
      for (const [id, fp, source, tenant, status] of [
        ["1", "both", "a", "tenant", "active"],
        ["2", "missing", "b", "tenant", "active"],
        ["3", "ended", "a", "tenant", "closed"],
        ["4", "other", "a", "other", "active"],
      ]) {
        await request(`${index}/_doc/${id}?refresh=wait_for`, "PUT", {
          alert_id: id,
          fingerprint: fp,
          event_source_id: source,
          bk_tenant_id: tenant,
          status,
          labels: { strategy_id: id === "1" ? "123" : 123 },
          update_at: "2020-01-01T00:00:00Z",
        });
      }
      await redis.sAdd(key, ["both", "extra", "ended"]);
      await redis.sAdd(otherKey, ["other"]);
      const redisConfig = {
        mode: "standalone" as const,
        address: redisAddress!,
        database: 0,
        username: process.env.LINKD_TEST_REDIS_USERNAME,
        password: process.env.LINKD_TEST_REDIS_PASSWORD,
      };
      const config = {
        server: { host: "127.0.0.1", port: 4399 },
        entities: {
          alerts: "elasticsearch",
          events: "elasticsearch",
          alertLogs: "elasticsearch",
        },
        query: {
          timeoutMilliseconds: 5000,
          defaultRangeSeconds: 3600,
          maxRangeSeconds: 86400,
          defaultLimit: 50,
          maxLimit: 200,
        },
        elasticsearch: {
          baseUrl: es!,
          indexPrefix: prefix,
          auth: {},
          alertTargets: [index],
          eventTargets: [],
          alertLogTargets: [],
        },
        eventSources: normalizeEventSources(
          ["a", "b"].map((id) => ({
            event_source_id: id,
            enabled: true,
            storage: {
              type: "kafka",
              kafka: {
                brokers: ["unused:9092"],
                topic: "unused",
                consumer_group: "unused",
              },
            },
            hooks: [
              {
                name: "active",
                type: "active-alert-by-strategy",
                config: { redis: redisConfig, key_prefix: prefix },
              },
            ],
          })),
          "/tmp",
        ),
      } satisfies ConsoleConfig;
      const scanned = [];
      for await (const page of new ElasticsearchConnector(
        config,
      ).scanActiveStrategyAlerts(["a", "b"], AbortSignal.timeout(5000)))
        scanned.push(...page);
      expect(scanned.map((row) => row.alert_id).sort()).toEqual([
        "1",
        "2",
        "4",
      ]);
      const c = new StrategyIndexConnector(
        config,
        new ElasticsearchConnector(config),
      );
      const query = {
        event_source_id: "a",
        hook_name: "active",
        bk_tenant_id: "tenant",
        strategy_id: "123",
      };
      const result = await c.inspect(query);
      expect(result.complete).toBe(true);
      expect(result.rows.map((r) => [r.fingerprint, r.status])).toEqual([
        ["both", "matched"],
        ["ended", "redis_only"],
        ["extra", "redis_only"],
        ["missing", "missing_redis"],
      ]);
      expect((await redis.sMembers(key)).sort()).toEqual([
        "both",
        "ended",
        "extra",
      ]);
      expect(await redis.sMembers(otherKey)).toEqual(["other"]);
      for (const [id, value] of [
        ["large-number", 1e21],
        ["exponent-string", "1e+21"],
      ]) {
        await request(`${index}/_doc/${id}?refresh=wait_for`, "PUT", {
          alert_id: id,
          fingerprint: id,
          event_source_id: "a",
          bk_tenant_id: "tenant",
          status: "active",
          labels: { strategy_id: value },
        });
      }
      const large = await c.inspect({
        ...query,
        strategy_id: "1000000000000000000000",
      });
      expect(large.rows.map((row) => row.fingerprint)).toEqual([
        "large-number",
      ]);
      const operations =
        Array.from({ length: 1005 }, (_, i) => [
          JSON.stringify({ index: { _index: index, _id: `page-${i}` } }),
          JSON.stringify({
            alert_id: `page-${String(i).padStart(5, "0")}`,
            fingerprint: `fp-${i}`,
            event_source_id: "paged",
            bk_tenant_id: "tenant",
            status: "active",
            labels: { strategy_id: "bulk" },
          }),
        ])
          .flat()
          .join("\n") + "\n";
      const bulk = await fetch(`${es}/_bulk?refresh=wait_for`, {
        method: "POST",
        headers: { "content-type": "application/x-ndjson" },
        body: operations,
        signal: AbortSignal.timeout(10000),
      });
      expect(bulk.ok).toBe(true);
      expect(await bulk.json()).toMatchObject({ errors: false });
      const pages = [];
      for await (const page of new ElasticsearchConnector(
        config,
      ).scanActiveStrategyAlerts(["paged"], AbortSignal.timeout(5000)))
        pages.push(page);
      expect(pages.map((page) => page.length)).toEqual([1000, 5]);
      expect(new Set(pages.flat().map((row) => row.alert_id)).size).toBe(1005);
      await redis.del(key);
      await redis.set(key, "wrong-type");
      const failed = await c.inspect(query);
      expect(failed.complete).toBe(false);
      expect(failed.rows.every((r) => r.status === "unknown")).toBe(true);
    } finally {
      if (redis.isReady) await redis.del([key, otherKey]);
      if (redis.isOpen) redis.destroy();
      if (created) await request(index, "DELETE");
    }
  },
  30000,
);
