import { useQuery, useQueryClient } from "@tanstack/react-query";
import { useState } from "react";
import { RefreshControls } from "../components/RefreshControls";
import { StatusBadge } from "../components/StatusBadge";
import { formatTime, useTimeMode } from "../time";
import { EventSourceEditor } from "./EventSourceEditor";
import {
  hasMetadataSuccess,
  listSources,
  schedulingSchema,
  sourceRequest,
  sourceRoles,
  type Scheduling,
  type SourceRecord,
} from "./event-sources";

function SourceState({ source }: { source: SourceRecord }) {
  return (
    <span
      className={`status-badge ${source.deleted ? "danger" : source.spec.enabled ? "success" : "neutral"}`}
    >
      {source.deleted ? "已删除" : source.spec.enabled ? "启用" : "停用"}
    </span>
  );
}

function SourceRuntime({
  source,
  data,
  loading,
  error,
}: {
  source?: string;
  data?: Scheduling;
  loading: boolean;
  error: Error | null;
}) {
  const timeMode = useTimeMode();
  const tasks = Object.values(data?.tasks ?? {})
    .filter((task) => task.source === source)
    .sort(
      (a, b) =>
        a.role.localeCompare(b.role) ||
        a.worker.localeCompare(b.worker) ||
        a.id.localeCompare(b.id),
    );
  return (
    <>
      {error && (
        <div role="alert" className="error-banner">
          调度信息刷新失败：{error.message}。
          {data ? "以下为上次成功的快照。" : "运行状态未知。"}
        </div>
      )}
      {loading && <p role="status">正在加载调度信息…</p>}
      <h3>调度目标</h3>
      <div className="event-source-runtime-cards">
        {sourceRoles.map((role) => {
          const status = data?.statuses?.find(
            (s) => s.source === source && s.role === role,
          );
          const metadataKnown = hasMetadataSuccess(status?.metadata?.success);
          return (
            <article className="event-source-runtime-card" key={role}>
              <h4>{role === "cleaner" ? "Cleaner" : "Lifecycle"}</h4>
              <dl>
                <div>
                  <dt>匹配 Worker</dt>
                  <dd>{status?.matching ?? "未知"}</dd>
                </div>
                <div>
                  <dt>运行 / 目标</dt>
                  <dd>
                    {status ? `${status.running} / ${status.target}` : "未知"}
                  </dd>
                </div>
                {role === "cleaner" && (
                  <>
                    <div>
                      <dt>Kafka 分区上限</dt>
                      <dd>
                        {metadataKnown ? status?.metadata?.partitions : "未知"}
                      </dd>
                    </div>
                    <div>
                      <dt>最近探测成功</dt>
                      <dd>
                        {metadataKnown
                          ? formatTime(status!.metadata!.success, timeMode)
                          : "尚未成功"}
                      </dd>
                    </div>
                  </>
                )}
              </dl>
              {status?.reason && (
                <p className="event-source-runtime-note">{status.reason}</p>
              )}
              {status?.metadata?.error && (
                <p className="event-source-error-text">
                  最近探测失败：{status.metadata.error}
                </p>
              )}
              {!status && (
                <p className="event-source-hint">暂无该角色的调度快照。</p>
              )}
            </article>
          );
        })}
      </div>
      <h3>
        实际任务{" "}
        <span className="event-source-hint">
          {data ? `${tasks.length} 个` : "未知"}
        </span>
      </h3>
      <div className="table-panel table-scroll">
        <table className="event-source-task-table">
          <thead>
            <tr>
              <th>角色 / Worker</th>
              <th>版本 / 阶段</th>
              <th>Partitions / 错误</th>
            </tr>
          </thead>
          <tbody>
            {tasks.map((task) => (
              <tr key={task.id}>
                <td>
                  <strong>{task.role}</strong>
                  <span className="event-source-cell-secondary">
                    {task.worker}
                  </span>
                </td>
                <td>
                  <span>v{task.version}</span>
                  <span className="event-source-cell-secondary">
                    <StatusBadge value={task.phase} />
                  </span>
                </td>
                <td>
                  {task.partitions?.length ? (
                    <details>
                      <summary>{task.partitions.length} 个分区</summary>
                      <ul>
                        {task.partitions.map((p) => (
                          <li key={p}>{p}</li>
                        ))}
                      </ul>
                    </details>
                  ) : (
                    "—"
                  )}
                  {task.error && (
                    <details className="event-source-error-text">
                      <summary>查看任务错误</summary>
                      <p>{task.error}</p>
                    </details>
                  )}
                </td>
              </tr>
            ))}
            {!tasks.length && (
              <tr>
                <td colSpan={3} className="empty-row">
                  {loading
                    ? "正在加载任务…"
                    : !data
                      ? "任务状态未知"
                      : "该来源暂无任务"}
                </td>
              </tr>
            )}
          </tbody>
        </table>
      </div>
    </>
  );
}

export function EventSourcesPage() {
  const cache = useQueryClient();
  const [autoRefresh, setAutoRefresh] = useState(true);
  const [tab, setTab] = useState<"sources" | "workers">("sources");
  const [search, setSearch] = useState("");
  const [state, setState] = useState("current");
  const [page, setPage] = useState(0);
  const [selection, setSelection] = useState<{
    source?: SourceRecord;
    key: number;
  }>();
  const [session, setSession] = useState(0);
  const records = useQuery({
    queryKey: ["event-sources"],
    queryFn: ({ signal }) => listSources(signal),
    refetchInterval: autoRefresh ? 5000 : false,
  });
  const runtime = useQuery({
    queryKey: ["scheduling"],
    queryFn: async ({ signal }) =>
      schedulingSchema.parse(
        await sourceRequest("/local-api/scheduling", "GET", undefined, signal),
      ),
    refetchInterval: autoRefresh ? 5000 : false,
  });
  const query = search.trim().toLowerCase();
  const filtered = (records.data ?? []).filter((source) => {
    const matchesState =
      state === "all" ||
      (state === "deleted"
        ? source.deleted
        : !source.deleted &&
          (state === "current" ||
            source.spec.enabled === (state === "enabled")));
    return (
      matchesState &&
      (!query ||
        source.id.toLowerCase().includes(query) ||
        source.spec.storage.kafka.topic.toLowerCase().includes(query))
    );
  });
  const pageCount = Math.max(1, Math.ceil(filtered.length / 20));
  const currentPage = Math.min(page, pageCount - 1);
  const rows = filtered.slice(currentPage * 20, (currentPage + 1) * 20);
  const fetching = records.isFetching || runtime.isFetching;
  const successAt =
    records.dataUpdatedAt && runtime.dataUpdatedAt
      ? Math.min(records.dataUpdatedAt, runtime.dataUpdatedAt)
      : undefined;
  function refresh() {
    void records.refetch();
    void runtime.refetch();
  }
  function open(source?: SourceRecord) {
    setSession(session + 1);
    setSelection({ source, key: session + 1 });
  }
  function saved(source: SourceRecord) {
    setSelection((current) => (current ? { ...current, source } : current));
    void cache.invalidateQueries({ queryKey: ["event-sources"] });
    void cache.invalidateQueries({ queryKey: ["scheduling"] });
  }
  const workers = Object.values(runtime.data?.workers ?? {}).sort((a, b) =>
    a.id.localeCompare(b.id),
  );
  return (
    <section className="event-sources-page">
      <header className="page-heading">
        <div>
          <p className="eyebrow">EVENT SOURCES</p>
          <h1>事件来源与任务调度</h1>
          <p>
            管理来源配置、发布版本与 Cleaner / Lifecycle
            调度，查看各来源的实际运行情况。
          </p>
        </div>
        <RefreshControls
          status={
            records.error || runtime.error
              ? "partial"
              : records.isPending || runtime.isPending
                ? "loading"
                : "available"
          }
          lastSuccessfulAt={successAt}
          isFetching={fetching}
          autoRefresh={autoRefresh}
          intervalSeconds={5}
          onRefresh={refresh}
          onToggleAutoRefresh={() => setAutoRefresh((value) => !value)}
        >
          <button
            type="button"
            className="primary-button"
            onClick={() => open()}
          >
            新增来源
          </button>
        </RefreshControls>
      </header>
      <div
        className="event-source-tabs"
        role="tablist"
        aria-label="来源管理视图"
      >
        <button
          type="button"
          role="tab"
          id="sources-tab"
          aria-controls="sources-panel"
          aria-selected={tab === "sources"}
          onClick={() => setTab("sources")}
        >
          事件来源
          {records.data &&
            ` (${records.data.filter((s) => !s.deleted).length})`}
        </button>
        <button
          type="button"
          role="tab"
          id="workers-tab"
          aria-controls="workers-panel"
          aria-selected={tab === "workers"}
          onClick={() => setTab("workers")}
        >
          Workers{runtime.data && ` (${workers.length})`}
        </button>
      </div>
      {records.error && (
        <div role="alert" className="error-banner">
          来源配置加载失败：{records.error.message}。
          {records.data ? "以下保留上次成功的快照。" : "请检查连接并重试。"}
        </div>
      )}
      {runtime.error && (
        <div role="alert" className="error-banner">
          调度信息刷新失败：{runtime.error.message}。
          {runtime.data
            ? "运行数据为上次成功的快照。"
            : "运行数据暂不可用，状态显示为未知。"}
        </div>
      )}
      {tab === "sources" ? (
        <div role="tabpanel" id="sources-panel" aria-labelledby="sources-tab">
          <div className="filter-panel event-source-filters">
            <label>
              <span>搜索来源 ID / Topic</span>
              <input
                aria-label="搜索来源 ID / Topic"
                type="search"
                value={search}
                onChange={(e) => {
                  setSearch(e.target.value);
                  setPage(0);
                }}
              />
            </label>
            <label>
              <span>来源状态</span>
              <select
                aria-label="来源状态"
                value={state}
                onChange={(e) => {
                  setState(e.target.value);
                  setPage(0);
                }}
              >
                <option value="current">未删除</option>
                <option value="enabled">启用</option>
                <option value="disabled">停用</option>
                <option value="deleted">已删除</option>
                <option value="all">全部</option>
              </select>
            </label>
            {(search || state !== "current") && (
              <button
                type="button"
                onClick={() => {
                  setSearch("");
                  setState("current");
                  setPage(0);
                }}
              >
                清除筛选
              </button>
            )}
          </div>
          <div className="table-panel">
            <div className="table-meta">
              <span>
                {records.isPending
                  ? "正在加载来源…"
                  : records.data
                    ? `共 ${filtered.length} 个来源`
                    : "来源数据不可用"}
              </span>
              <span>运行数 / 目标数 · 每 5 秒刷新</span>
            </div>
            <div className="table-scroll">
              <table className="event-source-list-table">
                <thead>
                  <tr>
                    <th>来源 ID</th>
                    <th>状态</th>
                    <th>Kafka Topic</th>
                    <th>发布版本</th>
                    <th>Cleaner</th>
                    <th>Lifecycle</th>
                    <th>操作</th>
                  </tr>
                </thead>
                <tbody>
                  {rows.map((source) => (
                    <tr key={source.id}>
                      <td>
                        <button
                          type="button"
                          className="event-source-link"
                          onClick={() => open(source)}
                        >
                          {source.id}
                        </button>
                      </td>
                      <td>
                        <SourceState source={source} />
                      </td>
                      <td>
                        <span title={source.spec.storage.kafka.topic}>
                          {source.spec.storage.kafka.topic}
                        </span>
                      </td>
                      <td>
                        v{source.published}
                        {source.published !== source.revision && (
                          <span className="event-source-cell-secondary">
                            待发布 v{source.revision}
                          </span>
                        )}
                      </td>
                      {sourceRoles.map((role) => {
                        const status = runtime.data?.statuses?.find(
                          (s) => s.source === source.id && s.role === role,
                        );
                        return (
                          <td key={role}>
                            {status ? (
                              `${status.running} / ${status.target}`
                            ) : (
                              <span className="event-source-hint">未知</span>
                            )}
                          </td>
                        );
                      })}
                      <td>
                        <button
                          type="button"
                          onClick={() => open(source)}
                          aria-label={`查看 ${source.id}`}
                        >
                          查看详情
                        </button>
                      </td>
                    </tr>
                  ))}
                  {!rows.length && (
                    <tr>
                      <td colSpan={7} className="empty-row">
                        {records.isPending
                          ? "正在加载来源…"
                          : !records.data
                            ? "无法加载来源，请点击立即刷新重试"
                            : records.data.length === 0
                              ? "暂无事件来源，点击新增来源开始配置"
                              : "没有符合条件的来源"}
                      </td>
                    </tr>
                  )}
                </tbody>
              </table>
            </div>
            <div className="pagination">
              <span>
                第 {currentPage + 1} / {pageCount} 页 · 每页 20 条
              </span>
              <button
                type="button"
                disabled={currentPage === 0}
                onClick={() => setPage(currentPage - 1)}
              >
                上一页
              </button>
              <button
                type="button"
                disabled={currentPage + 1 >= pageCount}
                onClick={() => setPage(currentPage + 1)}
              >
                下一页
              </button>
            </div>
          </div>
        </div>
      ) : (
        <div role="tabpanel" id="workers-panel" aria-labelledby="workers-tab">
          <div className="table-panel table-scroll">
            <table className="event-source-worker-table">
              <thead>
                <tr>
                  <th>Worker</th>
                  <th>角色</th>
                  <th>标签</th>
                  <th>选择器要求</th>
                </tr>
              </thead>
              <tbody>
                {workers.map((worker) => (
                  <tr key={worker.id}>
                    <td>{worker.id}</td>
                    <td>{worker.roles.join(" / ")}</td>
                    <td>
                      {Object.entries(worker.labels ?? {}).length ? (
                        <details>
                          <summary>
                            {Object.keys(worker.labels!).length} 个标签
                          </summary>
                          <ul>
                            {Object.entries(worker.labels!).map(
                              ([key, value]) => (
                                <li key={key}>
                                  <code>
                                    {key}={value}
                                  </code>
                                </li>
                              ),
                            )}
                          </ul>
                        </details>
                      ) : (
                        "无标签"
                      )}
                    </td>
                    <td>
                      {worker.require_explicit_selector
                        ? "必须显式匹配"
                        : "接受空选择器"}
                    </td>
                  </tr>
                ))}
                {!workers.length && (
                  <tr>
                    <td colSpan={4} className="empty-row">
                      {runtime.isPending
                        ? "正在加载 Worker…"
                        : runtime.data
                          ? "暂无注册 Worker"
                          : "Worker 状态未知"}
                    </td>
                  </tr>
                )}
              </tbody>
            </table>
          </div>
        </div>
      )}
      {selection && (
        <EventSourceEditor
          key={selection.key}
          initial={selection.source}
          onClose={() => setSelection(undefined)}
          onSaved={saved}
          latestRevision={
            records.data?.find((s) => s.id === selection.source?.id)?.revision
          }
          runtime={
            <SourceRuntime
              source={selection.source?.id}
              data={runtime.data}
              loading={runtime.isPending}
              error={runtime.error}
            />
          }
        />
      )}
    </section>
  );
}
