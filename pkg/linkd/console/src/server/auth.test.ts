import { afterEach, describe, expect, it, vi } from "vitest";
import { loadServerAccess, validateServerAccess } from "./auth.js";
import { createApp } from "./app.js";
import type { ConsoleConfig } from "./config.js";

const credentials = { username: "operator", password: "test-only:secret" };
const authorization =
  "Basic " +
  Buffer.from(credentials.username + ":" + credentials.password).toString(
    "base64",
  );
const config: ConsoleConfig = {
  server: {
    host: "0.0.0.0",
    port: 4399,
    access: { mode: "server", basicAuth: credentials },
  },
  dispatch: {
    url: "http://control-plane:8090",
    apiToken: "upstream-test-token",
    deployment: "default",
  },
  query: {
    defaultRangeSeconds: 3600,
    maxRangeSeconds: 604800,
    defaultLimit: 50,
    maxLimit: 200,
    timeoutMilliseconds: 100,
  },
  entities: { alerts: "mysql", events: "mysql", alertLogs: "mysql" },
};
afterEach(() => vi.unstubAllGlobals());

describe("server access configuration", () => {
  it("preserves local loopback defaults", () => {
    expect(loadServerAccess({})).toEqual({ mode: "local" });
    expect(() =>
      validateServerAccess("127.0.0.1", { mode: "local" }),
    ).not.toThrow();
    expect(() => validateServerAccess("0.0.0.0", { mode: "local" })).toThrow(
      "loopback",
    );
  });
  it.each([
    { LINKD_CONSOLE_MODE: "invalid" },
    { LINKD_CONSOLE_MODE: "server" },
    { LINKD_CONSOLE_BASIC_AUTH_ENABLED: "yes" },
    { LINKD_CONSOLE_BASIC_AUTH_ENABLED: "true" },
    {
      LINKD_CONSOLE_BASIC_AUTH_ENABLED: "true",
      LINKD_CONSOLE_BASIC_AUTH_USERNAME: "bad:user",
      LINKD_CONSOLE_BASIC_AUTH_PASSWORD: "secret",
    },
  ])("rejects incomplete or unsafe access configuration", (env) => {
    expect(() => loadServerAccess(env)).toThrow();
  });
  it("allows explicitly authenticated server mode", () => {
    const access = loadServerAccess({
      LINKD_CONSOLE_MODE: "server",
      LINKD_CONSOLE_BASIC_AUTH_ENABLED: "true",
      LINKD_CONSOLE_BASIC_AUTH_USERNAME: credentials.username,
      LINKD_CONSOLE_BASIC_AUTH_PASSWORD: credentials.password,
    });
    expect(() => validateServerAccess("0.0.0.0", access)).not.toThrow();
  });
});

describe("Basic Auth boundary", () => {
  it.each([
    undefined,
    "Bearer upstream-test-token",
    "Basic ???",
    "Basic dXNlcg==",
    "Basic dXNlcg=",
    "Basic " + Buffer.from("operator:wrong").toString("base64"),
  ])(
    "rejects absent or malformed authorization before every route",
    async (header) => {
      const fetch = vi.fn();
      vi.stubGlobal("fetch", fetch);
      const app = await createApp(config);
      try {
        for (const url of [
          "/",
          "/assets/example.js",
          "/config",
          "/local-api/capabilities",
          "/local-api/version",
          "/local-api/unknown",
        ]) {
          const response = await app.inject({
            url,
            headers: header ? { authorization: header } : {},
          });
          expect(response.statusCode).toBe(401);
          expect(response.headers["www-authenticate"]).toContain(
            'Basic realm="Linkd Console"',
          );
          expect(response.body).not.toContain(credentials.password);
        }
        const write = await app.inject({
          method: "DELETE",
          url: "/local-api/event-sources/test",
          headers: header ? { authorization: header } : {},
        });
        expect(write.statusCode).toBe(401);
        expect(fetch).not.toHaveBeenCalled();
      } finally {
        await app.close();
      }
    },
  );
  it("authenticates local API without exposing either credential", async () => {
    const app = await createApp(config);
    try {
      for (const url of [
        "/local-api/capabilities",
        "/local-api/config",
        "/local-api/version",
      ]) {
        const response = await app.inject({ url, headers: { authorization } });
        expect(response.statusCode).toBe(200);
        expect(response.body).not.toContain(credentials.password);
        expect(response.body).not.toContain("upstream-test-token");
      }
    } finally {
      await app.close();
    }
  });
  it("rejects cross-origin and malformed-origin writes, and uses only upstream Bearer auth", async () => {
    const fetch = vi
      .fn()
      .mockResolvedValue(
        new Response(JSON.stringify({ ok: true }), { status: 200 }),
      );
    vi.stubGlobal("fetch", fetch);
    const app = await createApp(config);
    try {
      for (const origin of ["https://other.example", "null", "invalid"]) {
        const response = await app.inject({
          method: "DELETE",
          url: "/local-api/event-sources/test",
          headers: { authorization, origin, host: "console.example" },
        });
        expect(response.statusCode).toBe(403);
      }
      expect(fetch).not.toHaveBeenCalled();
      const response = await app.inject({
        method: "DELETE",
        url: "/local-api/event-sources/test",
        headers: {
          authorization,
          origin: "https://console.example",
          host: "console.example",
        },
      });
      expect(response.statusCode).toBe(200);
      expect(fetch).toHaveBeenCalledWith(
        "http://control-plane:8090/api/v1/event-sources/test",
        expect.objectContaining({
          headers: {
            Authorization: "Bearer upstream-test-token",
            "Content-Type": "application/json",
          },
        }),
      );
    } finally {
      await app.close();
    }
  });
});
