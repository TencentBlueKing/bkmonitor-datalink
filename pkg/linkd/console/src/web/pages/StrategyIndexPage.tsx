import { useMutation, useQuery } from "@tanstack/react-query";
import { useEffect, useRef, useState } from "react";
import { Link } from "react-router-dom";
import {
  browseStrategyIndex,
  getStrategyTargets,
  reconcileStrategyIndex,
} from "../api";
import { formatTime, useTimeMode } from "../time";
import type { StrategyResult } from "../../shared/strategy-index";
import { StrategyAuditPanel } from "../components/StrategyAuditPanel";

const statuses = {
  matched: "一致",
  missing_redis: "Redis 缺失",
  redis_only: "Redis 独有",
  unknown: "无法确认",
};
type RowStatus = keyof typeof statuses;

export function StrategyIndexPage() {
  const timeMode = useTimeMode();
  const targets = useQuery({
    queryKey: ["strategy-index-targets"],
    queryFn: getStrategyTargets,
    retry: false,
    refetchOnWindowFocus: false,
  });
  const [selection, setSelection] = useState("");
  const [cursors, setCursors] = useState<Array<string | undefined>>([
    undefined,
  ]);
  const [scanCount, setScanCount] = useState(50);
  const detailRef = useRef<HTMLElement>(null);
  const [filter, setFilter] = useState<RowStatus | "all">("all");
  const [page, setPage] = useState(0);
  const result = useMutation({
    mutationFn: reconcileStrategyIndex,
    retry: false,
  });
  const chosen =
    targets.data?.find(
      (t) => `${t.eventSourceId}/${t.hookName}` === selection,
    ) ?? (!selection ? targets.data?.[0] : undefined);
  const cursor = cursors.at(-1);
  const catalog = useQuery({
    queryKey: [
      "strategy-index-browse",
      chosen?.eventSourceId,
      chosen?.hookName,
      cursor,
      scanCount,
    ],
    queryFn: ({ signal }) =>
      browseStrategyIndex(
        {
          event_source_id: chosen!.eventSourceId,
          hook_name: chosen!.hookName,
          cursor,
          count: scanCount,
        },
        signal,
      ),
    enabled: !!chosen,
    retry: false,
    refetchOnWindowFocus: false,
  });
  const data = result.data;
  useEffect(() => {
    if (data)
      detailRef.current?.scrollIntoView?.({
        behavior: "smooth",
        block: "start",
      });
  }, [data]);
  const rows =
    data?.rows.filter((r) => filter === "all" || r.status === filter) ?? [];
  const counts = (status: RowStatus) =>
    data?.rows.filter((r) => r.status === status).length ?? 0;
  const reset = () => {
    result.reset();
    setPage(0);
  };
  return (
    <section className="strategy-page">
      <div className="page-heading">
        <div>
          <p className="eyebrow">ACTIVE ALERT STRATEGY INDEX</p>
          <h1>策略活跃索引</h1>
          <p>
            查询 active-by-strategy 的 fingerprint 集合，与当前 Active Alert
            对账。状态由控制面统一维护，允许短暂传播延迟。
          </p>
        </div>
        <button
          type="button"
          onClick={() => {
            setSelection("");
            setCursors([undefined]);
            reset();
            void targets.refetch();
          }}
          disabled={targets.isFetching || result.isPending}
        >
          刷新缓存目标
        </button>
      </div>
      {targets.isError && (
        <p role="alert">Hook 配置读取失败：{targets.error.message}</p>
      )}
      {targets.isLoading && <p>正在读取 Hook 配置…</p>}
      {targets.isSuccess && targets.data.length === 0 && (
        <p>当前来源未配置 active-alert-by-strategy Hook。</p>
      )}
      <section className="strategy-query panel" aria-label="缓存目标">
        <fieldset disabled={result.isPending}>
          <label>
            缓存目标
            <select
              value={chosen ? `${chosen.eventSourceId}/${chosen.hookName}` : ""}
              onChange={(e) => {
                setSelection(e.target.value);
                setCursors([undefined]);
                reset();
              }}
            >
              {!chosen && <option value="">暂无缓存目标</option>}
              {targets.data?.map((t) => (
                <option
                  key={`${t.eventSourceId}/${t.hookName}`}
                  value={`${t.eventSourceId}/${t.hookName}`}
                >
                  {t.keyPrefix} · DB {t.database} · {t.address}
                </option>
              ))}
            </select>
          </label>
          <label>
            每批扫描量
            <select
              value={scanCount}
              onChange={(e) => {
                setScanCount(Number(e.target.value));
                setCursors([undefined]);
                reset();
              }}
            >
              {[10, 50, 100, 200].map((n) => (
                <option key={n} value={n}>
                  {n}
                </option>
              ))}
            </select>
          </label>
          <button
            type="button"
            disabled={!chosen || catalog.isFetching}
            onClick={() => {
              setCursors([undefined]);
              reset();
              if (cursor === undefined) void catalog.refetch();
            }}
          >
            重新扫描
          </button>
        </fieldset>
        {chosen && (
          <p className="strategy-scope">
            共享来源：{chosen.sources.join("、")}
            <br />
            通知 Channel：
            {chosen.notifyChannel ?? `${chosen.keyPrefix}:changes`}
          </p>
        )}
        <p>
          自动列出已缓存或待刷新的租户与策略。点击对应行查看成员和对账，无需填写
          ID。
        </p>
      </section>
      <StrategyAuditPanel
        target={chosen}
        onInspect={(query) => {
          setPage(0);
          setFilter("all");
          result.mutate(query);
        }}
      />
      {catalog.isFetching && <p role="status">正在读取扫描批次…</p>}
      {catalog.isError && (
        <p role="alert">组合列表读取失败：{catalog.error.message}</p>
      )}
      {catalog.data && (
        <>
          <section className="panel strategy-summary" aria-label="缓存维护任务">
            <h2>缓存维护任务</h2>
            <p>根据已发布的策略 Hook 自动启用 · Hook 触发刷新与周期校准</p>
            <div className="strategy-task-summary">
              <div>
                <span>周期发现状态</span>
                <strong>
                  {catalog.data.health.error
                    ? "最近发现失败"
                    : catalog.data.health.lastSuccess
                      ? "已有成功记录"
                      : "尚无执行记录"}
                </strong>
              </div>
              <div>
                <span>最近完整发现</span>
                <strong>
                  {catalog.data.health.lastSuccess
                    ? formatTime(catalog.data.health.lastSuccess, timeMode)
                    : "无"}
                </strong>
              </div>
              <div>
                <span>待刷新组合</span>
                <strong>{catalog.data.health.pendingCount}</strong>
              </div>
              <div>
                <span>最早计划刷新</span>
                <strong>
                  {catalog.data.health.oldestDueAt
                    ? formatTime(catalog.data.health.oldestDueAt, timeMode)
                    : "无"}
                </strong>
              </div>
            </div>
            <p>
              读取于 {formatTime(catalog.data.scannedAt, timeMode)}
              。成功记录表示曾完成校准，不代表进程此刻仍在运行。
            </p>
          </section>
          <section className="panel strategy-results" aria-label="租户策略组合">
            <header>
              <h2>租户与策略组合</h2>
              <span>
                第 {cursors.length} 批 · {catalog.data.rows.length} 个组合
              </span>
            </header>
            <div className="table-scroll">
              <table>
                <thead>
                  <tr>
                    <th>租户</th>
                    <th>策略 ID</th>
                    <th>缓存成员</th>
                    <th>刷新状态</th>
                    <th>最近成功校准</th>
                    <th>操作</th>
                  </tr>
                </thead>
                <tbody>
                  {catalog.data.rows.map((row) => (
                    <tr key={row.key}>
                      <td>{row.tenantId}</td>
                      <td className="strategy-fingerprint">{row.strategyId}</td>
                      <td>{row.members ?? "未知"}</td>
                      <td>
                        {row.error
                          ? "读取或刷新失败"
                          : row.pending
                            ? "待刷新"
                            : row.lastSuccess
                              ? "已校准"
                              : "尚未校准"}
                      </td>
                      <td>
                        {row.lastSuccess
                          ? formatTime(row.lastSuccess, timeMode)
                          : "无"}
                      </td>
                      <td>
                        <button
                          type="button"
                          disabled={result.isPending || catalog.isFetching}
                          aria-label={`对账租户 ${row.tenantId} 策略 ${row.strategyId}`}
                          onClick={() => {
                            setPage(0);
                            setFilter("all");
                            result.mutate({
                              event_source_id:
                                catalog.data.target.eventSourceId,
                              hook_name: catalog.data.target.hookName,
                              bk_tenant_id: row.tenantId,
                              strategy_id: row.strategyId,
                            });
                          }}
                        >
                          查看对账
                        </button>
                      </td>
                    </tr>
                  ))}
                </tbody>
              </table>
            </div>
            {catalog.data.rows.length === 0 && (
              <p>
                {catalog.data.nextCursor
                  ? "本批没有匹配组合，可继续扫描下一批。"
                  : "扫描已结束，本批没有匹配组合。"}
              </p>
            )}
            <div className="strategy-pagination">
              <button
                type="button"
                disabled={cursors.length <= 1 || catalog.isFetching}
                onClick={() => setCursors((current) => current.slice(0, -1))}
              >
                上一批
              </button>
              <button
                type="button"
                disabled={!catalog.data.nextCursor || catalog.isFetching}
                onClick={() => {
                  const next = catalog.data?.nextCursor;
                  if (next) setCursors((current) => [...current, next]);
                }}
              >
                下一批
              </button>
              <span>
                {catalog.data.nextCursor ? "仍有未扫描范围" : "已完成一轮扫描"}
              </span>
            </div>
            <ul>
              {catalog.data.warnings.map((w) => (
                <li key={w}>{w}</li>
              ))}
            </ul>
          </section>
        </>
      )}
      {result.isError && <p role="alert">查询失败：{result.error.message}</p>}
      {data && (
        <>
          <article
            ref={detailRef}
            className="panel strategy-summary"
            aria-label="对账摘要"
          >
            <h2>{data.complete ? "已读完当前查询范围" : "对账不完整"}</h2>
            <p className="strategy-scope">{data.key}</p>
            <p>
              租户 {data.tenantId} · 策略 {data.strategyId} ·{" "}
              {data.target.address} · DB {data.target.database}
            </p>
            <p>
              读取时间：{formatTime(data.startedAt, timeMode)} —{" "}
              {formatTime(data.finishedAt, timeMode)}
            </p>
            <p>
              Redis 集合大小：{data.redis.total ?? "未知"}，已读{" "}
              {data.redis.scanned} 个成员；已扫描 {data.alerts.scanned} 条
              Active Alert，其中 {data.alerts.matched} 条匹配本策略。
            </p>
            {data.redis.projection && (
              <p>
                控制面投影：
                {data.redis.projection.error
                  ? `刷新失败（${data.redis.projection.error}）`
                  : data.redis.projection.lastSuccess
                    ? "已校准"
                    : "尚未校准"}
                {data.redis.projection.pending ? " · 待刷新" : ""}
                <br />
                最近成功：
                {data.redis.projection.lastSuccess
                  ? formatTime(data.redis.projection.lastSuccess, timeMode)
                  : "无"}
                {data.redis.projection.lastSuccess &&
                  `（距查询结束 ${Math.max(0, Math.floor((Date.parse(data.finishedAt) - Date.parse(data.redis.projection.lastSuccess)) / 1000))} 秒）`}
                <br />
                目标周期发现：
                {data.redis.projection.discoveryError ??
                  (data.redis.projection.discoverySuccess
                    ? formatTime(
                        data.redis.projection.discoverySuccess,
                        timeMode,
                      )
                    : "尚未完成")}
              </p>
            )}
            <div className="strategy-counts">
              {Object.entries(statuses).map(([status, label]) => (
                <span key={status} data-status={status}>
                  {label} <strong>{counts(status as RowStatus)}</strong>
                </span>
              ))}
            </div>
            <ul>
              {data.warnings.map((w) => (
                <li key={w}>{w}</li>
              ))}
            </ul>
          </article>
          <article className="panel strategy-results" aria-label="索引成员对账">
            <header>
              <h2>索引成员对账</h2>
              <label>
                对账结果
                <select
                  value={filter}
                  onChange={(e) => {
                    setFilter(e.target.value as RowStatus | "all");
                    setPage(0);
                  }}
                >
                  <option value="all">全部</option>
                  {Object.entries(statuses).map(([s, l]) => (
                    <option key={s} value={s}>
                      {l}
                    </option>
                  ))}
                </select>
              </label>
            </header>
            <p>
              Redis
              独有表示当前关联来源中未找到对应活动告警；外部写入、旧配置残留和短暂延迟需进一步确认。
            </p>
            <div className="table-scroll">
              <table>
                <thead>
                  <tr>
                    <th>Fingerprint</th>
                    <th>对账结果</th>
                    <th>当前活动告警</th>
                  </tr>
                </thead>
                <tbody>
                  {rows.slice(page * 100, (page + 1) * 100).map((row) => (
                    <tr key={row.fingerprint}>
                      <td className="strategy-fingerprint">
                        {row.fingerprint}
                      </td>
                      <td>{statuses[row.status]}</td>
                      <td>
                        <AlertLinks row={row} tenant={data.tenantId} />
                      </td>
                    </tr>
                  ))}
                </tbody>
              </table>
            </div>
            {rows.length === 0 && (
              <p>
                {data.complete
                  ? "当前筛选下没有成员。"
                  : "当前未读取到成员，不能据此判断两侧为空或一致。"}
              </p>
            )}
            <div className="strategy-pagination">
              <button
                type="button"
                disabled={page === 0}
                onClick={() => setPage(page - 1)}
              >
                上一页
              </button>
              <span>
                {page + 1} / {Math.max(1, Math.ceil(rows.length / 100))} ·{" "}
                {rows.length} 个 fingerprint
              </span>
              <button
                type="button"
                disabled={(page + 1) * 100 >= rows.length}
                onClick={() => setPage(page + 1)}
              >
                下一页
              </button>
            </div>
          </article>
        </>
      )}
    </section>
  );
}

function AlertLinks({
  row,
  tenant,
}: {
  row: StrategyResult["rows"][number];
  tenant: string;
}) {
  if (!row.alerts.length) return <>—</>;
  return (
    <>
      {row.alerts.map((a) => (
        <div key={`${a.eventSourceId}/${a.alertId}`}>
          <Link
            to={`/explore/alerts?${new URLSearchParams({ bk_tenant_id: tenant, id: a.alertId })}`}
          >
            {a.eventSourceId} / {a.alertId}
          </Link>
        </div>
      ))}
    </>
  );
}
