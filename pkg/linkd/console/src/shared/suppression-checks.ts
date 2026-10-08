import { z } from "zod";
import { suppressionKey, suppressionWindow } from "./suppression-runtime.js";
const tenant = z.string().regex(/^[a-zA-Z0-9_-]{1,64}$/),
  hash = z.string().regex(/^[a-f0-9]{64}$/),
  entity = z.string().min(1).max(160),
  at = z.string().datetime({ offset: true });
const reason = z
  .string()
  .refine(
    (v) => v.trim().length > 0 && new TextEncoder().encode(v).length <= 1024,
  );
export const suppressionCheckCommand = z
  .object({
    bk_tenant_id: tenant,
    expected_epoch: entity,
    expected_owner_alert_id: z.string().max(160),
    operation_id: z.string().uuid(),
    reason: reason.transform((v) => v.trim()),
  })
  .strict();
const command = suppressionCheckCommand
  .extend({
    kind: z.enum(["clip", "aggregation"]),
    window_id: z.string().max(129),
    operation_id: z.string().regex(/^[a-zA-Z0-9_-]{1,128}$/),
    operator_id: z.string().min(1).max(256),
    reason,
  })
  .superRefine((c, z) => {
    if (
      !suppressionKey.safeParse({ kind: c.kind, id: c.window_id }).success ||
      (c.kind === "aggregation" && !c.expected_owner_alert_id)
    )
      z.addIssue({ code: "custom", message: "窗口命令范围不合法" });
  });
const result = z.object({
  changed: z.boolean(),
  checked_at: at,
  outcome: z.enum(["retained", "cleared", "absent", "superseded", "failed"]),
  reason: z.enum([
    "candidate_pending",
    "unbound_counter",
    "active_owner",
    "terminal_owner",
    "owner_missing",
    "owner_not_admitted",
    "window_missing",
    "window_changed",
    "window_unavailable",
    "owner_unavailable",
    "scope_mismatch",
    "invalid_state",
    "cleanup_unavailable",
    "execution_unavailable",
  ]),
  observed: suppressionWindow.optional(),
  owner_revision: z
    .number()
    .int()
    .min(1)
    .max(Number.MAX_SAFE_INTEGER)
    .optional(),
  owner_status: z.enum(["active", "recovered", "closed"]).optional(),
});
export const suppressionCheckRequest = z
  .object({
    id: hash,
    command,
    state: z.enum(["pending", "completed", "failed", "superseded"]),
    created_at: at,
    started_at: at.optional(),
    previous_unconfirmed: z.boolean(),
    completed_at: at.optional(),
    result: result.optional(),
  })
  .superRefine((r, c) => {
    const fail = () =>
        c.addIssue({ code: "custom", message: "对账请求状态不一致" }),
      v = r.result,
      w = v?.observed,
      q = r.command;
    if (
      (r.started_at && Date.parse(r.started_at) < Date.parse(r.created_at)) ||
      (r.previous_unconfirmed && !r.started_at)
    )
      return fail();
    if (r.state === "pending") {
      if (v || r.completed_at) return fail();
      return;
    }
    if (
      !v ||
      !r.started_at ||
      !r.completed_at ||
      Date.parse(r.completed_at) < Date.parse(r.started_at) ||
      r.completed_at !== v.checked_at
    )
      return fail();
    const state =
      v.outcome === "failed"
        ? "failed"
        : v.outcome === "superseded"
          ? "superseded"
          : "completed";
    const reasons = {
      retained: ["unbound_counter", "active_owner", "candidate_pending"],
      cleared: ["terminal_owner", "owner_missing", "owner_not_admitted"],
      absent: ["window_missing"],
      superseded: ["window_changed"],
      failed: [
        "window_unavailable",
        "owner_unavailable",
        "scope_mismatch",
        "invalid_state",
        "cleanup_unavailable",
        "execution_unavailable",
      ],
    };
    if (
      state !== r.state ||
      !reasons[v.outcome].includes(v.reason) ||
      Boolean(v.owner_revision) !== Boolean(v.owner_status)
    )
      return fail();
    const same =
      w &&
      w.epoch === q.expected_epoch &&
      (w.owner_alert_id ?? "") === q.expected_owner_alert_id;
    if (
      w &&
      (w.bk_tenant_id !== q.bk_tenant_id ||
        w.kind !== q.kind ||
        w.id !== q.window_id)
    )
      return fail();
    if ((v.outcome === "cleared" || v.outcome === "retained") && !same)
      return fail();
    if (
      (v.outcome === "cleared" && !v.changed) ||
      (v.changed &&
        (!same ||
          !q.expected_owner_alert_id ||
          !["cleared", "failed"].includes(v.outcome)))
    )
      return fail();
    if (v.outcome === "absent" && (w || v.owner_revision)) return fail();
    if (
      v.reason === "terminal_owner" &&
      (!v.owner_revision || v.owner_status === "active")
    )
      return fail();
    if (
      v.reason === "owner_not_admitted" &&
      (!v.owner_revision ||
        v.owner_status !== "active" ||
        q.kind !== "aggregation")
    )
      return fail();
    if (
      v.reason === "active_owner" &&
      (!v.owner_revision || v.owner_status !== "active")
    )
      return fail();
    if (v.reason === "owner_missing" && v.owner_revision) return fail();
    if (
      v.reason === "candidate_pending" &&
      (v.owner_revision ||
        !w ||
        w.kind !== "aggregation" ||
        w.state !== "pending" ||
        w.observed_at_ms > (w.pending_until_ms ?? 0))
    )
      return fail();
    if (
      v.reason === "unbound_counter" &&
      (q.kind !== "clip" || q.expected_owner_alert_id || v.owner_revision)
    )
      return fail();
  });
export const suppressionCheckPage = z.object({
  bk_tenant_id: tenant,
  kind: z.enum(["clip", "aggregation"]),
  window_id: z.string().max(129),
  items: z.array(suppressionCheckRequest).max(4),
  next: z.string().max(2048),
});
export type SuppressionCheckCommand = z.infer<typeof suppressionCheckCommand>;
export type SuppressionCheckRequest = z.infer<typeof suppressionCheckRequest>;
