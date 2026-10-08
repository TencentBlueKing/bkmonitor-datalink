import Fastify from "fastify";
import { afterEach, expect, it, vi } from "vitest";
import { registerPolicyRoutes } from "./policies.js";
import type { ConsoleConfig } from "./config.js";
import {
  policyRecord,
  policyRelease,
  policyPreview,
} from "../test-fixtures/policies.js";

afterEach(() => vi.unstubAllGlobals());
const config = {
  dispatch: {
    url: "http://control-plane",
    jwt: { secretKey: "private-key", username: "admin" },
  },
  query: { timeoutMilliseconds: 5000 },
} as ConsoleConfig;
async function app() {
  const app = Fastify();
  registerPolicyRoutes(app, config);
  await app.ready();
  return app;
}
const previewBody = {
  bk_tenant_id: "tenant-a",
  type: "suppression",
  id: "db-noise",
  version: 2,
  event_id: "event-a",
};
it("proxies only tenant-scoped reads and preview, keeping authentication server-side", async () => {
  const fetcher = vi.fn(async (url: string) =>
    Response.json(
      url.includes("/preview")
        ? policyPreview()
        : url.includes("/releases/")
          ? policyRelease()
          : url.includes("/db-noise")
            ? policyRecord()
            : { items: [policyRecord()], next: "" },
    ),
  );
  vi.stubGlobal("fetch", fetcher);
  const server = await app();
  try {
    for (const url of [
      "/local-api/policies?bk_tenant_id=tenant-a&type=suppression",
      "/local-api/policies/suppression/db-noise?bk_tenant_id=tenant-a",
      "/local-api/policies/suppression/db-noise/releases/2?bk_tenant_id=tenant-a",
    ]) {
      const response = await server.inject(url);
      expect(response.statusCode).toBe(200);
      expect(response.headers["cache-control"]).toBe("no-store");
      expect(response.body).not.toContain("private-key");
    }
    expect(
      (
        await server.inject({
          method: "POST",
          url: "/local-api/policies/preview",
          payload: previewBody,
        })
      ).statusCode,
    ).toBe(200);
    for (const method of ["PUT", "DELETE"] as const)
      expect(
        (
          await server.inject({
            method,
            url: "/local-api/policies/suppression/db-noise",
            payload: {},
          })
        ).statusCode,
      ).toBe(404);
    expect(fetcher).toHaveBeenCalledTimes(4);
    expect(fetcher).toHaveBeenCalledWith(
      expect.stringMatching(/^http:\/\/control-plane\/api\/v1\/policies/),
      expect.objectContaining({
        redirect: "error",
        headers: expect.objectContaining({
          "Internal-Token": expect.stringMatching(
            /^Bearer [^.]+\.[^.]+\.[^.]+$/,
          ),
        }),
      }),
    );
  } finally {
    await server.close();
  }
});
it("rejects missing/ambiguous scope, injected destinations and cross-tenant inline facts before forwarding", async () => {
  const fetcher = vi.fn();
  vi.stubGlobal("fetch", fetcher);
  const server = await app();
  try {
    for (const url of [
      "/local-api/policies?type=suppression",
      "/local-api/policies?bk_tenant_id=tenant-a&type=invalid",
      "/local-api/policies?bk_tenant_id=tenant-a&bk_tenant_id=tenant-b&type=shield",
      "/local-api/policies?bk_tenant_id=tenant-a&type=shield&url=http://other",
      "/local-api/policies?bk_tenant_id=tenant-a&type=merge&limit=17",
      "/local-api/policies/merge/a/releases/0?bk_tenant_id=tenant-a",
    ])
      expect((await server.inject(url)).statusCode).toBe(400);
    const inline = { ...previewBody, event_id: undefined };
    expect(
      (
        await server.inject({
          method: "POST",
          url: "/local-api/policies/preview",
          payload: { ...inline, event: { bk_tenant_id: "tenant-b" } },
        })
      ).statusCode,
    ).toBe(400);
    expect(
      (
        await server.inject({
          method: "POST",
          url: "/local-api/policies/preview",
          headers: { origin: "https://untrusted.test", host: "localhost" },
          payload: previewBody,
        })
      ).statusCode,
    ).toBe(403);
    expect(fetcher).not.toHaveBeenCalled();
  } finally {
    await server.close();
  }
});
it("preserves empty filtered pages, rejects wrong releases and nested pending tenant mismatches", async () => {
  const fetcher = vi.fn(async () =>
    Response.json({ items: [], next: "last-scanned" }),
  );
  vi.stubGlobal("fetch", fetcher);
  const server = await app();
  try {
    const page = await server.inject(
      "/local-api/policies?bk_tenant_id=tenant-a&type=suppression&is_enable=true",
    );
    expect(page.json()).toEqual({ items: [], next: "last-scanned" });
    fetcher.mockImplementation(async () => Response.json(policyRelease(1)));
    expect(
      (
        await server.inject(
          "/local-api/policies/suppression/db-noise/releases/2?bk_tenant_id=tenant-a",
        )
      ).statusCode,
    ).toBe(502);
    const record = policyRecord();
    record.pending!.bk_tenant_id = "tenant-b";
    fetcher.mockImplementation(async () => Response.json(record));
    expect(
      (
        await server.inject(
          "/local-api/policies/suppression/db-noise?bk_tenant_id=tenant-a",
        )
      ).statusCode,
    ).toBe(502);
  } finally {
    await server.close();
  }
});
it("bounds responses and conceals raw backend errors", async () => {
  const fetcher = vi.fn(
    async () => new Response("private credentials payload", { status: 500 }),
  );
  vi.stubGlobal("fetch", fetcher);
  const server = await app();
  try {
    const failed = await server.inject(
      "/local-api/policies?bk_tenant_id=tenant-a&type=shield",
    );
    expect(failed.statusCode).toBe(500);
    expect(failed.body).not.toContain("private");
    fetcher.mockImplementation(
      async () => new Response("x".repeat((2 << 20) + 1)),
    );
    expect(
      (
        await server.inject({
          method: "POST",
          url: "/local-api/policies/preview",
          payload: previewBody,
        })
      ).statusCode,
    ).toBe(502);
  } finally {
    await server.close();
  }
});

it("bounds concurrent proxy requests and releases capacity after completion", async () => {
  const waiting: Array<(response: Response) => void> = [];
  const fetcher = vi.fn(
    () => new Promise<Response>((resolve) => waiting.push(resolve)),
  );
  vi.stubGlobal("fetch", fetcher);
  const server = await app();
  const url = "/local-api/policies?bk_tenant_id=tenant-a&type=suppression";
  try {
    const first = Promise.resolve(server.inject(url));
    const second = Promise.resolve(server.inject(url));
    await vi.waitFor(() => expect(fetcher).toHaveBeenCalledTimes(2));
    expect((await server.inject(url)).statusCode).toBe(429);
    for (const done of waiting) done(Response.json({ items: [], next: "" }));
    expect((await first).statusCode).toBe(200);
    expect((await second).statusCode).toBe(200);
    fetcher.mockImplementation(async () =>
      Response.json({ items: [], next: "" }),
    );
    expect((await server.inject(url)).statusCode).toBe(200);
  } finally {
    for (const done of waiting) done(Response.json({ items: [], next: "" }));
    await server.close();
  }
});
it("propagates caller abort to the control-plane read and releases its request slot", async () => {
  let captured: import("fastify").FastifyRequest | undefined;
  let upstream: AbortSignal | undefined;
  vi.stubGlobal(
    "fetch",
    vi.fn(
      (_url: string, init?: RequestInit) =>
        new Promise<Response>((_resolve, reject) => {
          upstream = init?.signal as AbortSignal;
          upstream.addEventListener(
            "abort",
            () => reject(new Error("aborted")),
            { once: true },
          );
        }),
    ),
  );
  const server = Fastify();
  server.addHook("onRequest", async (request) => {
    captured = request;
  });
  registerPolicyRoutes(server, config);
  try {
    const pending = Promise.resolve(
      server.inject(
        "/local-api/policies?bk_tenant_id=tenant-a&type=suppression",
      ),
    );
    await vi.waitFor(() => expect(upstream).toBeDefined());
    captured!.raw.emit("aborted");
    expect((await pending).statusCode).toBe(502);
    expect(upstream?.aborted).toBe(true);
  } finally {
    await server.close();
  }
});
