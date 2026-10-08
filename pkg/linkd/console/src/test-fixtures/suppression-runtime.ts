import type { SuppressionWindow } from "../shared/suppression-runtime.js";
export const suppressionTenant = "tenant-a",
  clipID =
    "eb127310d9f368370acd0a6fe199de992e60b306d1b84d22a767f94ce3a32a70:0e966c7947469e8e4efa1778639ac1d184f16b03b8d2e8731c3e6d71641a28e5",
  aggregationID = "d".repeat(64);
export function runtimeClip(count = 2): SuppressionWindow {
  return {
    id: clipID,
    bk_tenant_id: suppressionTenant,
    kind: "clip",
    policy: { id: "policy", version: 1, digest: "a".repeat(64) },
    event_source_id: "source",
    fingerprint: "fingerprint",
    epoch: "opening-event",
    owner_alert_id: "owner-alert",
    state: "retained",
    observed_at_ms: 1791151200000,
    retention_ms: 65000,
    duration_seconds: 60,
    threshold: 3,
    observed_count: count,
    member_count: 3,
    last_evaluated_at_ms: 1791151190000,
  };
}
export function runtimeAggregation(): SuppressionWindow {
  return {
    id: aggregationID,
    bk_tenant_id: suppressionTenant,
    kind: "aggregation",
    policy: { id: "aggregation", version: 2, digest: "c".repeat(64) },
    group_key: "b".repeat(64),
    epoch: "owner-event",
    owner_alert_id: "main-alert",
    owner_event_id: "owner-event",
    owner_source_id: "another-source",
    owner_fingerprint: "group-owner",
    state: "admitted",
    observed_at_ms: 1791151200000,
    retention_ms: 65000,
    duration_seconds: 60,
    member_count: 2,
    started_at_ms: 1791151190000,
    expires_at_ms: 1791151250000,
  };
}
export function runtimeSuppressionMembers(
  v: SuppressionWindow = runtimeClip(),
  next = "",
) {
  return {
    bk_tenant_id: v.bk_tenant_id,
    kind: v.kind,
    id: v.id,
    epoch: v.epoch,
    observed_at_ms: v.observed_at_ms,
    items: [
      {
        event_id: v.epoch,
        event_source_id: v.event_source_id ?? v.owner_source_id!,
        fingerprint: v.fingerprint ?? v.owner_fingerprint!,
        at_ms: v.last_evaluated_at_ms ?? v.started_at_ms!,
      },
    ],
    next,
  };
}
