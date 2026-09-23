import type {
  Capabilities,
  EntityItem,
  EntityKind,
  EntityStats,
} from "../../src/shared/contracts";
import { explorerFilters } from "../../src/web/pages/explorer";

export const explorerCapabilities: Capabilities = {
  version: "test",
  metrics: { configured: false, source: "prometheus" },
  entities: Object.fromEntries(
    (["events", "alerts", "alert-logs"] as EntityKind[]).map((entity) => [
      entity,
      {
        source: "elasticsearch",
        filters: [
          "tenantId",
          "id",
          "from",
          "to",
          ...explorerFilters[entity].map((f) => f.capability),
        ],
      },
    ]),
  ) as Capabilities["entities"],
  storage: { elasticsearch: { configured: true } },
  limits: {
    defaultRangeSeconds: 3600,
    maxRangeSeconds: 604800,
    defaultLimit: 50,
    maxLimit: 200,
  },
};
export const alertFixture: EntityItem = {
  tenantId: "tenant-a",
  id: "alert-cpu-001",
  timestamp: "2026-09-23T04:10:00.000Z",
  summary: { status: "active", severity: "critical" },
  payload: {
    alert_id: "alert-cpu-001",
    bk_tenant_id: "tenant-a",
    event_source_id: "host-monitor",
    event_source_version: 3,
    title: "主机 CPU 使用率持续超过阈值",
    content: "db-prod-01 的 CPU 使用率超过 90%，当前观测值 96.4%。",
    fingerprint: "host:db-prod-01:cpu",
    status: "active",
    severity: "critical",
    subject_id: "host-101",
    subject_name: "db-prod-01",
    subject_system: "cmdb",
    subject_type: "host",
    begin_at: "2026-09-10T01:00:00.000Z",
    create_at: "2026-09-10T01:00:02.000Z",
    update_at: "2026-09-23T04:10:00.000Z",
    last_occurred_at: "2026-09-23T04:09:58.000Z",
    trigger_event_id: "event-trigger-001",
    latest_event_id: "event-update-002",
    enrich_status: "succeeded",
    labels: { strategy_id: "1001", environment: "production" },
    dimensions: { host: "db-prod-01" },
    enrich: { status: "succeeded", processors: {} },
  },
};
export const eventFixture: EntityItem = {
  tenantId: "tenant-a",
  id: "event-update-002",
  timestamp: "2026-09-23T04:10:00.000Z",
  summary: { state: "accepted", outcome: "alert_updated" },
  payload: {
    event_id: "event-update-002",
    title: "CPU 持续异常采样",
    event_source_id: "host-monitor",
    event_source_version: 3,
    related_alert_ids: ["alert-cpu-001", "alert-cpu-old"],
    fingerprint: "host:db-prod-01:cpu",
    subject_id: "host-101",
    subject_name: "db-prod-01",
    values: { usage: 96.4 },
    evaluations: [
      {
        severity: "critical",
        action: "triggered",
        action_reason: "value > 90",
      },
    ],
    received_at: "2026-09-23T04:10:00.000Z",
    occurred_at: "2026-09-23T04:09:58.000Z",
    _processing: {
      state: "accepted",
      outcome: "alert_updated",
      processed_at: "2026-09-23T04:10:00.500Z",
      evaluations: [
        {
          severity: "critical",
          action: "triggered",
          state: "accepted",
          outcome: "alert_updated",
          reason_code: "",
          related_alert_ids: ["alert-cpu-001"],
        },
      ],
    },
  },
};
export const logFixture: EntityItem = {
  tenantId: "tenant-a",
  id: "log-trigger-001",
  timestamp: "2026-09-23T04:10:00.000Z",
  summary: {
    operation_kind: "trigger",
    operator_kind: "source",
    alert_id: "alert-cpu-001",
  },
  payload: {
    log_id: "log-trigger-001",
    alert_id: "alert-cpu-001",
    operation_kind: "trigger",
    operator_kind: "source",
    created_time: "2026-09-23T04:10:00.000Z",
    params: { event_id: "event-update-002", reason_code: "threshold_exceeded" },
  },
};
export function explorerStats(entity: EntityKind): EntityStats {
  return {
    entity,
    source: "elasticsearch",
    total: 128,
    warnings: [],
    facets:
      entity === "alerts"
        ? [
            {
              name: "status",
              values: [
                { value: "active", count: 96 },
                { value: "recovered", count: 24 },
                { value: "closed", count: 8 },
              ],
            },
            {
              name: "severity",
              values: [
                { value: "critical", count: 100 },
                { value: "warning", count: 28 },
              ],
            },
          ]
        : entity === "events"
          ? [
              {
                name: "processing_state",
                values: [
                  { value: "accepted", count: 120 },
                  { value: "rejected", count: 8 },
                ],
              },
            ]
          : [
              {
                name: "operation_kind",
                values: [
                  { value: "trigger", count: 88 },
                  { value: "push", count: 40 },
                ],
              },
            ],
    timeline: Array.from({ length: 40 }, (_, i) => ({
      timestamp: new Date(
        Date.parse("2026-09-23T03:30:00Z") + i * 60000,
      ).toISOString(),
      count: ((i * 7) % 19) + 1,
    })),
  };
}
