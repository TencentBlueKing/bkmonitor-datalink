import type { FastifyInstance } from "fastify";
import { z } from "zod";
import {
  matchesProjectionQuery,
  projectionPage,
  projectionQuery,
  projectionRetryCommand,
  projectionSnapshot,
  projectionTask,
} from "../shared/projection-tasks.js";
import { createPolicyProxy } from "./policies.js";
import type { ConsoleConfig } from "./config.js";
// 投影管理只代理固定管理路由；冻结快照单独读取，认证信息不进入浏览器。
export function registerProjectionTaskRoutes(
  app: FastifyInstance,
  config: ConsoleConfig,
) {
  const proxy = createPolicyProxy(config, 16, "投影任务"),
    base = "/local-api/projection-tasks",
    upstream = "/api/v1/projection-tasks";
  const key = z.object({ id: z.string().regex(/^[a-f0-9]{64}$/) }),
    scope = z
      .object({ bk_tenant_id: z.string().regex(/^[a-zA-Z0-9_-]{1,64}$/) })
      .strict();
  const invalid = (reply: import("fastify").FastifyReply) =>
    reply.code(400).send({ error: { message: "投影任务参数不合法" } });
  app.get(base, async (request, reply) => {
    const q = projectionQuery.safeParse(request.query);
    if (!q.success) return invalid(reply);
    return proxy(
      request,
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
        const p = projectionPage.parse(raw);
        let last = "";
        if (
          p.bk_tenant_id !== q.data.bk_tenant_id ||
          p.items.length > q.data.limit ||
          (p.next && p.next === q.data.after)
        )
          throw new Error("scope");
        for (const r of p.items) {
          if (
            r.bk_tenant_id !== q.data.bk_tenant_id ||
            r.id <= last ||
            !matchesProjectionQuery(r, q.data)
          )
            throw new Error("scope");
          last = r.id;
        }
        return p;
      },
      1 << 20,
    );
  });
  for (const suffix of ["", "/snapshot"]) {
    app.get(base + "/:id" + suffix, async (request, reply) => {
      const k = key.safeParse(request.params),
        q = scope.safeParse(request.query);
      if (!k.success || !q.success) return invalid(reply);
      return proxy(
        request,
        reply,
        upstream + "/" + k.data.id + suffix + "?" + new URLSearchParams(q.data),
        undefined,
        (raw) => {
          if (suffix) {
            const s = projectionSnapshot.parse(raw);
            if (
              s.bk_tenant_id !== q.data.bk_tenant_id ||
              s.id !== k.data.id ||
              s.alert.bk_tenant_id !== s.bk_tenant_id ||
              s.alert.revision !== s.revision
            )
              throw new Error("scope");
            return s;
          }
          const r = projectionTask.parse(raw);
          if (r.bk_tenant_id !== q.data.bk_tenant_id || r.id !== k.data.id)
            throw new Error("scope");
          return r;
        },
        suffix ? (1 << 20) + (16 << 10) : 1 << 20,
      );
    });
  }
  app.post(base + "/:id/retry", { bodyLimit: 4096 }, async (request, reply) => {
    reply.header("Cache-Control", "no-store");
    if (request.headers.origin) {
      try {
        const origin = new URL(request.headers.origin);
        if (
          !["http:", "https:"].includes(origin.protocol) ||
          origin.host !== request.headers.host
        )
          return reply.code(403).send({ error: { message: "请求来源不匹配" } });
      } catch {
        return reply.code(403).send({ error: { message: "请求来源不匹配" } });
      }
    }
    const k = key.safeParse(request.params),
      c = projectionRetryCommand.safeParse(request.body);
    if (
      !k.success ||
      !c.success ||
      !z.object({}).strict().safeParse(request.query).success
    )
      return invalid(reply);
    const command = {
      ...c.data,
      operator_id:
        config.server?.access?.basicAuth?.username ?? "console-local",
    };
    return proxy(
      request,
      reply,
      upstream + "/" + k.data.id + "/retry",
      command,
      (raw) => {
        const r = projectionTask.parse(raw);
        const audit = r.progress.last_retry;
        if (
          r.bk_tenant_id !== c.data.bk_tenant_id ||
          r.id !== k.data.id ||
          !audit ||
          audit.command.task_id !== k.data.id ||
          Object.entries(command).some(
            ([key, v]) =>
              audit.command[key as keyof typeof audit.command] !== v,
          )
        )
          throw new Error("scope");
        return r;
      },
      64 << 10,
    );
  });
}
