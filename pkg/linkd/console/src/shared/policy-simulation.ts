import { z } from "zod";
import { policyScopeSchema, policySummarySchema } from "./policies.js";
const object = z.record(z.string(), z.unknown());
export const simulationStepSchema = z
  .object({
    at: z.string().datetime({ offset: true }),
    event_id: z.string().min(1).max(160).optional(),
    event: object.optional(),
  })
  .strict()
  .refine((v) => !(v.event && v.event_id), "每步只能选择一个事件输入");
export const simulationRequestSchema = policyScopeSchema
  .extend({
    id: z
      .string()
      .regex(/^[a-zA-Z0-9_-]{1,80}$/)
      .optional(),
    version: z.number().int().min(1).max(Number.MAX_SAFE_INTEGER).optional(),
    spec: object.optional(),
    steps: z.array(simulationStepSchema).min(1).max(128),
  })
  .strict()
  .superRefine((v, ctx) => {
    if (v.type === "shield")
      ctx.addIssue({ code: "custom", message: "屏蔽使用只读匹配预览" });
    if (
      v.spec
        ? v.id !== undefined || v.version !== undefined
        : !v.id || !v.version
    )
      ctx.addIssue({ code: "custom", message: "请选择精确版本或候选配置" });
    if (!v.steps.length) return;
    let previous = -1;
    const first = Date.parse(v.steps[0].at);
    for (const step of v.steps) {
      const at = Date.parse(step.at);
      if (
        at < 0 ||
        at < previous ||
        at - first > 30 * 86400000 ||
        (step.event && step.event.bk_tenant_id !== v.bk_tenant_id)
      )
        ctx.addIssue({
          code: "custom",
          message: "时间须单调递增且跨度不超过30天，事件须属于当前租户",
        });
      previous = at;
    }
  });
export type SimulationRequest = z.infer<typeof simulationRequestSchema>;

const simulationDecision = z
  .object({
    suppression: z
      .object({
        bypass_reason: z.string().optional(),
        evaluations: z
          .array(
            z
              .object({
                severity: z.string(),
                steps: z
                  .array(
                    z
                      .object({
                        scheme: z.string(),
                        outcome: z.string(),
                        count: z.number().int().optional(),
                        threshold: z.number().int().optional(),
                        reason_code: z.string().optional(),
                      })
                      .passthrough(),
                  )
                  .max(512)
                  .optional(),
              })
              .passthrough(),
          )
          .max(64)
          .optional(),
      })
      .passthrough()
      .optional(),
  })
  .passthrough();
export const simulationResponseSchema = policyScopeSchema.extend({
  id: z.string().max(80),
  version: z.number().int().nonnegative(),
  compiled: policySummarySchema,
  mode: z.literal("state_simulation"),
  steps: z
    .array(
      z.object({
        at: z.string().datetime({ offset: true }),
        event_id: z.string().max(160).optional(),
        outcome: z.string().max(80),
        replayed: z.boolean(),
        decision: simulationDecision.optional(),
        alerts: z.array(object).max(256),
        windows: z
          .array(
            z.object({
              id: z.string().regex(/^[a-f0-9]{64}$/),
              started_at: z.string(),
              deadline: z.string(),
              outcome: z.enum(["succeeded", "failed"]),
              members: z.array(z.string().max(160)).max(256),
            }),
          )
          .max(128),
      }),
    )
    .min(1)
    .max(128),
});
export type SimulationResponse = z.infer<typeof simulationResponseSchema>;
export const statisticsQuerySchema = policyScopeSchema
  .extend({
    ids: z
      .array(z.string().regex(/^[a-zA-Z0-9_-]{1,80}$/))
      .min(1)
      .max(8),
    hours: z.union([z.literal(1), z.literal(6), z.literal(24)]),
  })
  .strict()
  .refine((v) => new Set(v.ids).size === v.ids.length);
export type StatisticsQuery = z.infer<typeof statisticsQuerySchema>;
const count = z.number().int().nonnegative().max(Number.MAX_SAFE_INTEGER);
export const statisticsResponseSchema = policyScopeSchema.extend({
  from: z.string(),
  to: z.string(),
  hours: z.number().int(),
  mode: z.literal("execution_observations"),
  items: z
    .array(
      z.object({
        id: z.string().max(80),
        matched: count,
        not_matched: count,
        unavailable: count,
        execution_skipped: count,
      }),
    )
    .max(8),
});
