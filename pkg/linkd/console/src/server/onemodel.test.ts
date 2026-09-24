import Fastify from "fastify";
import { afterEach, expect, it, vi } from "vitest";
import { registerOneModelRoutes } from "./onemodel.js";
import type { ConsoleConfig } from "./config.js";
afterEach(() => vi.unstubAllGlobals());
const config = {
  dispatch: {
    url: "http://control-plane/",
    apiToken: "private-token",
    deployment: "test",
  },
  query: { timeoutMilliseconds: 1000 },
} as ConsoleConfig;
it("proxies only fixed OneModel operations with server credentials and no caching", async () => {
  const fetcher = vi.fn(
    async () =>
      new Response(JSON.stringify({ items: [], elapsed_milliseconds: 1 })),
  );
  vi.stubGlobal("fetch", fetcher);
  const app = Fastify();
  registerOneModelRoutes(app, config);
  try {
    for (const operation of ["search", "related", "close"]) {
      const response = await app.inject({
        method: "POST",
        url: `/local-api/onemodel/${operation}?url=http://untrusted`,
        payload: { bk_tenant_id: "t", model_id: "host" },
      });
      expect(response.statusCode).toBe(200);
      expect(response.headers["cache-control"]).toBe("no-store");
      expect(response.body).not.toContain("private-token");
      expect(fetcher).toHaveBeenLastCalledWith(
        `http://control-plane/api/v1/onemodel/${operation}`,
        expect.objectContaining({
          headers: expect.objectContaining({
            Authorization: "Bearer private-token",
          }),
          signal: expect.any(AbortSignal),
        }),
      );
    }
    expect(
      (
        await app.inject({
          method: "POST",
          url: "/local-api/onemodel/search",
          headers: { origin: "http://untrusted" },
          payload: {},
        })
      ).statusCode,
    ).toBe(403);
    expect(
      (
        await app.inject({
          method: "POST",
          url: "/local-api/onemodel/delete",
          payload: {},
        })
      ).statusCode,
    ).toBe(404);
    expect(fetcher).toHaveBeenCalledTimes(3);
  } finally {
    await app.close();
  }
});
it("preserves typed errors and hides upstream transport details", async () => {
  const fetcher = vi.fn(
    async () =>
      new Response(
        JSON.stringify({ error: { message: "查询快照已过期，请重新查询" } }),
        { status: 410 },
      ),
  );
  vi.stubGlobal("fetch", fetcher);
  const app = Fastify();
  registerOneModelRoutes(app, config);
  try {
    const request = {
      method: "POST" as const,
      url: "/local-api/onemodel/search",
      payload: { bk_tenant_id: "t" },
    };
    let response = await app.inject(request);
    expect(response.statusCode).toBe(410);
    expect(response.body).toContain("快照已过期");
    fetcher.mockRejectedValue(new Error("private-token"));
    response = await app.inject(request);
    expect(response.statusCode).toBe(502);
    expect(response.body).not.toContain("private-token");
  } finally {
    await app.close();
  }
});
it("reports missing control plane without querying", async () => {
  const app = Fastify();
  registerOneModelRoutes(app, {
    query: { timeoutMilliseconds: 1000 },
  } as ConsoleConfig);
  try {
    expect(
      (
        await app.inject({
          method: "POST",
          url: "/local-api/onemodel/search",
          payload: {},
        })
      ).statusCode,
    ).toBe(503);
  } finally {
    await app.close();
  }
});
