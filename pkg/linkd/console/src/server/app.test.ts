import { describe, expect, it } from "vitest";

import { createApp } from "./app.js";
import type { ConsoleConfig } from "./config.js";

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
} satisfies ConsoleConfig;

describe("local API", () => {
  it("requires explicit strategy scope and sends no-store for index reads", async () => {
    const app = await createApp(config);
    try {
      const targets = await app.inject("/local-api/strategy-index/targets");
      expect(targets.json()).toEqual([]);
      expect(targets.headers["cache-control"]).toBe("no-store");
      const invalid = await app.inject(
        "/local-api/strategy-index/reconcile?event_source_id=source&hook_name=active",
      );
      expect(invalid.statusCode).toBe(400);
      expect(invalid.body).not.toContain("secret-not-visible");
      const invalidBrowse = await app.inject(
        "/local-api/strategy-index/browse?event_source_id=source&hook_name=active&count=201",
      );
      expect(invalidBrowse.statusCode).toBe(400);
      expect(invalidBrowse.headers["cache-control"]).toBe("no-store");
      const unknownTarget = await app.inject(
        "/local-api/strategy-index/browse?event_source_id=source&hook_name=active",
      );
      expect(unknownTarget.statusCode).toBe(400);
    } finally {
      await app.close();
    }
  });
  it("mounts all API routes under the configured path and keeps authentication", async () => {
    const prefix = "/apps/linkd";
    const app = await createApp({
      ...config,
      server: {
        ...config.server,
        basePath: prefix,
        access: {
          mode: "server",
          basicAuth: { username: "admin", password: "test-password" },
        },
      },
    });
    const headers = {
      authorization: `Basic ${Buffer.from("admin:test-password").toString("base64")}`,
    };
    try {
      for (const route of [
        "/local-api/version",
        "/local-api/capabilities",
        "/local-api/config",
        "/local-api/strategy-index/targets",
        "/local-api/strategy-index/audits",
      ]) {
        expect((await app.inject(prefix + route)).statusCode).toBe(401);
        expect(
          (await app.inject({ url: prefix + route, headers })).statusCode,
        ).toBe(200);
        expect((await app.inject({ url: route, headers })).statusCode).toBe(
          404,
        );
      }
      const browseURL =
        prefix +
        "/local-api/strategy-index/browse?event_source_id=source&hook_name=active";
      expect((await app.inject(browseURL)).statusCode).toBe(401);
      expect((await app.inject({ url: browseURL, headers })).statusCode).toBe(
        400,
      );
      const auditURL = prefix + "/local-api/strategy-index/audits";
      expect(
        (
          await app.inject({
            method: "POST",
            url: auditURL,
            payload: { event_source_id: "source", hook_name: "active" },
          })
        ).statusCode,
      ).toBe(401);
      expect(
        (
          await app.inject({
            method: "POST",
            url: auditURL,
            headers,
            payload: {
              event_source_id: "source",
              hook_name: "active",
              address: "arbitrary",
            },
          })
        ).statusCode,
      ).toBe(400);
      // 正式来源管理路由也必须经过前缀和认证，不能落到 SPA fallback。
      const source = await app.inject({
        url: prefix + "/local-api/event-sources",
        headers,
      });
      expect(source.statusCode).not.toBe(404);
      expect(source.headers["content-type"]).toContain("application/json");
      expect(
        (await app.inject({ url: prefix + "-other/local-api/config", headers }))
          .statusCode,
      ).toBe(404);
    } finally {
      await app.close();
    }
  });
  it("exposes Console build metadata separately from capabilities schema version", async () => {
    const app = await createApp(config);
    const response = await app.inject({
      method: "GET",
      url: "/local-api/version",
    });
    await app.close();
    expect(response.statusCode).toBe(200);
    expect(response.json()).toEqual({ version: "dev", git_commit: "unknown" });
  });
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

  it("validates the chart calculation window", async () => {
    const app = await createApp(config);
    const baseQuery =
      "from=2026-09-04T00%3A00%3A00.000Z&to=2026-09-04T01%3A00%3A00.000Z&step=15";
    const accepted = await app.inject({
      method: "GET",
      url: `/local-api/metrics?${baseQuery}&calculation_window_seconds=60`,
    });
    const rejected = await app.inject({
      method: "GET",
      url: `/local-api/metrics?${baseQuery}&calculation_window_seconds=14`,
    });
    await app.close();

    expect(accepted.statusCode).toBe(200);
    expect(rejected.statusCode).toBe(400);
    expect(rejected.json().error.code).toBe("invalid_argument");
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

  it("requires the control-plane API instead of inferring tasks from Console YAML", async () => {
    const app = await createApp(config);
    try {
      const response = await app.inject("/local-api/runtime/control-plane");
      expect(response.statusCode).toBe(503);
      expect(response.json().tasks).toBeUndefined();
    } finally {
      await app.close();
    }
  });
});
