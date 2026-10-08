import { z } from "zod";
import { policyScopeSchema } from "./policies.js";

export const policyLinkQuerySchema = policyScopeSchema
  .extend({
    id: z
      .string()
      .regex(/^[a-zA-Z0-9_-]{1,80}$/)
      .optional(),
    space_code: z.string().trim().min(1).max(128).optional(),
  })
  .strict();
export type PolicyLinkQuery = z.infer<typeof policyLinkQuerySchema>;

// 链接是浏览器导航，不能携带控制面的凭据；查询串和 hash 路由使用同样的检查。
export function validPolicyURL(value: string): boolean {
  if (
    value.length > 2048 ||
    !/^https?:\/\//i.test(value) ||
    /[\s\\]/.test(value)
  )
    return false;
  try {
    const url = new URL(value);
    if (
      !["http:", "https:"].includes(url.protocol) ||
      url.username ||
      url.password
    )
      return false;
    const query = new URLSearchParams(url.hash.split("?").slice(1).join("?"));
    return [...url.searchParams.keys(), ...query.keys()].every(
      (key) =>
        !/token|secret|password|authorization|api[_-]?key|signature/i.test(key),
    );
  } catch {
    return false;
  }
}

export const policyLinkSchema = policyScopeSchema
  .extend({
    url: z.string().refine(validPolicyURL).nullable(),
    reason: z.enum(["not_configured", "missing_context"]).optional(),
  })
  .superRefine((value, ctx) => {
    if ((value.url === null) !== (value.reason !== undefined))
      ctx.addIssue({ code: "custom", message: "配置入口状态不一致" });
  });
export type PolicyLink = z.infer<typeof policyLinkSchema>;
