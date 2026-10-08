import type { ShieldBinding, ShieldRecord } from "../shared/shield-runtime.js";
export const shieldTenant = "tenant-a",
  childID = "child-alert";
export function runtimeBinding(): ShieldBinding {
  return {
    binding_id: "a".repeat(64),
    policy: { id: "switch-shield", version: 2, digest: "b".repeat(64) },
    type: "rely_shield",
    mode: "cmdb_shield",
    source_event_id: "event-child",
    severity: "warning",
    main_alert_id: "main-alert",
    bound_at: "2026-10-05T00:00:00Z",
    reason: "交换机故障引发的主机告警",
  };
}
export function runtimeShield(released = false): ShieldRecord {
  return {
    bk_tenant_id: shieldTenant,
    alert_id: childID,
    event_source_id: "source-b",
    revision: released ? 3 : 2,
    status: "active",
    severity: "warning",
    shield: released
      ? { active: false, bindings: [] }
      : {
          active: true,
          bindings: [runtimeBinding()],
          next_check_at: "2026-10-05T00:00:05Z",
        },
    admission: {},
  };
}
export function runtimeShieldHistory() {
  return {
    bk_tenant_id: shieldTenant,
    alert_id: childID,
    next: "",
    items: [
      {
        bk_tenant_id: shieldTenant,
        alert_id: childID,
        log_id: "unshield-log",
        operation_kind: "unshield",
        operator_kind: "system",
        created_time: "2026-10-05T00:01:00Z",
        params: {
          operation_id: "stable-check",
          bindings: [runtimeBinding()],
          before_bindings: [runtimeBinding()],
          after_bindings: [],
        },
      },
    ],
  };
}
