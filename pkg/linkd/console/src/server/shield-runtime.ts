import type { FastifyInstance } from "fastify";
import { z } from "zod";
import {
  matchesShieldQuery,
  shieldHistory,
  shieldPage,
  shieldQuery,
  shieldRecord,
} from "../shared/shield-runtime.js";
import { createPolicyProxy } from "./policies.js";
import type { ConsoleConfig } from "./config.js";

// 屏蔽管理只读代理；完整范围/游标由控制面验证，浏览器不能指定后端地址或认证头。
export function registerShieldRuntimeRoutes(
  app: FastifyInstance,
  config: ConsoleConfig,
  proxy = createPolicyProxy(config),
) {
  const base = "/local-api/policy-runtime/shield/alerts",
    upstream = "/api/v1/policy-runtime/shield/alerts";
  const key = z.object({ id: z.string().min(1).max(160) }),
    scope = z
      .object({ bk_tenant_id: z.string().regex(/^[a-zA-Z0-9_-]{1,64}$/) })
      .strict();
  app.get(base, async (request, reply) => {
    const parsed = shieldQuery.safeParse(request.query);
    if (!parsed.success)
      return reply.code(400).send({ error: { message: "屏蔽查询参数不合法" } });
    const q = parsed.data,
      params = new URLSearchParams(
        Object.entries(q)
          .filter(([, v]) => v !== undefined)
          .map(([k, v]) => [k, String(v)]),
      );
    return proxy(
      request,
      reply,
      upstream + "?" + params,
      undefined,
      (raw) => {
        const page = shieldPage.parse(raw);
        if (
          page.bk_tenant_id !== q.bk_tenant_id ||
          page.items.length > q.limit ||
          (page.next && page.next === q.after)
        )
          throw new Error("scope");
        let last = "";
        for (const row of page.items) {
          if (
            row.bk_tenant_id !== q.bk_tenant_id ||
            row.alert_id <= last ||
            !matchesShieldQuery(row, q) ||
            (!row.shield.active && !row.policy_change)
          )
            throw new Error("scope");
          last = row.alert_id;
        }
        return page;
      },
      1 << 20,
    );
  });
  app.get(base + "/:id", async (request, reply) => {
    const k = key.safeParse(request.params),
      q = scope.safeParse(request.query);
    if (!k.success || !q.success)
      return reply.code(400).send({ error: { message: "屏蔽详情参数不合法" } });
    return proxy(
      request,
      reply,
      upstream +
        "/" +
        encodeURIComponent(k.data.id) +
        "?" +
        new URLSearchParams(q.data),
      undefined,
      (raw) => {
        const row = shieldRecord.parse(raw);
        if (
          row.bk_tenant_id !== q.data.bk_tenant_id ||
          row.alert_id !== k.data.id
        )
          throw new Error("scope");
        return row;
      },
      1 << 20,
    );
  });
  app.get(base + "/:id/history", async (request, reply) => {
    const k = key.safeParse(request.params),
      q = scope
        .extend({
          after: z.string().max(8192).default(""),
          limit: z.coerce.number().int().min(1).max(4).default(4),
        })
        .safeParse(request.query);
    if (!k.success || !q.success)
      return reply.code(400).send({ error: { message: "屏蔽历史参数不合法" } });
    const params = new URLSearchParams({
      ...q.data,
      limit: String(q.data.limit),
    });
    return proxy(
      request,
      reply,
      upstream + "/" + encodeURIComponent(k.data.id) + "/history?" + params,
      undefined,
      (raw) => {
        const page = shieldHistory.parse(raw);
        if (
          page.bk_tenant_id !== q.data.bk_tenant_id ||
          page.alert_id !== k.data.id ||
          page.items.length > q.data.limit ||
          (page.next && page.next === q.data.after) ||
          page.items.some(
            (row) =>
              row.bk_tenant_id !== q.data.bk_tenant_id ||
              row.alert_id !== k.data.id,
          )
        )
          throw new Error("scope");
        return page;
      },
      1 << 20,
    );
  });
}
