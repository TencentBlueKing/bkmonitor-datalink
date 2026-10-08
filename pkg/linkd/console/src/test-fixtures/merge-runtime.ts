import type {
  MergeDecision,
  MergeRelation,
  MergeWindow,
} from "../shared/merge-runtime.js";
export const mergeTenant = "tenant-a";
export const decisionID = "a".repeat(64),
  windowID = "b".repeat(64);
const policy = { id: "db-merge", version: 2, digest: "d".repeat(64) };
const base = { bk_tenant_id: mergeTenant, group_key: "c".repeat(64), policy };
const at = "2026-10-05T00:00:00Z",
  deadline = "2026-10-05T00:01:00Z";
export function runtimeDecision(): MergeDecision {
  return {
    ...base,
    id: decisionID,
    window_id: windowID,
    started_at: at,
    deadline,
    frozen_at: at,
    updated_at: deadline,
    outcome: "succeeded",
    phase: "completed",
    parent_event_id: "event-parent",
    parent_alert_id: "parent",
    member_count: 2,
    wait_member_count: 2,
    capture_offset: 2,
    member_offset: 2,
    window_finished: true,
  };
}
export function runtimeWindow(): MergeWindow {
  return {
    ...base,
    id: windowID,
    started_at: at,
    deadline,
    cyclic: false,
    revision: 3,
    member_count: 2,
    committed_count: 1,
    group_counts: [1, 0],
    members: [
      {
        main: {
          alert_id: "child-a",
          event_id: "event-a",
          event_source_id: "source-a",
          fingerprint: "fp-a",
          severity: "warning",
        },
        groups: [0],
        first_at_ms: Date.parse(at),
        committed: true,
      },
      {
        main: {
          alert_id: "child-b",
          event_id: "event-b",
          event_source_id: "source-b",
          fingerprint: "fp-b",
          severity: "warning",
        },
        groups: [1],
        first_at_ms: Date.parse(at),
        committed: false,
      },
    ],
  };
}
export function runtimeRelation(): MergeRelation {
  return {
    ...base,
    id: decisionID,
    window_id: windowID,
    parent_alert_id: "parent",
    parent_fingerprint: "fp-parent",
    wait_member_ids: ["child-a", "child-b"],
    members: [
      { alert_id: "child-a", state: "linked" },
      { alert_id: "child-b", state: "linked" },
    ],
    state: "ended",
    end_offset: 2,
    end_reason: "parent_ended",
    end_started_at: at,
    ended_at: deadline,
    index_offset: 3,
    created_at: at,
    updated_at: deadline,
  };
}
export function runtimeMembers() {
  return {
    bk_tenant_id: mergeTenant,
    next: "",
    items: [
      {
        bk_tenant_id: mergeTenant,
        decision_id: decisionID,
        alert_id: "child-a",
        event_source_id: "source-a",
        severity: "warning",
        status: "active",
        trigger_event_id: "event-a",
        enrich_status: "succeeded",
        revision: 1,
      },
    ],
  };
}

export function runtimeSnapshot() {
  return {
    bk_tenant_id: mergeTenant,
    decision_id: decisionID,
    alert: {
      bk_tenant_id: mergeTenant,
      alert_id: "child-a",
      status: "active",
      severity: "warning",
      content: "opening content",
      revision: 1,
    },
  };
}
