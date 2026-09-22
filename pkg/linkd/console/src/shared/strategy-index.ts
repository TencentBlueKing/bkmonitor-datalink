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
export const strategyBrowseQuerySchema = strategyQuerySchema
  .pick({ event_source_id: true, hook_name: true })
  .extend({
    cursor: z.string().min(1).max(2048).optional(),
    count: z.coerce.number().int().min(10).max(200).default(50),
  })
  .strict();
export type StrategyBrowseQuery = z.infer<typeof strategyBrowseQuerySchema>;

export const strategyBrowseResultSchema = z.object({
  target: strategyTargetSchema,
  scannedAt: z.string(),
  nextCursor: z.string().nullable(),
  phase: z.enum(["sets", "pending"]),
  warnings: z.array(z.string()),
  health: z.object({
    lastSuccess: z.string().nullable(),
    lastAttempt: z.string().nullable(),
    error: z.string().nullable(),
    pendingCount: z.number().int().nonnegative(),
    oldestDueAt: z.string().nullable(),
  }),
  rows: z.array(
    z.object({
      tenantId: z.string(),
      strategyId: z.string(),
      key: z.string(),
      members: z.number().int().nonnegative().nullable(),
      pending: z.boolean().nullable(),
      lastSuccess: z.string().nullable(),
      lastAttempt: z.string().nullable(),
      error: z.string().nullable(),
    }),
  ),
});
export type StrategyBrowseResult = z.infer<typeof strategyBrowseResultSchema>;

export const strategyAuditRequestSchema = strategyQuerySchema
  .pick({ event_source_id: true, hook_name: true })
  .strict();
export type StrategyAuditRequest = z.infer<typeof strategyAuditRequestSchema>;
export const strategyAuditSchema = z.object({
  id: z.string(),
  target: strategyTargetSchema,
  status: z.enum(["running", "completed", "incomplete", "canceled"]),
  phase: z.enum(["alerts", "redis", "remaining", "done"]),
  startedAt: z.string(),
  finishedAt: z.string().nullable(),
  scannedAlerts: z.number(),
  skippedAlerts: z.number(),
  invalidAlerts: z.number(),
  checkedStrategies: z.number(),
  discoveredStrategies: z.number(),
  incompleteStrategies: z.number(),
  redisMembers: z.number(),
  matched: z.number(),
  missing: z.number(),
  extra: z.number(),
  differences: z.array(
    z.object({
      tenantId: z.string(),
      strategyId: z.string(),
      fingerprint: z.string().nullable(),
      status: z.enum(["missing_redis", "redis_only", "unknown"]),
    }),
  ),
  samplesTruncated: z.boolean(),
  warnings: z.array(z.string()),
});
export type StrategyAudit = z.infer<typeof strategyAuditSchema>;
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
