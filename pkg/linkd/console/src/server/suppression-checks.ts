import type { FastifyInstance } from "fastify";
import { z } from "zod";
import type { ConsoleConfig } from "./config.js";
import { createPolicyProxy } from "./policies.js";
import {
  suppressionKey,
  suppressionScope,
} from "../shared/suppression-runtime.js";
import {
  suppressionCheckCommand,
  suppressionCheckPage,
  suppressionCheckRequest,
} from "../shared/suppression-checks.js";
// 固定路由和完整命令；认证操作者只由 Console 服务端提供。
export function registerSuppressionCheckRoutes(
  app: FastifyInstance,
  config: ConsoleConfig,
  proxy = createPolicyProxy(config, 16, "抑制对账"),
) {
  const base = "/local-api/policy-runtime/suppression/:kind/:id",
    upstream = "/api/v1/policy-runtime/suppression/";
  const url = (kind: string, id: string) =>
    upstream + kind + "/" + encodeURIComponent(id);
  for (const exact of [false, true])
    app.get(
      base + "/requests" + (exact ? "/:request" : ""),
      async (req, reply) => {
        const raw = req.params as Record<string, unknown>,
          k = suppressionKey.safeParse({ kind: raw.kind, id: raw.id }),
          requestID = exact
            ? z
                .string()
                .regex(/^[a-f0-9]{64}$/)
                .safeParse(raw.request)
            : undefined;
        const q = (
          exact
            ? suppressionScope
            : suppressionScope.extend({
                after: z.string().max(2048).default(""),
                limit: z.coerce.number().int().min(1).max(4).default(4),
              })
        ).safeParse(req.query);
        if (!k.success || !q.success || (requestID && !requestID.success))
          return reply
            .code(400)
            .send({ error: { message: "对账查询参数不合法" } });
        const params = new URLSearchParams(
            Object.entries(q.data).map(([k, v]) => [k, String(v)]),
          ),
          id = requestID?.data;
        return proxy(
          req,
          reply,
          url(k.data.kind, k.data.id) +
            "/requests" +
            (id ? "/" + id : "") +
            "?" +
            params,
          undefined,
          (data) => {
            const matches = (r: z.infer<typeof suppressionCheckRequest>) =>
              r.command.bk_tenant_id === q.data.bk_tenant_id &&
              r.command.kind === k.data.kind &&
              r.command.window_id === k.data.id;
            if (id) {
              const r = suppressionCheckRequest.parse(data);
              if (r.id !== id || !matches(r)) throw new Error("scope");
              return r;
            }
            const p = suppressionCheckPage.parse(data);
            if (
              p.bk_tenant_id !== q.data.bk_tenant_id ||
              p.kind !== k.data.kind ||
              p.window_id !== k.data.id ||
              p.items.some((r) => !matches(r)) ||
              ("limit" in q.data && p.items.length > Number(q.data.limit)) ||
              (p.next && "after" in q.data && p.next === q.data.after)
            )
              throw new Error("scope");
            return p;
          },
          256 << 10,
        );
      },
    );
  app.post(base + "/reconcile", { bodyLimit: 4096 }, async (req, reply) => {
    reply.header("Cache-Control", "no-store");
    if (req.headers.origin) {
      try {
        const u = new URL(req.headers.origin);
        if (
          !["http:", "https:"].includes(u.protocol) ||
          u.host !== req.headers.host
        )
          return reply.code(403).send({ error: { message: "请求来源不匹配" } });
      } catch {
        return reply.code(403).send({ error: { message: "请求来源不匹配" } });
      }
    }
    const key = suppressionKey.safeParse(req.params),
      c = suppressionCheckCommand.safeParse(req.body);
    if (
      !key.success ||
      !c.success ||
      !z.object({}).strict().safeParse(req.query).success ||
      (key.data.kind === "aggregation" && !c.data.expected_owner_alert_id)
    )
      return reply.code(400).send({ error: { message: "对账命令不合法" } });
    const command = {
      ...c.data,
      operator_id:
        config.server?.access?.basicAuth?.username ?? "console-local",
    };
    return proxy(
      req,
      reply,
      url(key.data.kind, key.data.id) + "/reconcile",
      command,
      (data) => {
        const r = suppressionCheckRequest.parse(data);
        if (
          r.command.kind !== key.data.kind ||
          r.command.window_id !== key.data.id ||
          Object.entries(command).some(
            ([k, v]) => r.command[k as keyof typeof r.command] !== v,
          )
        )
          throw new Error("scope");
        return r;
      },
      64 << 10,
    );
  });
}
