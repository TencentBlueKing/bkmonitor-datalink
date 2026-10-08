import { z } from "zod";
const tenant = z.string().regex(/^[a-zA-Z0-9_-]{1,64}$/),
  source = z.string().regex(/^[a-zA-Z0-9_-]{1,32}$/),
  id = z.string().min(1).max(160),
  hash = z.string().regex(/^[a-f0-9]{64}$/),
  at = z.string().datetime({ offset: true });
const window = z
  .object({
    id: z.string().regex(/^[a-f0-9]{64}(:[a-f0-9]{64})?$/),
    epoch: id.optional(),
    missing: z.boolean().optional(),
  })
  .strict();
const outcome = z.discriminatedUnion("state", [
  z
    .object({
      state: z.literal("confirmed"),
      removed: z.number().int().min(0).max(512),
      windows: z.array(window).max(512).optional(),
    })
    .strict(),
  z.object({ state: z.literal("unavailable") }).strict(),
  z.object({ state: z.literal("not_applicable") }).strict(),
]);
export const cleanupRecord = z
  .object({
    id: hash,
    cause: z.object({
      bk_tenant_id: tenant,
      event_source_id: source,
      fingerprint: z.string().min(1).max(128),
      trigger: z.enum(["alert_terminal", "event_terminal"]),
      alert_id: id.optional(),
      revision: z.number().int().min(1).max(Number.MAX_SAFE_INTEGER).optional(),
      status: z.enum(["recovered", "closed"]).optional(),
      event_id: id.optional(),
    }),
    state: z.enum(["pending", "completed"]),
    started_at: at,
    finished_at: at.optional(),
    previous_unconfirmed: z.boolean(),
    result: z.object({ clip: outcome, aggregation: outcome }).optional(),
  })
  .superRefine((r, c) => {
    const cause = r.cause;
    for (const [kind, o] of Object.entries(r.result ?? {})) {
      if (o.state !== "confirmed") continue;
      const windows = o.windows ?? [];
      let last = "";
      if (windows.length !== o.removed)
        c.addIssue({ code: "custom", message: "删除数量与明细不一致" });
      for (const w of windows) {
        if (
          !(
            kind === "clip" ? /^[a-f0-9]{64}:[a-f0-9]{64}$/ : /^[a-f0-9]{64}$/
          ).test(w.id) ||
          w.id <= last ||
          (w.missing ? kind !== "clip" || w.epoch !== undefined : !w.epoch)
        )
          c.addIssue({ code: "custom", message: "清理窗口身份或代次不合法" });
        last = w.id;
      }
    }

    if (
      cause.trigger === "alert_terminal"
        ? !cause.alert_id || !cause.revision || !cause.status || cause.event_id
        : !cause.event_id || cause.alert_id || cause.revision || cause.status
    )
      c.addIssue({ code: "custom", message: "清理原因不完整" });
    if (
      r.state === "pending"
        ? r.result || r.finished_at
        : !r.result ||
          !r.finished_at ||
          Date.parse(r.finished_at) < Date.parse(r.started_at)
    )
      c.addIssue({ code: "custom", message: "清理阶段不一致" });
    if (
      r.result &&
      (r.result.clip.state === "not_applicable" ||
        (cause.trigger === "event_terminal") !==
          (r.result.aggregation.state === "not_applicable"))
    )
      c.addIssue({ code: "custom", message: "清理范围不一致" });
  });
export const cleanupQuery = z
  .object({
    bk_tenant_id: tenant,
    event_source_id: source.optional(),
    fingerprint: z.string().min(1).max(128).optional(),
    alert_id: id.optional(),
    event_id: id.optional(),
    state: z.enum(["pending", "completed"]).optional(),
    window_kind: z.enum(["clip", "aggregation"]).optional(),
    window_id: z.string().max(129).optional(),
    epoch: id.optional(),
    after: z.string().max(2048).default(""),
    limit: z.coerce.number().int().min(1).max(4).default(4),
  })
  .strict()
  .superRefine((q, c) => {
    if (
      (q.window_id &&
        !(
          q.window_kind === "clip"
            ? /^[a-f0-9]{64}:[a-f0-9]{64}$/
            : q.window_kind === "aggregation"
              ? /^[a-f0-9]{64}$/
              : /^$/
        ).test(q.window_id)) ||
      (q.epoch && !q.window_id)
    )
      c.addIssue({ code: "custom", message: "窗口筛选不完整" });
  });
export const cleanupPage = z.object({
  bk_tenant_id: tenant,
  items: z.array(cleanupRecord).max(4),
  next: z.string().max(2048),
});
export type CleanupRecord = z.infer<typeof cleanupRecord>;
export type CleanupQuery = z.infer<typeof cleanupQuery>;
export function cleanupMatches(r: CleanupRecord, q: CleanupQuery) {
  const selected = q.window_kind ? r.result?.[q.window_kind] : undefined;
  const windowMatch =
    !q.window_kind ||
    (selected?.state === "confirmed" &&
      selected.windows?.some(
        (w) =>
          (!q.window_id || q.window_id === w.id) &&
          (!q.epoch || q.epoch === w.epoch),
      ));
  return Boolean(
    (!q.event_source_id || q.event_source_id === r.cause.event_source_id) &&
    (!q.fingerprint || q.fingerprint === r.cause.fingerprint) &&
    (!q.alert_id || q.alert_id === r.cause.alert_id) &&
    (!q.event_id || q.event_id === r.cause.event_id) &&
    (!q.state || q.state === r.state) &&
    windowMatch,
  );
}
