import { z } from "zod";
import type { FastifyInstance } from "fastify";
import type { ConsoleConfig } from "./config.js";
import {
  policyLinkQuerySchema,
  policyLinkSchema,
  validPolicyURL,
  type PolicyLink,
  type PolicyLinkQuery,
} from "../shared/policy-links.js";

const placeholders = /\{(policy_id|bk_tenant_id|space_code)\}/g;
const template = z.string().refine((value) => {
  const replaced = value.replace(placeholders, "linkd-reference");
  // 占位符只能出现在路径/查询/hash 中，不允许输入改变目的站点。
  return (
    value.length <= 2048 &&
    !/[{}]/.test(replaced) &&
    !/^[a-z]+:\/\/[^/?#]*\{/i.test(value) &&
    validPolicyURL(replaced)
  );
});
const linkConfigSchema = z
  .record(
    z.string().regex(/^[a-zA-Z0-9_-]{1,64}$/),
    z
      .object({
        suppression: template.optional(),
        shield: template.optional(),
        merge: template.optional(),
      })
      .strict(),
  )
  .refine((value) => Object.keys(value).length <= 64);
export type PolicyLinksConfig = z.infer<typeof linkConfigSchema>;

// 环境变量只定义租户到 KAC 页面入口的映射；不推断 KAC 与 Linkd 的策略 ID 对应关系。
export function loadPolicyLinks(raw: string | undefined): PolicyLinksConfig {
  if (raw === undefined) return {};
  try {
    if (Buffer.byteLength(raw) > 64 << 10) throw new Error("budget");
    return linkConfigSchema.parse(JSON.parse(raw));
  } catch {
    // 配置错误不能把 URL 中误填的凭据带到启动日志。
    throw new Error("invalid LINKD_CONSOLE_KAC_POLICY_LINKS");
  }
}

export function resolvePolicyLink(
  config: PolicyLinksConfig,
  query: PolicyLinkQuery,
): PolicyLink {
  const scope = { bk_tenant_id: query.bk_tenant_id, type: query.type };
  const value = Object.hasOwn(config, query.bk_tenant_id)
    ? config[query.bk_tenant_id]?.[query.type]
    : undefined;
  if (!value) return { ...scope, url: null, reason: "not_configured" };
  const context: Record<string, string | undefined> = {
    policy_id: query.id,
    bk_tenant_id: query.bk_tenant_id,
    space_code: query.space_code,
  };
  let complete = true;
  const url = value.replace(placeholders, (_, key: string) => {
    if (!context[key]) complete = false;
    return encodeURIComponent(context[key] ?? "");
  });
  if (!complete) return { ...scope, url: null, reason: "missing_context" };
  return policyLinkSchema.parse({ ...scope, url });
}

export function registerPolicyLinkRoutes(
  app: FastifyInstance,
  config: ConsoleConfig,
) {
  // 只返回所请求租户/类型的入口；不公开全部租户映射，不请求 KAC，也不替用户切换登录租户。
  app.get("/local-api/policy-links", (request, reply) => {
    reply.header("Cache-Control", "no-store");
    const query = policyLinkQuerySchema.safeParse(request.query);
    if (!query.success)
      return reply
        .code(400)
        .send({ error: { message: "配置入口查询参数无效" } });
    return resolvePolicyLink(config.policyLinks ?? {}, query.data);
  });
}
