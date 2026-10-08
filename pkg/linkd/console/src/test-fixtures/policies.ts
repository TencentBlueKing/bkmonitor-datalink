import type {
  PolicyRelease,
  PolicyRecord,
  PolicyPreview,
  PolicyKind,
} from "../shared/policies.js";

export function policyRelease(
  version = 2,
  kind: PolicyKind = "suppression",
): PolicyRelease {
  const expression = {
    expression: "A AND B",
    A: { condition: "term", target_key: "source_id", target_value: "database" },
    B: { condition: "term", target_key: "level", target_value: "warning" },
  };
  const common = {
    name: "数据库告警降噪",
    is_enable: true,
    updated_at: "2026-10-05T00:00:00Z",
    space_code: "bkcc__2",
    timezone: "Asia/Shanghai",
    activate_times: [
      {
        period: "everyday",
        open_clock_time: "00:00:00",
        close_clock_time: "23:59:59",
      },
    ],
    policy: expression,
  };
  const spec =
    kind === "suppression"
      ? {
          ...common,
          scheme: [
            {
              type: "clip",
              count: version === 1 ? 2 : 3,
              duration: 60,
              duration_type: "second",
            },
          ],
        }
      : kind === "shield"
        ? {
            ...common,
            shield_type: "rely_shield",
            shield_mode: "custom_shield",
            rely_policy: expression,
          }
        : {
            ...common,
            policy: [expression, expression],
            merge_cycle: 60,
            is_cycle_merge: false,
            aggregate_fields: ["model_id", "model_inst_id"],
            new_alarm_config: [
              { key: "name", value: "合并告警" },
              { key: "level", value: "warning" },
            ],
            max_merge_field_length: 500,
          };
  return {
    bk_tenant_id: "tenant-a",
    type: kind,
    id: "db-noise",
    version,
    spec,
    deleted: false,
    operation_id: "sync-" + version,
    request_digest: "b".repeat(64),
    created_at: "2026-10-05T00:00:00Z",
    compiled: {
      compiler_version: 1,
      digest: String(version).repeat(64),
      timezone: "Asia/Shanghai",
      condition_groups: kind === "merge" ? 2 : 1,
      target_selectors: 0,
    },
  };
}
export function policyRecord(kind: PolicyKind = "suppression"): PolicyRecord {
  const release = policyRelease(2, kind);
  return {
    bk_tenant_id: release.bk_tenant_id,
    type: release.type,
    id: release.id,
    revision: 3,
    published: 2,
    spec: release.spec,
    compiled: release.compiled,
    deleted: false,
    pending: policyRelease(3, kind),
  };
}
export function policyPreview(version = 2): PolicyPreview {
  return {
    id: "db-noise",
    version,
    compiled: policyRelease(version).compiled,
    mode: "matching_only",
    at: "2026-10-05T00:02:00Z",
    evaluations: [
      {
        severity: "warning",
        evaluated: true,
        matched: true,
        group_key: "a".repeat(64),
        groups: [
          {
            index: 0,
            evaluated: true,
            matched: true,
            conditions: [
              { id: "A", evaluated: true, matched: true },
              { id: "B", evaluated: true, matched: true },
            ],
          },
        ],
      },
      {
        severity: "critical",
        evaluated: false,
        matched: false,
        reason: "condition_unavailable",
        groups: [
          {
            index: 0,
            evaluated: false,
            matched: false,
            conditions: [
              { id: "A", evaluated: true, matched: true },
              {
                id: "B",
                evaluated: false,
                matched: false,
                reason: "field_unavailable",
              },
            ],
          },
        ],
      },
    ],
  };
}
