import { z } from "zod";
import type { FastifyInstance } from "fastify";
import type { ConsoleConfig } from "./config.js";
import { createPolicyProxy } from "./policies.js";
import {
  simulationRequestSchema,
  simulationResponseSchema,
  statisticsQuerySchema,
  statisticsResponseSchema,
} from "../shared/policy-simulation.js";

export function registerPolicyDiagnostics(
  app: FastifyInstance,
  config: ConsoleConfig,
) {
  const proxy = createPolicyProxy(config, 0, "策略模拟");
  const statsProxy = createPolicyProxy(config, 2, "策略统计");
  app.post(
    "/local-api/policies/simulate",
    { bodyLimit: 3 << 20 },
    async (request, reply) => {
      if (request.headers.origin) {
        try {
          const origin = new URL(request.headers.origin);
          if (
            !["http:", "https:"].includes(origin.protocol) ||
            origin.host !== request.headers.host
          )
            return reply
              .code(403)
              .send({ error: { message: "请求来源不匹配" } });
        } catch {
          return reply.code(403).send({ error: { message: "请求来源不匹配" } });
        }
      }
      const parsed = simulationRequestSchema.safeParse(request.body);
      if (!parsed.success)
        return reply.code(400).send({ error: { message: "模拟输入不合法" } });
      const q = parsed.data;
      return proxy(
        request,
        reply,
        "/api/v1/policies/simulate",
        q,
        (raw) => {
          const r = simulationResponseSchema.parse(raw);
          if (
            r.bk_tenant_id !== q.bk_tenant_id ||
            r.type !== q.type ||
            r.id !== (q.spec ? "preview" : q.id) ||
            r.version !== (q.spec ? 0 : q.version) ||
            r.steps.length !== q.steps.length
          )
            throw new Error("scope");
          for (let i = 0; i < r.steps.length; i++) {
            const s = r.steps[i],
              input = q.steps[i];
            if (
              Date.parse(s.at) !== Date.parse(input.at) ||
              s.event_id !== (input.event_id ?? input.event?.event_id) ||
              s.alerts.some((a) => a.bk_tenant_id !== q.bk_tenant_id)
            )
              throw new Error("step scope");
          }
          return r;
        },
        8 << 20,
      );
    },
  );
  app.get("/local-api/policies/statistics", async (request, reply) => {
    const raw = z
      .object({
        bk_tenant_id: z.string(),
        type: z.string(),
        ids: z.string().max(647),
        hours: z.coerce.number(),
      })
      .strict()
      .safeParse(request.query);
    if (!raw.success)
      return reply.code(400).send({ error: { message: "统计查询不合法" } });
    const parsed = statisticsQuerySchema.safeParse({
      ...raw.data,
      ids: raw.data.ids.split(","),
    });
    if (!parsed.success)
      return reply.code(400).send({ error: { message: "统计查询不合法" } });
    const q = parsed.data;
    const params = new URLSearchParams({
      bk_tenant_id: q.bk_tenant_id,
      type: q.type,
      ids: q.ids.join(","),
      hours: String(q.hours),
    });
    return statsProxy(
      request,
      reply,
      "/api/v1/policies/statistics?" + params,
      undefined,
      (raw) => {
        const r = statisticsResponseSchema.parse(raw);
        if (
          r.bk_tenant_id !== q.bk_tenant_id ||
          r.type !== q.type ||
          r.hours !== q.hours ||
          r.items.length !== q.ids.length ||
          r.items.some((v, i) => v.id !== q.ids[i])
        )
          throw new Error("scope");
        return r;
      },
      32 << 10,
    );
  });
}
