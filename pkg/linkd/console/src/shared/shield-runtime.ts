import { z } from "zod";
const tenant = z.string().regex(/^[a-zA-Z0-9_-]{1,64}$/),
  id = z.string().min(1).max(160),
  at = z.string().datetime({ offset: true });
const hash = z.string().regex(/^[a-f0-9]{64}$/);
export const shieldBinding = z
  .object({
    binding_id: hash,
    policy: z.object({
      id: z.string().min(1).max(80),
      version: z.number().int().positive().max(Number.MAX_SAFE_INTEGER),
      digest: hash,
    }),
    type: z.enum(["time_shield", "rely_shield"]),
    mode: z.enum(["custom_shield", "cmdb_shield"]).optional(),
    source_event_id: id,
    severity: z.string().min(1).max(32),
    main_alert_id: id.optional(),
    main_candidate: z
      .object({
        alert_id: id,
        event_id: id,
        event_source_id: z.string().max(32),
        fingerprint: z.string().max(128),
        severity: z.string().max(32),
      })
      .optional(),
    activation_id: hash.optional(),
    bound_at: at,
    reason: z.string().max(4096).optional(),
  })
  .superRefine((b, ctx) => {
    if (
      b.type === "time_shield"
        ? !b.activation_id || Boolean(b.mode) || Boolean(b.main_alert_id)
        : !b.mode || !b.main_alert_id
    )
      ctx.addIssue({ code: "custom", message: "屏蔽类型与绑定身份不一致" });
    if (b.main_candidate && b.main_candidate.alert_id !== b.main_alert_id)
      ctx.addIssue({ code: "custom", message: "依赖主引用不一致" });
  });
export const shieldRecord = z
  .object({
    bk_tenant_id: tenant,
    alert_id: id,
    event_source_id: z.string().min(1).max(32),
    revision: z.number().int().positive().max(Number.MAX_SAFE_INTEGER),
    status: z.enum(["active", "recovered", "closed"]),
    severity: z.string().min(1).max(32),
    shield: z.object({
      active: z.boolean(),
      bindings: z.array(shieldBinding).max(16).default([]),
      next_check_at: at.optional(),
    }),
    admission: z.object({
      admitted_at: at.optional(),
      severity: z.string().max(32).optional(),
      cause_type: z.string().max(32).optional(),
      cause_id: z.string().max(256).optional(),
    }),
    policy_change: z
      .object({
        operation_id: z.string().min(1).max(128),
        effective_at: at,
        before: z.array(shieldBinding).max(16),
        after: z.array(shieldBinding).max(16).default([]),
      })
      .optional(),
  })
  .superRefine((r, c) => {
    if (
      r.shield.active !== r.shield.bindings.length > 0 ||
      (r.shield.active && !r.shield.next_check_at) ||
      (r.status !== "active" && r.shield.active)
    )
      c.addIssue({ code: "custom", message: "屏蔽状态不一致" });
  });
export const shieldQuery = z
  .object({
    bk_tenant_id: tenant,
    policy_id: z
      .string()
      .regex(/^[a-zA-Z0-9_-]{1,80}$/)
      .optional(),
    main_alert_id: id.optional(),
    binding_type: z
      .enum(["time_shield", "custom_shield", "cmdb_shield"])
      .optional(),
    after: z.string().max(8192).default(""),
    limit: z.coerce.number().int().min(1).max(4).default(4),
  })
  .strict();
export const shieldPage = z.object({
  bk_tenant_id: tenant,
  items: z.array(shieldRecord).max(4),
  next: z.string().max(8192),
});
export const shieldHistory = z.object({
  bk_tenant_id: tenant,
  alert_id: id,
  items: z
    .array(
      z.object({
        bk_tenant_id: tenant,
        alert_id: id,
        log_id: z.string().min(1).max(256),
        operator_kind: z.string().max(32),
        operation_kind: z.enum(["shield", "unshield"]),
        params: z.record(z.string(), z.unknown()),
        created_time: at,
      }),
    )
    .max(4),
  next: z.string().max(8192),
});
export type ShieldRecord = z.infer<typeof shieldRecord>;
export type ShieldQuery = z.infer<typeof shieldQuery>;
export type ShieldBinding = z.infer<typeof shieldBinding>;
export function matchesShieldQuery(row: ShieldRecord, q: ShieldQuery) {
  if (!q.policy_id && !q.main_alert_id && !q.binding_type) return true;
  return [
    ...row.shield.bindings,
    ...(row.policy_change?.before ?? []),
    ...(row.policy_change?.after ?? []),
  ].some(
    (b) =>
      (!q.policy_id || q.policy_id === b.policy.id) &&
      (!q.main_alert_id || q.main_alert_id === b.main_alert_id) &&
      (!q.binding_type ||
        q.binding_type === (b.type === "time_shield" ? b.type : b.mode)),
  );
}
