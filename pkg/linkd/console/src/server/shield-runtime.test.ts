import Fastify from "fastify";
import { afterEach, expect, it, vi } from "vitest";
import { registerShieldRuntimeRoutes } from "./shield-runtime.js";
import type { ConsoleConfig } from "./config.js";
import {
  childID,
  runtimeShield,
  runtimeShieldHistory,
  shieldTenant,
} from "../test-fixtures/shield-runtime.js";
afterEach(() => vi.unstubAllGlobals());
const config = {
  dispatch: {
    url: "http://control-plane",
    jwt: { secretKey: "private-key", username: "admin" },
  },
  query: { timeoutMilliseconds: 5000 },
} as ConsoleConfig;
const base = "/local-api/policy-runtime/shield/alerts";
async function app() {
  const a = Fastify();
  registerShieldRuntimeRoutes(a, config);
  await a.ready();
  return a;
}
it("proxies only scoped reads including historical released Alerts", async () => {
  const fetcher = vi.fn(async (input: string) => {
    const u = new URL(input);
    return Response.json(
      u.pathname.endsWith("/history")
        ? runtimeShieldHistory()
        : u.pathname.endsWith(childID)
          ? runtimeShield(true)
          : { bk_tenant_id: shieldTenant, items: [runtimeShield()], next: "" },
    );
  });
  vi.stubGlobal("fetch", fetcher);
  const a = await app();
  try {
    for (const path of [
      base,
      base + "/" + childID,
      base + "/" + childID + "/history",
    ]) {
      const r = await a.inject(path + "?bk_tenant_id=" + shieldTenant);
      expect(r.statusCode).toBe(200);
      expect(r.headers["cache-control"]).toBe("no-store");
      expect(r.body).not.toContain("private-key");
    }
    for (const method of ["POST", "PUT", "DELETE"] as const)
      expect(
        (await a.inject({ method, url: base + "/" + childID })).statusCode,
      ).toBe(404);
    expect(fetcher).toHaveBeenCalledWith(
      expect.stringMatching(
        /^http:\/\/control-plane\/api\/v1\/policy-runtime\/shield\/alerts/,
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
it("rejects missing scope, invalid modes and inconsistent response tenant/filter/history", async () => {
  const fetcher = vi.fn();
  vi.stubGlobal("fetch", fetcher);
  const a = await app();
  try {
    for (const q of [
      "",
      "?bk_tenant_id=tenant-a&limit=5",
      "?bk_tenant_id=tenant-a&binding_type=manual",
      "?bk_tenant_id=tenant-a&url=http://other",
      "?bk_tenant_id=tenant-a&bk_tenant_id=other",
    ]) {
      expect((await a.inject(base + q)).statusCode).toBe(400);
    }
    expect(fetcher).not.toHaveBeenCalled();
    for (const query of [
      "main_alert_id=wrong",
      "policy_id=wrong",
      "binding_type=time_shield",
    ]) {
      fetcher.mockResolvedValueOnce(
        Response.json({
          bk_tenant_id: shieldTenant,
          items: [runtimeShield()],
          next: "",
        }),
      );
      expect(
        (await a.inject(base + "?bk_tenant_id=" + shieldTenant + "&" + query))
          .statusCode,
      ).toBe(502);
    }
    fetcher.mockResolvedValueOnce(
      Response.json({
        ...runtimeShieldHistory(),
        items: [{ ...runtimeShieldHistory().items[0], bk_tenant_id: "other" }],
      }),
    );
    expect(
      (
        await a.inject(
          base + "/" + childID + "/history?bk_tenant_id=" + shieldTenant,
        )
      ).statusCode,
    ).toBe(502);
    fetcher.mockResolvedValueOnce(
      Response.json({ ...runtimeShield(), alert_id: "other" }),
    );
    expect(
      (await a.inject(base + "/" + childID + "?bk_tenant_id=" + shieldTenant))
        .statusCode,
    ).toBe(502);
    fetcher.mockResolvedValueOnce(
      new Response("secret-driver", { status: 500 }),
    );
    expect(
      (await a.inject(base + "?bk_tenant_id=" + shieldTenant)).body,
    ).not.toContain("secret-driver");
  } finally {
    await a.close();
  }
});
it("keeps filtered empty history cursors and fails oversized payloads", async () => {
  const fetcher = vi
    .fn()
    .mockResolvedValueOnce(
      Response.json({
        ...runtimeShieldHistory(),
        items: [],
        next: "later-page",
      }),
    )
    .mockResolvedValueOnce(new Response("x".repeat((1 << 20) + 1)));
  vi.stubGlobal("fetch", fetcher);
  const a = await app();
  try {
    const r = await a.inject(
      base + "/" + childID + "/history?bk_tenant_id=" + shieldTenant,
    );
    expect(r.statusCode).toBe(200);
    expect(r.json().next).toBe("later-page");
    expect(
      (await a.inject(base + "?bk_tenant_id=" + shieldTenant)).statusCode,
    ).toBe(502);
  } finally {
    await a.close();
  }
});
