import { KACAlertLink } from "../components/KACAlertLink";
import { useState, type FormEvent } from "react";
import { DeliveryMetricsPanel } from "./DeliveryMetricsPanel";
import { Link, useSearchParams } from "react-router-dom";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import {
  projectionQuery,
  projectionRetryCommand,
  projectionTask,
  type ProjectionTask,
  type ProjectionRetryCommand,
} from "../../shared/projection-tasks";
import {
  getProjectionSnapshot,
  getProjectionTask,
  listProjectionTasks,
} from "../api";
import { consoleURL } from "../base-path";
import { JsonViewer } from "../components/JsonViewer";
import { formatTime, useTimeMode } from "../time";
import "./policies.css";
import "./merge-runtime.css";
import "./projection-tasks.css";
const states = {
  pending: "待执行",
  sending: "尝试中",
  retry: "等待自动重试",
  delivered: "远端已确认，待本地 ACK",
  succeeded: "本任务已同步",
  failed: "失败，等待人工恢复",
};
function alertLink(row: ProjectionTask) {
  return (
    "/explore/alerts?" +
    new URLSearchParams({
      bk_tenant_id: row.bk_tenant_id,
      detail_tenant: row.bk_tenant_id,
      detail: row.alert_id,
    })
  );
}
export function ProjectionTasksPage() {
  const [params, setParams] = useSearchParams(),
    client = useQueryClient(),
    [error, setError] = useState(""),
    [refreshing, setRefreshing] = useState(false),
    [showMetrics, setShowMetrics] = useState(false),
    mode = useTimeMode();
  const tenant = params.get("bk_tenant_id") ?? "",
    id = params.get("id") ?? "";
  const parsed = projectionQuery.safeParse({
    bk_tenant_id: tenant,
    after: params.get("after") ?? "",
    limit: 4,
    ...Object.fromEntries(
      ["alert_id", "target_id", "source_id", "state"]
        .filter((k) => params.get(k))
        .map((k) => [k, params.get(k)]),
    ),
  });
  const list = useQuery({
    queryKey: ["projection-tasks", params.toString()],
    queryFn: ({ signal }) => {
      if (!parsed.success) throw new Error("请填写合法租户");
      return listProjectionTasks(parsed.data, signal);
    },
    enabled: parsed.success,
  });
  const detail = useQuery({
    queryKey: ["projection-task", tenant, id],
    queryFn: ({ signal }) => getProjectionTask(tenant, id, signal),
    enabled: /^[a-zA-Z0-9_-]{1,64}$/.test(tenant) && /^[a-f0-9]{64}$/.test(id),
    refetchInterval: (q) =>
      q.state.data &&
      ["pending", "sending", "retry", "delivered"].includes(
        q.state.data.progress.state,
      )
        ? 5000
        : false,
  });
  function search(e: FormEvent<HTMLFormElement>) {
    e.preventDefault();
    const data = new FormData(e.currentTarget),
      input = {
        bk_tenant_id: String(data.get("tenant") ?? "").trim(),
        after: "",
        limit: 4,
        ...Object.fromEntries(
          ["alert_id", "target_id", "source_id", "state"]
            .filter((k) => data.get(k))
            .map((k) => [k, String(data.get(k)).trim()]),
        ),
      },
      q = projectionQuery.safeParse(input),
      selected = String(data.get("id") ?? "").trim();
    if (!q.success || (selected && !/^[a-f0-9]{64}$/.test(selected))) {
      setError("请检查租户、筛选条件和任务 ID");
      return;
    }
    setError("");
    setParams({
      ...Object.fromEntries(
        Object.entries(q.data).map(([k, v]) => [k, String(v)]),
      ),
      ...(selected ? { id: selected } : {}),
    });
  }
  return (
    <div className="policies-page projection-tasks-page">
      <header className="page-header">
        <div>
          <h1>告警投影任务</h1>
          <p>查看固定快照、远端确认与本地同步进度。</p>
        </div>
        <button
          disabled={
            !parsed.success ||
            refreshing ||
            list.isFetching ||
            detail.isFetching
          }
          onClick={async () => {
            setRefreshing(true);
            try {
              const first = new URLSearchParams(params);
              first.delete("after");
              await client.invalidateQueries({
                queryKey: ["projection-tasks"],
                refetchType: "none",
              });
              if (params.get("after")) setParams(first);
              else await list.refetch();
              if (id) await detail.refetch();
            } finally {
              setRefreshing(false);
            }
          }}
        >
          刷新
        </button>
      </header>
      <p className="merge-note">
        恢复操作保存为待执行，由运行中的投递任务继续推进；可在控制面任务页确认是否启动。它不代表
        KAC 已同步。现有 KAC Kafka Hook 的发送不在此列表中。
      </p>
      <button
        type="button"
        aria-expanded={showMetrics}
        onClick={() => setShowMetrics((value) => !value)}
      >
        {showMetrics ? "收起投影运行观测" : "查看投影运行观测"}
      </button>
      {showMetrics && <DeliveryMetricsPanel kind="projection" />}
      <form className="merge-filters" key={params.toString()} onSubmit={search}>
        <label>
          租户
          <input name="tenant" defaultValue={tenant} required />
        </label>
        <label>
          Alert ID
          <input name="alert_id" defaultValue={params.get("alert_id") ?? ""} />
        </label>
        <label>
          目标 ID
          <input
            name="target_id"
            defaultValue={params.get("target_id") ?? ""}
          />
        </label>
        <label>
          来源 ID
          <input
            name="source_id"
            defaultValue={params.get("source_id") ?? ""}
          />
        </label>
        <label>
          任务状态
          <select name="state" defaultValue={params.get("state") ?? ""}>
            <option value="">全部状态</option>
            {Object.entries(states).map(([v, text]) => (
              <option key={v} value={v}>
                {text}
              </option>
            ))}
          </select>
        </label>
        <label>
          精确任务 ID
          <input name="id" defaultValue={id} />
        </label>
        <button>查询</button>
      </form>
      {error && <p role="alert">{error}</p>}
      {!parsed.success ? (
        <p>填写租户后查询。</p>
      ) : (
        <>
          {list.isPending && <p>正在读取任务…</p>}
          {list.error && <p role="alert">{list.error.message}</p>}
          {list.data && !list.error && (
            <>
              <table className="merge-table" aria-label="投影任务列表">
                <thead>
                  <tr>
                    <th>Alert / 目标</th>
                    <th>状态与版本</th>
                    <th>最近尝试</th>
                    <th>操作</th>
                  </tr>
                </thead>
                <tbody>
                  {list.data.items.map((row) => (
                    <tr key={row.id}>
                      <td>
                        <Link to={alertLink(row)}>{row.alert_id}</Link>
                        <span>
                          {row.target_id} · {row.source_id}
                        </span>
                      </td>
                      <td>
                        <strong>{states[row.progress.state]}</strong>
                        <p>
                          快照 r{row.revision} · {row.alert_status}
                        </p>
                      </td>
                      <td>
                        {row.progress.attempts}/8 次 · 第{" "}
                        {row.progress.generation} 轮
                        <p>{formatTime(row.progress.updated_at, mode)}</p>
                        {row.progress.error_code && (
                          <code>{row.progress.error_code}</code>
                        )}
                      </td>
                      <td>
                        <button
                          onClick={() => {
                            const next = new URLSearchParams(params);
                            next.set("id", row.id);
                            setParams(next);
                          }}
                        >
                          查看详情
                        </button>
                      </td>
                    </tr>
                  ))}
                </tbody>
              </table>
              {!list.data.items.length && (
                <p>本页没有符合条件的任务。空页仍可能有后续记录。</p>
              )}
              <p className="merge-note">
                按稳定任务 ID
                分页；筛选后的空页可以继续。列表可能稍后才反映新写入，精确详情以实时读取为准。
              </p>
              <div className="merge-pagination">
                <button
                  disabled={!params.get("after")}
                  onClick={() => {
                    const next = new URLSearchParams(params);
                    next.delete("after");
                    setParams(next);
                  }}
                >
                  回到首页
                </button>
                <button
                  disabled={!list.data.next || list.isFetching}
                  onClick={() => {
                    const next = new URLSearchParams(params);
                    next.set("after", list.data.next);
                    setParams(next);
                  }}
                >
                  下一页
                </button>
              </div>
            </>
          )}
        </>
      )}
      {id && (
        <section className="merge-detail">
          <h2>投影任务详情</h2>
          <code>{id}</code>
          {!/^[a-f0-9]{64}$/.test(id) ? (
            <p role="alert">任务 ID 不合法。</p>
          ) : (
            detail.isLoading && <p>正在读取详情…</p>
          )}
          {detail.error && <p role="alert">{detail.error.message}</p>}
          {detail.data && !detail.error && (
            <ProjectionDetail key={tenant + ":" + id} row={detail.data} />
          )}
        </section>
      )}
    </div>
  );
}
function ProjectionDetail({ row }: { row: ProjectionTask }) {
  const mode = useTimeMode(),
    [showSnapshot, setShowSnapshot] = useState(false),
    p = row.progress,
    receipt = p.receipt;
  const snapshot = useQuery({
    queryKey: ["projection-snapshot", row.bk_tenant_id, row.id],
    queryFn: ({ signal }) =>
      getProjectionSnapshot(row.bk_tenant_id, row.id, signal),
    enabled: showSnapshot,
  });
  return (
    <>
      <div className="merge-detail-summary">
        <strong>{states[p.state]}</strong>
        <span>
          任务快照 r{row.revision} · {row.alert_status}
        </span>
        <Link to={alertLink(row)}>查看当前 Alert</Link>
        <Link
          to={
            "/explore/kac-alarms?" +
            new URLSearchParams({
              bk_tenant_id: row.bk_tenant_id,
              id: row.alarm_id,
            })
          }
        >
          查询 KAC 实际文档
        </Link>
        <KACAlertLink tenant={row.bk_tenant_id} alarmID={row.alarm_id} />
      </div>
      <dl className="projection-facts">
        <dt>投影目标</dt>
        <dd>{row.target_id}</dd>
        <dt>来源发布</dt>
        <dd>
          {row.source_id} · v{row.source_version}
        </dd>
        <dt>KAC alarm_id</dt>
        <dd>
          <code>{row.alarm_id}</code>
        </dd>
        <dt>内容摘要</dt>
        <dd>
          <code>{row.content_hash}</code>
        </dd>
        <dt>任务 CAS 版本</dt>
        <dd>
          <code>{row.task_version}</code>
        </dd>
        <dt>创建时间</dt>
        <dd>{formatTime(row.created_at, mode)}</dd>
        <dt>尝试次数</dt>
        <dd>
          当前轮 {p.attempts}/8 · 累计 {p.total_attempts} · 第 {p.generation} 轮
        </dd>
        <dt>调度时间</dt>
        <dd>
          {p.due_at
            ? formatTime(p.due_at, mode)
            : p.lease_until
              ? "本次尝试期限 " + formatTime(p.lease_until, mode)
              : "无待调度时间"}
        </dd>
      </dl>
      {p.error_code && (
        <p role="status">
          安全错误码：<code>{p.error_code}</code>
          。请先确认依赖或配置问题已经处理，再恢复原任务。
        </p>
      )}
      <p>
        快照固定于排队版本；当前 Alert
        可能已有更新。本任务完成不等于最新版本也已同步。
      </p>
      {receipt && (
        <section className="projection-receipt">
          <h3>远端确认</h3>
          <p>
            已应用 r{receipt.applied_revision} · {receipt.applied_status} ·
            已确认可搜索
          </p>
          <p>
            文档定位：<code>{receipt.document_ref}</code>
          </p>
          {p.state === "delivered" && (
            <p>远端已确认，本地水位尚待完成；后续执行只补 ACK。</p>
          )}
        </section>
      )}
      {p.last_retry && (
        <section className="projection-receipt">
          <h3>最近一次人工恢复</h3>
          <p>
            {formatTime(p.last_retry.requested_at, mode)} ·{" "}
            {p.last_retry.command.operator_id}
          </p>
          <p>{p.last_retry.command.reason}</p>
          <code>{p.last_retry.command.operation_id}</code>
          <p>
            依据任务 CAS 版本 {p.last_retry.command.expected_version}
            。这里只展示最近一次恢复，累计次数见上方。
          </p>
        </section>
      )}
      <ProjectionRetryPanel row={row} />
      <button onClick={() => setShowSnapshot((v) => !v)}>
        {showSnapshot ? "收起冻结快照" : "查看冻结快照"}
      </button>
      {showSnapshot && (
        <section className="projection-snapshot">
          <h3>冻结 Alert 快照</h3>
          {snapshot.isPending && <p>正在读取快照…</p>}
          {snapshot.error && <p role="alert">{snapshot.error.message}</p>}
          {snapshot.data &&
            !snapshot.error &&
            (snapshot.data.revision !== row.revision ||
            snapshot.data.content_hash !== row.content_hash ||
            snapshot.data.alert.alert_id !== row.alert_id ? (
              <p role="alert">快照与任务身份不一致。</p>
            ) : (
              <JsonViewer value={snapshot.data.alert} />
            ))}
        </section>
      )}
    </>
  );
}
function ProjectionRetryPanel({ row }: { row: ProjectionTask }) {
  const client = useQueryClient(),
    key =
      "linkd:projection-retry:" +
      encodeURIComponent(row.bk_tenant_id) +
      ":" +
      row.id;
  const [command, setCommand] = useState<ProjectionRetryCommand | undefined>(
      () => {
        try {
          const c = projectionRetryCommand.safeParse(
            JSON.parse(sessionStorage.getItem(key) ?? "null"),
          );
          return c.success && c.data.bk_tenant_id === row.bk_tenant_id
            ? c.data
            : undefined;
        } catch {
          return undefined;
        }
      },
    ),
    [reason, setReason] = useState(command?.reason ?? ""),
    [sending, setSending] = useState(false),
    [error, setError] = useState(""),
    [accepted, setAccepted] = useState(false);
  async function retry() {
    setSending(true);
    setError("");
    try {
      const c =
        command ??
        projectionRetryCommand.parse({
          bk_tenant_id: row.bk_tenant_id,
          expected_version: row.task_version,
          operation_id: crypto.randomUUID(),
          reason,
        });
      sessionStorage.setItem(key, JSON.stringify(c));
      setCommand(c);
      const response = await fetch(
          consoleURL("/local-api/projection-tasks/" + row.id + "/retry"),
          {
            method: "POST",
            headers: { "Content-Type": "application/json" },
            body: JSON.stringify(c),
            signal: AbortSignal.timeout(20000),
          },
        ),
        raw = (await response.json()) as unknown;
      if (!response.ok) {
        if ([400, 401, 403, 404, 409].includes(response.status)) {
          sessionStorage.removeItem(key);
          setCommand(undefined);
          void client.invalidateQueries({
            queryKey: ["projection-task", row.bk_tenant_id, row.id],
          });
        }
        throw new Error(
          (raw as { error?: { message?: string } }).error?.message ??
            "恢复结果尚未确认，请重试同一操作。",
        );
      }
      const next = projectionTask.parse(raw),
        audit = next.progress.last_retry;
      if (
        next.id !== row.id ||
        next.bk_tenant_id !== row.bk_tenant_id ||
        !audit ||
        Object.entries(c).some(
          ([k, v]) => audit.command[k as keyof typeof audit.command] !== v,
        )
      )
        throw new Error("返回命令不一致，请重试同一操作。");
      sessionStorage.removeItem(key);
      setCommand(undefined);
      setAccepted(true);
      client.setQueryData(["projection-task", row.bk_tenant_id, row.id], next);
      void client.invalidateQueries({ queryKey: ["projection-tasks"] });
    } catch (err) {
      setError(
        err instanceof Error
          ? err.message
          : "恢复结果尚未确认，请重试同一操作。",
      );
    } finally {
      setSending(false);
    }
  }
  return (
    <section className="projection-retry" aria-label="恢复投影任务">
      <h3>人工恢复</h3>
      <p>
        恢复失败任务的新一轮自动尝试，保留原快照、业务来源版本和累计次数；不会直接发送，也不会重新匹配告警策略。
      </p>
      {accepted && (
        <p role="status">
          恢复操作已被接受。当前任务进度见上方，接受不代表已经同步。
        </p>
      )}
      {row.progress.state === "failed" || command ? (
        <form
          onSubmit={(e) => {
            e.preventDefault();
            void retry();
          }}
        >
          <label>
            恢复原因
            <textarea
              required
              value={reason}
              disabled={sending || Boolean(command)}
              onChange={(e) => setReason(e.target.value)}
            />
          </label>
          {command && (
            <p>
              待确认操作：{command.operation_id}。保留原 CAS 版本和原因重试。
            </p>
          )}
          <button disabled={sending || (!command && !reason.trim())}>
            {sending
              ? "正在提交…"
              : command
                ? "重试同一恢复操作"
                : "恢复原任务"}
          </button>
        </form>
      ) : (
        <p>仅失败任务可人工恢复；当前状态由投递器继续推进。</p>
      )}
      {error && <p role="alert">{error}</p>}
    </section>
  );
}
