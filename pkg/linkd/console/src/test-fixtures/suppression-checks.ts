import type { SuppressionCheckRequest } from "../shared/suppression-checks.js";
import { runtimeClip } from "./suppression-runtime.js";
export function suppressionRequestFixture(
  done = false,
): SuppressionCheckRequest {
  const w = runtimeClip(),
    at = "2026-10-05T10:00:00Z";
  return {
    id: "a".repeat(64),
    command: {
      bk_tenant_id: w.bk_tenant_id,
      kind: w.kind,
      window_id: w.id,
      expected_epoch: w.epoch,
      expected_owner_alert_id: w.owner_alert_id ?? "",
      operation_id: "11111111-1111-4111-8111-111111111111",
      operator_id: "operator-a",
      reason: "核对 owner 状态",
    },
    state: done ? "completed" : "pending",
    created_at: at,
    previous_unconfirmed: false,
    ...(done
      ? {
          started_at: at,
          completed_at: at,
          result: {
            checked_at: at,
            changed: false,
            outcome: "retained" as const,
            reason: "active_owner" as const,
            observed: w,
            owner_revision: 2,
            owner_status: "active" as const,
          },
        }
      : {}),
  };
}
