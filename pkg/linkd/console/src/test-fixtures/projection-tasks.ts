import type { ProjectionTask } from "../shared/projection-tasks.js";
export const projectionID = "a".repeat(64),
  projectionTenant = "tenant-a";
export function projectionFixture(
  state: ProjectionTask["progress"]["state"] = "failed",
): ProjectionTask {
  const at = "2026-10-05T02:00:00Z";
  const row: ProjectionTask = {
    bk_tenant_id: projectionTenant,
    id: projectionID,
    alert_id: "opening-alert",
    alarm_id: "linkd-550e8400-e29b-51d4-a716-446655440000",
    target_id: "kac",
    source_id: "source-a",
    source_version: 4,
    revision: 2,
    alert_status: "active",
    content_hash: "b".repeat(64),
    task_version: "opaque-cas-1",
    created_at: at,
    progress: {
      state,
      attempts: 1,
      total_attempts: 1,
      generation: 1,
      updated_at: at,
    },
  };
  const p = row.progress;
  if (state === "failed" || state === "retry")
    p.error_code = "remote_unauthorized";
  if (state === "pending" || state === "retry") p.due_at = at;
  if (state === "pending") p.attempts = p.total_attempts = 0;
  if (state === "sending") p.lease_until = "2026-10-05T02:00:30Z";
  if (state === "delivered" || state === "succeeded")
    p.receipt = {
      schema_version: "linkd.kac-projection.v1",
      bk_tenant_id: projectionTenant,
      target_id: row.target_id,
      linkd_alert_id: row.alert_id,
      alarm_id: row.alarm_id,
      applied_revision: row.revision,
      content_hash: row.content_hash,
      applied_status: row.alert_status,
      search_visible: true,
      document_ref: "alarm-000001/linkd-550e8400-e29b-51d4-a716-446655440000",
    };
  return row;
}
export function recoveredProjection(command?: Record<string, unknown>) {
  const row = projectionFixture("pending");
  row.task_version = "opaque-cas-2";
  row.progress.generation = 2;
  row.progress.total_attempts = 1;
  row.progress.last_retry = {
    requested_at: row.progress.updated_at,
    command: {
      bk_tenant_id: projectionTenant,
      task_id: projectionID,
      operation_id: "11111111-1111-4111-8111-111111111111",
      operator_id: "operator-a",
      expected_version: "opaque-cas-1",
      reason: "鉴权恢复后重试",
      ...command,
    },
  };
  return row;
}
