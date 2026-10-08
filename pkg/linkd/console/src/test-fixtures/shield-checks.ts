import type {
  ShieldCheck,
  ShieldCheckRequest,
} from "../shared/shield-checks.js";
import { childID, shieldTenant } from "./shield-runtime.js";
export function runtimeCheck(): ShieldCheck {
  return {
    bk_tenant_id: shieldTenant,
    alert_id: childID,
    trigger: "timer",
    started_at: "2026-10-05T00:00:00Z",
    finished_at: "2026-10-05T00:00:01Z",
    report: {
      observed_revision: 2,
      result_revision: 2,
      checked_at: "2026-10-05T00:00:00Z",
      outcome: "partial",
      changed: false,
      remaining_bindings: 1,
      decision: {
        severity: "warning",
        steps: [
          {
            policy: {
              type: "shield",
              id: "switch-shield",
              version: 2,
              digest: "b".repeat(64),
            },
            outcome: "skipped",
            reason_code: "dependency_child_unavailable",
            binding_id: "a".repeat(64),
            from_binding: true,
          },
        ],
      },
    },
  };
}
export function runtimeCheckRequest(completed = false): ShieldCheckRequest {
  const r: ShieldCheckRequest = {
    id: "c".repeat(64),
    command: {
      bk_tenant_id: shieldTenant,
      alert_id: childID,
      operation_id: "a38ff051-bb9e-4cec-8421-d0ba821bde50",
      operator_id: "console-local",
      reason: "依赖服务恢复后复查",
      expected_revision: 2,
    },
    state: "pending",
    created_at: "2026-10-04T23:59:59Z",
  };
  if (completed) {
    r.state = "completed";
    r.result = { ...runtimeCheck(), trigger: "request", request_id: r.id };
    r.completed_at = r.result.finished_at;
  }
  return r;
}
