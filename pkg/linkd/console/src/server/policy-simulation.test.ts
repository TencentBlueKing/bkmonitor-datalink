import Fastify from "fastify";
import { afterEach, it, expect, vi } from "vitest";
import { registerPolicyDiagnostics } from "./policy-simulation.js";
import type { ConsoleConfig } from "./config.js";
import { policyRelease } from "../test-fixtures/policies.js";
afterEach(() => vi.unstubAllGlobals());
const config = {
  dispatch: {
    url: "http://control",
    jwt: { secretKey: "private-key", username: "admin" },
  },
  query: { timeoutMilliseconds: 5000 },
} as ConsoleConfig;
const request = {
  bk_tenant_id: "tenant-a",
  type: "suppression",
  id: "p",
  version: 1,
  steps: [
    { at: "2026-10-08T00:00:00Z", event_id: "a" },
    { at: "2026-10-08T00:01:00Z" },
  ],
};
const result = () => ({
  ...request,
  mode: "state_simulation",
  compiled: policyRelease().compiled,
  steps: request.steps.map((s) => ({
    ...s,
    outcome: "time_advanced",
    replayed: false,
    alerts: [],
    windows: [],
  })),
});
it("proxies bounded simulation and statistics with JWT, rejects foreign scope and unsafe input", async () => {
  const fetcher = vi.fn(async () => Response.json(result()));
  vi.stubGlobal("fetch", fetcher);
  const app = Fastify();
  registerPolicyDiagnostics(app, config);
  try {
    const ok = await app.inject({
      method: "POST",
      url: "/local-api/policies/simulate",
      payload: request,
    });
    expect(ok.statusCode).toBe(200);
    expect(ok.body).not.toContain("private-key");
    expect(fetcher).toHaveBeenCalledWith(
      "http://control/api/v1/policies/simulate",
      expect.objectContaining({
        redirect: "error",
        headers: expect.objectContaining({
          "Internal-Token": expect.stringMatching(/^Bearer /),
        }),
      }),
    );
    for (const invalid of [
      { ...request, steps: [] },
      { ...request, type: "shield" },
      {
        ...request,
        steps: [{ ...request.steps[0], event: { bk_tenant_id: "other" } }],
      },
      { ...request, steps: [...request.steps].reverse() },
      { ...request, url: "http://evil" },
    ])
      expect(
        (
          await app.inject({
            method: "POST",
            url: "/local-api/policies/simulate",
            payload: invalid,
          })
        ).statusCode,
      ).toBe(400);
    expect(
      (
        await app.inject({
          method: "POST",
          url: "/local-api/policies/simulate",
          payload: request,
          headers: { origin: "https://evil.example" },
        })
      ).statusCode,
    ).toBe(403);
    fetcher.mockImplementation(async () =>
      Response.json({ ...result(), bk_tenant_id: "other" }),
    );
    expect(
      (
        await app.inject({
          method: "POST",
          url: "/local-api/policies/simulate",
          payload: request,
        })
      ).statusCode,
    ).toBe(502);
    fetcher.mockImplementation(async () =>
      Response.json({
        bk_tenant_id: "tenant-a",
        type: "merge",
        mode: "execution_observations",
        hours: 1,
        from: request.steps[0].at,
        to: request.steps[1].at,
        items: [
          {
            id: "p",
            matched: 1,
            not_matched: 0,
            unavailable: 0,
            execution_skipped: 0,
          },
        ],
      }),
    );
    expect(
      (
        await app.inject(
          "/local-api/policies/statistics?bk_tenant_id=tenant-a&type=merge&ids=p&hours=1",
        )
      ).statusCode,
    ).toBe(200);
    expect(
      (
        await app.inject(
          "/local-api/policies/statistics?bk_tenant_id=tenant-a&type=merge&ids=q&hours=1",
        )
      ).statusCode,
    ).toBe(502);
    expect(
      (
        await app.inject(
          "/local-api/policies/statistics?bk_tenant_id=tenant-a&type=merge&ids=p,p&hours=1",
        )
      ).statusCode,
    ).toBe(400);
  } finally {
    await app.close();
  }
});
