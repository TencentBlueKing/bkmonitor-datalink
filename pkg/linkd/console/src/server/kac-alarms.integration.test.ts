// @vitest-environment node
import { readFile } from "node:fs/promises";
import { expect, it } from "vitest";
import { createKACAlarmConnector } from "./elasticsearch.js";
import type { ConsoleConfig } from "./config.js";

const endpoint = process.env.LINKD_TEST_ELASTICSEARCH_URL;

// 仅显式启用时创建本次测试的独立索引，finally 只删除确已创建的索引。
it.skipIf(!endpoint)(
  "queries real KAC mapping across indices, tenants and Shanghai date boundaries",
  async () => {
    const alias = `linkd-console-kac-${process.pid}-${Date.now()}`;
    const created: string[] = [];
    async function request(path: string, method: string, body?: unknown) {
      const response = await fetch(`${endpoint}${path}`, {
        method,
        headers: { "content-type": "application/json" },
        body: body === undefined ? undefined : JSON.stringify(body),
        signal: AbortSignal.timeout(15000),
      });
      if (!response.ok)
        throw new Error(`KAC integration ${method} failed: ${response.status}`);
      return response.json();
    }
    try {
      const mappings = JSON.parse(
        await readFile(
          new URL(
            "../../../internal/kaccompat/alarm_event_mapping.json",
            import.meta.url,
          ),
          "utf8",
        ),
      );
      const settings = {
        number_of_shards: 1,
        number_of_replicas: 0,
        analysis: {
          analyzer: {
            split_by_whitespace_analyzer: {
              type: "custom",
              char_filter: ["split_by_whitespace_analyzer"],
              tokenizer: "whitespace",
              filter: ["lowercase"],
            },
          },
          char_filter: {
            split_by_whitespace_analyzer: {
              type: "pattern_replace",
              pattern: "(.+?)",
              replacement: "$1 ",
            },
          },
        },
      };
      for (const suffix of ["a", "b"]) {
        const index = `${alias}-${suffix}`;
        await request(`/${index}`, "PUT", {
          settings,
          mappings,
          aliases: { [alias]: {} },
        });
        created.push(index);
      }
      const docs = [
        {
          bk_tenant_id: "tenant-a",
          alarm_id: "old",
          alarm_time: "2026-10-09 00:29:59",
        },
        {
          bk_tenant_id: "tenant-a",
          alarm_id: "shared",
          alarm_time: "2026-10-09 00:30:00",
        },
        {
          bk_tenant_id: "tenant-a",
          alarm_id: "next",
          alarm_time: "2026-10-09 01:30:00",
        },
        {
          bk_tenant_id: "tenant-b",
          alarm_id: "shared",
          alarm_time: "2026-10-09 00:30:00",
        },
      ];
      for (const [i, doc] of docs.entries())
        await request(`/${created[i % 2]}/_doc/${i}?refresh=wait_for`, "PUT", {
          ...doc,
          name: "真实 KAC 文档",
          status: "executing",
          source_id: "source",
          level: "critical",
          action: "firing",
          event_id: "event",
          storage_time: doc.alarm_time,
        });
      const cfg: ConsoleConfig = {
        server: { host: "127.0.0.1", port: 4399 },
        query: {
          defaultRangeSeconds: 3600,
          maxRangeSeconds: 604800,
          defaultLimit: 50,
          maxLimit: 200,
          timeoutMilliseconds: 15000,
        },
        entities: { events: "mysql", alerts: "mysql", alertLogs: "mysql" },
        plugins: {
          kac: {
            enabled: false,
            alarm_event_index: alias,
            elasticsearch: { addresses: [endpoint!] },
          },
        },
      };
      const connector = createKACAlarmConnector(cfg)!;
      const query = {
        tenantId: "tenant-a",
        from: "2026-10-08T16:30:00Z",
        to: "2026-10-08T17:30:00Z",
        sourceId: "source",
        status: "executing",
        level: "critical",
        action: "firing",
        eventId: "event",
        limit: 1,
        order: "asc" as const,
      };
      const first = await connector.search("kac-alarms", query);
      expect(first.items.map((item) => item.id)).toEqual(["shared"]);
      expect(first.items[0].timestamp).toBe("2026-10-08T16:30:00.000Z");
      expect(first.items[0].payload.alarm_time).toBe("2026-10-09 00:30:00");
      expect(first.nextCursor).toBeDefined();
      const next = await connector.search("kac-alarms", {
        ...query,
        cursor: first.nextCursor,
      });
      expect(next.items.map((item) => item.id)).toEqual(["next"]);
      expect(next.nextCursor).toBeUndefined();
      const stats = await connector.stats("kac-alarms", query);
      expect(stats.total).toBe(2);
      expect(
        stats.timeline
          .filter((bucket) => bucket.count)
          .map((bucket) => bucket.timestamp),
      ).toEqual(["2026-10-08T16:30:00.000Z", "2026-10-08T17:30:00.000Z"]);
      expect(
        (await connector.detail("kac-alarms", "tenant-b", "shared"))?.tenantId,
      ).toBe("tenant-b");
      expect(
        await connector.detail("kac-alarms", "tenant-c", "shared"),
      ).toBeUndefined();
    } finally {
      for (const index of created) await request(`/${index}`, "DELETE");
    }
  },
  60000,
);
