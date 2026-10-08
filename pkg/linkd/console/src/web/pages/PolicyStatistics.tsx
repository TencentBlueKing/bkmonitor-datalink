import { useState } from "react";
import { useQuery } from "@tanstack/react-query";
import type { PolicyKind } from "../../shared/policies";
import { getPolicyStatistics } from "../api";
import { formatTime, useTimeMode } from "../time";

export function PolicyStatistics({
  tenant,
  kind,
  ids,
}: {
  tenant: string;
  kind: PolicyKind;
  ids: string[];
}) {
  const [hours, setHours] = useState<1 | 6 | 24>(24);
  const mode = useTimeMode();
  const stats = useQuery({
    queryKey: ["policy-statistics", tenant, kind, ids, hours],
    queryFn: ({ signal }) =>
      getPolicyStatistics(
        { bk_tenant_id: tenant, type: kind, ids, hours },
        signal,
      ),
    enabled: ids.length > 0,
    retry: false,
  });
  if (!ids.length) return null;
  return (
    <section className="policy-statistics" aria-label="逐策略统计">
      <h3>本页策略执行观察</h3>
      <label>
        统计范围
        <select
          aria-label="统计范围"
          value={hours}
          onChange={(e) => setHours(Number(e.target.value) as 1 | 6 | 24)}
        >
          <option value={1}>当前小时</option>
          <option value={6}>最近 6 个小时桶</option>
          <option value={24}>最近 24 个小时桶</option>
        </select>
      </label>
      <button onClick={() => void stats.refetch()} disabled={stats.isFetching}>
        刷新统计
      </button>
      <p className="policy-note">
        包含所有版本、重试、定时重查和依赖候选匹配，不是唯一事件数。执行跳过可能与匹配结果重叠。缓存采样可能丢失，不补历史。
      </p>
      {stats.isFetching && <p role="status">正在读取统计…</p>}
      {stats.error ? (
        <p role="alert">统计暂不可用，不能视为零。</p>
      ) : (
        stats.data && (
          <>
            <p>
              {formatTime(stats.data.from, mode)} —{" "}
              {formatTime(stats.data.to, mode)}
            </p>
            <div className="policy-statistics-scroll">
              <table>
                <thead>
                  <tr>
                    <th>策略</th>
                    <th>命中</th>
                    <th>未命中</th>
                    <th>求值失败</th>
                    <th>执行跳过</th>
                  </tr>
                </thead>
                <tbody>
                  {stats.data.items.map((row) => (
                    <tr key={row.id}>
                      <th>{row.id}</th>
                      <td>{row.matched}</td>
                      <td>{row.not_matched}</td>
                      <td>{row.unavailable}</td>
                      <td>{row.execution_skipped}</td>
                    </tr>
                  ))}
                </tbody>
              </table>
            </div>
          </>
        )
      )}
    </section>
  );
}
