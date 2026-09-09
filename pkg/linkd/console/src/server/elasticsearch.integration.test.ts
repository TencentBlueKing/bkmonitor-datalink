// @vitest-environment node
import { expect, it } from "vitest";

import type { ConsoleConfig } from "./config.js";
import { ElasticsearchConnector } from "./elasticsearch.js";

const endpoint = process.env.LINKD_TEST_ELASTICSEARCH_URL;

// 显式启用的真实 ES 协议测试；普通单测不连接开发者本机服务。
it.skipIf(!endpoint)(
  "Elasticsearch explorer version contract",
  async () => {
    const prefix = `linkd-console-compat-${process.pid}-${Date.now()}`;
    const created: string[] = [];
    async function request(path: string, method = "GET", body?: unknown) {
      const response = await fetch(`${endpoint}${path}`, {
        method,
        headers: { "content-type": "application/json" },
        body: body === undefined ? undefined : JSON.stringify(body),
        signal: AbortSignal.timeout(20000),
      });
      const result = await response.json();
      if (!response.ok)
        throw new Error(
          `${method} ${path}: ${response.status} ${JSON.stringify(result)}`,
        );
      return result;
    }
    try {
      const info = (await request("/")) as { version: { number: string } };
      console.log(`Elasticsearch explorer contract: ${info.version.number}`);
      const cfg = {
        server: { host: "127.0.0.1", port: 4399 },
        query: {
          defaultRangeSeconds: 3600,
          maxRangeSeconds: 604800,
          defaultLimit: 50,
          maxLimit: 200,
          timeoutMilliseconds: 20000,
        },
        elasticsearch: {
          baseUrl: endpoint!,
          auth: {},
          eventTargets: [`${prefix}-events`],
          alertTargets: [`${prefix}-alerts`],
          alertLogTargets: [`${prefix}-alert-logs`],
        },
        entities: {
          events: "elasticsearch",
          alerts: "elasticsearch",
          alertLogs: "elasticsearch",
        },
      } satisfies ConsoleConfig;
      const connector = new ElasticsearchConnector(cfg);
      for (const entity of ["events", "alerts", "alert-logs"] as const) {
        const idField =
          entity === "events"
            ? "event_id"
            : entity === "alerts"
              ? "alert_id"
              : "log_id";
        const timeField =
          entity === "events"
            ? "received_at"
            : entity === "alerts"
              ? "update_at"
              : "created_time";
        const alias = `${prefix}-${entity}`;
        for (const suffix of ["a", "b", "empty"]) {
          const index = `${alias}-${suffix}`;
          await request(`/${index}`, "PUT", {
            settings: { number_of_shards: 2, number_of_replicas: 0 },
            aliases: { [alias]: {} },
            mappings: {
              properties: {
                bk_tenant_id: { type: "keyword" },
                [idField]: { type: "keyword" },
                [timeField]: { type: "date_nanos" },
                event_source_id: { type: "keyword" },
                status: { type: "keyword" },
                processing: { properties: { state: { type: "keyword" } } },
                operation_kind: { type: "keyword" },
                operator_kind: { type: "keyword" },
              },
            },
          });
          created.push(index);
        }
        for (let i = 0; i < 4; i++) {
          await request(
            `/${alias}-${i % 2 ? "b" : "a"}/_doc/${i}?refresh=wait_for`,
            "PUT",
            {
              bk_tenant_id: i === 3 ? "other" : "system",
              [idField]: `id-${i}`,
              [timeField]: `2026-09-09T14:55:47.12100000${i}Z`,
              event_source_id: "test",
              status: "active",
              processing: { state: "accepted" },
              operation_kind: "create",
              operator_kind: "system",
            },
          );
        }
        const ids: string[] = [];
        let cursor: string | undefined;
        for (let page = 0; page < 6; page++) {
          const result = await connector.search(entity, { limit: 1, cursor });
          ids.push(...result.items.map((item) => item.id));
          cursor = result.nextCursor;
          if (!cursor) break;
        }
        expect(cursor).toBeUndefined();
        expect(ids).toEqual(["id-3", "id-2", "id-1", "id-0"]);
        expect((await connector.stats(entity, { limit: 1 })).total).toBe(4);
        expect((await connector.detail(entity, "system", "id-0"))?.id).toBe(
          "id-0",
        );
        expect(
          (await connector.search(entity, { limit: 1, tenantId: "missing" }))
            .items,
        ).toEqual([]);
      }
    } finally {
      for (const index of created) await request(`/${index}`, "DELETE");
    }
  },
  120000,
);
