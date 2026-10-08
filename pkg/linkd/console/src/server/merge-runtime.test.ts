import Fastify from "fastify";
import { afterEach, expect, it, vi } from "vitest";
import { registerMergeRuntimeRoutes } from "./merge-runtime.js";
import type { ConsoleConfig } from "./config.js";
import {
  decisionID,
  mergeTenant,
  runtimeDecision,
  runtimeMembers,
  runtimeRelation,
  runtimeWindow,
  runtimeSnapshot,
  windowID,
} from "../test-fixtures/merge-runtime.js";
afterEach(() => vi.unstubAllGlobals());
const config = {
  dispatch: {
    url: "http://control-plane",
    jwt: { secretKey: "private-key", username: "admin" },
  },
  query: { timeoutMilliseconds: 5000 },
} as ConsoleConfig;
async function app() {
  const a = Fastify();
  registerMergeRuntimeRoutes(a, config);
  await a.ready();
  return a;
}
const base = "/local-api/policy-runtime/merge";
it("forwards bounded tenant reads and preserves missing-window errors without exposing authentication", async () => {
  const fetcher = vi.fn(async (input: string) => {
    const u = new URL(input);
    if (u.pathname.includes("/members/"))
      return Response.json(runtimeSnapshot());
    if (u.pathname.endsWith("/members")) return Response.json(runtimeMembers());
    if (u.pathname.includes("/windows/"))
      return new Response("private-driver-details", { status: 404 });
    if (u.pathname.endsWith(decisionID))
      return Response.json(runtimeDecision());
    return Response.json({
      bk_tenant_id: mergeTenant,
      items: [runtimeDecision()],
      next: "",
    });
  });
  vi.stubGlobal("fetch", fetcher);
  const a = await app();
  try {
    for (const path of [
      "?resource=decisions",
      `/decisions/${decisionID}?`,
      `/decisions/${decisionID}/members?`,
      `/decisions/${decisionID}/members/child-a?`,
    ]) {
      const u =
        base +
        path +
        (path.endsWith("?") ? "" : "&") +
        "bk_tenant_id=" +
        mergeTenant;
      const r = await a.inject(u);
      expect(r.statusCode).toBe(200);
      expect(r.body).not.toContain("private-key");
      expect(r.headers["cache-control"]).toBe("no-store");
    }
    const missing = await a.inject(
      base + `/windows/${windowID}?bk_tenant_id=${mergeTenant}`,
    );
    expect(missing.statusCode).toBe(404);
    expect(missing.body).not.toContain("private-driver");
    expect(fetcher).toHaveBeenCalledWith(
      expect.stringMatching(
        /^http:\/\/control-plane\/api\/v1\/policy-runtime\/merge/,
      ),
      expect.objectContaining({
        redirect: "error",
        headers: expect.objectContaining({
          "Internal-Token": expect.stringMatching(/^Bearer /),
        }),
      }),
    );
    for (const method of ["POST", "PUT", "DELETE"] as const)
      expect(
        (await a.inject({ method, url: base + `/windows/${windowID}` }))
          .statusCode,
      ).toBe(404);
  } finally {
    await a.close();
  }
});
it("rejects unscoped requests, cross-tenant responses, wrong filter results and unrelated Alert relations", async () => {
  const fetcher = vi.fn();
  vi.stubGlobal("fetch", fetcher);
  const a = await app();
  try {
    for (const q of [
      "resource=windows",
      "resource=relations&bk_tenant_id=tenant-a",
      "resource=windows&bk_tenant_id=tenant-a&limit=5",
      "resource=windows&bk_tenant_id=tenant-a&phase=completed",
      "resource=decisions&bk_tenant_id=tenant-a&bk_tenant_id=tenant-b",
      "resource=windows&bk_tenant_id=tenant-a&url=http://other",
    ]) {
      expect((await a.inject(base + "?" + q)).statusCode).toBe(400);
    }
    expect(fetcher).not.toHaveBeenCalled();
    for (const [query, row] of [
      ["resource=decisions", { ...runtimeDecision(), bk_tenant_id: "other" }],
      ["resource=decisions&phase=releasing", runtimeDecision()],
      ["resource=relations&alert_id=unrelated", runtimeRelation()],
      ["resource=windows&policy_id=other", runtimeWindow()],
    ] as const) {
      fetcher.mockResolvedValueOnce(
        Response.json({ bk_tenant_id: mergeTenant, items: [row], next: "" }),
      );
      expect(
        (await a.inject(base + "?bk_tenant_id=" + mergeTenant + "&" + query))
          .statusCode,
      ).toBe(502);
    }
    fetcher.mockResolvedValueOnce(
      Response.json({ ...runtimeDecision(), id: windowID }),
    );
    expect(
      (
        await a.inject(
          base + `/decisions/${decisionID}?bk_tenant_id=${mergeTenant}`,
        )
      ).statusCode,
    ).toBe(502);
  } finally {
    await a.close();
  }
});
it("retains filtered empty-page cursors and rejects oversized runtime responses", async () => {
  const fetcher = vi
    .fn()
    .mockResolvedValueOnce(
      Response.json({
        bk_tenant_id: mergeTenant,
        items: [],
        next: "next-page",
      }),
    )
    .mockResolvedValueOnce(new Response("x".repeat((1 << 20) + 1)));
  vi.stubGlobal("fetch", fetcher);
  const a = await app();
  try {
    const r = await a.inject(
      base + "?resource=decisions&bk_tenant_id=" + mergeTenant,
    );
    expect(r.statusCode).toBe(200);
    expect(r.json().next).toBe("next-page");
    expect(
      (await a.inject(base + "?resource=decisions&bk_tenant_id=" + mergeTenant))
        .statusCode,
    ).toBe(502);
  } finally {
    await a.close();
  }
});
