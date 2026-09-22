import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useState } from "react";
import {
  cancelStrategyAudit,
  latestStrategyAudit,
  startStrategyAudit,
} from "../api";
import type {
  StrategyQuery,
  StrategyTarget,
} from "../../shared/strategy-index";
import { formatTime, useTimeMode } from "../time";

const phases = {
  alerts: "扫描 Active Alert",
  redis: "扫描 Redis",
  remaining: "核验数据库独有策略",
  done: "已结束",
};
export function StrategyAuditPanel({
  target,
  onInspect,
}: {
  target: StrategyTarget | undefined;
  onInspect: (query: StrategyQuery) => void;
}) {
  const timeMode = useTimeMode(),
    client = useQueryClient();
  const [page, setPage] = useState(0);
  const job = useQuery({
    queryKey: ["strategy-audit-current"],
    queryFn: latestStrategyAudit,
    retry: false,
    refetchOnWindowFocus: false,
    refetchInterval: (q) => (q.state.data?.status === "running" ? 2000 : false),
  });
  const start = useMutation({
    mutationFn: startStrategyAudit,
    onSuccess: (data) => {
      setPage(0);
      client.setQueryData(["strategy-audit-current"], data);
    },
  });
  const cancel = useMutation({
    mutationFn: cancelStrategyAudit,
    onSuccess: (data) => client.setQueryData(["strategy-audit-current"], data),
  });
  const data = job.data,
    running = data?.status === "running";
  const title = !data
    ? "尚未执行"
    : running
      ? "整体对账进行中"
      : data.status === "canceled"
        ? "对账已取消"
        : data.status === "incomplete"
          ? "整体对账不完整"
          : data.missing || data.extra
            ? "发现不一致"
            : "本轮读取一致";
  return (
    <section className="panel strategy-results" aria-label="整体活跃告警对账">
      <header>
        <h2>整体活跃告警对账</h2>
        <button
          type="button"
          disabled={!target || running || start.isPending || job.isPending}
          onClick={() =>
            target &&
            start.mutate({
              event_source_id: target.eventSourceId,
              hook_name: target.hookName,
            })
          }
        >
          开始整体对账
        </button>
      </header>
      <p>
        覆盖当前缓存目标下所有租户、策略和共享来源，双向检查 Redis 与 Active
        Alert。只读检查，不执行修复。
      </p>
      {(start.isError || job.isError || cancel.isError) && (
        <p role="alert">
          对账任务请求失败：
          {(start.error ?? job.error ?? cancel.error)?.message}
        </p>
      )}
      <h3>{title}</h3>
      {data && (
        <>
          <p className="strategy-scope">
            任务范围：{data.target.keyPrefix} · {data.target.address} · DB{" "}
            {data.target.database} · 来源 {data.target.sources.join("、")}
          </p>
          <p>
            {phases[data.phase]} · 开始于 {formatTime(data.startedAt, timeMode)}
            {data.finishedAt &&
              ` · 结束于 ${formatTime(data.finishedAt, timeMode)}`}
          </p>
          <div className="strategy-task-summary">
            <div>
              <span>已扫描 Active Alert</span>
              <strong>{data.scannedAlerts}</strong>
            </div>
            <div>
              <span>已检查 / 已发现组合</span>
              <strong>
                {data.checkedStrategies} / {data.discoveredStrategies}
              </strong>
            </div>
            <div>
              <span>匹配成员</span>
              <strong>{data.matched}</strong>
            </div>
            <div>
              <span>Redis 缺失</span>
              <strong>{data.missing}</strong>
            </div>
            <div>
              <span>Redis 多余</span>
              <strong>{data.extra}</strong>
            </div>
            <div>
              <span>无法确认的组合</span>
              <strong>{data.incompleteStrategies}</strong>
            </div>
          </div>
          {running && (
            <button
              type="button"
              disabled={cancel.isPending}
              onClick={() => cancel.mutate(data.id)}
            >
              取消对账
            </button>
          )}
          {!!data.skippedAlerts && (
            <p>{data.skippedAlerts} 条告警没有策略标签，不属于策略缓存范围。</p>
          )}
          {!!data.invalidAlerts && (
            <p>
              有 {data.invalidAlerts}{" "}
              条告警身份或策略标签异常，不能判定全量一致。
            </p>
          )}
          <ul>
            {data.warnings.map((w) => (
              <li key={w}>{w}</li>
            ))}
          </ul>
          {!!data.differences.length && (
            <>
              <div className="table-scroll">
                <table>
                  <thead>
                    <tr>
                      <th>租户</th>
                      <th>策略</th>
                      <th>Fingerprint</th>
                      <th>差异</th>
                      <th>操作</th>
                    </tr>
                  </thead>
                  <tbody>
                    {data.differences
                      .slice(page * 50, (page + 1) * 50)
                      .map((row, index) => (
                        <tr key={`${page}:${index}`}>
                          <td>{row.tenantId}</td>
                          <td>{row.strategyId}</td>
                          <td className="strategy-fingerprint">
                            {row.fingerprint ?? "—"}
                          </td>
                          <td>
                            {row.status === "missing_redis"
                              ? "Redis 缺失"
                              : row.status === "redis_only"
                                ? "Redis 多余"
                                : "无法确认"}
                          </td>
                          <td>
                            <button
                              type="button"
                              onClick={() =>
                                onInspect({
                                  event_source_id: data.target.eventSourceId,
                                  hook_name: data.target.hookName,
                                  bk_tenant_id: row.tenantId,
                                  strategy_id: row.strategyId,
                                })
                              }
                            >
                              查看该策略
                            </button>
                          </td>
                        </tr>
                      ))}
                  </tbody>
                </table>
              </div>
              <div className="strategy-pagination">
                <button
                  type="button"
                  disabled={page === 0}
                  onClick={() => setPage((p) => p - 1)}
                >
                  上一页差异
                </button>
                <span>第 {page + 1} 页</span>
                <button
                  type="button"
                  disabled={(page + 1) * 50 >= data.differences.length}
                  onClick={() => setPage((p) => p + 1)}
                >
                  下一页差异
                </button>
              </div>
            </>
          )}
          {data.samplesTruncated && (
            <p>这里只展示前 500 条差异样本，汇总数量仍覆盖已完成的检查范围。</p>
          )}
        </>
      )}
    </section>
  );
}
