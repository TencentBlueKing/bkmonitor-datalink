import type { ActionDelivery } from "../shared/action-deliveries.js";
export const actionID = "a".repeat(64),
  actionTenant = "tenant-a";
export function actionFixture(
  state: ActionDelivery["progress"]["state"] = "failed",
): ActionDelivery {
  const at = "2026-10-06T02:00:00Z";
  const row: ActionDelivery = {
    bk_tenant_id: actionTenant,
    id: actionID,
    alert_id: "opening-alert",
    alarm_id: "stable-alarm",
    target_id: "kac",
    source_id: "source-a",
    source_version: 4,
    revision: 2,
    alert_status: "active",
    action: "firing",
    action_id: "c".repeat(64),
    request_hash: "d".repeat(64),
    cause: { type: "source_event", id: "opening-event" },
    content_hash: "b".repeat(64),
    task_version: "opaque-cas-1",
    created_at: at,
    progress: {
      state,
      attempts: 1,
      total_attempts: 1,
      generation: 1,
      updated_at: at,
      previous_unconfirmed: false,
    },
  };
  const p = row.progress;
  if (state === "failed" || state === "retry")
    p.error_code = "remote_unauthorized";
  if (["pending", "retry", "waiting_projection"].includes(state)) p.due_at = at;
  if (state === "pending" || state === "waiting_projection")
    p.attempts = p.total_attempts = 0;
  if (state === "waiting_projection") p.error_code = "projection_pending";
  if (state === "sending") p.lease_until = "2026-10-06T02:00:30Z";
  if (state === "succeeded" || state === "skipped")
    p.projection = {
      schema_version: "linkd.kac-projection.v1",
      bk_tenant_id: actionTenant,
      target_id: row.target_id,
      linkd_alert_id: row.alert_id,
      alarm_id: row.alarm_id,
      applied_revision: state === "skipped" ? 3 : row.revision,
      content_hash: row.content_hash,
      applied_status: state === "skipped" ? "closed" : row.alert_status,
      search_visible: true,
      document_ref: "alarm-000001/stable-alarm",
    };
  if (state === "skipped") p.error_code = "superseded_by_terminal";
  if (state === "succeeded")
    p.receipt = {
      schema_version: "linkd.kac-action.v2",
      bk_tenant_id: actionTenant,
      target_id: row.target_id,
      linkd_alert_id: row.alert_id,
      alarm_id: row.alarm_id,
      action_id: row.action_id,
      request_hash: row.request_hash,
      outcome: "queued",
      task_id: "celery-action-1",
    };
  return row;
}
export function recoveredAction(command?: Record<string, unknown>) {
  const row = actionFixture("pending");
  row.task_version = "opaque-cas-2";
  row.progress.generation = 2;
  row.progress.total_attempts = 1;
  row.progress.last_retry = {
    requested_at: row.progress.updated_at,
    command: {
      bk_tenant_id: actionTenant,
      task_id: actionID,
      operation_id: "11111111-1111-4111-8111-111111111111",
      operator_id: "operator-a",
      expected_version: "opaque-cas-1",
      reason: "鉴权恢复后重试",
      ...command,
    },
  };
  return row;
}
export function actionSnapshotFixture(row = actionFixture()) {
  return {
    bk_tenant_id: row.bk_tenant_id,
    id: row.id,
    revision: row.revision,
    request_hash: row.request_hash,
    request: {
      schema_version: "linkd.kac-action.v2",
      action_id: row.action_id,
      bk_tenant_id: row.bk_tenant_id,
      target_id: row.target_id,
      linkd_alert_id: row.alert_id,
      alarm_id: row.alarm_id,
      linkd_revision: row.revision,
      action: row.action,
      cause: row.cause,
      content_hash: row.content_hash,
      alert: {
        bk_tenant_id: row.bk_tenant_id,
        alert_id: row.alert_id,
        revision: row.revision,
        status: row.alert_status,
        title: "冻结时的标题",
        enrich: { object: { a: 1, b: false } },
      },
    },
  };
}
export function actionOrderFixture(
  row = actionFixture(),
  head: ActionDelivery | null = ["succeeded", "skipped"].includes(
    row.progress.state,
  )
    ? null
    : row,
) {
  return {
    bk_tenant_id: row.bk_tenant_id,
    id: row.id,
    alert_id: row.alert_id,
    target_id: row.target_id,
    observed_at: "2026-10-06T02:01:00Z",
    head,
  };
}
