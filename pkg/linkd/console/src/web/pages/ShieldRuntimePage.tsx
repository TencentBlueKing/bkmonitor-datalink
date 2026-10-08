import { ShieldCheckPanel } from "./ShieldCheckPanel";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { type FormEvent, useState } from "react";
import { Link, useSearchParams } from "react-router-dom";
import {
  shieldQuery,
  type ShieldBinding,
  type ShieldRecord,
} from "../../shared/shield-runtime";
import { getShieldHistory, getShieldRuntime, listShieldRuntime } from "../api";
import { JsonViewer } from "../components/JsonViewer";
import { formatTime, useTimeMode } from "../time";
import "./policies.css";
import "./merge-runtime.css";
import "./shield-runtime.css";

const types = {
  time_shield: "时间屏蔽",
  custom_shield: "自定义依赖",
  cmdb_shield: "CMDB 依赖",
};
const kind = (b: ShieldBinding) =>
  types[b.type === "time_shield" ? b.type : (b.mode ?? "custom_shield")];
function entity(tenant: string, id: string, type = "alerts") {
  return (
    "/explore/" +
    type +
    "?" +
    new URLSearchParams({
      bk_tenant_id: tenant,
      detail_tenant: tenant,
      detail: id,
    })
  );
}
export function ShieldRuntimePage() {
  const [params, setParams] = useSearchParams(),
    client = useQueryClient();
  const tenant = params.get("bk_tenant_id") ?? "",
    id = params.get("id") ?? "";
  const query = shieldQuery.safeParse({
    bk_tenant_id: tenant,
    after: params.get("after") ?? "",
    limit: 4,
    ...Object.fromEntries(
      ["policy_id", "main_alert_id", "binding_type"]
        .filter((k) => params.get(k))
        .map((k) => [k, params.get(k)]),
    ),
  });
  const [error, setError] = useState(""),
    [refreshing, setRefreshing] = useState(false);
  const [historyEpoch, setHistoryEpoch] = useState(0);
  const mode = useTimeMode();
  const list = useQuery({
    queryKey: ["shield-runtime", params.toString()],
    queryFn: ({ signal }) => {
      if (!query.success) throw new Error("请填写合法租户");
      return listShieldRuntime(query.data, signal);
    },
    enabled: query.success,
  });
  const detail = useQuery({
    queryKey: ["shield-detail", tenant, id],
    queryFn: ({ signal }) => getShieldRuntime(tenant, id, signal),
    enabled:
      /^[a-zA-Z0-9_-]{1,64}$/.test(tenant) && id.length > 0 && id.length <= 160,
  });
  function search(e: FormEvent<HTMLFormElement>) {
    e.preventDefault();
    const data = new FormData(e.currentTarget);
    const input = {
      bk_tenant_id: String(data.get("tenant") ?? "").trim(),
      after: "",
      limit: 4,
      ...Object.fromEntries(
        ["policy_id", "main_alert_id", "binding_type"]
          .filter((k) => data.get(k))
          .map((k) => [k, String(data.get(k)).trim()]),
      ),
    };
    const parsed = shieldQuery.safeParse(input),
      selected = String(data.get("id") ?? "").trim();
    if (!parsed.success || selected.length > 160) {
      setError("请检查租户和查询范围");
      return;
    }
    setError("");
    setParams({
      ...Object.fromEntries(
        Object.entries(parsed.data).map(([k, v]) => [k, String(v)]),
      ),
      ...(selected ? { id: selected } : {}),
    });
  }
  return (
    <div className="policies-page shield-runtime-page">
      <header className="page-header">
        <div>
          <h1>屏蔽运行态</h1>
          <p>当前绑定、计划复查和历史变更，保留真实生命周期。</p>
        </div>
        <button
          disabled={
            !query.success || refreshing || list.isFetching || detail.isFetching
          }
          onClick={async () => {
            setRefreshing(true);
            try {
              await Promise.all([
                list.refetch(),
                ...(id ? [detail.refetch()] : []),
              ]);
              if (id)
                await Promise.all(
                  [
                    "shield-history",
                    "shield-check",
                    "shield-requests",
                    "shield-request",
                  ].map((name) =>
                    client.invalidateQueries({ queryKey: [name, tenant, id] }),
                  ),
                );
              setHistoryEpoch((value) => value + 1);
            } finally {
              setRefreshing(false);
            }
          }}
        >
          刷新
        </button>
      </header>
      <form className="merge-filters" key={params.toString()} onSubmit={search}>
        <label>
          租户
          <input name="tenant" defaultValue={tenant} required />
        </label>
        <label>
          策略 ID
          <input
            name="policy_id"
            defaultValue={params.get("policy_id") ?? ""}
          />
        </label>
        <label>
          依赖主 Alert ID
          <input
            name="main_alert_id"
            defaultValue={params.get("main_alert_id") ?? ""}
          />
        </label>
        <label>
          屏蔽类型
          <select
            name="binding_type"
            defaultValue={params.get("binding_type") ?? ""}
          >
            <option value="">全部类型</option>
            {Object.entries(types).map(([v, text]) => (
              <option key={v} value={v}>
                {text}
              </option>
            ))}
          </select>
        </label>
        <label>
          精确 Alert ID
          <input name="id" defaultValue={id} />
        </label>
        <button>查询</button>
      </form>
      {error && <p role="alert">{error}</p>}
      <p className="merge-note">
        列表展示当前屏蔽及状态输出待办，包括尚未到复查时间的绑定。解除不等于处置放行；已解除历史请按
        Alert ID 查看。筛选可能产生空页，仍可继续翻页。
      </p>
      {!query.success ? (
        <p>请填写租户后查询。</p>
      ) : (
        <>
          {list.isPending && <p>正在读取屏蔽状态…</p>}
          {list.error && <p role="alert">{list.error.message}</p>}
          {list.data && !list.error && (
            <>
              <table className="merge-table" aria-label="当前屏蔽关系">
                <thead>
                  <tr>
                    <th>Alert / 来源</th>
                    <th>生命周期与屏蔽</th>
                    <th>计划复查</th>
                    <th>操作</th>
                  </tr>
                </thead>
                <tbody>
                  {list.data.items.map((row) => (
                    <tr key={row.alert_id}>
                      <td>
                        <Link to={entity(tenant, row.alert_id)}>
                          {row.alert_id}
                        </Link>
                        <span>{row.event_source_id}</span>
                      </td>
                      <td>
                        <strong>
                          {row.shield.active
                            ? `屏蔽中 · ${row.shield.bindings.length} 条绑定`
                            : "屏蔽已解除"}
                        </strong>
                        <p>
                          {row.status} / {row.severity} · r{row.revision}
                        </p>
                        {row.policy_change && <p>状态输出待完成</p>}
                      </td>
                      <td>
                        {row.policy_change
                          ? "等待补齐状态输出"
                          : row.shield.next_check_at
                            ? formatTime(row.shield.next_check_at, mode)
                            : "当前无绑定"}
                      </td>
                      <td>
                        <button
                          onClick={() => {
                            const next = new URLSearchParams(params);
                            next.set("id", row.alert_id);
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
              {list.data.items.length === 0 && <p>本页没有匹配记录。</p>}
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
          <h2>Alert 屏蔽详情</h2>
          <code>{id}</code>
          {detail.isPending && <p>正在读取详情…</p>}
          {detail.error && <p role="alert">{detail.error.message}</p>}
          {detail.data && !detail.error && (
            <ShieldDetail
              key={tenant + ":" + id + ":" + historyEpoch}
              row={detail.data}
            />
          )}
        </section>
      )}
    </div>
  );
}
function ShieldDetail({ row }: { row: ShieldRecord }) {
  const [after, setAfter] = useState("");
  const mode = useTimeMode();
  const history = useQuery({
    queryKey: ["shield-history", row.bk_tenant_id, row.alert_id, after],
    queryFn: ({ signal }) =>
      getShieldHistory(row.bk_tenant_id, row.alert_id, after, signal),
  });
  return (
    <>
      <div className="merge-detail-summary">
        <strong>{row.shield.active ? "屏蔽中" : "当前未屏蔽"}</strong>
        <span>
          生命周期 {row.status} · {row.severity} · r{row.revision}
        </span>
        <Link to={entity(row.bk_tenant_id, row.alert_id)}>查看完整 Alert</Link>
      </div>
      <p>
        {row.admission.admitted_at
          ? `最近放行：${formatTime(row.admission.admitted_at, mode)} / ${row.admission.severity}。这是历史资格。`
          : "本生命周期尚未放行处置。解除屏蔽后仍需等待下一条触发 Event。"}
      </p>
      <h3>当前绑定</h3>
      {row.shield.bindings.length === 0 ? (
        <p>当前没有活动屏蔽绑定。</p>
      ) : (
        row.shield.bindings.map((b) => (
          <article className="shield-binding" key={b.binding_id}>
            <header>
              <strong>{kind(b)}</strong>
              <Link
                to={
                  "/policies?" +
                  new URLSearchParams({
                    bk_tenant_id: row.bk_tenant_id,
                    type: "shield",
                    id: b.policy.id,
                    version: String(b.policy.version),
                  })
                }
              >
                {b.policy.id} · v{b.policy.version}
              </Link>
            </header>
            <p>{b.reason || "未配置说明"}</p>
            <dl>
              <dt>绑定时间</dt>
              <dd>{formatTime(b.bound_at, mode)}</dd>
              <dt>绑定等级</dt>
              <dd>{b.severity}</dd>
              <dt>来源 Event</dt>
              <dd>
                <Link
                  to={entity(row.bk_tenant_id, b.source_event_id, "events")}
                >
                  {b.source_event_id}
                </Link>
                {b.origin === "manual" && (
                  <p>
                    快捷屏蔽 · {b.operator_id} · 操作 {b.operation_id}
                  </p>
                )}
              </dd>
              {b.main_alert_id && (
                <>
                  <dt>固定依赖主</dt>
                  <dd>
                    <Link to={entity(row.bk_tenant_id, b.main_alert_id)}>
                      {b.main_alert_id}
                    </Link>
                  </dd>
                </>
              )}
            </dl>
            <details>
              <summary>绑定快照与身份</summary>
              <JsonViewer value={b} />
            </details>
          </article>
        ))
      )}
      {row.policy_change && (
        <details className="shield-pending">
          <summary>状态输出待完成</summary>
          <p>
            业务状态已经保存，后台将继续补齐流水和状态输出；这不会补发处置。
          </p>
          <JsonViewer value={row.policy_change} />
        </details>
      )}
      <ShieldCheckPanel key={row.bk_tenant_id + ":" + row.alert_id} row={row} />
      <h3>屏蔽变更历史</h3>
      <p>
        只显示实际屏蔽与解除流水，保留变更前后绑定；新写入记录可能需刷新后可见。
      </p>
      {history.isPending && <p>正在读取历史…</p>}
      {history.error && (
        <p role="alert">
          {history.error.message}
          <button
            onClick={() => {
              if (after) setAfter("");
              else void history.refetch();
            }}
          >
            重新读取历史首页
          </button>
        </p>
      )}
      {history.data && !history.error && (
        <>
          <table className="merge-table" aria-label="屏蔽变更历史">
            <thead>
              <tr>
                <th>时间 / 操作</th>
                <th>记录详情</th>
              </tr>
            </thead>
            <tbody>
              {history.data.items.map((log) => (
                <tr key={log.log_id}>
                  <td>
                    {formatTime(log.created_time, mode)}
                    <p>
                      {log.operation_kind === "unshield"
                        ? "解除绑定"
                        : "屏蔽关系变更"}{" "}
                      · {log.operator_kind}
                    </p>
                  </td>
                  <td>
                    <details>
                      <summary>查看前后绑定与原因</summary>
                      <JsonViewer value={log.params} />
                    </details>
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
          {history.data.items.length === 0 && (
            <p>
              本页没有屏蔽流水。
              {history.data.next ? "继续下一页查找历史。" : ""}
            </p>
          )}
          <div className="merge-pagination">
            <button disabled={!after} onClick={() => setAfter("")}>
              历史首页
            </button>
            <button
              disabled={!history.data.next || history.isFetching}
              onClick={() => setAfter(history.data.next)}
            >
              下一页历史
            </button>
          </div>
        </>
      )}
    </>
  );
}
