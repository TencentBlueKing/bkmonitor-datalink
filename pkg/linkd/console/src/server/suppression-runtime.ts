import type { FastifyInstance } from "fastify";
import {
  suppressionKey,
  suppressionKind,
  suppressionQuery,
  suppressionScope,
  suppressionMemberQuery,
  suppressionWindow,
  suppressionPage,
  suppressionMembers,
  matchesSuppressionQuery,
} from "../shared/suppression-runtime.js";
import { createPolicyProxy } from "./policies.js";
import type { ConsoleConfig } from "./config.js";

// 浏览器不能指定 Redis 键或控制面地址；所有读取复核租户/身份/过滤范围，不注册写入方法。
export function registerSuppressionRuntimeRoutes(
  app: FastifyInstance,
  config: ConsoleConfig,
  proxy = createPolicyProxy(config),
) {
  const base = "/local-api/policy-runtime/suppression/:kind",
    upstream = "/api/v1/policy-runtime/suppression/";
  app.get(base, async (request, reply) => {
    const kind = suppressionKind.safeParse(
        (request.params as Record<string, unknown>).kind,
      ),
      parsed = suppressionQuery.safeParse(request.query);
    if (!kind.success || !parsed.success)
      return reply.code(400).send({ error: { message: "抑制查询参数不合法" } });
    const q = parsed.data,
      params = new URLSearchParams(
        Object.entries(q)
          .filter(([, v]) => v !== undefined)
          .map(([k, v]) => [k, String(v)]),
      );
    return proxy(
      request,
      reply,
      upstream + kind.data + "?" + params,
      undefined,
      (raw) => {
        const page = suppressionPage.parse(raw),
          ids = new Set<string>();
        if (
          page.bk_tenant_id !== q.bk_tenant_id ||
          page.kind !== kind.data ||
          page.items.length > q.limit ||
          (page.next && page.next === q.after)
        )
          throw new Error("scope");
        for (const row of page.items) {
          if (
            row.bk_tenant_id !== q.bk_tenant_id ||
            row.kind !== kind.data ||
            !matchesSuppressionQuery(row, q) ||
            ids.has(row.id)
          )
            throw new Error("scope");
          ids.add(row.id);
        }
        return page;
      },
      1 << 20,
    );
  });
  app.get(base + "/:id", async (request, reply) => {
    const key = suppressionKey.safeParse(request.params),
      q = suppressionScope.safeParse(request.query);
    if (!key.success || !q.success)
      return reply.code(400).send({ error: { message: "抑制详情参数不合法" } });
    return proxy(
      request,
      reply,
      upstream +
        key.data.kind +
        "/" +
        encodeURIComponent(key.data.id) +
        "?" +
        new URLSearchParams(q.data),
      undefined,
      (raw) => {
        const row = suppressionWindow.parse(raw);
        if (
          row.bk_tenant_id !== q.data.bk_tenant_id ||
          row.kind !== key.data.kind ||
          row.id !== key.data.id
        )
          throw new Error("scope");
        return row;
      },
      1 << 20,
    );
  });
  app.get(base + "/:id/members", async (request, reply) => {
    const key = suppressionKey.safeParse(request.params),
      q = suppressionMemberQuery.safeParse(request.query);
    if (!key.success || !q.success)
      return reply.code(400).send({ error: { message: "抑制成员参数不合法" } });
    return proxy(
      request,
      reply,
      upstream +
        key.data.kind +
        "/" +
        encodeURIComponent(key.data.id) +
        "/members?" +
        new URLSearchParams({ ...q.data, limit: String(q.data.limit) }),
      undefined,
      (raw) => {
        const page = suppressionMembers.parse(raw);
        if (
          page.bk_tenant_id !== q.data.bk_tenant_id ||
          page.kind !== key.data.kind ||
          page.id !== key.data.id ||
          page.epoch !== q.data.epoch ||
          page.items.length > q.data.limit ||
          (page.next && page.next === q.data.after) ||
          new Set(page.items.map((m) => m.event_id)).size !== page.items.length
        )
          throw new Error("scope");
        return page;
      },
      1 << 20,
    );
  });
}
