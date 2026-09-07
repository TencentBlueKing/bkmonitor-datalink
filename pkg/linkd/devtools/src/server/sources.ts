import type { FastifyInstance } from "fastify";
import { z } from "zod";
import type { DevtoolsConfig } from "./config.js";

// 管理写入只代理正式控制面，不直接修改来源存储；token 不进入浏览器。
export function registerSourceRoutes(
  app: FastifyInstance,
  config: DevtoolsConfig,
) {
  const params = z.object({ id: z.string().regex(/^[a-zA-Z0-9_-]{1,32}$/) });
  app.route({
    method: ["GET", "PUT", "DELETE"],
    url: "/local-api/event-sources/:id",
    async handler(request, reply) {
      const { id } = params.parse(request.params);
      if (request.method !== "GET") {
        const origin = request.headers.origin;
        if (origin && new URL(origin).host !== request.headers.host)
          return reply.code(403).send({ error: { message: "请求来源不匹配" } });
      }
      return proxy(
        request.method,
        `/api/v1/event-sources/${encodeURIComponent(id)}`,
        request.body,
        reply,
      );
    },
  });
  app.get("/local-api/event-sources", async (request, reply) => {
    const query = z
      .object({
        after: z.string().max(256).default(""),
        limit: z.coerce.number().int().min(1).max(1000).default(100),
      })
      .parse(request.query);
    return proxy(
      "GET",
      `/api/v1/event-sources?${new URLSearchParams({ after: query.after, limit: String(query.limit) })}`,
      undefined,
      reply,
    );
  });
  app.get("/local-api/scheduling", async (_request, reply) =>
    proxy("GET", "/api/v1/runtime", undefined, reply),
  );
  async function proxy(
    method: string,
    path: string,
    body: unknown,
    reply: { code(value: number): { send(value: unknown): unknown } },
  ) {
    if (!config.dispatch?.apiToken)
      return reply.code(503).send({
        error: { message: "请在 Linkd 配置中设置 dispatch.url 与 api_token" },
      });
    try {
      const response = await fetch(
        `${config.dispatch.url.replace(/\/$/, "")}${path}`,
        {
          method,
          headers: {
            Authorization: `Bearer ${config.dispatch.apiToken}`,
            "Content-Type": "application/json",
          },
          body: body === undefined ? undefined : JSON.stringify(body),
          signal: AbortSignal.timeout(config.query.timeoutMilliseconds),
        },
      );
      if (!response.ok)
        return reply.code(response.status).send({
          error: {
            message:
              response.status === 409
                ? "配置版本已变化，请刷新后重试"
                : `控制面请求失败（${response.status}）`,
          },
        });
      return reply.code(response.status).send(await response.json());
    } catch {
      return reply.code(502).send({ error: { message: "控制面不可用" } });
    }
  }
}
