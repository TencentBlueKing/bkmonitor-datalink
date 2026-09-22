import type { FastifyInstance } from "fastify";
import { z } from "zod";
import type { ConsoleConfig } from "./config.js";

// 管理写入只代理正式控制面，不直接修改来源存储；token 不进入浏览器。
export function registerSourceRoutes(
  app: FastifyInstance,
  config: ConsoleConfig,
) {
  const params = z.object({ id: z.string().regex(/^[a-zA-Z0-9_-]{1,32}$/) });
  app.route({
    method: ["GET", "PUT", "DELETE"],
    url: "/local-api/event-sources/:id",
    async handler(request, reply) {
      const { id } = params.parse(request.params);
      if (request.method !== "GET") {
        const origin = request.headers.origin;
        if (origin) {
          let matches = false;
          try {
            const url = new URL(origin);
            matches =
              ["http:", "https:"].includes(url.protocol) &&
              url.host === request.headers.host;
          } catch {
            /* 畸形 Origin 与跨源请求统一拒绝，不返回原始输入。 */
          }
          if (!matches)
            return reply
              .code(403)
              .send({ error: { message: "请求来源不匹配" } });
        }
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
  app.get("/local-api/metrics/catalog", async (_request, reply) =>
    proxy("GET", "/api/v1/metrics/catalog", undefined, reply),
  );
  app.get("/local-api/dynamic-config", async (_request, reply) =>
    proxy("GET", "/api/v1/dynamic-config", undefined, reply),
  );
  app.post(
    "/local-api/enrich/preview",
    { bodyLimit: 1 << 20 },
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
      return proxy("POST", "/api/v1/enrich/preview", request.body, reply);
    },
  );
  app.get("/local-api/enrich/config/:id", async (request, reply) => {
    const { id } = params.parse(request.params);
    if (!config.dispatch?.apiToken)
      return reply.code(503).send({ error: { message: "控制面未配置" } });
    try {
      const get = async (path: string) => {
        const response = await fetch(
          `${config.dispatch!.url.replace(/\/$/, "")}${path}`,
          {
            headers: { Authorization: `Bearer ${config.dispatch!.apiToken}` },
            signal: AbortSignal.timeout(config.query.timeoutMilliseconds),
          },
        );
        if (!response.ok) throw new Error("read failed");
        return response.json() as Promise<unknown>;
      };
      const record = z
        .object({ published: z.number(), deleted: z.boolean() })
        .parse(await get(`/api/v1/event-sources/${id}`));
      if (record.deleted || record.published <= 0)
        return reply.code(404).send({ error: { message: "来源尚未发布" } });
      const release = z
        .object({
          spec: z.object({
            enrich: z
              .object({ processors: z.array(z.unknown()).optional() })
              .optional(),
          }),
        })
        .parse(
          await get(`/api/v1/event-sources/${id}/releases/${record.published}`),
        );
      return { enrich: { processors: release.spec.enrich?.processors ?? [] } };
    } catch {
      return reply.code(502).send({ error: { message: "加载已发布配置失败" } });
    }
  });
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
      if (!response.ok && path === "/api/v1/enrich/preview") {
        const error = z
          .object({ error: z.object({ message: z.string().max(4096) }) })
          .safeParse(await response.json().catch(() => null));
        return reply.code(response.status).send({
          error: {
            message: error.success
              ? error.data.error.message
              : `预览失败（${response.status}）`,
          },
        });
      }
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
