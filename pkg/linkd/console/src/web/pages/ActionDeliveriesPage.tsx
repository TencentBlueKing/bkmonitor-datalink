import { DeliveryMetricsPanel } from "./DeliveryMetricsPanel";
import { useState, type FormEvent } from "react";
import { Link, useSearchParams } from "react-router-dom";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import {
  actionQuery,
  actionRetryCommand,
  actionDelivery,
  type ActionDelivery,
  type ActionRetryCommand,
} from "../../shared/action-deliveries";
import {
  getActionSnapshot,
  getActionOrder,
  getActionDelivery,
  listActionDeliveries,
} from "../api";
import { consoleURL } from "../base-path";
import { JsonViewer } from "../components/JsonViewer";
import { formatTime, useTimeMode } from "../time";
import "./policies.css";
import "./merge-runtime.css";
import "./projection-tasks.css";
const states = {
  pending: "已入队，待执行",
  waiting_projection: "等待投影可见",
  sending: "尝试中",
  retry: "等待自动重试",
  succeeded: "接收端已受理",
  skipped: "已跳过旧触发",
  failed: "失败，保留顺序屏障",
};
const actions = { firing: "触发", resolved: "恢复", close: "关闭" };
function alertLink(row: ActionDelivery) {
  return (
    "/explore/alerts?" +
    new URLSearchParams({
      bk_tenant_id: row.bk_tenant_id,
      detail_tenant: row.bk_tenant_id,
      detail: row.alert_id,
    })
  );
}
export function ActionDeliveriesPage() {
  const [params, setParams] = useSearchParams(),
    client = useQueryClient(),
    [error, setError] = useState(""),
    [refreshing, setRefreshing] = useState(false),
    [showMetrics, setShowMetrics] = useState(false),
    mode = useTimeMode();
  const tenant = params.get("bk_tenant_id") ?? "",
    id = params.get("id") ?? "";
  const parsed = actionQuery.safeParse({
    bk_tenant_id: tenant,
    after: params.get("after") ?? "",
    limit: 4,
    ...Object.fromEntries(
      ["alert_id", "target_id", "source_id", "state", "action"]
        .filter((k) => params.get(k))
        .map((k) => [k, params.get(k)]),
    ),
  });
  const list = useQuery({
    queryKey: ["action-deliveries", params.toString()],
    queryFn: ({ signal }) => {
      if (!parsed.success) throw new Error("请填写合法租户");
      return listActionDeliveries(parsed.data, signal);
    },
    enabled: parsed.success,
  });
  const detail = useQuery({
    queryKey: ["action-delivery", tenant, id],
    queryFn: ({ signal }) => getActionDelivery(tenant, id, signal),
    enabled: /^[a-zA-Z0-9_-]{1,64}$/.test(tenant) && /^[a-f0-9]{64}$/.test(id),
    refetchInterval: (q) =>
      q.state.data &&
      ["pending", "waiting_projection", "sending", "retry"].includes(
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
          ["alert_id", "target_id", "source_id", "state", "action"]
            .filter((k) => data.get(k))
            .map((k) => [k, String(data.get(k)).trim()]),
        ),
      },
      q = actionQuery.safeParse(input),
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
          <h1>告警动作投递</h1>
          <p>查看获准动作、冻结请求、投影等待和受理结果。</p>
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
                queryKey: ["action-deliveries"],
                refetchType: "none",
              });
              if (params.get("after")) setParams(first);
              else await list.refetch();
              if (id) {
                await detail.refetch();
                await client.invalidateQueries({
                  queryKey: ["action-order", tenant, id],
                });
              }
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
        接收端已受理，更不代表处置已执行完成。现有 KAC Kafka Hook
        的发送不在此列表中。
      </p>
      <button
        type="button"
        aria-expanded={showMetrics}
        onClick={() => setShowMetrics((v) => !v)}
      >
        {showMetrics ? "收起动作运行观测" : "查看动作运行观测"}
      </button>
      {showMetrics && <DeliveryMetricsPanel kind="action" />}
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
          动作类型
          <select name="action" defaultValue={params.get("action") ?? ""}>
            <option value="">全部动作</option>
            {Object.entries(actions).map(([v, text]) => (
              <option key={v} value={v}>
                {text}
              </option>
            ))}
          </select>
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
              <table className="merge-table" aria-label="动作投递列表">
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
                          {actions[row.action]} · 快照 r{row.revision} ·{" "}
                          {row.alert_status}
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
          <h2>动作投递详情</h2>
          <code>{id}</code>
          {!/^[a-f0-9]{64}$/.test(id) ? (
            <p role="alert">任务 ID 不合法。</p>
          ) : (
            detail.isLoading && <p>正在读取详情…</p>
          )}
          {detail.error && <p role="alert">{detail.error.message}</p>}
          {detail.data && !detail.error && (
            <ActionDetail key={tenant + ":" + id} row={detail.data} />
          )}
        </section>
      )}
    </div>
  );
}
function ActionDetail({ row }: { row: ActionDelivery }) {
  const mode = useTimeMode(),
    [showSnapshot, setShowSnapshot] = useState(false),
    p = row.progress,
    receipt = p.receipt;
  const snapshot = useQuery({
    queryKey: ["action-snapshot", row.bk_tenant_id, row.id],
    queryFn: ({ signal }) =>
      getActionSnapshot(row.bk_tenant_id, row.id, signal),
    enabled: showSnapshot,
  });
  return (
    <>
      <div className="merge-detail-summary">
        <strong>{states[p.state]}</strong>
        <span>
          {actions[row.action]} · 任务快照 r{row.revision} · {row.alert_status}
        </span>
        <Link to={alertLink(row)}>查看当前 Alert</Link>
      </div>
      <dl className="projection-facts">
        <dt>动作类型</dt>
        <dd>{actions[row.action]}</dd>
        <dt>原动作原因</dt>
        <dd>
          {row.cause.type} · <code>{row.cause.id}</code>
        </dd>
        <dt>动作 ID</dt>
        <dd>
          <code>{row.action_id}</code>
        </dd>
        <dt>完整请求摘要</dt>
        <dd>
          <code>{row.request_hash}</code>
        </dd>
        <dt>处置目标</dt>
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
          {p.state === "waiting_projection" || p.state === "skipped"
            ? "状态原因："
            : "安全错误码："}
          <code>{p.error_code}</code>
          {p.state === "failed"
            ? "。请先处理故障，再恢复原任务。"
            : p.state === "retry"
              ? "。后续按原任务预算继续尝试。"
              : "。"}
        </p>
      )}
      <p>
        请求固定于原获准版本；当前 Alert
        可能已有更新。受理确认只证明可靠排入处置待办，通知、工单或自动处置的完成情况应在接收端查看。
      </p>
      {p.previous_unconfirmed && (
        <p className="merge-note">
          此前尝试结果未确认。当前跳过不代表此前从未受理；请结合受理记录核对。
        </p>
      )}
      <ActionOrder row={row} />
      <ActionLogLocator row={row} />
      {p.projection && (
        <section className="projection-receipt">
          <h3>投影可见性依据</h3>
          <p>
            已应用 r{p.projection.applied_revision} ·{" "}
            {p.projection.applied_status} · 已确认可搜索
          </p>
          <p>
            文档定位：<code>{p.projection.document_ref}</code>
          </p>
        </section>
      )}
      {receipt && (
        <section className="projection-receipt">
          <h3>动作受理确认</h3>
          <p>
            {receipt.outcome === "accepted"
              ? "接收端已持久受理"
              : "接收端已确认跳过"}{" "}
            · r{receipt.applied_revision} · {receipt.applied_status}
          </p>
          <p>
            受理引用：<code>{receipt.acceptance_id}</code>
          </p>
          {receipt.reason && <code>{receipt.reason}</code>}
          <p>这不是处置执行完成的确认。</p>
        </section>
      )}
      {p.state === "skipped" && !receipt && (
        <p>本次依据较新终态投影在本地跳过，未取得新的动作受理确认。</p>
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
      <ActionRetryPanel row={row} />
      <button onClick={() => setShowSnapshot((v) => !v)}>
        {showSnapshot ? "收起冻结快照" : "查看冻结快照"}
      </button>
      {showSnapshot && (
        <section className="projection-snapshot">
          <h3>冻结动作请求</h3>
          {snapshot.isPending && <p>正在读取快照…</p>}
          {snapshot.error && <p role="alert">{snapshot.error.message}</p>}
          {snapshot.data &&
            !snapshot.error &&
            (snapshot.data.revision !== row.revision ||
            snapshot.data.request_hash !== row.request_hash ||
            snapshot.data.request.linkd_alert_id !== row.alert_id ||
            snapshot.data.request.action_id !== row.action_id ||
            snapshot.data.request.target_id !== row.target_id ||
            snapshot.data.request.action !== row.action ||
            snapshot.data.request.content_hash !== row.content_hash ||
            snapshot.data.request.cause.type !== row.cause.type ||
            snapshot.data.request.cause.id !== row.cause.id ? (
              <p role="alert">快照与任务身份不一致。</p>
            ) : (
              <JsonViewer value={snapshot.data.request} />
            ))}
        </section>
      )}
    </>
  );
}
function ActionLogLocator({ row }: { row: ActionDelivery }) {
  const to = new Date(row.progress.updated_at),
    from = new Date(to.getTime() - 3600_000);
  const logs =
    "/explore/alert-logs?" +
    new URLSearchParams({
      bk_tenant_id: row.bk_tenant_id,
      alert_id: row.alert_id,
      from: from.toISOString(),
      to: to.toISOString(),
      order: "asc",
    });
  return (
    <section className="action-log-locator" aria-label="动作日志定位">
      <h3>日志定位</h3>
      <p>
        运行失败日志位于投递进程日志中，Console
        尚未接入其检索。可使用以下字段定位；这里只列出筛选条件，不代表已查到日志。
      </p>
      <pre aria-label="发送日志定位字段">
        {JSON.stringify(
          {
            task: "action-delivery",
            bk_tenant_id: row.bk_tenant_id,
            alert_id: row.alert_id,
            action_task_id: row.id,
          },
          null,
          2,
        )}
      </pre>
      <p>
        入队补扫使用 task=action-enqueue，并按租户与 Alert ID 查找；补扫日志没有
        action_task_id。每个失败页最多记录四个样本，未找到样本不代表没有失败。前序任务阻塞时请沿队首任务继续定位。
      </p>
      <Link to={logs}>查看 Alert 操作流水（任务更新时间前 1 小时）</Link>
      <p>
        操作流水记录生命周期和 Hook
        等业务事实，不替代动作任务的受理记录或进程错误日志。
      </p>
    </section>
  );
}

function ActionRetryPanel({ row }: { row: ActionDelivery }) {
  const client = useQueryClient(),
    key =
      "linkd:action-retry:" +
      encodeURIComponent(row.bk_tenant_id) +
      ":" +
      row.id;
  const [command, setCommand] = useState<ActionRetryCommand | undefined>(() => {
      try {
        const c = actionRetryCommand.safeParse(
          JSON.parse(sessionStorage.getItem(key) ?? "null"),
        );
        return c.success && c.data.bk_tenant_id === row.bk_tenant_id
          ? c.data
          : undefined;
      } catch {
        return undefined;
      }
    }),
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
        actionRetryCommand.parse({
          bk_tenant_id: row.bk_tenant_id,
          expected_version: row.task_version,
          operation_id: crypto.randomUUID(),
          reason,
        });
      sessionStorage.setItem(key, JSON.stringify(c));
      setCommand(c);
      const response = await fetch(
          consoleURL("/local-api/action-deliveries/" + row.id + "/retry"),
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
            queryKey: ["action-delivery", row.bk_tenant_id, row.id],
          });
        }
        throw new Error(
          (raw as { error?: { message?: string } }).error?.message ??
            "恢复结果尚未确认，请重试同一操作。",
        );
      }
      const next = actionDelivery.parse(raw),
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
      client.setQueryData(["action-delivery", row.bk_tenant_id, row.id], next);
      void client.invalidateQueries({ queryKey: ["action-deliveries"] });
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
    <section className="projection-retry" aria-label="恢复动作任务">
      <h3>人工恢复</h3>
      <p>
        恢复失败任务的新一轮自动尝试，保留原快照、业务来源版本和累计次数；不会直接发送，也不会重新匹配告警策略。
      </p>
      {accepted && (
        <p role="status">
          恢复操作已被接受。当前任务进度见上方，接受不代表动作已受理或处置已完成。
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
        <p>仅失败任务可人工恢复。成功或过期跳过的动作不能重新执行。</p>
      )}
      {error && <p role="alert">{error}</p>}
    </section>
  );
}

function ActionOrder({ row }: { row: ActionDelivery }) {
  const mode = useTimeMode();
  const order = useQuery({
    queryKey: ["action-order", row.bk_tenant_id, row.id],
    queryFn: ({ signal }) => getActionOrder(row.bk_tenant_id, row.id, signal),
    refetchInterval: ["succeeded", "skipped"].includes(row.progress.state)
      ? false
      : 5000,
  });
  const value = order.data,
    head = value?.head;
  return (
    <section className="projection-receipt">
      <h3>同目标动作顺序</h3>
      {order.isPending && <p>正在查询队首…</p>}
      {order.error && <p role="alert">{order.error.message}</p>}
      {value &&
        !order.error &&
        (value.alert_id !== row.alert_id ||
        value.target_id !== row.target_id ? (
          <p role="alert">队首与当前任务作用域不一致。</p>
        ) : (
          <>
            <p>观察时间：{formatTime(value.observed_at, mode)}</p>
            {head ? (
              <>
                <p>
                  {head.id === row.id
                    ? "当前任务是可见队首。"
                    : "存在前序或其他未结清动作。"}{" "}
                  {states[head.progress.state]} · r{head.revision} ·{" "}
                  {actions[head.action]}
                </p>
                {head.id !== row.id && (
                  <Link
                    to={
                      "/action-deliveries?" +
                      new URLSearchParams({
                        bk_tenant_id: row.bk_tenant_id,
                        id: head.id,
                        alert_id: row.alert_id,
                        target_id: row.target_id,
                      })
                    }
                  >
                    查看队首任务
                  </Link>
                )}
              </>
            ) : (
              <p>暂无可见的未结清动作，索引可能尚未刷新。</p>
            )}
            <p>
              前序失败会阻塞同一
              Alert、同一目标的后续动作。队首位置不表示投影已可见，也不授权立即发送。
            </p>
          </>
        ))}
    </section>
  );
}
