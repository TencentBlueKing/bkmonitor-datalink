import { useQuery } from "@tanstack/react-query";
import { Fragment, useState } from "react";
import { useSearchParams } from "react-router-dom";

import {
  matchesMetric,
  metricTypeLabels,
  type MetricCatalog,
  type MetricDefinition,
} from "../../shared/metric-catalog";
import { getMetricCatalog } from "../api";

const pageSize = 20;

export function MetricCatalogPage() {
  const catalog = useQuery({
    queryKey: ["metric-catalog"],
    queryFn: getMetricCatalog,
    staleTime: 60_000,
  });
  const [params, setParams] = useSearchParams();
  const [expanded, setExpanded] = useState<string | null>(null);
  const query = params.get("q") ?? "";
  const module = params.get("module") ?? "";
  const kind = params.get("type") ?? "";
  const purpose = params.get("purpose") ?? "";
  const data = catalog.data;
  const moduleOrder = new Map(data?.modules.map((c, i) => [c.id, i]));
  const metrics = (data?.metrics ?? [])
    .filter(
      (m) =>
        (!module || m.module === module) &&
        (!kind || m.type === kind) &&
        (!purpose || m.purpose === purpose) &&
        matchesMetric(m, query),
    )
    .sort(
      (a, b) =>
        (moduleOrder.get(a.module) ?? 0) - (moduleOrder.get(b.module) ?? 0) ||
        a.name.localeCompare(b.name),
    );
  const pages = Math.max(1, Math.ceil(metrics.length / pageSize));
  const requestedPage = Number(params.get("page") ?? 1);
  const page = Number.isSafeInteger(requestedPage)
    ? Math.min(pages, Math.max(1, requestedPage))
    : 1;
  const update = (key: string, value: string) => {
    setParams(
      (previous) => {
        const next = new URLSearchParams(previous);
        if (value) next.set(key, value);
        else next.delete(key);
        if (key !== "page") next.delete("page");
        return next;
      },
      { replace: true },
    );
  };
  return (
    <section className="metric-catalog">
      <div className="page-heading">
        <div>
          <p className="eyebrow">METRIC CATALOG</p>
          <h1>指标目录</h1>
          <p>查找 Linkd 可提供的指标，了解统计口径、维度与查询名称。</p>
        </div>
        <button
          className="catalog-refresh"
          onClick={() => void catalog.refetch()}
          disabled={catalog.isFetching}
        >
          {catalog.isFetching ? "正在读取…" : "刷新目录"}
        </button>
      </div>
      {catalog.isError && (
        <div className="error-banner" role="alert">
          指标目录读取失败：{catalog.error.message}
        </div>
      )}
      {catalog.isPending && (
        <div className="page-loading" role="status">
          正在读取控制面指标目录…
        </div>
      )}
      {data && (
        <>
          <div className="catalog-summary">
            <div>
              <strong>{data.metrics.length}</strong>
              <span>可用指标定义</span>
            </div>
            <div>
              <strong>
                {
                  data.modules.filter((c) =>
                    data.metrics.some((m) => m.module === c.id),
                  ).length
                }
              </strong>
              <span>功能模块</span>
            </div>
            <div>
              <strong>
                {data.metrics.filter((m) => m.origin === "linkd").length}
              </strong>
              <span>自动登记的业务指标</span>
            </div>
            <p>
              与指标声明同步注册
              <br />
              <span>无需等待采集样本</span>
            </p>
          </div>
          <div className="catalog-layout">
            <aside className="catalog-modules" aria-label="指标功能模块">
              <h2>功能模块</h2>
              <button
                aria-pressed={!module}
                onClick={() => update("module", "")}
              >
                <span>全部模块</span>
                <small>{data.metrics.length}</small>
              </button>
              {data.modules.map((c) => (
                <button
                  key={c.id}
                  aria-label={c.name}
                  aria-pressed={module === c.id}
                  onClick={() => update("module", c.id)}
                >
                  <span>{c.name}</span>
                  <small>
                    {data.metrics.filter((m) => m.module === c.id).length}
                  </small>
                </button>
              ))}
            </aside>
            <div className="catalog-main">
              <div className="catalog-filters">
                <label className="catalog-search">
                  搜索指标
                  <input
                    type="search"
                    aria-label="搜索指标"
                    value={query}
                    onChange={(e) => update("q", e.target.value)}
                  />
                </label>
                <label>
                  指标类型
                  <select
                    aria-label="指标类型"
                    value={kind}
                    onChange={(e) => update("type", e.target.value)}
                  >
                    <option value="">全部类型</option>
                    {Object.entries(metricTypeLabels).map(([key, label]) => (
                      <option key={key} value={key}>
                        {label}
                      </option>
                    ))}
                  </select>
                </label>
                <label>
                  指标用途
                  <select
                    aria-label="指标用途"
                    value={purpose}
                    onChange={(e) => update("purpose", e.target.value)}
                  >
                    <option value="">全部用途</option>
                    {data.purposes.map((p) => (
                      <option key={p.id} value={p.id}>
                        {p.name}
                      </option>
                    ))}
                  </select>
                </label>
              </div>
              <div className="catalog-results-heading">
                <p aria-live="polite">
                  {data.modules.find((m) => m.id === module)?.name ??
                    "全部模块"}{" "}
                  <strong>{metrics.length}</strong> 项指标
                </p>
                <button onClick={() => setParams({})}>清除筛选</button>
              </div>
              <div className="catalog-table-wrap">
                <table className="catalog-table">
                  <thead>
                    <tr>
                      <th>指标 / 中文名</th>
                      <th>类型 / 单位</th>
                      <th>维度</th>
                      <th>用途</th>
                    </tr>
                  </thead>
                  <tbody>
                    {metrics
                      .slice((page - 1) * pageSize, page * pageSize)
                      .map((m) => (
                        <Fragment key={m.name}>
                          <tr
                            className={
                              expanded === m.name
                                ? "catalog-row-expanded"
                                : undefined
                            }
                          >
                            <td>
                              <button
                                className="catalog-metric-name"
                                aria-expanded={expanded === m.name}
                                aria-controls={`metric-${m.name}`}
                                aria-label={`查看${m.display_name}详情`}
                                onClick={() =>
                                  setExpanded(
                                    expanded === m.name ? null : m.name,
                                  )
                                }
                              >
                                <strong>{m.display_name}</strong>
                                <code>{m.prometheus_name}</code>
                              </button>
                              <p className="catalog-description">
                                {m.description}
                              </p>
                            </td>
                            <td>
                              <span
                                className={`catalog-type ${m.prometheus_type}`}
                              >
                                {m.type === "up_down_counter"
                                  ? "UpDownCounter"
                                  : m.type[0].toUpperCase() + m.type.slice(1)}
                              </span>
                              <span className="catalog-unit">
                                {m.unit_label}
                                {m.unit && ` · ${m.unit}`}
                              </span>
                            </td>
                            <td>
                              <div className="catalog-dimension-tags">
                                {m.dimensions.slice(0, 3).map((d) => (
                                  <code key={d.name} title={d.description}>
                                    {d.prometheus_name}
                                  </code>
                                ))}
                                {m.dimensions.length > 3 && (
                                  <small>另 {m.dimensions.length - 3} 个</small>
                                )}
                                {!m.dimensions.length && (
                                  <span className="catalog-muted">
                                    无业务维度
                                  </span>
                                )}
                              </div>
                            </td>
                            <td className="catalog-purpose">
                              {data.purposes.find((p) => p.id === m.purpose)
                                ?.name ?? m.purpose}
                            </td>
                          </tr>
                          {expanded === m.name && (
                            <tr id={`metric-${m.name}`}>
                              <td colSpan={4} className="catalog-detail-cell">
                                <MetricDetail metric={m} catalog={data} />
                              </td>
                            </tr>
                          )}
                        </Fragment>
                      ))}
                  </tbody>
                </table>
              </div>
              {!metrics.length && (
                <div className="catalog-empty">
                  没有匹配的指标，请调整关键词或筛选条件。
                </div>
              )}
              <div className="catalog-pagination">
                <span>
                  第 {page} / {pages} 页 · 每页 {pageSize} 项
                </span>
                <button
                  disabled={page === 1}
                  onClick={() => update("page", String(page - 1))}
                >
                  上一页
                </button>
                <button
                  disabled={page === pages}
                  onClick={() => update("page", String(page + 1))}
                >
                  下一页
                </button>
              </div>
            </div>
          </div>
          <details className="catalog-notes">
            <summary>目录说明与公共维度</summary>
            <ul>
              {data.notes.map((note) => (
                <li key={note}>{note}</li>
              ))}
            </ul>
            <DimensionList dimensions={data.common_dimensions} />
          </details>
        </>
      )}
    </section>
  );
}

function MetricDetail({
  metric,
  catalog,
}: {
  metric: MetricDefinition;
  catalog: MetricCatalog;
}) {
  const [copyState, setCopyState] = useState("");
  const copy = async (name: string) => {
    try {
      await navigator.clipboard.writeText(name);
      setCopyState(`已复制 ${name}`);
    } catch {
      setCopyState("复制失败，请手动选中指标名复制。");
    }
  };
  return (
    <section
      className="catalog-detail"
      aria-label={`${metric.display_name}详情`}
    >
      <p>{metric.description}</p>
      <dl>
        <div>
          <dt>{metric.origin === "linkd" ? "OTel 注册名" : "原生注册名"}</dt>
          <dd>
            <code>{metric.name}</code>
          </dd>
        </div>
        <div>
          <dt>Prometheus 类型</dt>
          <dd>
            {metric.prometheus_type}
            {metric.type === "up_down_counter" && "（增减计数导出为 Gauge）"}
          </dd>
        </div>
      </dl>
      <h3>查询序列</h3>
      <div className="catalog-series">
        {metric.series.map((s) => (
          <div key={s}>
            <code>{s}</code>
            <button onClick={() => void copy(s)} aria-label={`复制 ${s}`}>
              复制
            </button>
          </div>
        ))}
      </div>
      <span role="status" className="catalog-copy-status">
        {copyState}
      </span>
      {metric.prometheus_type === "histogram" && (
        <p className="catalog-muted">
          _bucket 带 le 上界标签；_sum 为累计观测值，_count 为观测次数。
        </p>
      )}
      {metric.prometheus_type === "summary" && (
        <p className="catalog-muted">
          分位数序列带 quantile 标签；_sum 与 _count 分别为累计观测值与次数。
        </p>
      )}
      <h3>维度定义</h3>
      <DimensionList dimensions={metric.dimensions} />
      {metric.origin === "linkd" && (
        <details>
          <summary>
            公共 OTel 维度 · {catalog.common_dimensions.length} 个
          </summary>
          <DimensionList dimensions={catalog.common_dimensions} />
        </details>
      )}
    </section>
  );
}

function DimensionList({
  dimensions,
}: {
  dimensions: MetricDefinition["dimensions"];
}) {
  return dimensions.length ? (
    <dl className="catalog-dimension-list">
      {dimensions.map((d) => (
        <div key={d.name}>
          <dt>
            <code>{d.prometheus_name}</code>
            {d.name !== d.prometheus_name && <small>{d.name}</small>}
          </dt>
          <dd>{d.description}</dd>
        </div>
      ))}
    </dl>
  ) : (
    <p className="catalog-muted">该指标没有业务维度。</p>
  );
}
