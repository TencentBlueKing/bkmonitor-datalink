import { Link } from "react-router-dom";
import type { EntityItem, EntityKind } from "../../shared/contracts";
import { formatTime, useTimeMode } from "../time";
import {
  display,
  entityURL,
  explorerMeta,
  record,
  valueNames,
} from "./explorer";

export function EntityState({ value }: { value: unknown }) {
  const raw = display(value);
  const tone = ["failed", "rejected"].includes(raw)
    ? "danger"
    : ["active", "accepted", "succeeded"].includes(raw)
      ? "success"
      : ["unprocessed", "partial", "pending", "orphaned"].includes(raw)
        ? "warning"
        : "neutral";
  return (
    <span className={`status-badge ${tone}`} title={raw}>
      {valueNames[raw] ?? raw}
    </span>
  );
}

export function EntityTable({
  entity,
  items,
  onInspect,
}: {
  entity: EntityKind;
  items: EntityItem[];
  onInspect: (item: EntityItem) => void;
}) {
  const mode = useTimeMode();
  if (entity === "kac-alarms")
    return <KACAlarmTable items={items} onInspect={onInspect} />;
  return (
    <div className="table-scroll">
      <table className="explorer-table">
        <thead>
          <tr>
            <th>{entity === "alert-logs" ? "操作 / 流水 ID" : "标题 / ID"}</th>
            <th>租户{entity !== "alert-logs" && " / 来源"}</th>
            <th>
              {entity === "events"
                ? "处理状态 / 结果"
                : entity === "alerts"
                  ? "状态 / 当前级别"
                  : "操作方"}
            </th>
            <th>
              {entity === "events"
                ? "来源判定 / 关联告警"
                : entity === "alerts"
                  ? "对象 / 丰富"
                  : "所属告警 / 原因"}
            </th>
            <th>
              {entity === "alerts" ? "生命周期时间" : explorerMeta[entity].time}
            </th>
          </tr>
        </thead>
        <tbody>
          {items.map((item) => {
            const p = item.payload;
            const process = record(p._processing);
            return (
              <tr key={`${item.tenantId}:${item.id}`}>
                <td>
                  <button
                    className="explorer-title-button"
                    onClick={() => onInspect(item)}
                  >
                    {entity === "alert-logs"
                      ? (valueNames[display(p.operation_kind)] ??
                        display(p.operation_kind))
                      : display(p.title) === "—"
                        ? "无标题"
                        : display(p.title)}
                  </button>
                  <span className="explorer-secondary mono" title={item.id}>
                    {item.id}
                  </span>
                </td>
                <td>
                  <span className="tenant-chip">{item.tenantId}</span>
                  {entity !== "alert-logs" && (
                    <span
                      className="explorer-secondary"
                      title={display(p.event_source_id)}
                    >
                      {display(p.event_source_id)}
                      {p.event_source_version
                        ? ` · v${display(p.event_source_version)}`
                        : ""}
                    </span>
                  )}
                </td>
                <td>
                  <EntityState
                    value={
                      entity === "events"
                        ? process.state
                        : entity === "alerts"
                          ? p.status
                          : p.operator_kind
                    }
                  />
                  <span className="explorer-secondary">
                    {entity === "events"
                      ? (valueNames[display(process.outcome)] ??
                        display(process.outcome))
                      : entity === "alerts"
                        ? `级别 ${display(p.severity)}`
                        : display(p.operation_kind)}
                  </span>
                </td>
                <td>
                  {entity === "events" ? (
                    <>
                      <span>
                        {Array.isArray(p.evaluations)
                          ? p.evaluations
                              .map((e) => {
                                const v = record(e);
                                return `${display(v.severity)} · ${display(v.action)}`;
                              })
                              .join(" / ")
                          : "—"}
                      </span>
                      <span className="explorer-secondary">
                        {Array.isArray(p.related_alert_ids)
                          ? p.related_alert_ids.length
                          : 0}{" "}
                        个关联告警 · 丰富{" "}
                        <EntityState value={p.enrich_status} />
                      </span>
                    </>
                  ) : entity === "alerts" ? (
                    <>
                      <span>{display(p.subject_name || p.subject_id)}</span>
                      <span className="explorer-secondary">
                        丰富 · <EntityState value={p.enrich_status} />
                      </span>
                    </>
                  ) : (
                    <>
                      <Link
                        className="explorer-inline-link"
                        to={entityURL("alerts", item.tenantId, {
                          id: display(p.alert_id),
                        })}
                      >
                        {display(p.alert_id)}
                      </Link>
                      <span className="explorer-secondary">
                        {display(
                          record(p.params).reason_code ??
                            record(p.params).reason,
                        )}
                      </span>
                    </>
                  )}
                </td>
                <td>
                  {entity === "alerts" ? (
                    <div className="explorer-row-times">
                      <span>
                        <i>首次发生</i>
                        {formatTime(display(p.begin_at), mode)}
                      </span>
                      <span>
                        <i>创建</i>
                        {formatTime(display(p.create_at), mode)}
                      </span>
                      <span>
                        <i>最近更新</i>
                        {formatTime(item.timestamp, mode)}
                      </span>
                    </div>
                  ) : (
                    <span className="mono">
                      {formatTime(item.timestamp, mode)}
                    </span>
                  )}
                </td>
              </tr>
            );
          })}
        </tbody>
      </table>
    </div>
  );
}

function KACAlarmTable({
  items,
  onInspect,
}: {
  items: EntityItem[];
  onInspect: (item: EntityItem) => void;
}) {
  const mode = useTimeMode();
  return (
    <div className="table-scroll">
      <table className="explorer-table">
        <thead>
          <tr>
            <th>名称 / alarm_id</th>
            <th>租户 / KAC 来源</th>
            <th>状态 / 级别</th>
            <th>对象 / 来源动作</th>
            <th>告警发生时间</th>
          </tr>
        </thead>
        <tbody>
          {items.map((item) => {
            const p = item.payload;
            return (
              <tr key={`${item.tenantId}:${item.id}`}>
                <td>
                  <button
                    className="explorer-title-button"
                    onClick={() => onInspect(item)}
                  >
                    {display(p.name)}
                  </button>
                  <span className="explorer-secondary mono">{item.id}</span>
                </td>
                <td>
                  <span className="tenant-chip">{item.tenantId}</span>
                  <span className="explorer-secondary">
                    {display(p.source_name)} · {display(p.source_id)}
                  </span>
                </td>
                <td>
                  <EntityState value={p.status} />
                  <span className="explorer-secondary">
                    级别 {display(p.level)}
                  </span>
                </td>
                <td>
                  {display(p.object)}
                  <span className="explorer-secondary">
                    {display(p.action)}
                  </span>
                </td>
                <td>
                  <span className="mono">
                    {formatTime(item.timestamp, mode)}
                  </span>
                </td>
              </tr>
            );
          })}
        </tbody>
      </table>
    </div>
  );
}

export function LogTimeline({
  items,
  onInspect,
}: {
  items: EntityItem[];
  onInspect: (item: EntityItem) => void;
}) {
  const mode = useTimeMode();
  return (
    <ol className="explorer-log-timeline">
      {items.map((item) => {
        const p = item.payload;
        const params = record(p.params);
        return (
          <li key={`${item.tenantId}:${item.id}`}>
            <time>{formatTime(item.timestamp, mode)}</time>
            <div className="explorer-log-entry">
              <div>
                <button
                  className="explorer-title-button"
                  onClick={() => onInspect(item)}
                >
                  {valueNames[display(p.operation_kind)] ??
                    display(p.operation_kind)}
                </button>
                <EntityState value={p.operator_kind} />
                <span className="tenant-chip">{item.tenantId}</span>
                <span className="muted mono">{display(p.operation_kind)}</span>
              </div>
              <p>
                Alert{" "}
                <Link
                  to={entityURL("alerts", item.tenantId, {
                    id: display(p.alert_id),
                  })}
                >
                  {display(p.alert_id)}
                </Link>
              </p>
              <p>
                {display(params.reason_code ?? params.reason)}
                {params.hook_name ? ` · Hook ${display(params.hook_name)}` : ""}
                {params.from_severity
                  ? ` · ${display(params.from_severity)} → ${display(params.to_severity)}`
                  : ""}
              </p>
              <button
                className="explorer-log-id mono"
                onClick={() => onInspect(item)}
              >
                查看流水 · {item.id} →
              </button>
            </div>
          </li>
        );
      })}
    </ol>
  );
}
