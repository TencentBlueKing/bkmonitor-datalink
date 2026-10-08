import Fastify from "fastify";
import { afterEach, expect, it, vi } from "vitest";
import type { ConsoleConfig } from "./config.js";
import { registerProjectionTaskRoutes } from "./projection-tasks.js";
import {
  projectionFixture,
  projectionID,
  projectionTenant,
  recoveredProjection,
} from "../test-fixtures/projection-tasks.js";
afterEach(() => vi.unstubAllGlobals());
const config = {
  server: { access: { basicAuth: { username: "operator-a" } } },
  dispatch: {
    url: "http://control",
    jwt: { secretKey: "private", username: "admin" },
  },
  query: { timeoutMilliseconds: 5000 },
} as ConsoleConfig;
const base = "/local-api/projection-tasks",
  scope = "?bk_tenant_id=" + projectionTenant;
it("proxies only bounded scoped reads and separates frozen snapshots from summaries", async () => {
  const fetcher = vi.fn();
  vi.stubGlobal("fetch", fetcher);
  const app = Fastify();
  registerProjectionTaskRoutes(app, config);
  const row = projectionFixture();
  try {
    fetcher.mockResolvedValueOnce(
      Response.json({
        bk_tenant_id: projectionTenant,
        items: [],
        next: "next-filtered-page",
      }),
    );
    const empty = await app.inject(base + scope + "&state=failed");
    expect(empty.statusCode).toBe(200);
    expect(empty.json().next).toBe("next-filtered-page");
    expect(empty.headers["cache-control"]).toBe("no-store");
    fetcher.mockResolvedValueOnce(Response.json(row));
    const detail = await app.inject(base + "/" + projectionID + scope);
    expect(detail.statusCode).toBe(200);
    expect(detail.json()).not.toHaveProperty("alert");
    const snapshot = {
      bk_tenant_id: projectionTenant,
      id: projectionID,
      revision: 2,
      content_hash: row.content_hash,
      alert: {
        bk_tenant_id: projectionTenant,
        alert_id: row.alert_id,
        revision: 2,
        title: "冻结标题",
      },
    };
    fetcher.mockResolvedValueOnce(Response.json(snapshot));
    expect(
      (await app.inject(base + "/" + projectionID + "/snapshot" + scope)).json()
        .alert.title,
    ).toBe("冻结标题");
    for (const invalid of [
      "&limit=5",
      "&state=other",
      "&force=true",
      "&bk_tenant_id=other",
    ]) {
      expect((await app.inject(base + scope + invalid)).statusCode).toBe(400);
    }
    expect(fetcher).toHaveBeenCalledTimes(3);
    for (const bad of [
      { ...row, bk_tenant_id: "other" },
      { ...row, id: "c".repeat(64) },
      { ...row, progress: { ...row.progress, state: "succeeded" } },
    ]) {
      fetcher.mockResolvedValueOnce(Response.json(bad));
      expect(
        (await app.inject(base + "/" + projectionID + scope)).statusCode,
      ).toBe(502);
    }
    fetcher.mockResolvedValueOnce(
      Response.json({
        ...snapshot,
        alert: { ...snapshot.alert, bk_tenant_id: "other" },
      }),
    );
    expect(
      (await app.inject(base + "/" + projectionID + "/snapshot" + scope))
        .statusCode,
    ).toBe(502);
    fetcher.mockResolvedValueOnce(
      Response.json({ bk_tenant_id: projectionTenant, items: [row], next: "" }),
    );
    expect(
      (await app.inject(base + scope + "&state=succeeded")).statusCode,
    ).toBe(502);
    fetcher.mockResolvedValueOnce(
      new Response("private driver error", { status: 500 }),
    );
    expect((await app.inject(base + scope)).body).not.toContain("private");
  } finally {
    await app.close();
  }
});
it("preserves accepted retry command and derives actor from server authentication", async () => {
  const fetcher = vi.fn();
  vi.stubGlobal("fetch", fetcher);
  const app = Fastify();
  registerProjectionTaskRoutes(app, config);
  const row = recoveredProjection(),
    audit = row.progress.last_retry!.command;
  const payload = {
    bk_tenant_id: projectionTenant,
    expected_version: audit.expected_version,
    operation_id: audit.operation_id,
    reason: audit.reason,
  };
  try {
    fetcher.mockResolvedValueOnce(Response.json(row, { status: 202 }));
    const result = await app.inject({
      method: "POST",
      url: base + "/" + projectionID + "/retry",
      payload,
    });
    expect(result.statusCode).toBe(202);
    expect(result.json().progress.state).toBe("pending");
    expect(fetcher).toHaveBeenCalledWith(
      "http://control/api/v1/projection-tasks/" + projectionID + "/retry",
      expect.objectContaining({
        method: "POST",
        body: JSON.stringify({ ...payload, operator_id: "operator-a" }),
        redirect: "error",
      }),
    );
    for (const bad of [
      { ...payload, operator_id: "forged" },
      { ...payload, reason: " " },
      { ...payload, expected_version: "" },
      { ...payload, force: true },
    ]) {
      expect(
        (
          await app.inject({
            method: "POST",
            url: base + "/" + projectionID + "/retry",
            payload: bad,
          })
        ).statusCode,
      ).toBe(400);
    }
    expect(
      (
        await app.inject({
          method: "POST",
          url: base + "/" + projectionID + "/retry",
          headers: { origin: "https://evil.example", host: "localhost" },
          payload,
        })
      ).statusCode,
    ).toBe(403);
    expect(fetcher).toHaveBeenCalledTimes(1);
    fetcher.mockResolvedValueOnce(
      Response.json(recoveredProjection({ reason: "changed" }), {
        status: 202,
      }),
    );
    expect(
      (
        await app.inject({
          method: "POST",
          url: base + "/" + projectionID + "/retry",
          payload,
        })
      ).statusCode,
    ).toBe(502);
    fetcher.mockResolvedValueOnce(
      Response.json(recoveredProjection({ operator_id: "forged" }), {
        status: 202,
      }),
    );
    expect(
      (
        await app.inject({
          method: "POST",
          url: base + "/" + projectionID + "/retry",
          payload,
        })
      ).statusCode,
    ).toBe(502);
  } finally {
    await app.close();
  }
});
