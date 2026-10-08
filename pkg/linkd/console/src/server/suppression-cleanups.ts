import type { FastifyInstance } from "fastify";
import { z } from "zod";
import type { ConsoleConfig } from "./config.js";
import { createPolicyProxy } from "./policies.js";
import {
  cleanupMatches,
  cleanupPage,
  cleanupQuery,
  cleanupRecord,
} from "../shared/suppression-cleanups.js";
// 历史查询只访问固定控制面路由，不透传 Redis 键或任何写入口。
export function registerSuppressionCleanupRoutes(
  app: FastifyInstance,
  config: ConsoleConfig,
) {
  const proxy = createPolicyProxy(config, 16, "抑制清理记录"),
    base = "/local-api/suppression-cleanups",
    upstream = "/api/v1/policy-runtime/suppression/cleanups";
  app.get(base, async (req, reply) => {
    const q = cleanupQuery.safeParse(req.query);
    if (!q.success)
      return reply
        .code(400)
        .send({ error: { message: "清理历史查询参数不合法" } });
    return proxy(
      req,
      reply,
      upstream +
        "?" +
        new URLSearchParams(
          Object.entries(q.data)
            .filter(([, v]) => v !== undefined)
            .map(([k, v]) => [k, String(v)]),
        ),
      undefined,
      (raw) => {
        const p = cleanupPage.parse(raw);
        if (
          p.bk_tenant_id !== q.data.bk_tenant_id ||
          p.items.length > q.data.limit ||
          (p.next && p.next === q.data.after)
        )
          throw new Error("scope");
        let last = "";
        for (const row of p.items) {
          if (
            row.cause.bk_tenant_id !== q.data.bk_tenant_id ||
            row.id <= last ||
            !cleanupMatches(row, q.data)
          )
            throw new Error("scope");
          last = row.id;
        }
        return p;
      },
      (8 << 20) + 8192,
    );
  });
  app.get(base + "/:id", async (req, reply) => {
    const p = z
        .object({ id: z.string().regex(/^[a-f0-9]{64}$/) })
        .safeParse(req.params),
      q = z
        .object({ bk_tenant_id: z.string().regex(/^[a-zA-Z0-9_-]{1,64}$/) })
        .strict()
        .safeParse(req.query);
    if (!p.success || !q.success)
      return reply
        .code(400)
        .send({ error: { message: "清理历史查询参数不合法" } });
    return proxy(
      req,
      reply,
      upstream + "/" + p.data.id + "?" + new URLSearchParams(q.data),
      undefined,
      (raw) => {
        const r = cleanupRecord.parse(raw);
        if (r.id !== p.data.id || r.cause.bk_tenant_id !== q.data.bk_tenant_id)
          throw new Error("scope");
        return r;
      },
      (2 << 20) + 4096,
    );
  });
}
