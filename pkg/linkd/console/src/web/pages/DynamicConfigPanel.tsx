import { useQuery } from "@tanstack/react-query";
import { getDynamicConfig } from "../api";

const states: Record<string, string> = {
  disabled: "未启用",
  pending: "等待同步",
  restored: "已恢复持久化快照",
  synced: "同步成功",
  source_error: "上游读取失败，保留有效配置",
  invalid_config: "上游配置非法，保留有效配置",
  persistence_error: "持久化失败，未发布新配置",
};
const origins: Record<string, string> = {
  yaml: "YAML 配置",
  persisted: "持久化快照",
  upstream: "上游配置",
};

export function DynamicConfigPanel({
  autoRefresh,
  managed,
}: {
  autoRefresh: boolean;
  managed?: {
    data?: Awaited<ReturnType<typeof getDynamicConfig>>;
    dataUpdatedAt: number;
    isError: boolean;
    isFetching: boolean;
  };
}) {
  const query = useQuery({
    queryKey: ["dynamic-config"],
    queryFn: getDynamicConfig,
    refetchInterval: !managed && autoRefresh ? 15_000 : false,
    enabled: !managed,
  });
  const state = managed ?? query;
  const data = state.data;
  const config = data?.config;
  const age = config?.persisted_at
    ? Math.max(
        0,
        Math.floor(
          (state.dataUpdatedAt - Date.parse(config.persisted_at)) / 1000,
        ),
      )
    : undefined;
  return (
    <section className="panel dynamic-config-panel" aria-label="动态配置">
      <div className="dynamic-config-heading">
        <div>
          <h2>动态配置</h2>
          <p>查看当前等级、持久化恢复与进程应用状态。</p>
        </div>
        {!managed && (
          <button
            type="button"
            disabled={query.isFetching}
            aria-busy={query.isFetching}
            onClick={() => void query.refetch()}
          >
            {query.isFetching ? "正在刷新…" : "刷新配置状态"}
          </button>
        )}
      </div>
      {state.isError && (
        <p role="alert">动态配置状态读取失败，请检查控制面连接。</p>
      )}
      {!data && !state.isError && <p>正在读取配置状态…</p>}
      {config && (
        <>
          <p>
            {states[config.sync_state] ?? config.sync_state}
            {config.enabled &&
              ` · 当前使用 ${origins[config.origin] ?? config.origin}`}
          </p>
          {config.enabled && (
            <>
              <p>
                来源：{config.source}（{config.source_type}） · 来源租户：
                {config.bk_tenant_id}
              </p>
              {config.origin === "yaml" && (
                <p>尚无可恢复的上游快照，正在使用 YAML 降级配置。</p>
              )}
              <p>
                最后同步成功：{config.last_success ?? "尚未成功"} ·
                快照保存时间：{config.persisted_at ?? "尚未保存"}
                {age !== undefined && ` · 快照年龄：${age} 秒`}
              </p>
              <p>
                当前摘要：<code>{config.current?.digest ?? "—"}</code>
              </p>
              <table>
                <thead>
                  <tr>
                    <th>等级</th>
                    <th>Priority（越小越严重）</th>
                    <th>默认等级</th>
                  </tr>
                </thead>
                <tbody>
                  {config.current?.severity.levels.map((level) => (
                    <tr key={level.name}>
                      <td>{level.name}</td>
                      <td>{level.priority}</td>
                      <td>
                        {level.name ===
                        config.current?.severity.default_severity
                          ? "是"
                          : ""}
                      </td>
                    </tr>
                  ))}
                </tbody>
              </table>
              <h3>Worker 应用状态</h3>
              <table>
                <thead>
                  <tr>
                    <th>Worker</th>
                    <th>应用状态</th>
                    <th>配置摘要</th>
                  </tr>
                </thead>
                <tbody>
                  {Object.values(data.workers).map((worker) => (
                    <tr key={worker.id}>
                      <td>{worker.id}</td>
                      <td>
                        {worker.config_error
                          ? `应用失败：${worker.config_error}`
                          : worker.config_digest === config.current?.digest
                            ? "已应用"
                            : "等待同步"}
                      </td>
                      <td>
                        <code>{worker.config_digest ?? "—"}</code>
                      </td>
                    </tr>
                  ))}
                </tbody>
              </table>
            </>
          )}
        </>
      )}
    </section>
  );
}
