import Fastify from "fastify";
import { afterEach, expect, it, vi } from "vitest";
import { registerCloseAlert } from "./close-alert.js";
import type { ConsoleConfig } from "./config.js";
const command = {
  bk_tenant_id: "tenant-a",
  operation_id: "581e3d13-c28b-45be-b06e-bc3f21d233cc",
  reason: "确认关闭",
  effective_at: "2026-09-23T04:00:00.000Z",
};
afterEach(() => vi.unstubAllGlobals());
it("forwards a bounded close command with server-only credentials and authenticated actor", async () => {
  const fetcher = vi.fn(async () =>
    Response.json({ alert: { status: "closed" }, already_closed: false }),
  );
  vi.stubGlobal("fetch", fetcher);
  const app = Fastify();
  registerCloseAlert(app, {
    server: { access: { basicAuth: { username: "operator-a" } } },
    dispatch: { url: "http://control-plane", apiToken: "secret" },
    query: { timeoutMilliseconds: 1000 },
  } as ConsoleConfig);
  try {
    const response = await app.inject({
      method: "POST",
      url: "/local-api/alerts/alert-a/close",
      payload: command,
    });
    expect(response.statusCode).toBe(200);
    expect(response.body).not.toContain("secret");
    expect(fetcher).toHaveBeenCalledWith(
      "http://control-plane/api/v1/alerts/alert-a/close",
      expect.objectContaining({
        headers: expect.objectContaining({ Authorization: "Bearer secret" }),
        body: JSON.stringify({ ...command, operator_id: "operator-a" }),
      }),
    );
    for (const payload of [
      { ...command, operator_id: "forged" },
      { ...command, reason: "中".repeat(86) },
    ])
      expect(
        (
          await app.inject({
            method: "POST",
            url: "/local-api/alerts/alert-a/close",
            payload,
          })
        ).statusCode,
      ).toBe(400);
    expect(
      (
        await app.inject({
          method: "POST",
          url: "/local-api/alerts/alert-a/close",
          headers: { origin: "https://untrusted.example" },
          payload: command,
        })
      ).statusCode,
    ).toBe(403);
    expect(fetcher).toHaveBeenCalledTimes(1);
    fetcher.mockRejectedValue(new Error("secret connection failure"));
    const failure = await app.inject({
      method: "POST",
      url: "/local-api/alerts/alert-a/close",
      payload: command,
    });
    expect(failure.statusCode).toBe(502);
    expect(failure.body).toContain("状态可能已改变");
    expect(failure.body).not.toContain("secret");
  } finally {
    await app.close();
  }
});
