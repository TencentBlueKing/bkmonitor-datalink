import type { FastifyInstance } from "fastify";
import { z } from "zod";
import {
  shieldCheckCommand,
  shieldCheckRequest,
  shieldCheckRequests,
  shieldLatestCheck,
} from "../shared/shield-checks.js";
import type { ConsoleConfig } from "./config.js";
import { createPolicyProxy } from "./policies.js";

// 控制命令只走管理 API；操作者取自 Console 认证，浏览器不可伪造。
export function registerShieldCheckRoutes(
  app: FastifyInstance,
  config: ConsoleConfig,
  proxy = createPolicyProxy(config),
) {
  const base = "/local-api/policy-runtime/shield/alerts/:id",
    upstream = "/api/v1/policy-runtime/shield/alerts/";
  const key = z.object({ id: z.string().min(1).max(160) }),
    scope = z
      .object({ bk_tenant_id: z.string().regex(/^[a-zA-Z0-9_-]{1,64}$/) })
      .strict();
  const invalid = (reply: import("fastify").FastifyReply) =>
    reply.code(400).send({ error: { message: "复查参数不合法" } });
  for (const suffix of ["check", "requests", "requests/:request"] as const) {
    app.get(base + "/" + suffix, async (request, reply) => {
      const k = (
          suffix === "requests/:request"
            ? key.extend({ request: z.string().regex(/^[a-f0-9]{64}$/) })
            : key
        ).safeParse(request.params),
        q = (
          suffix === "requests"
            ? scope.extend({
                after: z.string().max(8192).default(""),
                limit: z.coerce.number().int().min(1).max(4).default(4),
              })
            : scope
        ).safeParse(request.query);
      if (!k.success || !q.success) return invalid(reply);
      const scopeID = k.data.id,
        tenant = q.data.bk_tenant_id,
        requestID = "request" in k.data ? String(k.data.request) : "";
      const path =
        upstream +
        encodeURIComponent(scopeID) +
        "/" +
        (requestID ? "requests/" + requestID : suffix) +
        "?" +
        new URLSearchParams(
          Object.entries(q.data).map(([key, v]) => [key, String(v)]),
        );
      return proxy(
        request,
        reply,
        path,
        undefined,
        (raw) => {
          if (suffix === "check") {
            const v = shieldLatestCheck.parse(raw);
            if (
              v.bk_tenant_id !== tenant ||
              v.alert_id !== scopeID ||
              (v.check &&
                (v.check.bk_tenant_id !== tenant ||
                  v.check.alert_id !== scopeID))
            )
              throw new Error("scope");
            return v;
          }
          if (requestID) {
            const v = shieldCheckRequest.parse(raw);
            if (
              v.command.bk_tenant_id !== tenant ||
              v.command.alert_id !== scopeID ||
              v.id !== requestID
            )
              throw new Error("scope");
            return v;
          }
          const v = shieldCheckRequests.parse(raw);
          if (
            v.bk_tenant_id !== tenant ||
            v.alert_id !== scopeID ||
            v.items.some(
              (r) =>
                r.command.bk_tenant_id !== tenant ||
                r.command.alert_id !== scopeID,
            ) ||
            ("limit" in q.data && v.items.length > Number(q.data.limit)) ||
            (v.next && "after" in q.data && v.next === q.data.after)
          )
            throw new Error("scope");
          return v;
        },
        2 << 20,
      );
    });
  }
  app.post(base + "/reconcile", { bodyLimit: 4096 }, async (request, reply) => {
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
      c = shieldCheckCommand.safeParse(request.body);
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
      upstream + encodeURIComponent(k.data.id) + "/reconcile",
      command,
      (raw) => {
        const r = shieldCheckRequest.parse(raw);
        if (
          r.command.alert_id !== k.data.id ||
          Object.entries(command).some(
            ([key, value]) =>
              r.command[key as keyof typeof r.command] !== value,
          )
        )
          throw new Error("command");
        return r;
      },
      256 << 10,
    );
  });
}
