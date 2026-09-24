import { afterEach, describe, it, expect, vi } from "vitest";
import { createApp } from "./app.js";
import type { ConsoleConfig } from "./config.js";
import { controlPlaneFixture } from "../test-fixtures/control-plane.js";
const config: ConsoleConfig = {
  server: {
    host: "127.0.0.1",
    port: 4399,
    basePath: "/kingeye-linkd",
    access: {
      mode: "server",
      basicAuth: { username: "admin", password: "console-pass" },
    },
  },
  query: {
    defaultRangeSeconds: 3600,
    maxRangeSeconds: 86400,
    defaultLimit: 50,
    maxLimit: 200,
    timeoutMilliseconds: 1000,
  },
  entities: { alerts: "mysql", events: "mysql", alertLogs: "mysql" },
  dispatch: {
    url: "http://control-plane:8090",
    apiToken: "private-token",
    deployment: "test",
  },
};
afterEach(() => vi.unstubAllGlobals());
const headers = {
  authorization: `Basic ${Buffer.from("admin:console-pass").toString("base64")}`,
};
describe("control-plane runtime proxy", () => {
  it("uses the authoritative catalog, token, subpath and no-store without needing telemetry", async () => {
    const catalog = controlPlaneFixture();
    catalog.tasks[5].enabled = false;
    catalog.tasks[5].disabledReason = "显式关闭";
    const fetch = vi.fn(
      async () => new Response(JSON.stringify(catalog), { status: 200 }),
    );
    vi.stubGlobal("fetch", fetch);
    const app = await createApp(config);
    try {
      expect(
        (await app.inject("/kingeye-linkd/local-api/runtime/control-plane"))
          .statusCode,
      ).toBe(401);
      const r = await app.inject({
        url: "/kingeye-linkd/local-api/runtime/control-plane",
        headers,
      });
      expect(r.statusCode).toBe(200);
      expect(r.headers["cache-control"]).toBe("no-store");
      expect(r.json().tasks).toHaveLength(8);
      expect(r.json().tasks[5].enabled).toBe(false);
      expect(r.body).not.toContain("private-token");
      expect(fetch).toHaveBeenCalledWith(
        "http://control-plane:8090/api/v1/control-plane/tasks",
        expect.objectContaining({
          headers: { Authorization: "Bearer private-token" },
        }),
      );
    } finally {
      await app.close();
    }
  });
  it.each(["upstream", "invalid", "network"])(
    "refuses fabricated empty tasks on %s failures",
    async (mode) => {
      vi.stubGlobal(
        "fetch",
        vi.fn(async () => {
          if (mode === "network") throw new Error("private-token");
          return new Response(JSON.stringify({ tasks: [] }), {
            status: mode === "upstream" ? 500 : 200,
          });
        }),
      );
      const app = await createApp(config);
      try {
        const r = await app.inject({
          url: "/kingeye-linkd/local-api/runtime/control-plane",
          headers,
        });
        expect(r.statusCode).toBe(502);
        expect(r.body).not.toContain("private-token");
        expect(r.json().tasks).toBeUndefined();
      } finally {
        await app.close();
      }
    },
  );
});
