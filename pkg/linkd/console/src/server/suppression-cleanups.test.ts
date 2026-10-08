import Fastify from "fastify";
import { afterEach, expect, it, vi } from "vitest";
import type { ConsoleConfig } from "./config.js";
import { registerSuppressionCleanupRoutes } from "./suppression-cleanups.js";
import {
  cleanupFixture,
  cleanupID,
} from "../test-fixtures/suppression-cleanups.js";
afterEach(() => vi.unstubAllGlobals());
it("keeps cleanup history scoped and refuses fabricated success or write routes", async () => {
  const fetcher = vi.fn();
  vi.stubGlobal("fetch", fetcher);
  const app = Fastify();
  registerSuppressionCleanupRoutes(app, {
    dispatch: {
      url: "http://control",
      jwt: { secretKey: "private", username: "admin" },
    },
    query: { timeoutMilliseconds: 5000 },
  } as ConsoleConfig);
  const base = "/local-api/suppression-cleanups",
    q = "?bk_tenant_id=tenant-a",
    row = cleanupFixture();
  try {
    fetcher.mockResolvedValueOnce(
      Response.json({
        bk_tenant_id: "tenant-a",
        items: [],
        next: "filtered-next",
      }),
    );
    const empty = await app.inject(base + q);
    expect(empty.statusCode).toBe(200);
    expect(empty.json().next).toBe("filtered-next");
    expect(empty.headers["cache-control"]).toBe("no-store");
    fetcher.mockResolvedValueOnce(Response.json(row));
    const exact = await app.inject(base + "/" + cleanupID + q);
    expect(exact.statusCode).toBe(200);
    expect(exact.json().result.clip).not.toHaveProperty("removed");
    for (const bad of [
      { ...row, cause: { ...row.cause, bk_tenant_id: "other" } },
      { ...row, state: "pending" },
      {
        ...row,
        result: { ...row.result, clip: { state: "unavailable", removed: 0 } },
      },
    ]) {
      fetcher.mockResolvedValueOnce(Response.json(bad));
      expect((await app.inject(base + "/" + cleanupID + q)).statusCode).toBe(
        502,
      );
    }
    for (const tail of ["&limit=5", "&force=true", "&state=failed"]) {
      expect((await app.inject(base + q + tail)).statusCode).toBe(400);
    }
    expect(
      (
        await app.inject({
          method: "POST",
          url: base + "/" + cleanupID + "/reconcile",
          payload: {},
        })
      ).statusCode,
    ).toBe(404);
    fetcher.mockResolvedValueOnce(
      Response.json({ bk_tenant_id: "tenant-a", items: [row], next: "" }),
    );
    expect((await app.inject(base + q + "&alert_id=other")).statusCode).toBe(
      502,
    );
  } finally {
    await app.close();
  }
});
