import { useMutation, useQuery } from "@tanstack/react-query";
import { useState } from "react";
import { Link } from "react-router-dom";
import { getStrategyTargets, reconcileStrategyIndex } from "../api";
import { formatTime, useTimeMode } from "../time";
import type { StrategyResult } from "../../shared/strategy-index";

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
  const [tenant, setTenant] = useState("");
  const [strategy, setStrategy] = useState("");
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
  const data = result.data;
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
          onClick={() => void targets.refetch()}
          disabled={targets.isFetching || result.isPending}
        >
          刷新 Hook 列表
        </button>
      </div>
      {targets.isError && (
        <p role="alert">Hook 配置读取失败：{targets.error.message}</p>
      )}
      {targets.isLoading && <p>正在读取 Hook 配置…</p>}
      {targets.isSuccess && targets.data.length === 0 && (
        <p>当前来源未配置 active-alert-by-strategy Hook。</p>
      )}
      <form
        className="strategy-query panel"
        onSubmit={(e) => {
          e.preventDefault();
          if (!chosen) return;
          setPage(0);
          result.mutate({
            event_source_id: chosen.eventSourceId,
            hook_name: chosen.hookName,
            bk_tenant_id: tenant,
            strategy_id: strategy,
          });
        }}
      >
        <fieldset disabled={result.isPending}>
          <label>
            EventSource / Hook
            <select
              value={chosen ? `${chosen.eventSourceId}/${chosen.hookName}` : ""}
              onChange={(e) => {
                setSelection(e.target.value);
                reset();
              }}
              required
            >
              {!chosen && <option value="">请选择 Hook</option>}
              {targets.data?.map((t) => (
                <option
                  key={`${t.eventSourceId}/${t.hookName}`}
                  value={`${t.eventSourceId}/${t.hookName}`}
                >
                  {t.eventSourceId} / {t.hookName}
                </option>
              ))}
            </select>
          </label>
          <label>
            租户 ID
            <input
              value={tenant}
              onChange={(e) => {
                setTenant(e.target.value);
                reset();
              }}
              maxLength={256}
              required
            />
          </label>
          <label>
            策略 ID
            <input
              value={strategy}
              onChange={(e) => {
                setStrategy(e.target.value);
                reset();
              }}
              maxLength={1024}
              required
            />
          </label>
          <button type="submit" disabled={!chosen || !tenant || !strategy}>
            {result.isPending ? "正在查询与对账…" : "查询并对账"}
          </button>
        </fieldset>
        {chosen && (
          <p className="strategy-scope">
            {chosen.address} · DB {chosen.database} · 前缀 {chosen.keyPrefix}
            <br />
            共享来源：{chosen.sources.join("、")}
            {chosen.notifyChannel && (
              <>
                <br />
                通知 Channel：{chosen.notifyChannel}
              </>
            )}
          </p>
        )}
        <p>
          策略 ID
          按原样匹配，不去除空白。共享同一目标的来源合并对账；只读查询，不修改
          Redis 或告警。
        </p>
      </form>
      {result.isError && <p role="alert">查询失败：{result.error.message}</p>}
      {data && (
        <>
          <article className="panel strategy-summary" aria-label="对账摘要">
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
