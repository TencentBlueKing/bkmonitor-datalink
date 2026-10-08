import Fastify from "fastify";
import { afterEach, expect, it, vi } from "vitest";
import type { ConsoleConfig } from "./config.js";
import { registerShieldCheckRoutes } from "./shield-checks.js";
import {
  runtimeCheck,
  runtimeCheckRequest,
} from "../test-fixtures/shield-checks.js";
import { childID, shieldTenant } from "../test-fixtures/shield-runtime.js";
afterEach(() => vi.unstubAllGlobals());
const config = {
  server: { access: { basicAuth: { username: "operator-a" } } },
  dispatch: {
    url: "http://control",
    jwt: { secretKey: "private", username: "admin" },
  },
  query: { timeoutMilliseconds: 5000 },
} as ConsoleConfig;
const base = "/local-api/policy-runtime/shield/alerts/" + childID;
it("preserves accepted status and injects authenticated operator with immutable command", async () => {
  const record = runtimeCheckRequest();
  record.command.operator_id = "operator-a";
  const fetcher = vi.fn(async () => Response.json(record, { status: 202 }));
  vi.stubGlobal("fetch", fetcher);
  const a = Fastify();
  registerShieldCheckRoutes(a, config);
  const { bk_tenant_id, operation_id, expected_revision, reason } =
      record.command,
    payload = { bk_tenant_id, operation_id, expected_revision, reason };
  try {
    const r = await a.inject({
      method: "POST",
      url: base + "/reconcile",
      payload,
    });
    expect(r.statusCode).toBe(202);
    expect(r.json().state).toBe("pending");
    expect(r.headers["cache-control"]).toBe("no-store");
    expect(fetcher).toHaveBeenCalledWith(
      "http://control/api/v1/policy-runtime/shield/alerts/" +
        childID +
        "/reconcile",
      expect.objectContaining({
        method: "POST",
        body: JSON.stringify({ ...payload, operator_id: "operator-a" }),
        redirect: "error",
      }),
    );
    for (const invalid of [
      { ...payload, operator_id: "forged" },
      { ...payload, force: true },
      { ...payload, expected_revision: 0 },
    ])
      expect(
        (
          await a.inject({
            method: "POST",
            url: base + "/reconcile",
            payload: invalid,
          })
        ).statusCode,
      ).toBe(400);
    expect(
      (
        await a.inject({
          method: "POST",
          url: base + "/reconcile",
          headers: { origin: "https://evil.example", host: "localhost" },
          payload,
        })
      ).statusCode,
    ).toBe(403);
    expect(fetcher).toHaveBeenCalledTimes(1);
    fetcher.mockResolvedValueOnce(
      Response.json(
        { ...record, command: { ...record.command, expected_revision: 3 } },
        { status: 202 },
      ),
    );
    expect(
      (await a.inject({ method: "POST", url: base + "/reconcile", payload }))
        .statusCode,
    ).toBe(502);
  } finally {
    await a.close();
  }
});
it("bounds and checks scopes for latest diagnostic, history and exact request", async () => {
  const fetcher = vi.fn();
  vi.stubGlobal("fetch", fetcher);
  const a = Fastify();
  registerShieldCheckRoutes(a, config);
  try {
    fetcher.mockResolvedValueOnce(
      Response.json({
        bk_tenant_id: shieldTenant,
        alert_id: childID,
        check: runtimeCheck(),
      }),
    );
    expect(
      (await a.inject(base + "/check?bk_tenant_id=" + shieldTenant)).statusCode,
    ).toBe(200);
    fetcher.mockResolvedValueOnce(
      Response.json({
        bk_tenant_id: shieldTenant,
        alert_id: childID,
        check: { ...runtimeCheck(), bk_tenant_id: "other" },
      }),
    );
    expect(
      (await a.inject(base + "/check?bk_tenant_id=" + shieldTenant)).statusCode,
    ).toBe(502);
    const r = runtimeCheckRequest(true);
    r.command.operation_id = "external-operation";
    r.command.reason = " 原始操作原因 ";
    fetcher.mockResolvedValueOnce(Response.json(r));
    expect(
      (
        await a.inject(
          base + "/requests/" + r.id + "?bk_tenant_id=" + shieldTenant,
        )
      ).statusCode,
    ).toBe(200);
    fetcher.mockResolvedValueOnce(Response.json(r));
    const exact = await a.inject(
      base + "/requests/" + r.id + "?bk_tenant_id=" + shieldTenant,
    );
    expect(exact.json().command.reason).toBe(" 原始操作原因 ");
    fetcher.mockResolvedValueOnce(
      Response.json({
        bk_tenant_id: shieldTenant,
        alert_id: childID,
        items: [r],
        next: "",
      }),
    );
    expect(
      (await a.inject(base + "/requests?bk_tenant_id=" + shieldTenant))
        .statusCode,
    ).toBe(200);
    expect(
      (
        await a.inject(
          base + "/requests?bk_tenant_id=" + shieldTenant + "&limit=5",
        )
      ).statusCode,
    ).toBe(400);
    fetcher.mockResolvedValueOnce(
      new Response("private backend", { status: 500 }),
    );
    expect(
      (await a.inject(base + "/check?bk_tenant_id=" + shieldTenant)).body,
    ).not.toContain("private");
  } finally {
    await a.close();
  }
});
