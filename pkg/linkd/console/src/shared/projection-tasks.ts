import { z } from "zod";
const tenant = z.string().regex(/^[a-zA-Z0-9_-]{1,64}$/),
  id = z.string().min(1).max(160),
  hash = z.string().regex(/^[a-f0-9]{64}$/),
  at = z.string().datetime({ offset: true }),
  integer = z.number().int().min(0).max(Number.MAX_SAFE_INTEGER),
  version = z
    .string()
    .min(1)
    .max(128)
    .refine((v) => !/[\r\n]/.test(v));
const reason = z
  .string()
  .refine(
    (v) => v.trim().length > 0 && new TextEncoder().encode(v).length <= 1024,
  );
export const projectionState = z.enum([
  "pending",
  "sending",
  "retry",
  "delivered",
  "succeeded",
  "failed",
]);
export const projectionRetryCommand = z
  .object({
    bk_tenant_id: tenant,
    expected_version: version,
    operation_id: z.string().uuid(),
    reason: reason.transform((v) => v.trim()),
  })
  .strict();
const retryAudit = z.object({
  command: projectionRetryCommand.extend({
    operation_id: z.string().regex(/^[a-zA-Z0-9_-]{1,128}$/),
    reason,
    task_id: hash,
    operator_id: z.string().min(1).max(256),
  }),
  requested_at: at,
});
export const projectionReceipt = z.object({
  schema_version: z.literal("linkd.kac-projection.v1"),
  bk_tenant_id: tenant,
  target_id: z.string().regex(/^[a-zA-Z0-9_-]{1,64}$/),
  linkd_alert_id: id,
  alarm_id: z.string().min(1).max(64),
  applied_revision: integer.min(1),
  content_hash: hash,
  applied_status: z.enum(["active", "recovered", "closed"]),
  search_visible: z.literal(true),
  document_ref: z.string().min(1).max(512),
});
export const projectionTask = z
  .object({
    bk_tenant_id: tenant,
    id: hash,
    alert_id: id,
    alarm_id: z.string().min(1).max(64),
    target_id: z.string().regex(/^[a-zA-Z0-9_-]{1,64}$/),
    source_id: z.string().regex(/^[a-zA-Z0-9_-]{1,32}$/),
    source_version: integer.min(1),
    revision: integer.min(1),
    alert_status: z.enum(["active", "recovered", "closed"]),
    content_hash: hash,
    task_version: version,
    created_at: at,
    progress: z.object({
      state: projectionState,
      attempts: z.number().int().min(0).max(8),
      total_attempts: integer,
      generation: integer.min(1),
      updated_at: at,
      due_at: at.optional(),
      lease_until: at.optional(),
      error_code: z
        .enum([
          "transport_failed",
          "response_too_large",
          "response_invalid",
          "remote_unavailable",
          "remote_rejected",
          "remote_unauthorized",
          "revision_conflict",
          "visibility_pending",
          "attempt_interrupted",
          "target_unavailable",
        ])
        .optional(),
      receipt: projectionReceipt.optional(),
      last_retry: retryAudit.optional(),
    }),
  })
  .superRefine((v, c) => {
    const p = v.progress,
      r = p.receipt,
      a = p.last_retry,
      fail = () =>
        c.addIssue({ code: "custom", message: "投影任务状态不一致" });
    if (
      p.total_attempts < p.attempts ||
      Date.parse(p.updated_at) < Date.parse(v.created_at) ||
      (p.generation === 1) !== !a
    )
      return fail();
    if (
      a &&
      (a.command.bk_tenant_id !== v.bk_tenant_id ||
        a.command.task_id !== v.id ||
        Date.parse(a.requested_at) < Date.parse(v.created_at) ||
        Date.parse(a.requested_at) > Date.parse(p.updated_at))
    )
      return fail();
    if (
      p.state === "pending" &&
      (p.attempts !== 0 || !p.due_at || p.lease_until || p.error_code || r)
    )
      return fail();
    if (
      p.state === "retry" &&
      (p.attempts < 1 ||
        p.attempts >= 8 ||
        !p.due_at ||
        p.lease_until ||
        !p.error_code ||
        r)
    )
      return fail();
    if (
      p.state === "sending" &&
      (p.attempts < 1 || !p.lease_until || p.due_at || p.error_code || r)
    )
      return fail();
    if (
      p.state === "failed" &&
      (p.attempts < 1 || p.lease_until || p.due_at || !p.error_code || r)
    )
      return fail();
    if (
      ["delivered", "succeeded"].includes(p.state) &&
      (p.attempts < 1 || !r || p.lease_until || p.due_at || p.error_code)
    )
      return fail();
    if (
      r &&
      (r.bk_tenant_id !== v.bk_tenant_id ||
        r.target_id !== v.target_id ||
        r.linkd_alert_id !== v.alert_id ||
        r.alarm_id !== v.alarm_id ||
        r.applied_revision < v.revision ||
        (r.applied_revision === v.revision &&
          (r.content_hash !== v.content_hash ||
            r.applied_status !== v.alert_status)) ||
        (v.alert_status !== "active" && r.applied_status !== v.alert_status))
    )
      return fail();
  });
export const projectionQuery = z
  .object({
    bk_tenant_id: tenant,
    alert_id: id.optional(),
    target_id: z
      .string()
      .regex(/^[a-zA-Z0-9_-]{1,64}$/)
      .optional(),
    source_id: z
      .string()
      .regex(/^[a-zA-Z0-9_-]{1,32}$/)
      .optional(),
    state: projectionState.optional(),
    after: z.string().max(2048).default(""),
    limit: z.coerce.number().int().min(1).max(4).default(4),
  })
  .strict();
export const projectionPage = z.object({
  bk_tenant_id: tenant,
  items: z.array(projectionTask).max(4),
  next: z.string().max(2048),
});
export const projectionSnapshot = z.object({
  bk_tenant_id: tenant,
  id: hash,
  revision: integer.min(1),
  content_hash: hash,
  alert: z.record(z.string(), z.unknown()),
});
export type ProjectionTask = z.infer<typeof projectionTask>;
export type ProjectionQuery = z.infer<typeof projectionQuery>;
export type ProjectionRetryCommand = z.infer<typeof projectionRetryCommand>;
export function matchesProjectionQuery(r: ProjectionTask, q: ProjectionQuery) {
  return (
    (!q.alert_id || q.alert_id === r.alert_id) &&
    (!q.target_id || q.target_id === r.target_id) &&
    (!q.source_id || q.source_id === r.source_id) &&
    (!q.state || q.state === r.progress.state)
  );
}
