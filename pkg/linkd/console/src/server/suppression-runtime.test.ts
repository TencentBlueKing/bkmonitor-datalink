import Fastify from "fastify";
import { afterEach, expect, it, vi } from "vitest";
import { registerSuppressionRuntimeRoutes } from "./suppression-runtime.js";
import type { ConsoleConfig } from "./config.js";
import {
  clipID,
  runtimeClip,
  runtimeAggregation,
  runtimeSuppressionMembers,
  suppressionTenant,
} from "../test-fixtures/suppression-runtime.js";
import { suppressionWindow } from "../shared/suppression-runtime.js";
afterEach(() => vi.unstubAllGlobals());
const config = {
  dispatch: {
    url: "http://control-plane",
    jwt: { secretKey: "private-key", username: "admin" },
  },
  query: { timeoutMilliseconds: 5000 },
} as ConsoleConfig;
const base = "/local-api/policy-runtime/suppression/clip";
async function app() {
  const a = Fastify({ routerOptions: { maxParamLength: 160 } });
  registerSuppressionRuntimeRoutes(a, config);
  await a.ready();
  return a;
}
it("proxies scoped GETs without exposing credentials or accepting writes", async () => {
  const fetcher = vi.fn(async (input: string) => {
    const u = new URL(input),
      v = runtimeClip();
    return Response.json(
      u.pathname.endsWith("/members")
        ? runtimeSuppressionMembers(v)
        : u.pathname.endsWith("/clip")
          ? {
              bk_tenant_id: suppressionTenant,
              kind: "clip",
              items: [v],
              next: "",
            }
          : v,
    );
  });
  vi.stubGlobal("fetch", fetcher);
  const a = await app();
  try {
    for (const path of [
      base,
      base + "/" + clipID,
      base + "/" + clipID + "/members",
    ]) {
      const r = await a.inject(
        path +
          "?bk_tenant_id=" +
          suppressionTenant +
          (path.endsWith("members") ? "&epoch=opening-event" : ""),
      );
      expect(r.statusCode).toBe(200);
      expect(r.headers["cache-control"]).toBe("no-store");
      expect(r.body).not.toContain("private-key");
    }
    for (const method of ["POST", "PUT", "DELETE"] as const)
      expect((await a.inject({ method, url: base })).statusCode).toBe(404);
    expect(fetcher).toHaveBeenCalledWith(
      expect.stringContaining(
        "http://control-plane/api/v1/policy-runtime/suppression/clip",
      ),
      expect.objectContaining({
        redirect: "error",
        headers: expect.objectContaining({
          "Internal-Token": expect.stringMatching(/^Bearer /),
        }),
      }),
    );
  } finally {
    await a.close();
  }
});
it("validates query and backend tenant, identity, generation and numeric evidence", async () => {
  const fetcher = vi.fn();
  vi.stubGlobal("fetch", fetcher);
  const a = await app();
  try {
    for (const path of [
      base,
      base + "?bk_tenant_id=tenant-a&limit=5",
      base + "?bk_tenant_id=tenant-a&url=http://other",
      base + "?bk_tenant_id=tenant-a&bk_tenant_id=other",
      base + "/bad?bk_tenant_id=tenant-a",
      base + "/" + clipID + "?bk_tenant_id=tenant-a&limit=1",
      base + "/" + clipID + "/members?bk_tenant_id=tenant-a",
    ]) {
      expect((await a.inject(path)).statusCode).toBe(400);
    }
    expect(fetcher).not.toHaveBeenCalled();
    for (const body of [
      { ...runtimeClip(), bk_tenant_id: "foreign" },
      { ...runtimeClip(), id: "a".repeat(64) + ":" + "b".repeat(64) },
      { ...runtimeClip(), observed_count: undefined },
      { ...runtimeClip(), observed_count: 10001 },
    ]) {
      fetcher.mockResolvedValueOnce(Response.json(body));
      expect(
        (await a.inject(base + "/" + clipID + "?bk_tenant_id=tenant-a"))
          .statusCode,
      ).toBe(502);
    }
    fetcher.mockResolvedValueOnce(
      Response.json({
        bk_tenant_id: suppressionTenant,
        kind: "clip",
        items: [runtimeClip()],
        next: "",
      }),
    );
    expect(
      (await a.inject(base + "?bk_tenant_id=tenant-a&policy_id=another"))
        .statusCode,
    ).toBe(502);
    fetcher.mockResolvedValueOnce(
      Response.json({ ...runtimeSuppressionMembers(), epoch: "new-owner" }),
    );
    expect(
      (
        await a.inject(
          base +
            "/" +
            clipID +
            "/members?bk_tenant_id=tenant-a&epoch=opening-event",
        )
      ).statusCode,
    ).toBe(502);
  } finally {
    await a.close();
  }
});
it("keeps zero counts and distinguishes candidate, admitted and corrupt snapshots", () => {
  expect(suppressionWindow.parse(runtimeClip(0)).observed_count).toBe(0);
  const agg = runtimeAggregation();
  expect(suppressionWindow.parse(agg).state).toBe("admitted");
  expect(
    suppressionWindow.safeParse({
      ...agg,
      state: "pending",
      pending_until_ms: agg.observed_at_ms + 10000,
    }).success,
  ).toBe(true);
  for (const invalid of [
    { ...agg, state: "pending" },
    { ...agg, observed_count: 0 },
    { ...agg, owner_event_id: "wrong" },
    { ...agg, expires_at_ms: agg.started_at_ms },
    { ...runtimeClip(), observed_count: 4 },
  ])
    expect(suppressionWindow.safeParse(invalid).success).toBe(false);
});
it("shares a bounded proxy budget and releases slots after failed responses", async () => {
  const release: Array<(response: Response) => void> = [];
  vi.stubGlobal(
    "fetch",
    vi.fn(() => new Promise<Response>((resolve) => release.push(resolve))),
  );
  const a = await app();
  try {
    const requests = [
      a.inject(base + "?bk_tenant_id=tenant-a"),
      a.inject(base + "/" + clipID + "?bk_tenant_id=tenant-a"),
    ];
    await vi.waitFor(() => expect(release).toHaveLength(2));
    expect((await a.inject(base + "?bk_tenant_id=tenant-a")).statusCode).toBe(
      429,
    );
    release[0](Response.json({ message: "redis://secret" }, { status: 503 }));
    release[1](Response.json(runtimeClip()));
    const results = await Promise.all(requests);
    expect(results[0].body).not.toContain("secret");
    expect(results[1].statusCode).toBe(200);
    vi.stubGlobal(
      "fetch",
      vi.fn(async () =>
        Response.json({
          bk_tenant_id: suppressionTenant,
          kind: "clip",
          items: [],
          next: "",
        }),
      ),
    );
    expect((await a.inject(base + "?bk_tenant_id=tenant-a")).statusCode).toBe(
      200,
    );
  } finally {
    for (const done of release) done(Response.json({}, { status: 503 }));
    await a.close();
  }
});
