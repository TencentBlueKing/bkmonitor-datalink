import { useQuery } from "@tanstack/react-query";
import { useMemo, useState } from "react";
import { Link, useSearchParams } from "react-router-dom";
import type {
  EntityItem,
  EntityKind,
  EntityStats,
} from "../../shared/contracts";
import { getCapabilities, getEntityStats, searchEntities } from "../api";
import { useReportPageQueryFailure } from "../navigation";
import { formatTime, useTimeMode } from "../time";
import { ExplorerDetail } from "./ExplorerDetail";
import { ExplorerQuery } from "./ExplorerQuery";
import { EntityTable, LogTimeline } from "./ExplorerRecords";
import {
  display,
  entityURL,
  explorerFilters,
  explorerMeta,
  rangeOptions,
  record,
  searchValues,
  valueNames,
} from "./explorer";
import "./explorer.css";

export function ExplorerPage({ entity }: { entity: EntityKind }) {
  const mode = useTimeMode();
  const [params, setParams] = useSearchParams();
  const [anchor, setAnchor] = useState(Date.now);
  const [generation, setGeneration] = useState(0);
  const [history, setHistory] = useState<
    Array<{ current: string; previous: string }>
  >([]);
  const [localSearch, setLocalSearch] = useState("");
  const [notice, setNotice] = useState("");
  const capabilities = useQuery({
    queryKey: ["capabilities"],
    queryFn: getCapabilities,
  });
  const limits = capabilities.data?.limits;
  const values = useMemo(
    () =>
      searchValues(entity, params, anchor, limits?.defaultRangeSeconds ?? 3600),
    [entity, params, anchor, limits?.defaultRangeSeconds],
  );
  const statsValues = useMemo(() => {
    const next = { ...values };
    delete next.cursor;
    delete next.limit;
    delete next.order;
    return next;
  }, [values]);
  const result = useQuery({
    queryKey: ["entities", entity, values, generation],
    queryFn: ({ signal }) => searchEntities(entity, values, signal),
    // 前页复用本轮缓存；ES 末页会关闭 PIT，不能重新使用该游标读取前页。
    staleTime: Infinity,
    refetchOnWindowFocus: false,
    enabled: capabilities.isSuccess,
  });
  const stats = useQuery({
    queryKey: ["entity-stats", entity, statsValues, generation],
    queryFn: ({ signal }) => getEntityStats(entity, statsValues, signal),
    staleTime: Infinity,
    enabled: capabilities.isSuccess,
  });
  useReportPageQueryFailure(result.isError || capabilities.isError);
  const meta = explorerMeta[entity];
  const supported = capabilities.data?.entities[entity]?.filters ?? [];
  const fields = explorerFilters[entity].filter((f) =>
    supported.includes(f.capability),
  );
  const items = result.data?.items ?? [];
  const visible = items.filter(
    (item) =>
      !localSearch.trim() ||
      [
        item.id,
        item.tenantId,
        item.payload.title,
        item.payload.content,
        item.payload.subject_name,
        item.payload.subject_id,
        item.payload.event_source_id,
        item.payload.alert_id,
        record(item.payload.params).reason_code,
      ].some((value) =>
        display(value)
          .toLocaleLowerCase()
          .includes(localSearch.trim().toLocaleLowerCase()),
      ),
  );
  const currentCursor = values.cursor ?? "";
  const previous = history.at(-1);
  const warnings = [
    ...new Set([
      ...(result.data?.warnings ?? []),
      ...(stats.data?.warnings ?? []),
    ]),
  ];
  function apply(next: URLSearchParams) {
    next.delete("cursor");
    next.delete("detail");
    next.delete("detail_tenant");
    setHistory([]);
    setLocalSearch("");
    setNotice("");
    setParams(next);
  }
  function change(key: string, value?: string) {
    const next = new URLSearchParams(values);
    if (value) next.set(key, value);
    else next.delete(key);
    apply(next);
  }
  function refresh() {
    const now = Date.now();
    setAnchor(now);
    setGeneration((g) => g + 1);
    const next = new URLSearchParams(values);
    const preset = rangeOptions.find(([key]) => key === params.get("range"));
    if (preset || (!params.has("from") && !params.has("to") && !values.id)) {
      next.set("to", new Date(now).toISOString());
      next.set(
        "from",
        new Date(
          now - (preset?.[2] ?? limits?.defaultRangeSeconds ?? 3600) * 1000,
        ).toISOString(),
      );
      if (preset) next.set("range", preset[0]);
    }
    apply(next);
    if (capabilities.isError) void capabilities.refetch();
  }
  function inspect(item: EntityItem) {
    const next = new URLSearchParams(params);
    next.set("detail", item.id);
    next.set("detail_tenant", item.tenantId);
    setParams(next);
  }
  function closeDetail() {
    const next = new URLSearchParams(params);
    next.delete("detail");
    next.delete("detail_tenant");
    setParams(next, { replace: true });
  }
  function pageParams() {
    const next = new URLSearchParams(values);
    for (const key of ["range", "view"])
      if (params.has(key)) next.set(key, params.get(key)!);
    return next;
  }
  return (
    <section className="entity-explorer">
      <div className="page-heading explorer-heading">
        <div>
          <p className="eyebrow">CORE DATA / {meta.singular.toUpperCase()}</p>
          <h1>{meta.title}</h1>
          <p>{meta.description}</p>
        </div>
        <div className="explorer-heading-actions">
          <span className="source-label">
            {capabilities.data?.entities[entity]?.source ?? "连接中"} ·{" "}
            {entity === "alerts" ? "告警管理" : "只读"}
          </span>
          <button type="button" onClick={refresh} disabled={result.isFetching}>
            刷新查询
          </button>
        </div>
      </div>
      <nav className="explorer-tabs" aria-label="核心数据导航">
        {Object.entries(explorerMeta).map(([key, entry], index) => (
          <Link
            key={key}
            aria-current={key === entity ? "page" : undefined}
            to={entityURL(
              key as EntityKind,
              values.bk_tenant_id ?? "",
              Object.fromEntries(
                Object.entries(values).filter(([k]) =>
                  ["from", "to"].includes(k),
                ),
              ),
            )}
          >
            <span>0{index + 1}</span>
            {entry.name}
            <small>{entry.singular}</small>
          </Link>
        ))}
      </nav>
      {entity === "alerts" && (
        <p className="explorer-context">
          当前查询按<strong>最近更新时间</strong>
          过滤与排序；更新不代表新建。每条告警同时展示首次发生、创建和最近更新时间。
        </p>
      )}
      <ExplorerQuery
        key={`${entity}:${params.toString()}:${limits?.defaultRangeSeconds}`}
        entity={entity}
        values={values}
        fields={fields}
        range={
          params.get("range") ??
          (values.id && !values.from
            ? "exact"
            : params.has("from")
              ? "custom"
              : "1h")
        }
        maxRange={limits?.maxRangeSeconds ?? 604800}
        maxLimit={limits?.maxLimit ?? 200}
        defaultLimit={limits?.defaultLimit ?? 50}
        onApply={apply}
        onReset={() => {
          setAnchor(Date.now());
          apply(new URLSearchParams());
        }}
      />
      <div className="explorer-applied" aria-label="已应用筛选">
        <span>已应用</span>
        {[
          { key: "bk_tenant_id", label: "租户" },
          { key: "id", label: `${meta.singular} ID` },
          ...fields,
        ]
          .filter((field) => values[field.key])
          .map((field) => (
            <button
              key={field.key}
              type="button"
              title="移除此条件"
              onClick={() => change(field.key)}
            >
              {field.label}:{" "}
              {valueNames[values[field.key]] ?? values[field.key]}{" "}
              <span>×</span>
            </button>
          ))}
        {!values.bk_tenant_id && <span className="muted">全部租户</span>}
        <span className="muted">
          {meta.time} ·{" "}
          {values.from
            ? `${formatTime(values.from, mode)} — ${formatTime(values.to, mode)}`
            : "精确 ID · 不限时间"}
        </span>
      </div>
      {capabilities.isError && (
        <div className="error-banner" role="alert">
          查询能力加载失败：{capabilities.error.message}
          <button onClick={() => void capabilities.refetch()}>重试</button>
        </div>
      )}
      <StatsPanel
        entity={entity}
        data={stats.data}
        loading={stats.isFetching}
        error={stats.error?.message}
        retry={() => void stats.refetch()}
        onFilter={change}
        supported={supported}
      />
      {warnings.length > 0 && (
        <details className="explorer-notes">
          <summary>查询说明 · {warnings.length}</summary>
          {warnings.map((w) => (
            <p key={w}>{w}</p>
          ))}
        </details>
      )}
      {result.isError && (
        <div className="error-banner" role="alert">
          查询失败：{result.error.message}
          <button onClick={refresh}>重新查询</button>
        </div>
      )}
      <div className="table-panel explorer-results">
        <div className="explorer-results-heading">
          <div>
            <h2>
              {entity === "alert-logs" && params.get("view") !== "table"
                ? "操作时间线"
                : `${meta.name}列表`}
            </h2>
            <span>
              {result.isFetching
                ? "正在查询…"
                : `本页 ${items.length} 条${localSearch ? ` · 显示 ${visible.length} 条` : ""}`}{" "}
              · {values.order === "asc" ? "最早在前" : "最新在前"}
            </span>
          </div>
          <div className="explorer-result-tools">
            <input
              aria-label="本页搜索"
              placeholder="本页搜索标题 / 对象 / ID"
              value={localSearch}
              onChange={(event) => setLocalSearch(event.target.value)}
            />
            {entity === "alert-logs" && (
              <div className="explorer-view-switch" aria-label="展示方式">
                {[
                  ["timeline", "时间线"],
                  ["table", "表格"],
                ].map(([key, label]) => (
                  <button
                    key={key}
                    aria-pressed={(params.get("view") ?? "timeline") === key}
                    onClick={() => {
                      const next = new URLSearchParams(params);
                      next.set("view", key);
                      setParams(next, { replace: true });
                    }}
                  >
                    {label}
                  </button>
                ))}
              </div>
            )}
            <button
              onClick={async () => {
                try {
                  const url = new URL(window.location.href);
                  url.search = pageParams().toString();
                  url.searchParams.delete("cursor");
                  await navigator.clipboard.writeText(url.toString());
                  setNotice("查询链接已复制");
                } catch {
                  setNotice("复制失败，请从地址栏复制链接");
                }
              }}
            >
              复制查询链接
            </button>
          </div>
        </div>
        {notice && (
          <p className="explorer-notice" role="status">
            {notice}
          </p>
        )}
        <p className="explorer-result-note">
          本页搜索仅过滤已加载记录；上方条件查询整个存储范围。标题、正文尚未建立全文索引。
        </p>
        {result.isPending ? (
          <div className="explorer-empty" role="status">
            正在读取{meta.name}…
          </div>
        ) : result.isError ? (
          <div className="explorer-empty">未能读取结果，请重试或调整条件。</div>
        ) : visible.length === 0 ? (
          <div className="explorer-empty">
            <strong>
              {localSearch ? "本页没有匹配项" : "当前范围内没有记录"}
            </strong>
            <p>
              {localSearch
                ? "清除本页搜索以查看本页全部记录。"
                : "可扩大时间范围、移除筛选，或使用精确 ID 查询。"}
            </p>
            {localSearch && (
              <button onClick={() => setLocalSearch("")}>清除本页搜索</button>
            )}
          </div>
        ) : entity === "alert-logs" && params.get("view") !== "table" ? (
          <LogTimeline items={visible} onInspect={inspect} />
        ) : (
          <EntityTable entity={entity} items={visible} onInspect={inspect} />
        )}
        <div className="pagination">
          <span>
            {meta.time}排序 · 每页 {values.limit ?? limits?.defaultLimit ?? 50}{" "}
            条
          </span>
          <div>
            <button
              disabled={!currentCursor || result.isFetching}
              onClick={() => {
                const next = pageParams();
                if (previous?.current === currentCursor && previous.previous)
                  next.set("cursor", previous.previous);
                else next.delete("cursor");
                setHistory((h) => h.slice(0, -1));
                setLocalSearch("");
                setParams(next);
              }}
            >
              {previous?.current === currentCursor ? "← 上一页" : "返回首页"}
            </button>
            <button
              disabled={
                !result.data?.nextCursor || result.isFetching || result.isError
              }
              onClick={() => {
                const cursor = result.data!.nextCursor!;
                setHistory((h) => [
                  ...h,
                  { current: cursor, previous: currentCursor },
                ]);
                const next = pageParams();
                next.set("cursor", cursor);
                setLocalSearch("");
                setParams(next);
              }}
            >
              下一页 →
            </button>
          </div>
        </div>
      </div>
      {params.get("detail") && params.get("detail_tenant") && (
        <ExplorerDetail
          key={`${entity}:${params.get("detail_tenant")}:${params.get("detail")}`}
          entity={entity}
          id={params.get("detail")!}
          tenant={params.get("detail_tenant")!}
          maxRange={limits?.maxRangeSeconds ?? 604800}
          onClose={closeDetail}
        />
      )}
    </section>
  );
}

function StatsPanel({
  entity,
  data,
  loading,
  error,
  retry,
  onFilter,
  supported,
}: {
  entity: EntityKind;
  data?: EntityStats;
  loading: boolean;
  error?: string;
  retry: () => void;
  onFilter: (key: string, value: string) => void;
  supported: string[];
}) {
  const mode = useTimeMode();
  const max = Math.max(1, ...(data?.timeline.map((p) => p.count) ?? []));
  return (
    <section className="explorer-statistics" aria-label="查询统计">
      <div className="explorer-count">
        <span>匹配记录</span>
        <strong>
          {error ? "—" : data ? data.total.toLocaleString() : "…"}
        </strong>
        <small>{loading ? "统计中…" : "当前查询条件 · 独立快照"}</small>
      </div>
      <div className="explorer-distributions">
        {error ? (
          <div className="explorer-stat-error" role="status">
            统计不可用：{error}
            <button onClick={retry}>重试统计</button>
          </div>
        ) : (
          data?.facets.map((facet) => {
            const key =
              facet.name === "processing_state" ? "state" : facet.name;
            const field = explorerFilters[entity].find((f) => f.key === key);
            return (
              <div className="explorer-facet" key={facet.name}>
                <span>
                  {field?.label ?? facet.name} <small>TOP 6</small>
                </span>
                <div>
                  {facet.values.slice(0, 6).map((v) => (
                    <button
                      disabled={
                        !v.value ||
                        !field ||
                        !supported.includes(field.capability)
                      }
                      onClick={() => onFilter(key, v.value)}
                      key={v.value}
                      title={`筛选 ${v.value}`}
                    >
                      <span>{valueNames[v.value] ?? (v.value || "空值")}</span>
                      <b>{v.count.toLocaleString()}</b>
                    </button>
                  ))}
                </div>
              </div>
            );
          })
        )}
      </div>
      {data && !error && (
        <div className="explorer-spark">
          <span>{explorerMeta[entity].time}分布</span>
          {data.timeline.length ? (
            <>
              <div className="explorer-spark-bars">
                {data.timeline.map((point) => (
                  <span
                    key={point.timestamp}
                    style={{
                      height: `${point.count === 0 ? 0 : Math.max(2, (point.count / max) * 100)}%`,
                    }}
                    title={`${formatTime(point.timestamp, mode)} · ${point.count} 条`}
                  />
                ))}
              </div>
              <small>
                {formatTime(data.timeline[0].timestamp, mode)} →{" "}
                {formatTime(data.timeline.at(-1)!.timestamp, mode)}
              </small>
            </>
          ) : (
            <p className="muted">当前存储未提供时间趋势</p>
          )}
        </div>
      )}
    </section>
  );
}
