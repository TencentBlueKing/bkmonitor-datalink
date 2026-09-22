import Fastify from "fastify";
import { afterEach, expect, it, vi } from "vitest";
import { registerSourceRoutes } from "./sources.js";
import type { ConsoleConfig } from "./config.js";

afterEach(() => vi.unstubAllGlobals());
it("serves the metric catalog through a fixed read-only destination with server authentication", async () => {
  const fetcher = vi.fn(
    async () =>
      new Response(JSON.stringify({ schema_version: 1, metrics: [] })),
  );
  vi.stubGlobal("fetch", fetcher);
  const app = Fastify();
  registerSourceRoutes(app, {
    dispatch: {
      url: "http://control-plane/",
      apiToken: "private-admin-token",
      deployment: "test",
    },
    query: { timeoutMilliseconds: 1000 },
  } as ConsoleConfig);
  try {
    const response = await app.inject(
      "/local-api/metrics/catalog?url=http://untrusted",
    );
    expect(response.statusCode).toBe(200);
    expect(response.body).not.toContain("private-admin-token");
    expect(fetcher).toHaveBeenCalledWith(
      "http://control-plane/api/v1/metrics/catalog",
      expect.objectContaining({
        method: "GET",
        headers: expect.objectContaining({
          Authorization: "Bearer private-admin-token",
        }),
      }),
    );
    fetcher.mockImplementation(
      async () => new Response("unauthorized", { status: 401 }),
    );
    expect((await app.inject("/local-api/metrics/catalog")).statusCode).toBe(
      401,
    );
    fetcher.mockRejectedValue(new Error("connection refused"));
    expect((await app.inject("/local-api/metrics/catalog")).statusCode).toBe(
      502,
    );
    expect(
      (await app.inject({ method: "POST", url: "/local-api/metrics/catalog" }))
        .statusCode,
    ).toBe(404);
  } finally {
    await app.close();
  }
});

it("reports missing control-plane configuration for the catalog", async () => {
  const app = Fastify();
  registerSourceRoutes(app, {
    query: { timeoutMilliseconds: 1000 },
  } as ConsoleConfig);
  try {
    expect((await app.inject("/local-api/metrics/catalog")).statusCode).toBe(
      503,
    );
  } finally {
    await app.close();
  }
});

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

it("proxies preview with server token and rejects a foreign origin", async () => {
  const fetcher = vi.fn(
    async () =>
      new Response(
        JSON.stringify({ error: { message: "processors[0]: invalid target" } }),
        { status: 422 },
      ),
  );
  vi.stubGlobal("fetch", fetcher);
  const app = Fastify();
  registerSourceRoutes(app, {
    dispatch: {
      url: "http://control-plane",
      apiToken: "private-token",
      deployment: "test",
    },
    query: { timeoutMilliseconds: 1000 },
  } as ConsoleConfig);
  try {
    const blocked = await app.inject({
      method: "POST",
      url: "/local-api/enrich/preview",
      headers: { origin: "https://foreign.example" },
      payload: {},
    });
    expect(blocked.statusCode).toBe(403);
    expect(fetcher).not.toHaveBeenCalled();
    const response = await app.inject({
      method: "POST",
      url: "/local-api/enrich/preview",
      payload: { input: { alert: { title: "raw" } } },
    });
    expect(response.statusCode).toBe(422);
    expect(response.json().error.message).toContain("invalid target");
    expect(response.body).not.toContain("private-token");
    expect(fetcher).toHaveBeenCalledWith(
      "http://control-plane/api/v1/enrich/preview",
      expect.objectContaining({
        method: "POST",
        headers: expect.objectContaining({
          Authorization: "Bearer private-token",
        }),
      }),
    );
  } finally {
    await app.close();
  }
});
