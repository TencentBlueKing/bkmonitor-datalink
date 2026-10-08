import type { FastifyInstance } from "fastify";
import type { ConsoleConfig } from "./config.js";
import { validPolicyURL } from "../shared/policy-links.js";
import {
  kacAlertLinkQuery,
  kacAlertLinkResponse,
} from "../shared/kac-alert-link.js";

const placeholders = /\{(alarm_id|bk_tenant_id)\}/g;
// 可选全局导航模板。只在路径、查询或 hash 中替换经过编码的业务身份，绝不发送服务端请求。
export function loadKACAlertLink(raw: string | undefined): string | undefined {
  if (raw === undefined || raw === "") return undefined;
  const resolved = raw.replace(placeholders, "reference");
  if (
    raw.length > 2048 ||
    !raw.includes("{alarm_id}") ||
    /[{}]/.test(resolved) ||
    /^[a-z]+:\/\/[^/?#]*\{/i.test(raw) ||
    !validPolicyURL(resolved)
  )
    throw new Error("invalid LINKD_CONSOLE_KAC_ALERT_URL_TEMPLATE");
  return raw;
}
export function registerKACAlertLinkRoute(
  app: FastifyInstance,
  config: ConsoleConfig,
) {
  app.get("/local-api/kac-alert-link", (request, reply) => {
    reply.header("Cache-Control", "no-store");
    const query = kacAlertLinkQuery.safeParse(request.query);
    if (!query.success)
      return reply.code(400).send({ error: { message: "告警入口参数无效" } });
    const url =
      config.kacAlertURLTemplate?.replace(
        placeholders,
        (_, key: "alarm_id" | "bk_tenant_id") =>
          encodeURIComponent(query.data[key]),
      ) ?? null;
    return kacAlertLinkResponse.parse({ ...query.data, url });
  });
}
