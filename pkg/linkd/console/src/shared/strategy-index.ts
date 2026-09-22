import { z } from "zod";

export const strategyTargetSchema = z.object({
  eventSourceId: z.string(),
  hookName: z.string(),
  notifyChannel: z.string().optional(),
  keyPrefix: z.string(),
  address: z.string(),
  database: z.number(),
  sources: z.array(z.string()),
});
export const strategyTargetsSchema = z.array(strategyTargetSchema);
export type StrategyTarget = z.infer<typeof strategyTargetSchema>;
export const strategyQuerySchema = z
  .object({
    event_source_id: z.string().regex(/^[a-zA-Z0-9_-]{1,32}$/),
    hook_name: z.string().regex(/^[a-zA-Z0-9][a-zA-Z0-9_-]{0,63}$/),
    bk_tenant_id: z.string().min(1).max(256),
    strategy_id: z.string().min(1).max(1024),
  })
  .strict();
export type StrategyQuery = z.infer<typeof strategyQuerySchema>;
export const strategyAlertSchema = z.object({
  alertId: z.string(),
  eventSourceId: z.string(),
  fingerprint: z.string(),
});
export type StrategyAlert = z.infer<typeof strategyAlertSchema>;
export const strategyResultSchema = z.object({
  target: strategyTargetSchema,
  tenantId: z.string(),
  strategyId: z.string(),
  key: z.string(),
  startedAt: z.string(),
  finishedAt: z.string(),
  complete: z.boolean(),
  warnings: z.array(z.string()),
  redis: z.object({
    projection: z
      .object({
        lastSuccess: z.string().nullable(),
        lastAttempt: z.string().nullable(),
        error: z.string().nullable(),
        discoverySuccess: z.string().nullable(),
        discoveryError: z.string().nullable(),
        pending: z.boolean(),
      })
      .optional(),
    complete: z.boolean(),
    total: z.number().nullable(),
    scanned: z.number(),
  }),
  alerts: z.object({
    complete: z.boolean(),
    scanned: z.number(),
    matched: z.number(),
  }),
  rows: z.array(
    z.object({
      fingerprint: z.string(),
      status: z.enum(["matched", "missing_redis", "redis_only", "unknown"]),
      alerts: z.array(strategyAlertSchema),
    }),
  ),
});
export type StrategyResult = z.infer<typeof strategyResultSchema>;
