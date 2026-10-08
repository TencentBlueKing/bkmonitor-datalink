import { z } from "zod";

const identity = (n: number) => z.string().min(1).max(n);
const hash = z.string().regex(/^[a-f0-9]{64}$/);
const count = z.number().int().min(0).max(256);
const instant = z.string().datetime({ offset: true });
const ref = z.object({
  id: identity(80),
  version: z.number().int().positive().max(Number.MAX_SAFE_INTEGER),
  digest: hash,
});
export const mergeResource = z.enum(["windows", "decisions", "relations"]);
export type MergeResource = z.infer<typeof mergeResource>;
export const mergePhase = z.enum([
  "capturing",
  "prepared",
  "waiting_parent",
  "linking",
  "ending",
  "releasing",
  "completed",
]);
const base = {
  id: hash,
  bk_tenant_id: z.string().regex(/^[a-zA-Z0-9_-]{1,64}$/),
  group_key: hash,
  policy: ref,
};
export const mergeDecision = z.object({
  ...base,
  window_id: hash,
  started_at: instant,
  deadline: instant,
  frozen_at: instant,
  updated_at: instant,
  outcome: z.enum(["succeeded", "failed"]),
  phase: mergePhase,
  reason_code: z.string().max(80).optional(),
  parent_event_id: identity(160).optional(),
  parent_alert_id: identity(160).optional(),
  member_count: count,
  wait_member_count: count,
  capture_offset: count,
  member_offset: count,
  window_finished: z.boolean(),
});
const main = z.object({
  alert_id: identity(160),
  event_id: identity(160),
  event_source_id: identity(32),
  fingerprint: identity(128),
  severity: identity(32),
});
export const mergeWindow = z.object({
  ...base,
  started_at: instant,
  deadline: instant,
  cyclic: z.boolean(),
  revision: z.number().int().positive().max(Number.MAX_SAFE_INTEGER),
  member_count: count,
  committed_count: count,
  group_counts: z.array(count).min(1).max(32),
  frozen: z
    .object({
      outcome: z.enum(["succeeded", "failed"]),
      operation_id: hash,
      at_ms: z.number().int(),
      member_ids: z.array(identity(160)).max(256),
    })
    .optional(),
  members: z
    .array(
      z.object({
        main,
        groups: z.array(z.number().int().min(0).max(31)).min(1).max(32),
        first_at_ms: z.number().int(),
        committed: z.boolean(),
      }),
    )
    .max(256)
    .optional(),
});
export const mergeRelation = z.object({
  ...base,
  window_id: hash,
  parent_alert_id: identity(160),
  parent_fingerprint: identity(128),
  wait_member_ids: z.array(identity(160)).max(256),
  end_offset: count,
  end_reason: z.string().max(80).optional(),
  end_started_at: instant.optional(),
  ended_at: instant.optional(),
  members: z
    .array(
      z.object({
        alert_id: identity(160),
        state: z.enum(["pending", "linked", "terminal"]),
      }),
    )
    .min(2)
    .max(256),
  state: z.enum(["preparing", "ready", "ending", "ended"]),
  index_offset: z.number().int().min(0).max(257),
  created_at: instant,
  updated_at: instant,
});
export const mergeSnapshot = z.object({
  bk_tenant_id: base.bk_tenant_id,
  decision_id: hash,
  alert_id: identity(160),
  event_source_id: identity(32),
  severity: identity(32),
  status: z.enum(["active", "recovered", "closed"]),
  trigger_event_id: identity(160),
  enrich_status: z.string().max(32),
  revision: z.number().int().positive().max(Number.MAX_SAFE_INTEGER),
});
export const mergeQuery = z
  .object({
    bk_tenant_id: base.bk_tenant_id,
    resource: mergeResource,
    policy_id: z
      .string()
      .regex(/^[a-zA-Z0-9_-]{1,80}$/)
      .optional(),
    phase: mergePhase.optional(),
    alert_id: identity(160).optional(),
    after: z.string().max(1024).default(""),
    limit: z.coerce.number().int().min(1).max(4).default(4),
  })
  .strict()
  .superRefine((q, ctx) => {
    if (
      (q.phase && q.resource !== "decisions") ||
      (q.resource === "relations" ? !q.alert_id : q.alert_id !== undefined)
    )
      ctx.addIssue({ code: "custom", message: "请选择正确的运行态范围" });
  });
export type MergeQuery = z.infer<typeof mergeQuery>;
export type MergeDecision = z.infer<typeof mergeDecision>;
export type MergeWindow = z.infer<typeof mergeWindow>;
export type MergeRelation = z.infer<typeof mergeRelation>;
export type MergeRow = MergeDecision | MergeWindow | MergeRelation;
export const mergeRow = (resource: MergeResource) =>
  resource === "decisions"
    ? mergeDecision
    : resource === "windows"
      ? mergeWindow
      : mergeRelation;
export const mergePage = (resource: MergeResource) =>
  z.object({
    bk_tenant_id: base.bk_tenant_id,
    items: z.array(mergeRow(resource)).max(4),
    next: z.string().max(1024),
  });
export const mergeSnapshotPage = z.object({
  bk_tenant_id: base.bk_tenant_id,
  items: z.array(mergeSnapshot).max(4),
  next: z.string().max(1024),
});

export const frozenMergeSnapshot = z
  .object({
    bk_tenant_id: base.bk_tenant_id,
    decision_id: hash,
    alert: z
      .object({ bk_tenant_id: base.bk_tenant_id, alert_id: identity(160) })
      .passthrough(),
  })
  .superRefine((s, ctx) => {
    if (s.alert.bk_tenant_id !== s.bk_tenant_id)
      ctx.addIssue({ code: "custom", message: "冻结快照租户不一致" });
  });
