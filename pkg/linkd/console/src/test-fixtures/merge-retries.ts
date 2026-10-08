import type {
  MergeControlPoint,
  MergeRetryRequest,
} from "../shared/merge-retries.js";
import { decisionID, windowID, mergeTenant } from "./merge-runtime.js";
export function mergePointFixture(
  kind: "decisions" | "relations" = "decisions",
  complete = false,
): MergeControlPoint {
  return {
    bk_tenant_id: mergeTenant,
    kind,
    target_id: decisionID,
    window_id: windowID,
    token: "e".repeat(64),
    phase:
      kind === "decisions"
        ? complete
          ? "completed"
          : "prepared"
        : complete
          ? "ended"
          : "ready",
    ...(kind === "decisions" ? { outcome: "succeeded" as const } : {}),
    complete,
    updated_at: "2026-10-05T10:00:00Z",
    capture_offset: kind === "decisions" ? 2 : 0,
    member_offset: kind === "decisions" && complete ? 2 : 0,
    index_offset: kind === "relations" ? 3 : 0,
    end_offset: kind === "relations" && complete ? 2 : 0,
  };
}
export function mergeRetryFixture(done = false): MergeRetryRequest {
  const before = mergePointFixture(),
    at = "2026-10-05T10:00:00Z";
  return {
    id: "d".repeat(64),
    window_id: before.window_id,
    command: {
      bk_tenant_id: before.bk_tenant_id,
      kind: before.kind,
      target_id: before.target_id,
      expected_token: before.token,
      operation_id: "11111111-1111-4111-8111-111111111111",
      operator_id: "operator-a",
      reason: "接续原合并进度",
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
            outcome: "advanced" as const,
            reason: "progressed" as const,
            step_attempted: true,
            before,
            after: {
              ...before,
              token: "f".repeat(64),
              phase: "waiting_parent" as const,
            },
          },
        }
      : {}),
  };
}
