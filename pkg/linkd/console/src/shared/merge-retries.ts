import { z } from "zod";
const tenant = z.string().regex(/^[a-zA-Z0-9_-]{1,64}$/),
  hash = z.string().regex(/^[a-f0-9]{64}$/),
  at = z.string().datetime({ offset: true });
export const mergeRetryKind = z.enum(["decisions", "relations"]);
export const mergeRetryKey = z.object({ kind: mergeRetryKind, id: hash });
export const mergeRetryScope = z.object({ bk_tenant_id: tenant }).strict();
const textBytes = (max: number) =>
  z
    .string()
    .refine(
      (v) => v.trim().length > 0 && new TextEncoder().encode(v).length <= max,
    );
const reason = textBytes(1024);
export const mergeRetryCommand = z
  .object({
    bk_tenant_id: tenant,
    expected_token: hash,
    operation_id: z.string().uuid(),
    reason: reason.transform((v) => v.trim()),
  })
  .strict();
const command = mergeRetryCommand.extend({
  kind: mergeRetryKind,
  target_id: hash,
  operation_id: z.string().regex(/^[a-zA-Z0-9_-]{1,128}$/),
  operator_id: textBytes(256),
  reason,
});
export const mergeControlPoint = z
  .object({
    bk_tenant_id: tenant,
    kind: mergeRetryKind,
    target_id: hash,
    window_id: hash,
    token: hash,
    phase: z.enum([
      "capturing",
      "prepared",
      "waiting_parent",
      "linking",
      "ending",
      "releasing",
      "completed",
      "preparing",
      "ready",
      "ended",
    ]),
    outcome: z.enum(["succeeded", "failed"]).optional(),
    complete: z.boolean(),
    updated_at: at,
    capture_offset: z.number().int().min(0).max(256),
    member_offset: z.number().int().min(0).max(256),
    index_offset: z.number().int().min(0).max(257),
    end_offset: z.number().int().min(0).max(256),
  })
  .superRefine((p, c) => {
    const valid =
      p.kind === "decisions"
        ? Boolean(p.outcome) &&
          !p.index_offset &&
          !p.end_offset &&
          (!p.complete || p.phase === "completed") &&
          [
            "capturing",
            "prepared",
            "waiting_parent",
            "linking",
            "ending",
            "releasing",
            "completed",
          ].includes(p.phase)
        : !p.outcome &&
          !p.capture_offset &&
          !p.member_offset &&
          p.complete === (p.phase === "ended") &&
          ["preparing", "ready", "ending", "ended"].includes(p.phase);
    if (!valid) c.addIssue({ code: "custom", message: "控制点进度不一致" });
  });
const result = z.object({
  checked_at: at,
  outcome: z.enum(["advanced", "unchanged", "superseded", "failed"]),
  reason: z.enum([
    "progressed",
    "no_progress",
    "already_complete",
    "target_changed",
    "target_missing",
    "scope_mismatch",
    "invalid_state",
    "dependency_failed",
    "execution_unavailable",
    "step_timeout",
  ]),
  step_attempted: z.boolean(),
  before: mergeControlPoint.optional(),
  after: mergeControlPoint.optional(),
});
export const mergeRetryRequest = z
  .object({
    id: hash,
    command,
    window_id: hash,
    state: z.enum(["pending", "completed", "failed", "superseded"]),
    created_at: at,
    started_at: at.optional(),
    completed_at: at.optional(),
    previous_unconfirmed: z.boolean(),
    result: result.optional(),
  })
  .superRefine((r, c) => {
    const fail = () =>
        c.addIssue({ code: "custom", message: "合并请求结果不一致" }),
      v = r.result,
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
    const expected = ["failed", "superseded"].includes(v.outcome)
      ? v.outcome
      : "completed";
    if (r.state !== expected) return fail();
    for (const p of [v.before, v.after])
      if (
        p &&
        (p.bk_tenant_id !== q.bk_tenant_id ||
          p.kind !== q.kind ||
          p.target_id !== q.target_id ||
          p.window_id !== r.window_id)
      )
        return fail();
    if (v.after && !v.before) return fail();
    if (
      v.step_attempted &&
      (!v.before || v.before.token !== q.expected_token || v.before.complete)
    )
      return fail();
    switch (v.outcome) {
      case "superseded":
        if (
          v.reason !== "target_changed" ||
          !v.before ||
          v.before.token === q.expected_token ||
          v.after ||
          v.step_attempted
        )
          return fail();
        break;
      case "advanced":
      case "unchanged":
        if (!v.before || !v.after || v.before.token !== q.expected_token)
          return fail();
        if (v.outcome === "advanced") {
          if (
            v.reason !== "progressed" ||
            !v.step_attempted ||
            v.before.token === v.after.token
          )
            return fail();
        } else {
          if (v.before.token !== v.after.token) return fail();
          if (v.before.complete) {
            if (v.reason !== "already_complete" || v.step_attempted)
              return fail();
          } else if (v.reason !== "no_progress" || !v.step_attempted)
            return fail();
        }
        break;
      case "failed":
        if (
          ![
            "target_missing",
            "scope_mismatch",
            "invalid_state",
            "dependency_failed",
            "execution_unavailable",
            "step_timeout",
          ].includes(v.reason)
        )
          return fail();
    }
  });
export const mergeRetryPage = z.object({
  bk_tenant_id: tenant,
  kind: mergeRetryKind,
  target_id: hash,
  items: z.array(mergeRetryRequest).max(4),
  next: z.string().max(2048),
});
export type MergeRetryCommand = z.infer<typeof mergeRetryCommand>;
export type MergeRetryRequest = z.infer<typeof mergeRetryRequest>;
export type MergeControlPoint = z.infer<typeof mergeControlPoint>;
