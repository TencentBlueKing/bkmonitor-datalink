import { z } from "zod";
import {
  projectionReceipt,
  projectionRetryCommand,
} from "./projection-tasks.js";
const tenant = z.string().regex(/^[a-zA-Z0-9_-]{1,64}$/),
  id = z.string().min(1).max(160),
  hash = z.string().regex(/^[a-f0-9]{64}$/),
  at = z.string().datetime({ offset: true }),
  integer = z.number().int().min(0).max(Number.MAX_SAFE_INTEGER);
const version = z
  .string()
  .min(1)
  .max(128)
  .refine((v) => !/[\r\n]/.test(v));
const reason = z
  .string()
  .refine(
    (v) => v.trim().length > 0 && new TextEncoder().encode(v).length <= 1024,
  );
export const actionState = z.enum([
  "pending",
  "waiting_projection",
  "sending",
  "retry",
  "succeeded",
  "skipped",
  "failed",
]);
export const actionKind = z.enum(["firing", "resolved", "close"]);
const cause = z.object({
  type: z.enum(["source_event", "user_operation", "system_operation"]),
  id: id.refine((v) => v.trim().length > 0),
});
export const actionRetryCommand = projectionRetryCommand;
const retryAudit = z.object({
  command: actionRetryCommand.extend({
    operation_id: z.string().regex(/^[a-zA-Z0-9_-]{1,128}$/),
    reason,
    task_id: hash,
    operator_id: z.string().min(1).max(256),
  }),
  requested_at: at,
});
const actionReceipt = z.object({
  schema_version: z.literal("linkd.kac-action.v1"),
  bk_tenant_id: tenant,
  target_id: tenant,
  linkd_alert_id: id,
  alarm_id: z.string().min(1).max(64),
  action_id: hash,
  request_hash: hash,
  outcome: z.enum(["accepted", "skipped"]),
  reason: z.literal("superseded_by_terminal").optional(),
  applied_revision: integer.min(1),
  applied_status: z.enum(["active", "recovered", "closed"]),
  search_visible: z.literal(true),
  acceptance_id: z
    .string()
    .min(1)
    .max(256)
    .refine((v) => v.trim().length > 0),
});
export const actionDelivery = z
  .object({
    bk_tenant_id: tenant,
    id: hash,
    alert_id: id,
    alarm_id: z.string().min(1).max(64),
    target_id: tenant,
    source_id: z.string().regex(/^[a-zA-Z0-9_-]{1,32}$/),
    source_version: integer.min(1),
    revision: integer.min(1),
    alert_status: z.enum(["active", "recovered", "closed"]),
    content_hash: hash,
    request_hash: hash,
    action_id: hash,
    action: actionKind,
    cause,
    task_version: version,
    created_at: at,
    progress: z.object({
      state: actionState,
      attempts: integer.max(8),
      total_attempts: integer,
      generation: integer.min(1),
      updated_at: at,
      due_at: at.optional(),
      lease_until: at.optional(),
      previous_unconfirmed: z.boolean(),
      projection: projectionReceipt.optional(),
      receipt: actionReceipt.optional(),
      last_retry: retryAudit.optional(),
      error_code: z
        .enum([
          "projection_pending",
          "projection_unavailable",
          "target_unavailable",
          "transport_failed",
          "remote_unauthorized",
          "identity_conflict",
          "remote_unavailable",
          "remote_rejected",
          "response_too_large",
          "response_invalid",
          "visibility_pending",
          "attempt_interrupted",
          "superseded_by_terminal",
        ])
        .optional(),
    }),
  })
  .superRefine((v, c) => {
    const p = v.progress,
      r = p.receipt,
      g = p.projection,
      a = p.last_retry,
      fail = () =>
        c.addIssue({ code: "custom", message: "动作投递状态不一致" });
    const expected = {
      firing: "active",
      resolved: "recovered",
      close: "closed",
    }[v.action];
    if (
      v.alert_status !== expected ||
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
      p.due_at &&
      (Date.parse(p.due_at) < Date.parse(p.updated_at) ||
        Date.parse(p.due_at) - Date.parse(p.updated_at) > 60000 ||
        (p.state === "pending" && p.due_at !== p.updated_at))
    )
      return fail();
    if (
      g &&
      (g.bk_tenant_id !== v.bk_tenant_id ||
        g.target_id !== v.target_id ||
        g.linkd_alert_id !== v.alert_id ||
        g.alarm_id !== v.alarm_id ||
        g.applied_revision < v.revision ||
        (g.applied_revision === v.revision &&
          (g.content_hash !== v.content_hash ||
            g.applied_status !== v.alert_status)) ||
        (v.alert_status !== "active" && g.applied_status !== v.alert_status))
    )
      return fail();
    if (
      r &&
      (r.bk_tenant_id !== v.bk_tenant_id ||
        r.target_id !== v.target_id ||
        r.linkd_alert_id !== v.alert_id ||
        r.alarm_id !== v.alarm_id ||
        r.action_id !== v.action_id ||
        r.request_hash !== v.request_hash ||
        r.applied_revision < v.revision)
    )
      return fail();
    if (
      r &&
      (r.outcome === "accepted"
        ? r.reason !== undefined || r.applied_status !== expected
        : r.reason !== "superseded_by_terminal" ||
          v.action !== "firing" ||
          r.applied_revision <= v.revision ||
          r.applied_status === "active")
    )
      return fail();
    switch (p.state) {
      case "pending":
        if (
          p.attempts !== 0 ||
          !p.due_at ||
          p.lease_until ||
          p.error_code ||
          r ||
          g
        )
          return fail();
        break;
      case "waiting_projection":
        if (
          !p.due_at ||
          p.lease_until ||
          r ||
          g ||
          p.error_code !== "projection_pending"
        )
          return fail();
        break;
      case "sending":
        if (
          p.attempts < 1 ||
          !p.lease_until ||
          Date.parse(p.lease_until) - Date.parse(p.updated_at) !== 30000 ||
          p.due_at ||
          p.error_code ||
          r
        )
          return fail();
        break;
      case "retry":
        if (
          p.attempts < 1 ||
          p.attempts >= 8 ||
          !p.due_at ||
          p.lease_until ||
          !p.error_code ||
          ["projection_pending", "superseded_by_terminal"].includes(
            p.error_code,
          ) ||
          r
        )
          return fail();
        break;
      case "failed":
        if (
          p.attempts < 1 ||
          p.due_at ||
          p.lease_until ||
          !p.error_code ||
          ["projection_pending", "superseded_by_terminal"].includes(
            p.error_code,
          ) ||
          r
        )
          return fail();
        break;
      case "succeeded":
        if (
          p.attempts < 1 ||
          p.due_at ||
          p.lease_until ||
          p.error_code ||
          !g ||
          !r ||
          r.outcome !== "accepted"
        )
          return fail();
        break;
      case "skipped":
        if (
          p.due_at ||
          p.lease_until ||
          p.error_code !== "superseded_by_terminal" ||
          !g ||
          (r
            ? p.attempts < 1 || r.outcome !== "skipped"
            : v.action !== "firing" ||
              g.applied_revision <= v.revision ||
              g.applied_status === "active")
        )
          return fail();
        break;
    }
  });
export const actionQuery = z
  .object({
    bk_tenant_id: tenant,
    alert_id: id.optional(),
    target_id: tenant.optional(),
    source_id: z
      .string()
      .regex(/^[a-zA-Z0-9_-]{1,32}$/)
      .optional(),
    state: actionState.optional(),
    action: actionKind.optional(),
    after: z.string().max(2048).default(""),
    limit: z.coerce.number().int().min(1).max(4).default(4),
  })
  .strict();
export const actionPage = z.object({
  bk_tenant_id: tenant,
  items: z.array(actionDelivery).max(4),
  next: z.string().max(2048),
});
export const actionSnapshot = z
  .object({
    bk_tenant_id: tenant,
    id: hash,
    revision: integer.min(1),
    request_hash: hash,
    request: z.object({
      schema_version: z.literal("linkd.kac-action.v1"),
      action_id: hash,
      bk_tenant_id: tenant,
      target_id: tenant,
      linkd_alert_id: id,
      alarm_id: z.string().min(1).max(64),
      linkd_revision: integer.min(1),
      action: actionKind,
      cause,
      content_hash: hash,
      alert: z.record(z.string(), z.unknown()),
    }),
  })
  .superRefine((v, c) => {
    const q = v.request;
    if (
      q.bk_tenant_id !== v.bk_tenant_id ||
      q.linkd_revision !== v.revision ||
      q.alert.bk_tenant_id !== v.bk_tenant_id ||
      q.alert.alert_id !== q.linkd_alert_id ||
      q.alert.revision !== v.revision ||
      q.alert.status !==
        { firing: "active", resolved: "recovered", close: "closed" }[q.action]
    )
      c.addIssue({ code: "custom", message: "动作快照身份不一致" });
  });
export const actionOrder = z
  .object({
    bk_tenant_id: tenant,
    id: hash,
    alert_id: id,
    target_id: tenant,
    observed_at: at,
    head: actionDelivery.nullable(),
  })
  .superRefine((v, c) => {
    const h = v.head;
    if (
      h &&
      (h.bk_tenant_id !== v.bk_tenant_id ||
        h.alert_id !== v.alert_id ||
        h.target_id !== v.target_id ||
        ["succeeded", "skipped"].includes(h.progress.state))
    )
      c.addIssue({ code: "custom", message: "动作队首作用域不一致" });
  });
export type ActionDelivery = z.infer<typeof actionDelivery>;
export type ActionQuery = z.infer<typeof actionQuery>;
export type ActionRetryCommand = z.infer<typeof actionRetryCommand>;
export function matchesActionQuery(r: ActionDelivery, q: ActionQuery) {
  return (
    (!q.alert_id || q.alert_id === r.alert_id) &&
    (!q.target_id || q.target_id === r.target_id) &&
    (!q.source_id || q.source_id === r.source_id) &&
    (!q.state || q.state === r.progress.state) &&
    (!q.action || q.action === r.action)
  );
}
