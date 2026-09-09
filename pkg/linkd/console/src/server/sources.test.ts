import Fastify from "fastify";
import { afterEach, expect, it, vi } from "vitest";
import { registerSourceRoutes } from "./sources.js";
import type { ConsoleConfig } from "./config.js";

afterEach(() => vi.unstubAllGlobals());
it("never forwards browser requests for full credentials to the control plane", async () => {
  const calls: string[] = [];
  vi.stubGlobal(
    "fetch",
    vi.fn(async (url: string) => {
      calls.push(url);
      return new Response(JSON.stringify({ spec: { password: "******" } }));
    }),
  );
  const app = Fastify();
  registerSourceRoutes(app, {
    dispatch: {
      url: "http://control-plane",
      apiToken: "private-admin-token",
      deployment: "test",
    },
    query: { timeoutMilliseconds: 1000 },
  } as ConsoleConfig);
  try {
    for (const route of [
      "/local-api/event-sources?include_secrets=true",
      "/local-api/event-sources/test?include_secrets=true",
    ]) {
      const response = await app.inject(route);
      expect(response.statusCode).toBe(200);
      expect(response.body).not.toContain("private-admin-token");
    }
    expect(calls).toHaveLength(2);
    expect(calls.every((url) => !url.includes("include_secrets"))).toBe(true);
  } finally {
    await app.close();
  }
});
