import { z } from "zod";
const tenant = z.string().regex(/^[a-zA-Z0-9_-]{1,64}$/),
  source = z.string().regex(/^[a-zA-Z0-9_-]{1,32}$/),
  entity = z.string().min(1).max(160),
  fingerprint = z.string().min(1).max(128),
  hash = z.string().regex(/^[a-f0-9]{64}$/),
  millis = z
    .number()
    .int()
    .min(0)
    .max(2 ** 46);
export const suppressionKind = z.enum(["clip", "aggregation"]);
export type SuppressionKind = z.infer<typeof suppressionKind>;
export const suppressionKey = z
  .object({ kind: suppressionKind, id: z.string().min(1).max(129) })
  .superRefine((v, c) => {
    if (
      !(
        v.kind === "clip" ? /^[a-f0-9]{64}:[a-f0-9]{64}$/ : /^[a-f0-9]{64}$/
      ).test(v.id)
    )
      c.addIssue({ code: "custom", message: "抑制运行身份不合法" });
  });
export const suppressionQuery = z
  .object({
    bk_tenant_id: tenant,
    policy_id: z
      .string()
      .regex(/^[a-zA-Z0-9_-]{1,80}$/)
      .optional(),
    event_source_id: source.optional(),
    owner_alert_id: entity.optional(),
    after: z.string().max(2048).default(""),
    limit: z.coerce.number().int().min(1).max(4).default(4),
  })
  .strict();
export type SuppressionQuery = z.infer<typeof suppressionQuery>;
export const suppressionScope = z.object({ bk_tenant_id: tenant }).strict();
export const suppressionMemberQuery = suppressionScope.extend({
  epoch: entity,
  after: z.string().max(2048).default(""),
  limit: z.coerce.number().int().min(1).max(4).default(4),
});
export const suppressionWindow = z
  .object({
    id: z.string().min(1).max(129),
    bk_tenant_id: tenant,
    kind: suppressionKind,
    policy: z.object({
      id: z.string().regex(/^[a-zA-Z0-9_-]{1,80}$/),
      version: z.number().int().positive().max(Number.MAX_SAFE_INTEGER),
      digest: hash,
    }),
    event_source_id: source.optional(),
    fingerprint: fingerprint.optional(),
    group_key: hash.optional(),
    epoch: entity,
    owner_alert_id: entity.optional(),
    owner_event_id: entity.optional(),
    owner_source_id: source.optional(),
    owner_fingerprint: fingerprint.optional(),
    state: z.enum(["retained", "pending", "admitted"]),
    observed_at_ms: millis,
    retention_ms: z.number().int().min(0).max(2592060000),
    duration_seconds: z.number().int().min(1).max(2592000),
    threshold: z.number().int().min(1).max(10000).optional(),
    observed_count: z.number().int().min(0).max(10000).optional(),
    member_count: z.number().int().min(0).max(10000),
    last_evaluated_at_ms: millis.optional(),
    started_at_ms: millis.optional(),
    expires_at_ms: millis.optional(),
    pending_until_ms: millis.optional(),
  })
  .superRefine((v, c) => {
    if (!suppressionKey.safeParse(v).success)
      c.addIssue({ code: "custom", message: "抑制身份不一致" });
    const invalid =
      v.kind === "clip"
        ? v.state !== "retained" ||
          !v.event_source_id ||
          !v.fingerprint ||
          v.observed_count === undefined ||
          v.observed_count > v.member_count ||
          !v.threshold ||
          v.group_key !== undefined ||
          v.owner_event_id !== undefined ||
          v.owner_source_id !== undefined ||
          v.owner_fingerprint !== undefined ||
          v.expires_at_ms !== undefined ||
          v.started_at_ms !== undefined ||
          v.pending_until_ms !== undefined
        : !["pending", "admitted"].includes(v.state) ||
          !v.group_key ||
          !v.owner_alert_id ||
          v.owner_event_id !== v.epoch ||
          !v.owner_source_id ||
          !v.owner_fingerprint ||
          v.expires_at_ms === undefined ||
          v.expires_at_ms - (v.started_at_ms ?? 0) !==
            v.duration_seconds * 1000 ||
          v.observed_count !== undefined ||
          v.threshold !== undefined ||
          v.event_source_id !== undefined ||
          v.fingerprint !== undefined ||
          v.last_evaluated_at_ms !== undefined ||
          (v.state === "pending"
            ? !v.pending_until_ms
            : v.pending_until_ms !== undefined);
    if (invalid)
      c.addIssue({ code: "custom", message: "抑制运行快照不完整或状态冲突" });
  });
export type SuppressionWindow = z.infer<typeof suppressionWindow>;
export const suppressionPage = z.object({
  bk_tenant_id: tenant,
  kind: suppressionKind,
  items: z.array(suppressionWindow).max(4),
  next: z.string().max(2048),
});
export const suppressionMembers = z.object({
  bk_tenant_id: tenant,
  kind: suppressionKind,
  id: z.string().min(1).max(129),
  epoch: entity,
  observed_at_ms: millis,
  items: z
    .array(
      z.object({
        event_id: entity,
        event_source_id: source,
        fingerprint,
        at_ms: millis,
      }),
    )
    .max(4),
  next: z.string().max(2048),
});
export function matchesSuppressionQuery(
  v: SuppressionWindow,
  q: SuppressionQuery,
) {
  return (
    (!q.policy_id || v.policy.id === q.policy_id) &&
    (!q.event_source_id ||
      (v.event_source_id ?? v.owner_source_id) === q.event_source_id) &&
    (!q.owner_alert_id || v.owner_alert_id === q.owner_alert_id)
  );
}
