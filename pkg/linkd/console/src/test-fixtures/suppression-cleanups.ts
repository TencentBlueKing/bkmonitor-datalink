import type { CleanupRecord } from "../shared/suppression-cleanups.js";
export const cleanupID = "c".repeat(64);
export function cleanupFixture(pending = false): CleanupRecord {
  return {
    id: cleanupID,
    cause: {
      bk_tenant_id: "tenant-a",
      event_source_id: "source-a",
      fingerprint: "fp-a",
      trigger: "event_terminal",
      event_id: "recover-event",
    },
    state: pending ? "pending" : "completed",
    started_at: "2026-10-05T08:00:00Z",
    previous_unconfirmed: !pending,
    ...(!pending
      ? {
          finished_at: "2026-10-05T08:00:01Z",
          result: {
            clip: { state: "unavailable" as const },
            aggregation: { state: "not_applicable" as const },
          },
        }
      : {}),
  };
}
