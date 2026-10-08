import Fastify from "fastify";
import { afterEach, expect, it, vi } from "vitest";
import type { ConsoleConfig } from "./config.js";
import { registerSuppressionCheckRoutes } from "./suppression-checks.js";
import { suppressionRequestFixture } from "../test-fixtures/suppression-checks.js";
afterEach(() => vi.unstubAllGlobals());
it("keeps full command, derives actor and rejects scope/intent changes", async () => {
  const row = suppressionRequestFixture(),
    base =
      "/local-api/policy-runtime/suppression/clip/" +
      encodeURIComponent(row.command.window_id),
    q = "?bk_tenant_id=tenant-a",
    fetcher = vi.fn();
  vi.stubGlobal("fetch", fetcher);
  const app = Fastify({ routerOptions: { maxParamLength: 160 } });
  registerSuppressionCheckRoutes(app, {
    server: { access: { basicAuth: { username: "operator-a" } } },
    dispatch: {
      url: "http://control",
      jwt: { secretKey: "private", username: "admin" },
    },
    query: { timeoutMilliseconds: 5000 },
  } as ConsoleConfig);
  const {
      bk_tenant_id,
      expected_epoch,
      expected_owner_alert_id,
      operation_id,
      reason,
    } = row.command,
    payload = {
      bk_tenant_id,
      expected_epoch,
      expected_owner_alert_id,
      operation_id,
      reason,
    };
  try {
    fetcher.mockResolvedValueOnce(Response.json(row, { status: 202 }));
    const result = await app.inject({
      method: "POST",
      url: base + "/reconcile",
      payload,
    });
    expect(result.statusCode).toBe(202);
    expect(result.json().state).toBe("pending");
    expect(fetcher).toHaveBeenCalledWith(
      expect.any(String),
      expect.objectContaining({
        method: "POST",
        body: JSON.stringify({ ...payload, operator_id: "operator-a" }),
        redirect: "error",
      }),
    );
    for (const bad of [
      { ...payload, operator_id: "forged" },
      { ...payload, expected_epoch: "" },
      { ...payload, expected_owner_alert_id: undefined },
      { ...payload, force: true },
    ])
      expect(
        (
          await app.inject({
            method: "POST",
            url: base + "/reconcile",
            payload: bad,
          })
        ).statusCode,
      ).toBe(400);
    expect(
      (
        await app.inject({
          method: "POST",
          url: base + "/reconcile",
          payload,
          headers: { origin: "https://evil.example", host: "localhost" },
        })
      ).statusCode,
    ).toBe(403);
    expect(fetcher).toHaveBeenCalledTimes(1);
    fetcher.mockResolvedValueOnce(
      Response.json(
        { ...row, command: { ...row.command, expected_epoch: "other" } },
        { status: 202 },
      ),
    );
    expect(
      (await app.inject({ method: "POST", url: base + "/reconcile", payload }))
        .statusCode,
    ).toBe(502);
    fetcher.mockResolvedValueOnce(
      Response.json(suppressionRequestFixture(true)),
    );
    expect(
      (await app.inject(base + "/requests/" + row.id + q)).statusCode,
    ).toBe(200);
    fetcher.mockResolvedValueOnce(
      Response.json({
        ...row,
        command: { ...row.command, bk_tenant_id: "other" },
      }),
    );
    expect(
      (await app.inject(base + "/requests/" + row.id + q)).statusCode,
    ).toBe(502);
  } finally {
    await app.close();
  }
});
