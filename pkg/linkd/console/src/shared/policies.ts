import { z } from "zod";

const identity = (length: number) =>
  z
    .string()
    .min(1)
    .max(length)
    .regex(/^[a-zA-Z0-9_-]+$/);
const version = z.number().int().min(0).max(Number.MAX_SAFE_INTEGER);
const object = z.record(z.string(), z.unknown());

export const policyKindSchema = z.enum(["suppression", "shield", "merge"]);
export type PolicyKind = z.infer<typeof policyKindSchema>;
export const policyScopeSchema = z.object({
  bk_tenant_id: identity(64),
  type: policyKindSchema,
});
export const policySummarySchema = z.object({
  compiler_version: version,
  digest: z.string().max(64),
  timezone: z.string().max(128),
  condition_groups: z.number().int().min(0).max(32),
  target_selectors: z.number().int().min(0).max(64),
  scheme: z.array(object).max(16).optional(),
});
export const policyReleaseSchema = policyScopeSchema.extend({
  id: identity(80),
  version: version.min(1),
  operation_id: z.string().max(128),
  request_digest: z.string().max(64),
  spec: object,
  compiled: policySummarySchema,
  deleted: z.boolean(),
  created_at: z.string(),
});
export const policyRecordSchema = policyScopeSchema
  .extend({
    id: identity(80),
    revision: version.min(1),
    published: version,
    compiled: policySummarySchema,
    spec: object.nullable(),
    deleted: z.boolean(),
    pending: policyReleaseSchema.optional(),
  })
  .superRefine((row, ctx) => {
    if (
      row.published > row.revision ||
      (row.pending &&
        (row.pending.bk_tenant_id !== row.bk_tenant_id ||
          row.pending.type !== row.type ||
          row.pending.id !== row.id ||
          row.pending.version !== row.revision ||
          row.published >= row.revision))
    )
      ctx.addIssue({
        code: "custom",
        message: "策略发布指针或待发布作用域不一致",
      });
  });
export type PolicyRecord = z.infer<typeof policyRecordSchema>;
export type PolicyRelease = z.infer<typeof policyReleaseSchema>;
export const policyPageSchema = z.object({
  items: z.array(policyRecordSchema).max(8),
  next: z.string().max(80),
});
export const policyListQuerySchema = policyScopeSchema
  .extend({
    after: z.string().max(80).default(""),
    is_enable: z.enum(["true", "false"]).optional(),
    limit: z.coerce.number().int().min(1).max(8).default(8),
  })
  .strict();
export type PolicyListQuery = z.infer<typeof policyListQuerySchema>;
export const policyPreviewRequestSchema = policyScopeSchema
  .extend({
    id: identity(80).optional(),
    version: version.min(1).optional(),
    spec: object.optional(),
    event_id: z.string().min(1).max(160).optional(),
    alert_id: z.string().min(1).max(160).optional(),
    event: object.optional(),
    alert: object.optional(),
    severity: z.string().min(1).max(32).optional(),
    at: z.string().datetime({ offset: true }).optional(),
    rely: z.boolean().optional(),
    origin_alert_id: z.string().min(1).max(160).optional(),
  })
  .strict()
  .superRefine((value, ctx) => {
    if (
      [value.event_id, value.alert_id, value.event, value.alert].filter(
        (v) => v !== undefined,
      ).length !== 1
    )
      ctx.addIssue({
        code: "custom",
        message: "必须且只能选择一个 Event/Alert 输入",
      });
    if (
      value.spec
        ? value.id !== undefined || value.version !== undefined
        : !value.id || !value.version
    )
      ctx.addIssue({
        code: "custom",
        message: "请选择精确发布版本或临时策略配置",
      });
    for (const input of [value.event, value.alert]) {
      if (input && input.bk_tenant_id !== value.bk_tenant_id)
        ctx.addIssue({
          code: "custom",
          message: "样例中的 bk_tenant_id 必须与当前租户一致",
        });
    }
    if (
      (value.rely && value.type !== "shield") ||
      (value.origin_alert_id && !value.rely)
    )
      ctx.addIssue({
        code: "custom",
        message: "主告警引用仅用于依赖屏蔽的被屏蔽条件",
      });
  });
export type PolicyPreviewRequest = z.infer<typeof policyPreviewRequestSchema>;
const match = {
  evaluated: z.boolean(),
  matched: z.boolean(),
};
export const policyPreviewSchema = z.object({
  id: z.string().max(80),
  version,
  compiled: policySummarySchema,
  mode: z.literal("matching_only"),
  at: z.string(),
  evaluations: z
    .array(
      z.object({
        severity: z.string().max(32),
        ...match,
        reason: z.string().max(512).optional(),
        group_key: z.string().max(128).optional(),
        groups: z
          .array(
            z.object({
              index: z.number().int().min(0).max(31),
              ...match,
              conditions: z
                .array(
                  z.object({
                    id: z.string().max(128),
                    ...match,
                    reason: z.string().max(512).optional(),
                  }),
                )
                .max(64),
            }),
          )
          .max(32),
      }),
    )
    .min(1)
    .max(64),
});
export type PolicyPreview = z.infer<typeof policyPreviewSchema>;
