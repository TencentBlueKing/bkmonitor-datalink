import { useQuery } from "@tanstack/react-query";
import {
  createColumnHelper,
  flexRender,
  getCoreRowModel,
  useReactTable,
} from "@tanstack/react-table";
import { type FormEvent, useMemo, useState } from "react";
import { Link, useSearchParams } from "react-router-dom";

import type { EntityItem, EntityKind } from "../../shared/contracts";
import { getCapabilities, getEntityStats, searchEntities } from "../api";
import { HelpLabel } from "../components/HelpTip";
import { JsonViewer } from "../components/JsonViewer";
import { StatusBadge } from "../components/StatusBadge";
import { useReportPageQueryFailure } from "../navigation";
import { formatTime, useTimeMode } from "../time";

const metadata: Record<
  EntityKind,
  { title: string; description: string; singular: string }
> = {
  events: {
    title: "Event Explorer",
    description: "检查事件事实、处理状态和关联 Alert。",
    singular: "Event",
  },
  alerts: {
    title: "Alert Explorer",
    description: "检查告警当前态、丰富结果和生命周期关联。",
    singular: "Alert",
  },
  "alert-logs": {
    title: "AlertLog Explorer",
    description: "检查状态操作和最终输出流水。",
    singular: "AlertLog",
  },
};

const columnHelper = createColumnHelper<EntityItem>();

export function ExplorerPage({ entity }: { entity: EntityKind }) {
  const timeMode = useTimeMode();
  const [urlParams, setURLParams] = useSearchParams();
  const [selected, setSelected] = useState<EntityItem>();
  const [cursorHistory, setCursorHistory] = useState<string[]>([]);
  const capabilities = useQuery({
    queryKey: ["capabilities"],
    queryFn: getCapabilities,
  });
  const filters = capabilities.data?.entities[entity].filters ?? [];
  const queryValues = useMemo(
    () => Object.fromEntries(urlParams.entries()),
    [urlParams],
  );
  const result = useQuery({
    queryKey: ["entities", entity, queryValues],
    queryFn: () => searchEntities(entity, queryValues),
  });
  const statsValues = useMemo(() => {
    const values = { ...queryValues };
    delete values.cursor;
    delete values.limit;
    delete values.id;
    return values;
  }, [queryValues]);
  const stats = useQuery({
    queryKey: ["entity-stats", entity, statsValues],
    queryFn: () => getEntityStats(entity, statsValues),
  });
  useReportPageQueryFailure(result.isError);

  const columns = useMemo(
    () => [
      columnHelper.accessor("timestamp", {
        header: () => (
          <HelpLabel label="时间" help="该实体用于列表过滤与排序的业务时间。" />
        ),
        cell: ({ getValue }) => (
          <span className="mono muted">{formatTime(getValue(), timeMode)}</span>
        ),
      }),
      columnHelper.accessor("tenantId", {
        header: () => (
          <HelpLabel label="租户" help="实体所属的 bk_tenant_id 租户作用域。" />
        ),
        cell: ({ getValue }) => (
          <span className="tenant-chip">{getValue()}</span>
        ),
      }),
      columnHelper.accessor("id", {
        header: () => (
          <HelpLabel
            label={`${metadata[entity].singular} ID`}
            help={`${metadata[entity].singular} 的稳定业务标识。`}
          />
        ),
        cell: ({ getValue }) => (
          <span className="mono id-cell">{getValue()}</span>
        ),
      }),
      columnHelper.display({
        id: "status",
        header: () => (
          <HelpLabel
            label="状态 / 操作"
            help="Event 的处理状态、Alert 的当前状态，或 AlertLog 的操作类型。"
          />
        ),
        cell: ({ row }) => (
          <StatusBadge
            value={
              row.original.summary.state ??
              row.original.summary.status ??
              row.original.summary.operation_kind ??
              "—"
            }
          />
        ),
      }),
      columnHelper.display({
        id: "summary",
        header: () => (
          <HelpLabel
            label="摘要"
            help="优先展示标题、EventSource 或关联 Alert ID，便于快速识别。"
          />
        ),
        cell: ({ row }) => (
          <span className="summary-cell">
            {String(
              row.original.summary.title ??
                row.original.summary.event_source_id ??
                row.original.summary.alert_id ??
                "—",
            )}
          </span>
        ),
      }),
      columnHelper.display({
        id: "action",
        header: "",
        cell: ({ row }) => (
          <button
            className="inspect-button"
            type="button"
            onClick={() => setSelected(row.original)}
          >
            检查 →
          </button>
        ),
      }),
    ],
    [entity, timeMode],
  );
  // TanStack Table 返回带内部状态的函数集合，React Compiler 会主动跳过该组件的自动 memo。
  // eslint-disable-next-line react-hooks/incompatible-library
  const table = useReactTable({
    data: result.data?.items ?? [],
    columns,
    getCoreRowModel: getCoreRowModel(),
  });

  function submit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    const data = new FormData(event.currentTarget);
    const next = new URLSearchParams();
    for (const [key, value] of data.entries()) {
      const text = String(value).trim();
      if (!text) continue;
      next.set(
        key,
        key === "from" || key === "to" ? new Date(text).toISOString() : text,
      );
    }
    setCursorHistory([]);
    setURLParams(next);
  }

  function nextPage() {
    if (!result.data?.nextCursor) return;
    const current = urlParams.get("cursor");
    setCursorHistory((history) => [...history, current ?? ""]);
    const next = new URLSearchParams(urlParams);
    next.set("cursor", result.data.nextCursor);
    setURLParams(next);
  }

  function previousPage() {
    const previous = cursorHistory.at(-1);
    if (previous === undefined) return;
    const next = new URLSearchParams(urlParams);
    if (previous) next.set("cursor", previous);
    else next.delete("cursor");
    setCursorHistory((history) => history.slice(0, -1));
    setURLParams(next);
  }

  return (
    <section>
      <div className="page-heading explorer-heading">
        <div>
          <p className="eyebrow">STORAGE INSPECTOR</p>
          <h1>{metadata[entity].title}</h1>
          <p>{metadata[entity].description}</p>
        </div>
        <span className="source-label">
          SOURCE · {capabilities.data?.entities[entity].source ?? "—"}
        </span>
      </div>

      <form
        className="filter-panel"
        key={entity + urlParams.toString()}
        onSubmit={submit}
      >
        <Filter
          label="bk_tenant_id"
          name="bk_tenant_id"
          defaultValue={urlParams.get("bk_tenant_id") ?? ""}
          placeholder="全部租户"
          help="精确限定 bk_tenant_id；留空时在有界时间窗内跨租户查询。"
        />
        <Filter
          label="ID"
          name="id"
          defaultValue={urlParams.get("id") ?? ""}
          placeholder="精确匹配"
          help={`精确匹配 ${metadata[entity].singular} ID；提供 ID 时可省略时间范围。`}
        />
        <Filter
          label="开始时间"
          name="from"
          defaultValue={urlParams.get("from") ?? ""}
          type="datetime-local"
          help="查询时间窗起点；未填写时使用默认最近 1 小时。"
        />
        <Filter
          label="结束时间"
          name="to"
          defaultValue={urlParams.get("to") ?? ""}
          type="datetime-local"
          help="查询时间窗终点；未填写时固定为本次查询开始时刻。"
        />
        {filters.includes("state") && (
          <Filter
            label="State"
            name="state"
            defaultValue={urlParams.get("state") ?? ""}
            placeholder="unprocessed"
            help="按 EventProcessing 当前处理状态精确过滤。"
          />
        )}
        {filters.includes("status") && (
          <Filter
            label="Status"
            name="status"
            defaultValue={urlParams.get("status") ?? ""}
            placeholder="active"
            help="按 Alert 当前生命周期状态精确过滤。"
          />
        )}
        {filters.includes("eventSourceId") && (
          <Filter
            label="Event Source"
            name="event_source_id"
            defaultValue={urlParams.get("event_source_id") ?? ""}
            help="按产生 Event 的 EventSource 稳定标识过滤。"
          />
        )}
        {filters.includes("relatedAlertId") && (
          <Filter
            label="Related Alert ID"
            name="related_alert_id"
            defaultValue={urlParams.get("related_alert_id") ?? ""}
            help="查找当前已关联到指定 Alert 的 Event。"
          />
        )}
        {filters.includes("fingerprint") && (
          <Filter
            label="Fingerprint"
            name="fingerprint"
            defaultValue={urlParams.get("fingerprint") ?? ""}
            help="按 EventSource 规则生成的告警关联 fingerprint 精确过滤。"
          />
        )}
        {filters.includes("severity") && (
          <Filter
            label="Severity"
            name="severity"
            defaultValue={urlParams.get("severity") ?? ""}
            help="按事件或告警的严重程度精确过滤。"
          />
        )}
        {filters.includes("alertId") && (
          <Filter
            label="Alert ID"
            name="alert_id"
            defaultValue={urlParams.get("alert_id") ?? ""}
            help="按父 Alert ID 查看对应的 AlertLog 流水。"
          />
        )}
        {filters.includes("operationKind") && (
          <Filter
            label="Operation"
            name="operation_kind"
            defaultValue={urlParams.get("operation_kind") ?? ""}
            help="按 AlertLog 记录的业务操作类型精确过滤。"
          />
        )}
        {filters.includes("operatorKind") && (
          <Filter
            label="Operator"
            name="operator_kind"
            defaultValue={urlParams.get("operator_kind") ?? ""}
            help="按触发该操作的主体类型精确过滤。"
          />
        )}
        <label>
          <HelpLabel label="Limit" help="单页最多返回的记录数，上限为 200。" />
          <select
            name="limit"
            aria-label="Limit"
            defaultValue={urlParams.get("limit") ?? "50"}
          >
            <option value="25">25</option>
            <option value="50">50</option>
            <option value="100">100</option>
            <option value="200">200</option>
          </select>
        </label>
        <button className="primary-button" type="submit">
          执行查询
        </button>
      </form>

      {stats.data && (
        <div className="entity-stats-panel">
          <article className="storage-stat">
            <HelpLabel
              label="统计总数"
              help="当前筛选条件命中的实体总数，不受列表单页 Limit 影响。"
            />
            <strong>
              {new Intl.NumberFormat("en-US").format(stats.data.total)}
            </strong>
          </article>
          {stats.data.facets.map((facet) => (
            <article className="facet-card" key={facet.name}>
              <h3>
                <HelpLabel label={facet.name} help={facetHelp(facet.name)} />
              </h3>
              {facet.values.slice(0, 8).map((value) => (
                <div key={value.value}>
                  <span>{value.value || "(empty)"}</span>
                  <strong>{value.count}</strong>
                </div>
              ))}
            </article>
          ))}
          {stats.data.timeline.length > 0 && (
            <article className="timeline-card">
              <h3>
                <HelpLabel
                  label="时间趋势"
                  help="当前筛选时间窗内按固定桶聚合的实体数量；柱高按本图最大桶归一化。"
                />
              </h3>
              <div className="timeline-bars">
                {stats.data.timeline.slice(-80).map((point) => {
                  const max = Math.max(
                    ...stats.data.timeline.map((item) => item.count),
                    1,
                  );
                  return (
                    <span
                      key={point.timestamp}
                      title={`${point.timestamp}: ${point.count}`}
                      style={{
                        height: `${Math.max(2, (point.count / max) * 100)}%`,
                      }}
                    />
                  );
                })}
              </div>
            </article>
          )}
        </div>
      )}
      {stats.data?.warnings.map((warning) => (
        <div className="warning-banner" key={warning}>
          {warning}
        </div>
      ))}

      {result.data?.warnings.map((warning) => (
        <div className="warning-banner" key={warning}>
          {warning}
        </div>
      ))}
      {result.isError && (
        <div className="error-banner">查询失败：{result.error.message}</div>
      )}

      <div className="table-panel">
        <div className="table-meta">
          <span>
            {result.isFetching
              ? "正在查询…"
              : `${result.data?.items.length ?? 0} 条结果`}
          </span>
          <span>不执行全量 count</span>
        </div>
        <div className="table-scroll">
          <table>
            <thead>
              {table.getHeaderGroups().map((group) => (
                <tr key={group.id}>
                  {group.headers.map((header) => (
                    <th key={header.id}>
                      {flexRender(
                        header.column.columnDef.header,
                        header.getContext(),
                      )}
                    </th>
                  ))}
                </tr>
              ))}
            </thead>
            <tbody>
              {table.getRowModel().rows.map((row) => (
                <tr key={`${row.original.tenantId}:${row.original.id}`}>
                  {row.getVisibleCells().map((cell) => (
                    <td key={cell.id}>
                      {flexRender(
                        cell.column.columnDef.cell,
                        cell.getContext(),
                      )}
                    </td>
                  ))}
                </tr>
              ))}
              {!result.isLoading && table.getRowModel().rows.length === 0 && (
                <tr>
                  <td className="empty-row" colSpan={columns.length}>
                    当前条件下没有数据
                  </td>
                </tr>
              )}
            </tbody>
          </table>
        </div>
        <div className="pagination">
          <button
            type="button"
            disabled={cursorHistory.length === 0}
            onClick={previousPage}
          >
            ← 上一页
          </button>
          <button
            type="button"
            disabled={!result.data?.nextCursor}
            onClick={nextPage}
          >
            下一页 →
          </button>
        </div>
      </div>

      {selected && (
        <DetailDrawer
          entity={entity}
          item={selected}
          onClose={() => setSelected(undefined)}
        />
      )}
    </section>
  );
}

function Filter({
  label,
  name,
  defaultValue,
  placeholder,
  type = "text",
  help,
}: {
  label: string;
  name: string;
  defaultValue: string;
  placeholder?: string;
  type?: string;
  help: string;
}) {
  return (
    <label>
      <HelpLabel label={label} help={help} />
      <input
        aria-label={label}
        type={type}
        name={name}
        defaultValue={toInputValue(defaultValue, type)}
        placeholder={placeholder}
      />
    </label>
  );
}

function DetailDrawer({
  entity,
  item,
  onClose,
}: {
  entity: EntityKind;
  item: EntityItem;
  onClose: () => void;
}) {
  const timeMode = useTimeMode();
  const [copied, setCopied] = useState(false);
  const links = relationLinks(entity, item);
  return (
    <div className="drawer-backdrop" role="presentation" onMouseDown={onClose}>
      <aside
        className="detail-drawer"
        role="dialog"
        aria-modal="true"
        onMouseDown={(event) => event.stopPropagation()}
      >
        <header>
          <div>
            <p className="eyebrow">
              {metadata[entity].singular.toUpperCase()} DETAIL
            </p>
            <h2>{item.id}</h2>
            <span>
              {item.tenantId} · {formatTime(item.timestamp, timeMode)}
            </span>
          </div>
          <div className="drawer-actions">
            <button
              type="button"
              onClick={async () => {
                await navigator.clipboard.writeText(item.id);
                setCopied(true);
              }}
            >
              {copied ? "已复制" : "复制 ID"}
            </button>
            <button type="button" aria-label="关闭详情" onClick={onClose}>
              ×
            </button>
          </div>
        </header>
        {links.length > 0 && (
          <div className="relation-links">
            {links.map((link) => (
              <Link key={link.to} to={link.to} onClick={onClose}>
                {link.label} →
              </Link>
            ))}
          </div>
        )}
        <div className="detail-summary">
          {Object.entries(item.summary).map(([key, value]) => (
            <div key={key}>
              <HelpLabel label={key} help={summaryFieldHelp(key)} />
              <strong>{String(value)}</strong>
            </div>
          ))}
        </div>
        <JsonViewer
          value={item.payload}
          description="原始领域字段保持存储中的命名和值；这里不把未知或缺失字段推断为默认值。"
        />
      </aside>
    </div>
  );
}

function relationLinks(entity: EntityKind, item: EntityItem) {
  const tenant = encodeURIComponent(item.tenantId);
  const links: Array<{ label: string; to: string }> = [];
  if (entity === "events" && item.payload.related_alert_id) {
    links.push({
      label: "查看关联 Alert",
      to: `/explore/alerts?bk_tenant_id=${tenant}&id=${encodeURIComponent(String(item.payload.related_alert_id))}`,
    });
  }
  if (entity === "alerts") {
    for (const field of ["trigger_event_id", "latest_event_id"]) {
      if (item.payload[field])
        links.push({
          label: `查看 ${field}`,
          to: `/explore/events?bk_tenant_id=${tenant}&id=${encodeURIComponent(String(item.payload[field]))}`,
        });
    }
    links.push({
      label: "查看 AlertLog 时间线",
      to: `/explore/alert-logs?bk_tenant_id=${tenant}&alert_id=${encodeURIComponent(item.id)}`,
    });
  }
  if (entity === "alert-logs" && item.payload.alert_id) {
    links.push({
      label: "查看父 Alert",
      to: `/explore/alerts?bk_tenant_id=${tenant}&id=${encodeURIComponent(String(item.payload.alert_id))}`,
    });
  }
  return links;
}

function toInputValue(value: string, type: string): string {
  if (type !== "datetime-local" || !value) return value;
  const date = new Date(value);
  if (Number.isNaN(date.getTime())) return value;
  const offset = date.getTimezoneOffset() * 60_000;
  return new Date(date.getTime() - offset).toISOString().slice(0, 16);
}

function facetHelp(name: string): string {
  const help: Record<string, string> = {
    event_source_id: "按产生实体的 EventSource 统计数量。",
    processing_state: "按 EventProcessing 当前处理状态统计数量。",
    status: "按 Alert 当前生命周期状态统计数量。",
    severity: "按严重程度统计数量。",
    operation_kind: "按 AlertLog 业务操作类型统计数量。",
    operator_kind: "按触发操作的主体类型统计数量。",
  };
  return help[name] ?? "按该存储字段的实际值统计数量。";
}

function summaryFieldHelp(name: string): string {
  const help: Record<string, string> = {
    state: "EventProcessing 当前处理状态。",
    status: "Alert 当前生命周期状态。",
    operation_kind: "AlertLog 记录的业务操作类型。",
    title: "实体的可读标题。",
    event_source_id: "产生该实体的 EventSource 稳定标识。",
    alert_id: "该流水所属的 Alert ID。",
  };
  return help[name] ?? "从实体 payload 提取的列表摘要字段。";
}
