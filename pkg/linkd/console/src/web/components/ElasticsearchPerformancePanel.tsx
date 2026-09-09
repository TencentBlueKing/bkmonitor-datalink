import type { ElasticsearchPerformance } from "../../shared/contracts";
import { formatMetricNumber } from "../metricFormat";
import { formatTime, useTimeMode } from "../time";
import { HelpTableHeader } from "./HelpTip";

export function ElasticsearchPerformancePanel({
  data,
  error,
}: {
  data?: ElasticsearchPerformance;
  error?: string;
}) {
  const timeMode = useTimeMode();
  const n = (value: number | null, suffix = "") =>
    value === null ? "未知" : formatMetricNumber(value) + suffix;
  return (
    <section className="diagnostic-section" aria-label="ES 节点性能">
      <header>
        <h2>ES 节点性能</h2>
        <p>
          服务端快照与客户端 Bulk 延迟分开观察。write queue 为 0
          不能排除执行变慢；瞬时快照也可能漏掉短峰值。
        </p>
        <p>
          {data
            ? `采样于 ${formatTime(data.sampledAt, timeMode)}`
            : "尚无节点统计"}
        </p>
      </header>
      {(data?.message || error) && (
        <p className="diagnostic-note">{data?.message ?? error}</p>
      )}
      {Boolean(data?.nodes.length) && (
        <div className="table-scroll">
          <table>
            <thead>
              <tr>
                <th>节点</th>
                <HelpTableHeader
                  label="CPU"
                  help="ES 节点统计返回的进程 CPU 百分比，不等同于容器 CPU 配额使用率。"
                />
                <th>Heap 使用</th>
                <th>执行中写请求</th>
                <th>Write queue</th>
                <HelpTableHeader
                  label="累计写拒绝"
                  help="节点启动以来累计 rejected，不是当前失败速率；节点重启会重置。"
                />
                <th>当前 Merge</th>
                <HelpTableHeader
                  label="未提交 Translog"
                  help="已执行写入的恢复日志字节数，不是待执行请求队列。"
                />
              </tr>
            </thead>
            <tbody>
              {data?.nodes.map((node) => (
                <tr key={node.id}>
                  <td>{node.name}</td>
                  <td>{n(node.cpuPercent, "%")}</td>
                  <td>{n(node.heapPercent, "%")}</td>
                  <td>{n(node.writeActive)}</td>
                  <td>{n(node.writeQueue)}</td>
                  <td>{n(node.writeRejected)}</td>
                  <td>{n(node.mergeCurrent)}</td>
                  <td>
                    {n(
                      node.uncommittedTranslogBytes === null
                        ? null
                        : node.uncommittedTranslogBytes / 1048576,
                      " MiB",
                    )}
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}
    </section>
  );
}
