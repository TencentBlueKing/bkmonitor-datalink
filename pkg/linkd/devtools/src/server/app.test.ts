import { describe, expect, it } from "vitest";

import { createApp } from "./app.js";
import type { DevtoolsConfig } from "./config.js";

const config = {
  server: { host: "127.0.0.1", port: 4399 },
  query: {
    defaultRangeSeconds: 3600,
    maxRangeSeconds: 604800,
    defaultLimit: 50,
    maxLimit: 200,
    timeoutMilliseconds: 5000,
  },
  mysql: {
    host: "127.0.0.1",
    port: 3306,
    database: "linkd",
    username: "reader",
    password: "secret-not-visible",
    connectionLimit: 1,
  },
  entities: { alerts: "mysql", events: "mysql", alertLogs: "mysql" },
} satisfies DevtoolsConfig;

describe("local API", () => {
  it("returns capabilities without credentials", async () => {
    const app = await createApp(config);
    const response = await app.inject({
      method: "GET",
      url: "/local-api/capabilities",
    });
    await app.close();
    expect(response.statusCode).toBe(200);
    expect(response.body).not.toContain("secret-not-visible");
    expect(response.json().entities.alerts.source).toBe("mysql");
  });

  it("returns a redacted Linkd configuration summary", async () => {
    const app = await createApp(config);
    const response = await app.inject({
      method: "GET",
      url: "/local-api/config",
    });
    await app.close();
    expect(response.statusCode).toBe(200);
    expect(response.body).not.toContain("secret-not-visible");
    expect(response.json().repository).toBe("mysql");
    expect(response.json().storage.mysql.password).toBe("******");
  });

  it("requires tenant scope for details", async () => {
    const app = await createApp(config);
    const response = await app.inject({
      method: "GET",
      url: "/local-api/events/event-a",
    });
    await app.close();
    expect(response.statusCode).toBe(400);
    expect(response.json().error.code).toBe("invalid_argument");
  });

  it("bounds Redis detail queries before issuing storage commands", async () => {
    const app = await createApp(config);
    const response = await app.inject({
      method: "GET",
      url: "/local-api/infrastructure/redis/pending?limit=101",
    });
    await app.close();
    expect(response.statusCode).toBe(400);
    expect(response.json().error.code).toBe("invalid_argument");
  });

  it("returns an explicit unavailable Redis shape when it is not configured", async () => {
    const app = await createApp(config);
    const response = await app.inject({
      method: "GET",
      url: "/local-api/infrastructure/redis",
    });
    await app.close();
    expect(response.statusCode).toBe(200);
    expect(response.json()).toMatchObject({
      status: "unavailable",
      connection: { status: "unavailable" },
      signalQueue: { status: "unavailable", groups: [] },
    });
  });

  it("returns the four control-plane tasks even when they are disabled", async () => {
    const app = await createApp(config);
    const response = await app.inject({
      method: "GET",
      url: "/local-api/runtime/control-plane?range_seconds=3600",
    });
    await app.close();
    expect(response.statusCode).toBe(200);
    expect(response.json().tasks).toEqual([
      expect.objectContaining({
        id: "elasticsearch-schema-and-active-reconciler",
        enabled: false,
      }),
      expect.objectContaining({
        id: "elasticsearch-bucket-manager",
        dependsOn: ["elasticsearch-schema-and-active-reconciler"],
      }),
      expect.objectContaining({
        id: "elasticsearch-alert-archiver",
        dependsOn: ["elasticsearch-bucket-manager"],
      }),
      expect.objectContaining({
        id: "redis-stream-manager",
        enabled: false,
      }),
    ]);
  });

  it("enables Elasticsearch tasks from the repository and uses the fixed backlog query", async () => {
    const requests: string[] = [];
    const originalFetch = globalThis.fetch;
    globalThis.fetch = async (input) => {
      requests.push(String(input));
      return new Response(JSON.stringify({ count: 12 }), {
        status: 200,
        headers: { "content-type": "application/json" },
      });
    };
    const elasticsearchConfig: DevtoolsConfig = {
      ...config,
      mysql: undefined,
      elasticsearch: {
        baseUrl: "http://elasticsearch:9200",
        auth: {},
        eventTargets: ["linkd-events"],
        alertTargets: ["linkd-alerts"],
        alertLogTargets: ["linkd-alert-logs"],
        indexPrefix: "linkd",
      },
      entities: {
        events: "elasticsearch",
        alerts: "elasticsearch",
        alertLogs: "elasticsearch",
      },
    };
    try {
      const app = await createApp(elasticsearchConfig);
      const response = await app.inject({
        method: "GET",
        url: "/local-api/runtime/control-plane?range_seconds=3600",
      });
      await app.close();
      expect(response.statusCode).toBe(200);
      expect(response.json().archive.backlog).toBe(12);
      expect(
        response
          .json()
          .tasks.filter((task: { id: string }) =>
            task.id.startsWith("elasticsearch-"),
          )
          .every((task: { enabled: boolean }) => task.enabled),
      ).toBe(true);
      expect(requests.some((url) => url.includes("alerts-active/_count"))).toBe(
        true,
      );
    } finally {
      globalThis.fetch = originalFetch;
    }
  });
});
