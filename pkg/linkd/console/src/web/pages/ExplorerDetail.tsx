import { useQuery, useQueryClient } from "@tanstack/react-query";
import { type FormEvent, useEffect, useRef, useState } from "react";
import { Link } from "react-router-dom";
import type { EntityItem, EntityKind } from "../../shared/contracts";
import { getEntity, searchEntities } from "../api";
import { JsonViewer } from "../components/JsonViewer";
import { formatTime, useTimeMode } from "../time";
import {
  display,
  explorerFilters,
  explorerMeta,
  inputTime,
  parseInputTime,
  record,
  relationLinks,
  valueNames,
} from "./explorer";
import { EntityState, EntityTable, LogTimeline } from "./ExplorerRecords";
import { CloseAlertPanel } from "./CloseAlertPanel";

export function ExplorerDetail({
  entity,
  tenant,
  id,
  maxRange,
  onClose,
}: {
  entity: EntityKind;
  tenant: string;
  id: string;
  maxRange: number;
  onClose: () => void;
}) {
  const dialog = useRef<HTMLDialogElement>(null);
  const [tab, setTab] = useState("overview");
  const [notice, setNotice] = useState("");
  const [revision, setRevision] = useState(0);
  const [closedItem, setClosedItem] = useState<EntityItem>();
  const client = useQueryClient();
  const detail = useQuery({
    queryKey: ["entity-detail", entity, tenant, id],
    queryFn: ({ signal }) => getEntity(entity, tenant, id, signal),
  });
  // dialog 原生管理焦点陷阱、Esc 和关闭后的焦点恢复。
  useEffect(() => {
    const element = dialog.current;
    element?.showModal();
    return () => element?.close();
  }, []);
  const item = closedItem ?? detail.data;
  return (
    <dialog
      className="explorer-dialog entity-explorer"
      ref={dialog}
      onCancel={onClose}
      onClick={(e) => {
        if (e.target === e.currentTarget) onClose();
      }}
      aria-labelledby="explorer-detail-title"
    >
      <header>
        <div>
          <p className="eyebrow">
            {explorerMeta[entity].singular.toUpperCase()} DETAIL
          </p>
          <h2 id="explorer-detail-title">
            {display(item?.payload.title) === "—"
              ? id
              : display(item?.payload.title)}
          </h2>
          <p className="mono">
            {tenant} / {id}
          </p>
        </div>
        <div className="explorer-heading-actions">
          <button
            onClick={async () => {
              try {
                await navigator.clipboard.writeText(window.location.href);
                setNotice("详情链接已复制");
              } catch {
                setNotice("复制失败，请从地址栏复制链接");
              }
            }}
          >
            复制详情链接
          </button>
          <button
            onClick={() => {
              setClosedItem(undefined);
              setRevision((r) => r + 1);
              void detail.refetch();
            }}
          >
            刷新详情
          </button>
          <button aria-label="关闭详情" onClick={onClose}>
            ×
          </button>
        </div>
      </header>
      {notice && (
        <p className="explorer-notice" role="status">
          {notice}
        </p>
      )}
      {detail.isError && (
        <div className="error-banner" role="alert">
          详情读取失败：{detail.error.message}
        </div>
      )}
      {!item ? (
        <div className="explorer-empty">
          {detail.isError ? "请重试读取详情" : "正在读取详情…"}
        </div>
      ) : (
        <>
          <div
            className="explorer-detail-tabs"
            role="tablist"
            aria-label="详情内容"
          >
            {[
              [
                "overview",
                entity === "alerts" ? "概览与关联记录" : "结构化详情",
              ],
              ["fields", "字段与扩展数据"],
              ["json", "完整 JSON"],
            ].map(([key, title]) => (
              <button
                key={key}
                role="tab"
                id={`detail-tab-${key}`}
                aria-controls="explorer-detail-panel"
                aria-selected={tab === key}
                onClick={() => setTab(key)}
              >
                {title}
              </button>
            ))}
          </div>
          <div
            id="explorer-detail-panel"
            role="tabpanel"
            aria-labelledby={`detail-tab-${tab}`}
            className="explorer-detail-body"
          >
            {tab === "overview" && (
              <>
                <RecordOverview entity={entity} item={item} />
                <div className="explorer-relations">
                  {relationLinks(entity, item, maxRange).map((link) => (
                    <Link
                      key={link.to + link.label}
                      to={link.to}
                      onClick={onClose}
                    >
                      {link.label} ↗
                    </Link>
                  ))}
                </div>
                {entity === "alerts" && (
                  <>
                    <CloseAlertPanel
                      item={item}
                      onClosed={(alert) => {
                        const updated = {
                          ...item,
                          timestamp: String(alert.update_at),
                          payload: alert,
                          summary: { ...item.summary, status: alert.status },
                        };
                        setClosedItem(updated);
                        setRevision((r) => r + 1);
                        void client.invalidateQueries({
                          queryKey: ["entities", "alerts"],
                        });
                        void client.invalidateQueries({
                          queryKey: ["entity-stats", "alerts"],
                        });
                        client.setQueryData(
                          ["entity-detail", "alerts", tenant, id],
                          updated,
                        );
                      }}
                    />
                    <AlertRelations
                      key={`${revision}:${item.timestamp}`}
                      item={item}
                      maxRange={maxRange}
                      revision={revision}
                    />
                  </>
                )}
              </>
            )}
            {tab === "fields" && (
              <>
                {(entity === "events"
                  ? [
                      "dimensions",
                      "labels",
                      "values",
                      "source_raw_data",
                      "extra_data",
                    ]
                  : entity === "alerts"
                    ? ["dimensions", "labels", "enrich", "extra_data"]
                    : ["params"]
                ).map((key) => (
                  <section className="explorer-field-section" key={key}>
                    <h3>{key}</h3>
                    <JsonViewer value={item.payload[key] ?? null} />
                  </section>
                ))}
              </>
            )}
            {tab === "json" && <JsonViewer value={item.payload} />}
          </div>
        </>
      )}
    </dialog>
  );
}

function RecordOverview({
  entity,
  item,
}: {
  entity: EntityKind;
  item: EntityItem;
}) {
  const mode = useTimeMode();
  const p = item.payload;
  const processing = record(p._processing);
  const fields =
    entity === "alert-logs"
      ? ["operation_kind", "operator_kind", "alert_id"]
      : [
          "event_source_id",
          "event_source_version",
          "fingerprint",
          "subject_system",
          "subject_type",
          "subject_id",
          "subject_name",
          "source_event_id",
          "source_alert_id",
        ];
  const labels: Record<string, string> = {
    event_source_id: "事件来源",
    event_source_version: "来源版本",
    fingerprint: "Fingerprint",
    subject_system: "对象系统",
    subject_type: "对象类型",
    subject_id: "对象 ID",
    subject_name: "对象名称",
    source_event_id: "来源原始 Event ID",
    source_alert_id: "来源原始 Alert ID",
    operation_kind: "操作类型",
    operator_kind: "操作方",
    alert_id: "所属 Alert",
  };
  const times =
    entity === "alerts"
      ? [
          ["begin_at", "首次发生"],
          ["create_at", "创建时间"],
          ["last_occurred_at", "最近事件发生"],
          ["update_at", "最近更新"],
          ["end_at", "结束时间"],
        ]
      : entity === "events"
        ? [
            ["occurred_at", "发生时间"],
            ["produced_at", "生产时间"],
            ["received_at", "接收时间"],
            ["create_at", "入库时间"],
          ]
        : [["created_time", "记录时间"]];
  return (
    <section className="explorer-overview">
      <div className="explorer-state-line">
        <EntityState
          value={
            entity === "events"
              ? processing.state
              : entity === "alerts"
                ? p.status
                : p.operation_kind
          }
        />
        {entity === "alerts" && (
          <>
            <span>
              当前级别 <strong>{display(p.severity)}</strong>
            </span>
            <span>
              丰富 <EntityState value={p.enrich_status} />
            </span>
          </>
        )}
        {entity === "events" && (
          <span>
            {valueNames[display(processing.outcome)] ??
              display(processing.outcome)}{" "}
            · {display(processing.reason_code)}
          </span>
        )}
      </div>
      {entity === "alerts" && (
        <p className="explorer-context">
          一个 Alert
          表示一次持续生命周期；最近更新表示快照变化，创建时间才表示该告警何时产生。
        </p>
      )}
      <div className="explorer-milestones">
        {times.map(([key, label]) => (
          <div key={key}>
            <span>{label}</span>
            <strong>{formatTime(display(p[key]), mode)}</strong>
          </div>
        ))}
      </div>
      {entity === "alerts" && Boolean(p.end_type || p.end_reason) && (
        <p>
          结束方式：{display(p.end_type)} · {display(p.end_reason)}
        </p>
      )}
      {p.content ? (
        <p className="explorer-content">{display(p.content)}</p>
      ) : null}
      <dl className="explorer-facts">
        {fields.map((key) => (
          <div key={key}>
            <dt>{labels[key]}</dt>
            <dd>{valueNames[display(p[key])] ?? display(p[key])}</dd>
          </div>
        ))}
      </dl>
      {entity === "events" && (
        <>
          <EvaluationTable
            title="来源逐级判定"
            value={p.evaluations}
            fields={["severity", "action", "action_reason"]}
          />
          <EvaluationTable
            title="Lifecycle 逐级处理结果"
            value={processing.evaluations}
            fields={[
              "severity",
              "action",
              "state",
              "outcome",
              "reason_code",
              "related_alert_ids",
            ]}
          />
          <h3>观测值</h3>
          <FieldValues value={p.values} />
          <p className="muted">
            处理完成时间：{formatTime(display(processing.processed_at), mode)}
          </p>
        </>
      )}
      {entity === "alert-logs" && (
        <>
          <h3>操作参数</h3>
          <FieldValues value={p.params} />
        </>
      )}
    </section>
  );
}
function FieldValues({ value }: { value: unknown }) {
  const entries = Object.entries(record(value));
  return entries.length ? (
    <dl className="explorer-facts">
      {entries.map(([key, v]) => (
        <div key={key}>
          <dt>{key}</dt>
          <dd>{display(v)}</dd>
        </div>
      ))}
    </dl>
  ) : (
    <p className="muted">无记录</p>
  );
}
function EvaluationTable({
  title,
  value,
  fields,
}: {
  title: string;
  value: unknown;
  fields: string[];
}) {
  return (
    <section>
      <h3>{title}</h3>
      {Array.isArray(value) && value.length ? (
        <div className="table-scroll">
          <table className="explorer-evaluation-table">
            <thead>
              <tr>
                {fields.map((f) => (
                  <th key={f}>{f}</th>
                ))}
              </tr>
            </thead>
            <tbody>
              {value.map((row, i) => (
                <tr key={i}>
                  {fields.map((f) => (
                    <td key={f}>{display(record(row)[f])}</td>
                  ))}
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      ) : (
        <p className="muted">尚无逐级记录</p>
      )}
    </section>
  );
}

function AlertRelations({
  item,
  maxRange,
  revision,
}: {
  item: EntityItem;
  maxRange: number;
  revision: number;
}) {
  const mode = useTimeMode();
  const end = new Date(item.timestamp).getTime() + 1000;
  const begin = new Date(
    String(item.payload.create_at ?? item.payload.begin_at),
  ).getTime();
  // Event 接收可以远早于 Alert 创建（例如排队积压）；不能以 create_at 截断关联事件。
  const start = end - maxRange * 1000;
  const [range, setRange] = useState({
    from: new Date(start).toISOString(),
    to: new Date(end).toISOString(),
  });
  const [error, setError] = useState("");
  function submit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    const data = new FormData(event.currentTarget);
    try {
      const from = parseInputTime(String(data.get("from")), mode);
      const to = parseInputTime(String(data.get("to")), mode);
      const duration = Date.parse(to) - Date.parse(from);
      if (duration < 0 || duration > maxRange * 1000)
        throw new Error(`关联查询范围不能超过 ${maxRange / 3600} 小时。`);
      setRange({ from, to });
      setError("");
    } catch {
      setError(`请填写有效起止时间，范围不超过 ${maxRange / 3600} 小时。`);
    }
  }
  return (
    <section className="explorer-related" aria-label="告警关联记录">
      <h3>关联记录</h3>
      <p className="muted">
        直接查询同租户、同 Alert 的事件和流水。Event 按接收时间、AlertLog
        按记录时间；单次最多 {maxRange / 3600} 小时，范围外记录需切换时间查看。
      </p>
      {begin < start && (
        <p className="warning-banner">
          此告警生命周期超过查询上限，当前仅展示最近更新前的窗口，并非全部历史。
        </p>
      )}
      <form className="explorer-related-range" key={mode} onSubmit={submit}>
        <label>
          关联开始时间
          <input
            name="from"
            type="datetime-local"
            step="0.001"
            required
            defaultValue={inputTime(range.from, mode)}
          />
        </label>
        <label>
          关联结束时间
          <input
            name="to"
            type="datetime-local"
            step="0.001"
            required
            defaultValue={inputTime(range.to, mode)}
          />
        </label>
        <button type="submit">查询关联记录</button>
      </form>
      {error && (
        <div className="error-banner" role="alert">
          {error}
        </div>
      )}
      <RelatedRecords
        key={`events:${range.from}:${range.to}`}
        entity="events"
        item={item}
        range={range}
        revision={revision}
      />
      <RelatedRecords
        key={`logs:${range.from}:${range.to}`}
        entity="alert-logs"
        item={item}
        range={range}
        revision={revision}
      />
    </section>
  );
}
function RelatedRecords({
  entity,
  item,
  range,
  revision,
}: {
  entity: "events" | "alert-logs";
  item: EntityItem;
  range: { from: string; to: string };
  revision: number;
}) {
  const [cursor, setCursor] = useState("");
  const [history, setHistory] = useState<string[]>([]);
  const [filter, setFilter] = useState("");
  const [selected, setSelected] = useState<EntityItem>();
  const filterKey = entity === "events" ? "state" : "operation_kind";
  const spec = explorerFilters[entity].find((f) => f.key === filterKey)!;
  const query = {
    ...range,
    bk_tenant_id: item.tenantId,
    [entity === "events" ? "related_alert_id" : "alert_id"]: item.id,
    limit: "20",
    order: "asc",
    cursor,
    [filterKey]: filter,
  };
  const result = useQuery({
    queryKey: ["alert-related", entity, query, revision],
    queryFn: ({ signal }) => searchEntities(entity, query, signal),
    staleTime: Infinity,
  });
  return (
    <section
      className="explorer-related-records"
      aria-label={
        entity === "events" ? "关联 Event 列表" : "关联 AlertLog 时间线"
      }
    >
      <div className="explorer-results-heading">
        <div>
          <h3>
            {entity === "events" ? "关联 Event 列表" : "关联 AlertLog 时间线"}
          </h3>
          <span>本页 {result.data?.items.length ?? 0} 条 · 最早在前</span>
        </div>
        <label>
          {spec.label}
          <select
            value={filter}
            onChange={(e) => {
              setFilter(e.target.value);
              setCursor("");
              setHistory([]);
              setSelected(undefined);
            }}
          >
            <option value="">全部</option>
            {spec.options?.map((v) => (
              <option key={v} value={v}>
                {valueNames[v] ?? v}
              </option>
            ))}
          </select>
        </label>
      </div>
      {result.isError ? (
        <div className="error-banner" role="alert">
          关联查询失败：{result.error.message}
          <button onClick={() => void result.refetch()}>重试关联查询</button>
        </div>
      ) : result.isPending ? (
        <p className="explorer-empty">正在读取关联记录…</p>
      ) : !result.data.items.length ? (
        <p className="explorer-empty">当前时间范围和筛选条件下没有关联记录。</p>
      ) : entity === "events" ? (
        <EntityTable
          entity={entity}
          items={result.data.items}
          onInspect={setSelected}
        />
      ) : (
        <LogTimeline items={result.data.items} onInspect={setSelected} />
      )}
      {result.data?.warnings.map((w) => (
        <p className="explorer-result-note" key={w}>
          {w}
        </p>
      ))}
      <div className="pagination">
        <span>每页 20 条</span>
        <div>
          <button
            disabled={!history.length || result.isFetching}
            onClick={() => {
              setCursor(history.at(-1)!);
              setHistory((h) => h.slice(0, -1));
              setSelected(undefined);
            }}
          >
            上一页
          </button>
          <button
            disabled={
              !result.data?.nextCursor || result.isFetching || result.isError
            }
            onClick={() => {
              setHistory((h) => [...h, cursor]);
              setCursor(result.data!.nextCursor!);
              setSelected(undefined);
            }}
          >
            下一页
          </button>
        </div>
      </div>
      {selected && (
        <div className="explorer-inline-detail">
          <div className="explorer-query-title">
            <strong>
              {explorerMeta[entity].singular} · {selected.id}
            </strong>
            <button onClick={() => setSelected(undefined)}>收起记录详情</button>
          </div>
          <RecordOverview entity={entity} item={selected} />
          <details>
            <summary>完整 JSON</summary>
            <JsonViewer value={selected.payload} />
          </details>
        </div>
      )}
    </section>
  );
}
