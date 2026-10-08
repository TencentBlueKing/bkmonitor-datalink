import { z } from "zod";
const tenant = z.string().regex(/^[a-zA-Z0-9_-]{1,64}$/),
  id = z.string().min(1).max(160),
  hash = z.string().regex(/^[a-f0-9]{64}$/),
  at = z.string().datetime({ offset: true }),
  revision = z.number().int().min(0).max(Number.MAX_SAFE_INTEGER);
export const shieldCheckCommand = z
  .object({
    bk_tenant_id: tenant,
    operation_id: z.string().uuid(),
    expected_revision: revision.min(1),
    reason: z
      .string()
      .trim()
      .min(1)
      .refine((v) => new TextEncoder().encode(v).length <= 1024),
  })
  .strict();
export const shieldCheck = z
  .object({
    bk_tenant_id: tenant,
    alert_id: id,
    trigger: z.enum(["timer", "request", "hint"]),
    request_id: hash.optional(),
    started_at: at,
    finished_at: at,
    error_code: z
      .enum([
        "revision_changed",
        "scope_mismatch",
        "invalid_state",
        "record_missing",
        "configuration_unavailable",
        "alert_busy",
        "check_timeout",
        "check_cancelled",
        "version_conflict",
        "dependency_failed",
      ])
      .optional(),
    report: z.object({
      observed_revision: revision,
      result_revision: revision,
      checked_at: at,
      outcome: z.enum([
        "failed",
        "superseded",
        "inactive",
        "retained",
        "changed",
        "partial",
      ]),
      changed: z.boolean(),
      remaining_bindings: z.number().int().min(0).max(16),
      decision: z
        .object({
          severity: z.string().min(1).max(32),
          steps: z
            .array(
              z
                .object({
                  policy: z.object({
                    type: z.literal("shield"),
                    id: z.string().min(1).max(80),
                    version: revision.min(1),
                    digest: hash,
                  }),
                  outcome: z.enum([
                    "retained",
                    "released",
                    "skipped",
                    "bound",
                    "not_matched",
                  ]),
                  reason_code: z.string().max(80).optional(),
                  binding_id: hash.optional(),
                  from_binding: z.boolean().optional(),
                })
                .strict()
                .superRefine((s, ctx) => {
                  if (
                    s.from_binding
                      ? !s.binding_id ||
                        !["retained", "released", "skipped"].includes(s.outcome)
                      : !["bound", "not_matched", "skipped"].includes(
                          s.outcome,
                        ) || (s.outcome === "bound") !== Boolean(s.binding_id)
                  )
                    ctx.addIssue({
                      code: "custom",
                      message: "复查步骤来源与结果不一致",
                    });
                }),
            )
            .max(272)
            .default([]),
        })
        .optional(),
    }),
  })
  .superRefine((c, ctx) => {
    const r = c.report;
    const steps = r.decision?.steps ?? [];
    if (
      steps.filter((s) => s.from_binding).length > 16 ||
      steps.filter((s) => !s.from_binding).length > 256
    )
      ctx.addIssue({ code: "custom", message: "复查步骤超限" });
    if (
      (c.trigger === "request") !== Boolean(c.request_id) ||
      Date.parse(c.finished_at) < Date.parse(c.started_at) ||
      ["failed", "superseded"].includes(r.outcome) !== Boolean(c.error_code) ||
      (r.outcome === "superseded") !== (c.error_code === "revision_changed") ||
      r.result_revision !== r.observed_revision + Number(r.changed) ||
      (["inactive", "retained", "superseded"].includes(r.outcome) &&
        r.changed) ||
      (r.outcome === "changed" && !r.changed) ||
      (r.observed_revision === 0 &&
        (r.outcome !== "failed" || r.changed || r.decision))
    )
      ctx.addIssue({ code: "custom", message: "复查诊断不一致" });
  });
export const shieldCheckRequest = z
  .object({
    id: hash,
    command: shieldCheckCommand.extend({
      operation_id: z.string().regex(/^[a-zA-Z0-9_-]{1,128}$/),
      reason: z
        .string()
        .refine(
          (v) =>
            v.trim().length > 0 && new TextEncoder().encode(v).length <= 1024,
        ),
      alert_id: id,
      operator_id: z.string().min(1).max(256),
    }),
    state: z.enum(["pending", "completed", "failed", "superseded"]),
    created_at: at,
    completed_at: at.optional(),
    result: shieldCheck.optional(),
  })
  .superRefine((r, ctx) => {
    const c = r.result;
    if (
      r.state === "pending"
        ? Boolean(c || r.completed_at)
        : !c ||
          !r.completed_at ||
          c.bk_tenant_id !== r.command.bk_tenant_id ||
          c.alert_id !== r.command.alert_id ||
          c.trigger !== "request" ||
          c.request_id !== r.id ||
          c.finished_at !== r.completed_at ||
          Date.parse(r.completed_at) < Date.parse(r.created_at) ||
          r.state !==
            (c.report.outcome === "superseded"
              ? "superseded"
              : c.error_code
                ? "failed"
                : "completed") ||
          (r.state === "completed" &&
            c.report.observed_revision !== r.command.expected_revision)
    )
      ctx.addIssue({ code: "custom", message: "复查请求结果不一致" });
  });
export const shieldLatestCheck = z.object({
  bk_tenant_id: tenant,
  alert_id: id,
  check: shieldCheck.nullable(),
});
export const shieldCheckRequests = z.object({
  bk_tenant_id: tenant,
  alert_id: id,
  items: z.array(shieldCheckRequest).max(4),
  next: z.string().max(8192),
});
export type ShieldCheck = z.infer<typeof shieldCheck>;
export type ShieldCheckCommand = z.infer<typeof shieldCheckCommand>;
export type ShieldCheckRequest = z.infer<typeof shieldCheckRequest>;
