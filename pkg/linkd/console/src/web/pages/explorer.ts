import type { EntityItem, EntityKind } from "../../shared/contracts";
import type { TimeMode } from "../time";

export const explorerMeta = {
  events: {
    title: "Event Explorer",
    name: "事件",
    singular: "Event",
    time: "接收时间",
    description: "从来源事实到逐级裁决，追踪每一次事件的处理结果。",
  },
  alerts: {
    title: "Alert Explorer",
    name: "告警",
    singular: "Alert",
    time: "更新时间",
    description: "查看告警当前状态、丰富结果与完整关联链路。",
  },
  "alert-logs": {
    title: "AlertLog Explorer",
    name: "操作流水",
    singular: "AlertLog",
    time: "记录时间",
    description: "按时间还原状态操作与 Hook 输出记录。",
  },
} satisfies Record<EntityKind, unknown>;

export type FilterSpec = {
  key: string;
  capability: string;
  label: string;
  options?: string[];
  advanced?: boolean;
};
const identityFields: FilterSpec[] = [
  {
    key: "fingerprint",
    capability: "fingerprint",
    label: "Fingerprint",
    advanced: true,
  },
  {
    key: "subject_id",
    capability: "subjectId",
    label: "对象 ID",
    advanced: true,
  },
  {
    key: "source_event_id",
    capability: "sourceEventId",
    label: "来源原始 Event ID",
    advanced: true,
  },
  {
    key: "source_alert_id",
    capability: "sourceAlertId",
    label: "来源原始 Alert ID",
    advanced: true,
  },
];
export const explorerFilters: Record<EntityKind, FilterSpec[]> = {
  events: [
    { key: "event_source_id", capability: "eventSourceId", label: "事件来源" },
    {
      key: "state",
      capability: "state",
      label: "处理状态",
      options: [
        "unprocessed",
        "accepted",
        "suppressed",
        "orphaned",
        "rejected",
      ],
    },
    {
      key: "outcome",
      capability: "outcome",
      label: "处理结果",
      options: [
        "alert_created",
        "alert_updated",
        "alert_severity_changed",
        "alert_rotated",
        "alert_recovered",
        "alert_closed",
        "alert_suppressed",
        "event_orphaned",
        "rejected",
        "already_processed",
      ],
    },
    {
      key: "related_alert_id",
      capability: "relatedAlertId",
      label: "关联 Alert ID",
      advanced: true,
    },
    ...identityFields,
  ],
  alerts: [
    { key: "event_source_id", capability: "eventSourceId", label: "事件来源" },
    {
      key: "status",
      capability: "status",
      label: "告警状态",
      options: ["active", "recovered", "closed"],
    },
    { key: "severity", capability: "severity", label: "当前级别" },
    {
      key: "enrich_status",
      capability: "enrichStatus",
      label: "丰富状态",
      options: ["pending", "succeeded", "partial", "failed", "skipped"],
    },
    ...identityFields,
  ],
  "alert-logs": [
    { key: "alert_id", capability: "alertId", label: "Alert ID" },
    {
      key: "operation_kind",
      capability: "operationKind",
      label: "操作类型",
      options: [
        "trigger",
        "severity_change",
        "recover",
        "close",
        "suppress",
        "push",
      ],
    },
    {
      key: "operator_kind",
      capability: "operatorKind",
      label: "操作方",
      options: ["source", "user", "system"],
    },
  ],
};
export const valueNames: Record<string, string> = {
  unprocessed: "待处理",
  accepted: "已接受",
  suppressed: "已抑制",
  orphaned: "未关联",
  rejected: "已拒绝",
  active: "告警中",
  recovered: "已恢复",
  closed: "已关闭",
  pending: "待丰富",
  succeeded: "成功",
  partial: "部分成功",
  failed: "失败",
  skipped: "已跳过",
  trigger: "触发",
  severity_change: "级别变更",
  recover: "恢复",
  close: "关闭",
  suppress: "抑制",
  push: "输出",
  source: "来源",
  user: "用户",
  system: "系统",
  alert_created: "创建告警",
  alert_updated: "更新告警",
  alert_severity_changed: "更新级别",
  alert_rotated: "轮换告警",
  alert_recovered: "恢复告警",
  alert_closed: "关闭告警",
  alert_suppressed: "抑制告警",
  event_orphaned: "事件未关联",
  already_processed: "已处理",
};
export const rangeOptions = [
  ["15m", "最近 15 分钟", 900],
  ["1h", "最近 1 小时", 3600],
  ["6h", "最近 6 小时", 21600],
  ["24h", "最近 24 小时", 86400],
  ["7d", "最近 7 天", 604800],
] as const;

export function record(value: unknown): Record<string, unknown> {
  return value && typeof value === "object" && !Array.isArray(value)
    ? (value as Record<string, unknown>)
    : {};
}
export function display(value: unknown): string {
  if (value === undefined || value === null || value === "") return "—";
  return typeof value === "object" ? JSON.stringify(value) : String(value);
}
export function inputTime(value: string, mode: TimeMode): string {
  const date = new Date(value);
  if (!value || Number.isNaN(date.getTime())) return "";
  return new Date(
    date.getTime() - (mode === "local" ? date.getTimezoneOffset() * 60000 : 0),
  )
    .toISOString()
    .slice(0, 23);
}
export function parseInputTime(value: string, mode: TimeMode): string {
  return new Date(value + (mode === "utc" ? "Z" : "")).toISOString();
}
// URL 同时保存界面状态；仅白名单查询字段可以传入 API。
export function searchValues(
  entity: EntityKind,
  params: URLSearchParams,
  anchor: number,
  defaultRange: number,
): Record<string, string> {
  const result: Record<string, string> = {};
  for (const key of [
    "bk_tenant_id",
    "id",
    "from",
    "to",
    "order",
    "limit",
    "cursor",
    ...explorerFilters[entity].map((f) => f.key),
  ]) {
    const value = params.get(key);
    if (value) result[key] = value;
  }
  if (!result.id || result.from || result.to) {
    result.to ||= new Date(anchor).toISOString();
    const end = new Date(result.to).getTime();
    if (Number.isFinite(end))
      result.from ||= new Date(end - defaultRange * 1000).toISOString();
  }
  return result;
}

export function entityURL(
  entity: EntityKind,
  tenant: string,
  values: Record<string, string>,
) {
  return `/explore/${entity}?${new URLSearchParams({ bk_tenant_id: tenant, ...values })}`;
}

// 关联集合仍受最大时间窗限制。以实体的业务时间结束，避免旧告警跳转后落入“最近一小时”。
export function relationLinks(
  entity: EntityKind,
  item: EntityItem,
  maxRange: number,
) {
  const p = item.payload;
  const links: Array<{ label: string; to: string }> = [];
  const exact = (kind: EntityKind, id: unknown, label: string) => {
    if (typeof id === "string" && id)
      links.push({ label, to: entityURL(kind, item.tenantId, { id }) });
  };
  if (entity === "events") {
    if (Array.isArray(p.related_alert_ids))
      for (const id of p.related_alert_ids)
        exact("alerts", id, `关联告警 · ${display(id)}`);
  } else if (entity === "alerts") {
    exact("events", p.trigger_event_id, "首次触发事件");
    exact("events", p.latest_event_id, "最近关联事件");
    const end = new Date(item.timestamp).getTime() + 1000;
    if (Number.isFinite(end)) {
      const from = new Date(end - maxRange * 1000).toISOString();
      const to = new Date(end).toISOString();
      links.push({
        label: "关联事件（更新时间前窗口）",
        to: entityURL("events", item.tenantId, {
          related_alert_id: item.id,
          from,
          to,
        }),
      });
      links.push({
        label: "操作流水（更新时间前窗口）",
        to: entityURL("alert-logs", item.tenantId, {
          alert_id: item.id,
          from,
          to,
          order: "asc",
        }),
      });
    }
    links.push({
      label: "模拟丰富",
      to: `/enrich-preview?${new URLSearchParams({ bk_tenant_id: item.tenantId, alert_id: item.id, event_source_id: String(p.event_source_id ?? "") })}`,
    });
  } else {
    exact("alerts", p.alert_id, "所属告警");
    const params = record(p.params);
    exact(
      "events",
      params.event_id ??
        (params.cause_type === "source_event" ? params.cause_id : undefined),
      "触发事件",
    );
  }
  return links;
}
