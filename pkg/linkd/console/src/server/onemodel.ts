import type { FastifyInstance } from "fastify";
import { z } from "zod";
import type { ConsoleConfig } from "./config.js";

// OneModel 领域查询由控制面执行，浏览器不能指定连接、索引或管理 token。
export function registerOneModelRoutes(
  app: FastifyInstance,
  config: ConsoleConfig,
) {
  for (const operation of ["search", "related", "close"] as const) {
    app.post(
      `/local-api/onemodel/${operation}`,
      { bodyLimit: 1 << 20 },
      async (request, reply) => {
        reply.header("Cache-Control", "no-store");
        if (request.headers.origin) {
          try {
            const origin = new URL(request.headers.origin);
            if (
              !["http:", "https:"].includes(origin.protocol) ||
              origin.host !== request.headers.host
            ) {
              return reply
                .code(403)
                .send({ error: { message: "请求来源不匹配" } });
            }
          } catch {
            return reply
              .code(403)
              .send({ error: { message: "请求来源不匹配" } });
          }
        }
        if (!config.dispatch?.apiToken)
          return reply
            .code(503)
            .send({ error: { message: "请配置 dispatch.url 与 api_token" } });
        const abort = new AbortController();
        const canceled = () => abort.abort();
        const disconnected = () => {
          if (!reply.raw.writableEnded) abort.abort();
        };
        request.raw.once("aborted", canceled);
        reply.raw.once("close", disconnected);
        try {
          const response = await fetch(
            `${config.dispatch.url.replace(/\/$/, "")}/api/v1/onemodel/${operation}`,
            {
              method: "POST",
              headers: {
                Authorization: `Bearer ${config.dispatch.apiToken}`,
                "Content-Type": "application/json",
              },
              body: JSON.stringify(request.body),
              signal: AbortSignal.any([
                abort.signal,
                AbortSignal.timeout(
                  Math.min(15000, config.query.timeoutMilliseconds),
                ),
              ]),
            },
          );
          const value: unknown = await response.json().catch(() => null);
          if (!response.ok) {
            const error = z
              .object({ error: z.object({ message: z.string().max(4096) }) })
              .safeParse(value);
            return reply.code(response.status).send({
              error: {
                message: error.success
                  ? error.data.error.message
                  : `OneModel 查询失败（${response.status}）`,
              },
            });
          }
          return reply.send(value);
        } catch {
          return reply
            .code(abort.signal.aborted ? 504 : 502)
            .send({ error: { message: "控制面查询不可用或请求已取消" } });
        } finally {
          request.raw.off("aborted", canceled);
          reply.raw.off("close", disconnected);
        }
      },
    );
  }
}
