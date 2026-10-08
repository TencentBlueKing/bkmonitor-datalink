import { boundedJSON } from "./http-response.js";
import type { FastifyInstance, FastifyReply, FastifyRequest } from "fastify";
import { z } from "zod";
import {
  policyKindSchema,
  policyListQuerySchema,
  policyPageSchema,
  policyPreviewRequestSchema,
  policyPreviewSchema,
  policyRecordSchema,
  policyReleaseSchema,
} from "../shared/policies.js";
import type { ConsoleConfig } from "./config.js";
import { internalTokenHeaders } from "./internal-token.js";

const tenantQuery = z
  .object({
    bk_tenant_id: z.string().regex(/^[a-zA-Z0-9_-]{1,64}$/),
  })
  .strict();
const key = z.object({
  type: policyKindSchema,
  id: z.string().regex(/^[a-zA-Z0-9_-]{1,80}$/),
});
const maxResponse = 64 << 20;

// 配置与运行态查询各有独立的两并发额度；认证、取消和响应预算遵守同一控制面边界。
export function createPolicyProxy(
  config: ConsoleConfig,
  maxQueuedRequests = 0,
  subject = "策略",
) {
  if (
    !Number.isInteger(maxQueuedRequests) ||
    maxQueuedRequests < 0 ||
    maxQueuedRequests > 16
  )
    throw new Error("invalid policy proxy queue budget");
  let inflight = 0;
  const waiting: Array<{ grant: () => void }> = [];
  const capacity = new Error("policy proxy capacity");
  // 默认直接背压；屏蔽详情的多个读取端口显式共用此队列，避免一次页面刷新互相打满控制面预算。
  // 等待和 fetch 共用同一取消/总超时；排队数有硬上限，不积累已断开的浏览器请求。
  function release() {
    const next = waiting.shift();
    if (next) next.grant();
    else inflight--;
  }
  function acquire(signal: AbortSignal): Promise<() => void> {
    if (signal.aborted) return Promise.reject(signal.reason);
    if (inflight < 2) {
      inflight++;
      return Promise.resolve(release);
    }
    if (waiting.length >= maxQueuedRequests) return Promise.reject(capacity);
    return new Promise((resolve, reject) => {
      const canceled = () => {
        const index = waiting.indexOf(item);
        if (index >= 0) waiting.splice(index, 1);
        reject(signal.reason);
      };
      const item = {
        grant: () => {
          signal.removeEventListener("abort", canceled);
          resolve(release);
        },
      };
      waiting.push(item);
      signal.addEventListener("abort", canceled, { once: true });
    });
  }
  async function proxy(
    request: FastifyRequest,
    reply: FastifyReply,
    path: string,
    body: unknown,
    parse: (value: unknown) => unknown,
    bytes = maxResponse,
  ) {
    reply.header("Cache-Control", "no-store");
    if (!config.dispatch?.jwt.secretKey)
      return reply.code(503).send({ error: { message: "控制面连接尚未配置" } });
    const abort = new AbortController();
    const canceled = () => abort.abort();
    const disconnected = () => {
      if (!reply.raw.writableEnded) abort.abort();
    };
    request.raw.once("aborted", canceled);
    reply.raw.once("close", disconnected);
    const signal = AbortSignal.any([
      abort.signal,
      AbortSignal.timeout(Math.min(15000, config.query.timeoutMilliseconds)),
    ]);
    let permit: (() => void) | undefined;
    try {
      permit = await acquire(signal);
      signal.throwIfAborted();
      const response = await fetch(
        config.dispatch.url.replace(/\/$/, "") + path,
        {
          method: body === undefined ? "GET" : "POST",
          headers: {
            ...internalTokenHeaders(config.dispatch.jwt),
            "Content-Type": "application/json",
          },
          body: body === undefined ? undefined : JSON.stringify(body),
          redirect: "error",
          signal,
        },
      );
      if (!response.ok) {
        await response.body?.cancel();
        const messages: Record<number, string> = {
          400: "配置或输入不合法，请检查租户、版本和样例字段",
          401: "控制面认证失败",
          403: `当前租户无权访问该${subject}或输入`,
          404: `未找到当前租户中的${subject}、版本或输入`,
          409: "记录版本已变化或操作身份冲突，请刷新后检查",
          429: `${subject}请求繁忙，请稍后重试`,
          503: `${subject}服务或依赖暂不可用`,
          504: `${subject}查询超时`,
        };
        return reply
          .code(
            response.status >= 400 && response.status <= 599
              ? response.status
              : 502,
          )
          .send({
            error: {
              message: messages[response.status] ?? `控制面${subject}请求失败`,
            },
          });
      }
      const value = parse(await boundedJSON(response, bytes));
      return reply.code(response.status).send(value);
    } catch (error) {
      if (error === capacity)
        return reply
          .code(429)
          .send({ error: { message: `${subject}请求繁忙，请稍后重试` } });
      return reply.code(502).send({
        error: { message: `${subject}响应不可用、超限或与请求作用域不一致` },
      });
    } finally {
      permit?.();
      request.raw.off("aborted", canceled);
      reply.raw.off("close", disconnected);
    }
  }
  return proxy;
}

// 只注册读取和预览路由，KAC 是配置管理入口；JWT 仅由服务端签发。
export function registerPolicyRoutes(
  app: FastifyInstance,
  config: ConsoleConfig,
) {
  const proxy = createPolicyProxy(config);
  const invalid = (reply: FastifyReply) =>
    reply
      .code(400)
      .send({ error: { message: "策略查询参数或预览输入不合法" } });
  app.get("/local-api/policies", async (request, reply) => {
    const result = policyListQuerySchema.safeParse(request.query);
    if (!result.success) return invalid(reply);
    const q = result.data;
    const params = new URLSearchParams({
      bk_tenant_id: q.bk_tenant_id,
      type: q.type,
      after: q.after,
      limit: String(q.limit),
    });
    if (q.is_enable !== undefined) params.set("is_enable", q.is_enable);
    return proxy(
      request,
      reply,
      "/api/v1/policies?" + params,
      undefined,
      (raw) => {
        const page = policyPageSchema.parse(raw);
        let last = q.after;
        for (const row of page.items) {
          if (
            row.bk_tenant_id !== q.bk_tenant_id ||
            row.type !== q.type ||
            row.id <= last
          )
            throw new Error("scope");
          last = row.id;
        }
        // 启用筛选可产生空页，next 仍由原始扫描页推进。
        if (
          page.items.length > q.limit ||
          (page.next && (page.next <= q.after || page.next < last))
        )
          throw new Error("cursor");
        return page;
      },
    );
  });
  app.get("/local-api/policies/:type/:id", async (request, reply) => {
    const k = key.safeParse(request.params),
      q = tenantQuery.safeParse(request.query);
    if (!k.success || !q.success) return invalid(reply);
    return proxy(
      request,
      reply,
      "/api/v1/policies/" +
        k.data.type +
        "/" +
        k.data.id +
        "?" +
        new URLSearchParams(q.data),
      undefined,
      (raw) => {
        const row = policyRecordSchema.parse(raw);
        if (
          row.bk_tenant_id !== q.data.bk_tenant_id ||
          row.id !== k.data.id ||
          row.type !== k.data.type
        )
          throw new Error("scope");
        return row;
      },
      9 << 20,
    );
  });
  app.get(
    "/local-api/policies/:type/:id/releases/:version",
    async (request, reply) => {
      const k = key
        .extend({
          version: z.coerce.number().int().min(1).max(Number.MAX_SAFE_INTEGER),
        })
        .safeParse(request.params);
      const q = tenantQuery.safeParse(request.query);
      if (!k.success || !q.success) return invalid(reply);
      return proxy(
        request,
        reply,
        "/api/v1/policies/" +
          k.data.type +
          "/" +
          k.data.id +
          "/releases/" +
          k.data.version +
          "?" +
          new URLSearchParams(q.data),
        undefined,
        (raw) => {
          const row = policyReleaseSchema.parse(raw);
          if (
            row.bk_tenant_id !== q.data.bk_tenant_id ||
            row.id !== k.data.id ||
            row.type !== k.data.type ||
            row.version !== k.data.version
          )
            throw new Error("scope");
          return row;
        },
        4 << 20,
      );
    },
  );
  app.post(
    "/local-api/policies/preview",
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
      const q = policyPreviewRequestSchema.safeParse(request.body);
      if (!q.success) return invalid(reply);
      return proxy(
        request,
        reply,
        "/api/v1/policies/preview",
        q.data,
        (raw) => {
          const result = policyPreviewSchema.parse(raw);
          if (
            q.data.spec
              ? result.id !== "preview" || result.version !== 0
              : result.id !== q.data.id || result.version !== q.data.version
          )
            throw new Error("preview identity");
          return result;
        },
        2 << 20,
      );
    },
  );
}
