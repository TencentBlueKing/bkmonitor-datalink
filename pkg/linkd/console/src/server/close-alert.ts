import type { FastifyInstance } from "fastify";
import { z } from "zod";
import type { ConsoleConfig } from "./config.js";

const commandSchema = z
  .object({
    bk_tenant_id: z.string().trim().min(1).max(64),
    operation_id: z.string().uuid(),
    reason: z
      .string()
      .trim()
      .min(1)
      .refine((value) => Buffer.byteLength(value) <= 256),
    effective_at: z.string().datetime(),
  })
  .strict();

// 实体写入只代理管理 API；用户身份来自已认证的 Console，不能由浏览器伪造。
export function registerCloseAlert(
  app: FastifyInstance,
  config: ConsoleConfig,
) {
  app.post(
    "/local-api/alerts/:id/close",
    { bodyLimit: 4096 },
    async (request, reply) => {
      reply.header("Cache-Control", "no-store");
      const origin = request.headers.origin;
      if (origin) {
        try {
          const parsed = new URL(origin);
          if (
            !["http:", "https:"].includes(parsed.protocol) ||
            parsed.host !== request.headers.host
          )
            return reply
              .code(403)
              .send({ error: { message: "请求来源不匹配" } });
        } catch {
          return reply.code(403).send({ error: { message: "请求来源不匹配" } });
        }
      }
      const parsed = commandSchema.safeParse(request.body);
      const params = z
        .object({
          id: z
            .string()
            .min(1)
            .refine((value) => Buffer.byteLength(value) <= 160),
        })
        .safeParse(request.params);
      if (!parsed.success || !params.success)
        return reply
          .code(400)
          .send({ error: { message: "关闭参数无效，原因须为 1–256 字节" } });
      if (!config.dispatch?.apiToken)
        return reply
          .code(503)
          .send({ error: { message: "未配置控制面管理接口，无法关闭告警" } });
      try {
        const response = await fetch(
          `${config.dispatch.url.replace(/\/$/, "")}/api/v1/alerts/${encodeURIComponent(params.data.id)}/close`,
          {
            method: "POST",
            headers: {
              Authorization: `Bearer ${config.dispatch.apiToken}`,
              "content-type": "application/json",
            },
            body: JSON.stringify({
              ...parsed.data,
              operator_id:
                config.server.access?.basicAuth?.username ?? "console-local",
            }),
            signal: AbortSignal.timeout(
              Math.max(12000, config.query.timeoutMilliseconds),
            ),
          },
        );
        if (!response.ok) {
          await response.body?.cancel();
          const messages: Record<number, string> = {
            400: "关闭参数无效",
            401: "控制面管理鉴权失败",
            403: "租户与来源不匹配",
            404: "告警、来源或关闭接口不存在",
            409: "告警状态已变化，或来源尚未发布，请刷新详情核对",
            429: "关闭请求繁忙，请使用同一操作重试",
            503: "控制面未启用关闭能力",
          };
          return reply.code(response.status).send({
            error: {
              message:
                messages[response.status] ??
                "关闭结果尚未确认，状态可能已改变；请重试同一操作以补齐流水",
            },
          });
        }
        return z
          .object({
            alert: z.record(z.string(), z.unknown()),
            already_closed: z.boolean(),
          })
          .parse(await response.json());
      } catch {
        return reply.code(502).send({
          error: {
            message:
              "关闭结果尚未确认，状态可能已改变；请重试同一操作以补齐流水",
          },
        });
      }
    },
  );
}
