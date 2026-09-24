import { useQuery } from "@tanstack/react-query";
import { useState } from "react";
import { Link } from "react-router-dom";
import type {
  ControlPlaneRuntime,
  ControlPlaneTaskDefinition,
} from "../../shared/contracts";
import { getControlPlaneRuntime, getDynamicConfig } from "../api";
import { JsonViewer } from "../components/JsonViewer";
import { RefreshControls } from "../components/RefreshControls";
import { metricRanges } from "../metricRange";
import { useReportPageQueryFailure } from "../navigation";
import { formatTime, useTimeMode } from "../time";
import { DynamicConfigPanel } from "./DynamicConfigPanel";
import "./control-plane.css";

type Task = ControlPlaneTaskDefinition;
const labels: Record<string, string> = {
  healthy: "正常",
  running: "执行中",
  idle: "空闲",
  failed: "执行异常",
  overdue: "逾期",
  pending: "等待首次执行",
  stopped: "已停止",
  disabled: "未启用",
  succeeded: "成功",
  canceled: "已取消",
};
const kinds: Record<string, string> = {
  periodic: "周期执行",
  continuous: "连续处理",
  notification: "通知 + 周期",
  service: "常驻服务",
};
const origins: Record<string, string> = {
  default: "默认配置",
  explicit: "显式配置",
  injected: "启动注入",
};
const links: Record<string, { to: string; label: string }> = {
  scheduler: { to: "/event-sources", label: "查看来源与任务分配" },
  "source-providers": { to: "/event-sources", label: "查看来源" },
  "active-alert-indexes": {
    to: "/strategy-index",
    label: "查看逐策略刷新状态",
  },
  "redis-stream-manager": {
    to: "/infrastructure/redis?tab=signal",
    label: "查看 Signal Stream",
  },
};

export function ControlPlanePage() {
  const [autoRefresh, setAutoRefresh] = useState(true);
  const [rangeSeconds, setRangeSeconds] = useState(3600);
  const [search, setSearch] = useState("");
  const [group, setGroup] = useState("");
  const [filter, setFilter] = useState("all");
  const [selected, setSelected] = useState("scheduler");
  const [tab, setTab] = useState("runtime");
  const runtime = useQuery({
    queryKey: ["runtime-control-plane", rangeSeconds],
    queryFn: ({ signal }) => getControlPlaneRuntime({ rangeSeconds, signal }),
    refetchInterval: autoRefresh ? 15000 : false,
  });
  const dynamic = useQuery({
    queryKey: ["dynamic-config"],
    queryFn: getDynamicConfig,
    refetchInterval: autoRefresh ? 15000 : false,
  });
  useReportPageQueryFailure(runtime.isError || dynamic.isError);
  const data = runtime.data;
  const tasks = data?.tasks ?? [];
  const visible = tasks.filter(
    (t) =>
      (!group || t.group === group) &&
      `${t.name} ${t.id}`.toLowerCase().includes(search.toLowerCase()) &&
      (filter === "all" ||
        (filter === "enabled" ? t.enabled : attention(t, data))),
  );
  const task = visible.find((t) => t.id === selected) ?? visible[0];
  const timeMode = useTimeMode();
  const fetching = runtime.isFetching || dynamic.isFetching;
  const refresh = () => {
    void Promise.all([runtime.refetch(), dynamic.refetch()]);
  };
  return (
    <section className="cp-page">
      <header className="cp-header">
        <div>
          <p className="eyebrow">SYSTEM / CONTROL PLANE</p>
          <h1>Control Plane</h1>
          <p>查看控制面的任务执行、处理结果与生效配置。</p>
        </div>
        <RefreshControls
          isFetching={fetching}
          autoRefresh={autoRefresh}
          intervalSeconds={15}
          lastSuccessfulAt={runtime.dataUpdatedAt || undefined}
          onRefresh={refresh}
          onToggleAutoRefresh={() => setAutoRefresh((v) => !v)}
        />
      </header>
      {runtime.isError && (
        <div role="alert" className="error-banner">
          {runtime.error instanceof Error
            ? runtime.error.message
            : "任务状态加载失败"}
          {data &&
            `。当前展示 ${formatTime(data.snapshotAt, timeMode)} 的旧快照。`}
        </div>
      )}
      {!data && !runtime.isError && (
        <p role="status">正在读取控制面任务目录…</p>
      )}
      {data && (
        <>
          <div className="cp-summary">
            <Summary
              title="已启用任务"
              value={`${tasks.filter((t) => t.enabled).length} / ${tasks.length}`}
              note="任务目录由控制面注册"
            />
            <Summary
              title="需要关注"
              value={String(tasks.filter((t) => attention(t, data)).length)}
              note="异常、停止或观测信息不完整"
            />
            <Summary
              title="空闲任务"
              value={String(tasks.filter((t) => t.state === "idle").length)}
              note="本轮没有待处理工作"
            />
            <Summary
              title="当前控制面"
              value={data.owner}
              note={`启动于 ${formatTime(data.startedAt, timeMode)}`}
            />
          </div>
          <div className="cp-service-strip">
            {data.services.map((s) => (
              <span key={s.id}>
                <i className={`cp-dot ${s.state}`} aria-hidden="true" />
                <strong>{s.name}</strong> {labels[s.state] ?? s.state}
              </span>
            ))}
            <span>快照 {formatTime(data.snapshotAt, timeMode)}</span>
          </div>
          <p className="cp-boundary">
            当前状态来自上述单个控制面进程，历史指标来自
            Prometheus。任务共享进程监督；调度中心通过 Redis
            租约保持独占。页面刷新只读取状态。
          </p>
          <div className="cp-toolbar">
            <label>
              <span className="visually-hidden">搜索任务</span>
              <input
                aria-label="搜索任务"
                value={search}
                onChange={(e) => setSearch(e.target.value)}
                placeholder="搜索任务名称或 ID"
              />
            </label>
            <select
              aria-label="任务分组"
              value={group}
              onChange={(e) => setGroup(e.target.value)}
            >
              <option value="">全部分组</option>
              {[...new Set(tasks.map((t) => t.group))].map((g) => (
                <option key={g}>{g}</option>
              ))}
            </select>
            <div className="cp-filter" role="group" aria-label="任务筛选">
              {[
                ["all", "全部"],
                ["enabled", "已启用"],
                ["attention", "需要关注"],
              ].map(([id, label]) => (
                <button
                  key={id}
                  aria-pressed={filter === id}
                  onClick={() => setFilter(id)}
                >
                  {label}
                </button>
              ))}
            </div>
            <span className="cp-result-count">{visible.length} 个任务</span>
          </div>
          <div className="cp-workspace">
            <div className="cp-list" aria-label="任务列表">
              {visible.length === 0 && (
                <p className="cp-empty">没有匹配的任务。</p>
              )}
              {[...new Set(visible.map((t) => t.group))].map((g) => (
                <section className="cp-group" key={g}>
                  <h2>{g}</h2>
                  {visible
                    .filter((t) => t.group === g)
                    .map((t) => (
                      <button
                        className={`cp-task-row ${task?.id === t.id ? "selected" : ""}`}
                        key={t.id}
                        onClick={() => setSelected(t.id)}
                        aria-pressed={task?.id === t.id}
                      >
                        <div className="cp-task-row-title">
                          <strong>{t.name}</strong>
                          <Badge state={t.state} />
                        </div>
                        <span>
                          {t.enabled
                            ? `${kinds[t.kind] ?? t.kind}${t.intervalSeconds ? ` · ${t.intervalSeconds}s` : ""}`
                            : t.disabledReason}
                        </span>
                        <div className="cp-task-row-bottom">
                          <span>
                            {t.execution.lastSuccess
                              ? `最近成功 ${formatTime(t.execution.lastSuccess, timeMode)}`
                              : "尚无成功记录"}
                          </span>
                          {t.enabled && (
                            <small
                              className={
                                observed(data, t.id) ? "" : "cp-warning"
                              }
                            >
                              {observation(data, t.id)}
                            </small>
                          )}
                        </div>
                      </button>
                    ))}
                </section>
              ))}
            </div>
            {task && (
              <article className="cp-detail" aria-label="任务详情">
                <header>
                  <p className="eyebrow">{task.id}</p>
                  <div className="cp-detail-title">
                    <h2>{task.name}</h2>
                    <Badge state={task.state} />
                  </div>
                  <p>{task.description}</p>
                  {!task.enabled && (
                    <p className="cp-disabled-reason">
                      未启用：{task.disabledReason}
                    </p>
                  )}
                </header>
                <div
                  className="cp-tabs"
                  role="tablist"
                  aria-label="任务详情视图"
                >
                  {[
                    ["runtime", "运行详情"],
                    ["execution", "执行情况"],
                    ["config", "生效配置"],
                  ].map(([id, label]) => (
                    <button
                      key={id}
                      id={`cp-tab-${id}`}
                      role="tab"
                      aria-selected={tab === id}
                      aria-controls="cp-task-panel"
                      onClick={() => setTab(id)}
                    >
                      {label}
                    </button>
                  ))}
                </div>
                <div
                  role="tabpanel"
                  id="cp-task-panel"
                  aria-labelledby={`cp-tab-${tab}`}
                  className="cp-detail-body"
                >
                  {tab === "runtime" && (
                    <>
                      <dl className="cp-facts">
                        <Fact
                          label="当前 Owner"
                          value={task.active ? data.owner : "未运行"}
                        />
                        <Fact
                          label="执行方式"
                          value={kinds[task.kind] ?? task.kind}
                        />
                        <Fact
                          label="最近完成"
                          value={
                            task.execution.finishedAt
                              ? formatTime(task.execution.finishedAt, timeMode)
                              : "尚无记录"
                          }
                        />
                        <Fact
                          label="最近耗时"
                          value={
                            task.execution.finishedAt
                              ? `${task.execution.durationSeconds.toFixed(3)}s`
                              : "—"
                          }
                        />
                      </dl>
                      {task.enabled && !observed(data, task.id) && (
                        <p className="cp-notice">
                          {observation(data, task.id)}
                          。当前运行状态以控制面快照为准，历史数据暂不能完整归属到该进程。
                        </p>
                      )}
                      {duplicateOwners(data, task.id) > 1 && (
                        <p className="cp-notice">
                          历史指标发现 {duplicateOwners(data, task.id)} 个活跃
                          Owner，请检查是否存在重复控制面。
                        </p>
                      )}
                      <h3>最近子流程结果</h3>
                      <p className="cp-caption">
                        展示最近一轮及有限目标详情，时间是各子流程自己的完成时间。
                      </p>
                      {task.steps.length > 0 ? (
                        <div className="cp-table-scroll">
                          <table>
                            <thead>
                              <tr>
                                <th>子流程 / 目标</th>
                                <th>结果</th>
                                <th>工作量 / 失败项</th>
                                <th>完成时间</th>
                              </tr>
                            </thead>
                            <tbody>
                              {task.steps.map((s) => (
                                <tr key={s.id}>
                                  <td>
                                    {s.name}
                                    <small>{s.errorCode}</small>
                                  </td>
                                  <td>
                                    <Badge state={s.outcome} />
                                  </td>
                                  <td>
                                    {s.work < 0 ? "—" : s.work} / {s.failures}
                                  </td>
                                  <td>
                                    {s.finishedAt
                                      ? formatTime(s.finishedAt, timeMode)
                                      : "—"}
                                  </td>
                                </tr>
                              ))}
                            </tbody>
                          </table>
                        </div>
                      ) : (
                        <p className="cp-empty">
                          {task.enabled
                            ? "暂无子流程结果，主任务的执行记录见“执行情况”。"
                            : "启用后开始记录执行结果。"}
                        </p>
                      )}
                      {task.detailsTruncated && (
                        <p className="cp-notice">
                          目标详情达到 64 项上限，列表不代表全部目标。
                        </p>
                      )}
                      {task.id === "redis-stream-manager" && (
                        <p className="cp-caption">
                          每轮最多展示 16 个来源；单条 Stream
                          的历史执行次数与来源扫描轮次分别统计。
                        </p>
                      )}
                      {task.id === "active-alert-indexes" && (
                        <p className="cp-caption">
                          目标名称是无凭据的身份摘要；逐策略错误及待刷新队列可在策略索引页查看。
                        </p>
                      )}
                      {task.id === "dynamic-config" && (
                        <DynamicConfigPanel
                          autoRefresh={false}
                          managed={dynamic}
                        />
                      )}
                      <Drilldown task={task} />
                    </>
                  )}
                  {tab === "execution" && (
                    <>
                      <h3>当前进程累计</h3>
                      <p className="cp-caption">
                        从本次启动开始，重启后重置。取消单独计数。
                      </p>
                      <dl className="cp-facts">
                        <Fact
                          label="成功轮次"
                          value={String(task.execution.succeeded)}
                        />
                        <Fact
                          label="失败轮次"
                          value={String(task.execution.failed)}
                        />
                        <Fact
                          label="取消轮次"
                          value={String(task.execution.canceled)}
                        />
                        <Fact
                          label="最近工作量"
                          value={
                            task.execution.work < 0
                              ? "—"
                              : String(task.execution.work)
                          }
                        />
                      </dl>
                      {task.execution.errorCode && (
                        <p className="cp-notice">
                          最近错误：{task.execution.errorCode}
                          。详细原因请按进程与时间查看日志。
                        </p>
                      )}
                      <div className="cp-history-heading">
                        <h3>历史指标</h3>
                        <select
                          aria-label="历史时间范围"
                          value={rangeSeconds}
                          onChange={(e) =>
                            setRangeSeconds(Number(e.target.value))
                          }
                        >
                          {metricRanges.map((r) => (
                            <option key={r.seconds} value={r.seconds}>
                              {r.label}
                            </option>
                          ))}
                        </select>
                      </div>
                      <p className="cp-caption">
                        仅统计能与当前 Owner 对应的 Prometheus
                        实例；未采集显示“—”。
                      </p>
                      <dl className="cp-facts">
                        <Fact
                          label="窗口成功"
                          value={history(
                            data,
                            task.id,
                            "runCount",
                            "succeeded",
                          )}
                        />
                        <Fact
                          label="窗口失败"
                          value={history(data, task.id, "runCount", "failed")}
                        />
                        <Fact
                          label="平均耗时"
                          value={history(data, task.id, "averageDuration")}
                        />
                        <Fact
                          label="P95 耗时"
                          value={history(data, task.id, "p95Duration")}
                        />
                      </dl>
                      {task.id === "elasticsearch-alert-archiver" && (
                        <p>
                          当前待归档：{data.archive.backlog ?? "—"}。
                          {data.archive.message}
                        </p>
                      )}
                      <p className="cp-caption">
                        这是执行计数与最近结果，不提供持久化执行日志。连续归档的等待间隔不作为整轮超时。
                      </p>
                    </>
                  )}
                  {tab === "config" && (
                    <>
                      <dl className="cp-facts">
                        <Fact
                          label="配置来源"
                          value={
                            origins[task.configSource] ?? task.configSource
                          }
                        />
                        <Fact
                          label={
                            task.kind === "continuous"
                              ? "空闲 / 重试等待"
                              : "周期"
                          }
                          value={
                            task.intervalSeconds
                              ? `${task.intervalSeconds}s`
                              : "—"
                          }
                        />
                        <Fact
                          label="整轮观测预算"
                          value={
                            task.deadlineSeconds
                              ? `${task.deadlineSeconds}s`
                              : "未设置"
                          }
                        />
                        <Fact
                          label="启动依赖"
                          value={
                            task.dependsOn
                              .map(
                                (id) =>
                                  tasks.find((t) => t.id === id)?.name ?? id,
                              )
                              .join(" → ") || "无"
                          }
                        />
                      </dl>
                      <JsonViewer
                        value={task.settings}
                        description="控制面返回的生效参数；连接凭据不进入任务快照。启动配置变更需重启。"
                      />
                    </>
                  )}
                </div>
              </article>
            )}
          </div>
        </>
      )}
    </section>
  );
}
function Badge({ state }: { state: string }) {
  return <span className={`cp-badge ${state}`}>{labels[state] ?? state}</span>;
}
function Summary({
  title,
  value,
  note,
}: {
  title: string;
  value: string;
  note: string;
}) {
  return (
    <article>
      <span>{title}</span>
      <strong>{value}</strong>
      <small>{note}</small>
    </article>
  );
}
function Fact({ label, value }: { label: string; value: string }) {
  return (
    <div>
      <dt>{label}</dt>
      <dd>{value}</dd>
    </div>
  );
}
function Drilldown({ task }: { task: Task }) {
  const link =
    links[task.id] ??
    (task.id.startsWith("elasticsearch-")
      ? { to: "/storage/elasticsearch", label: "查看 ES 资源" }
      : undefined);
  return link ? (
    <Link className="cp-drilldown" to={link.to}>
      {link.label} →
    </Link>
  ) : null;
}
function instances(data: ControlPlaneRuntime) {
  return data.processes.items
    .filter((p) => p.serviceInstanceId === data.owner)
    .map((p) => p.instance);
}
function duplicateOwners(data: ControlPlaneRuntime, id: string) {
  return new Set(
    (data.metrics.series.active ?? [])
      .filter((s) => s.labels.linkd_task === id && s.value === 1)
      .map((s) => s.labels.instance),
  ).size;
}
function observed(data: ControlPlaneRuntime, id: string) {
  const owners = instances(data);
  return (
    data.metrics.status !== "unavailable" &&
    (data.metrics.series.active ?? []).some(
      (s) =>
        s.labels.linkd_task === id &&
        s.value === 1 &&
        owners.includes(s.labels.instance),
    )
  );
}
function observation(data: ControlPlaneRuntime, id: string) {
  return observed(data, id) ? "指标已关联" : "观测不完整";
}
function attention(task: Task, data?: ControlPlaneRuntime) {
  return (
    task.enabled &&
    (["failed", "overdue", "stopped"].includes(task.state) ||
      (!!data &&
        (!observed(data, task.id) || duplicateOwners(data, task.id) > 1)))
  );
}
function history(
  data: ControlPlaneRuntime,
  id: string,
  key: string,
  outcome?: string,
) {
  const owners = instances(data);
  const samples = (data.metrics.series[key] ?? [])
    .filter(
      (s) =>
        s.labels.linkd_task === id &&
        owners.includes(s.labels.instance) &&
        (!outcome || s.labels.linkd_outcome === outcome),
    )
    .map((s) => s.value)
    .filter((v): v is number => v !== null);
  if (!samples.length) return "—";
  const value =
    key === "runCount"
      ? samples.reduce((a, b) => a + b, 0)
      : Math.max(...samples);
  return key === "runCount" ? value.toLocaleString() : `${value.toFixed(3)}s`;
}
