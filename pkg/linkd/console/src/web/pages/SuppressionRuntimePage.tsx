import { SuppressionCheckPanel } from "./SuppressionCheckPanel";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { type FormEvent, useState } from "react";
import { Link, useSearchParams } from "react-router-dom";
import {
  suppressionKind,
  suppressionKey,
  suppressionQuery,
  type SuppressionWindow,
} from "../../shared/suppression-runtime";
import {
  listSuppressionRuntime,
  getSuppressionRuntime,
  getSuppressionMembers,
} from "../api";
import { JsonViewer } from "../components/JsonViewer";
import { formatTime, useTimeMode } from "../time";
import "./policies.css";
import "./merge-runtime.css";
import "./suppression-runtime.css";

const kinds = { clip: "防抖", aggregation: "关联聚合" };
function entity(tenant: string, id: string, kind = "alerts") {
  return (
    "/explore/" +
    kind +
    "?" +
    new URLSearchParams({
      bk_tenant_id: tenant,
      detail_tenant: tenant,
      detail: id,
    })
  );
}
function state(v: SuppressionWindow) {
  if (v.kind === "clip") return "计数记录";
  if (v.observed_at_ms > (v.expires_at_ms ?? 0)) return "窗口已到期";
  if (v.state === "pending")
    return v.observed_at_ms > (v.pending_until_ms ?? 0)
      ? "候选占位已到期"
      : "候选待放行确认";
  return "主告警已登记";
}
export function SuppressionRuntimePage() {
  const client = useQueryClient();
  const [params, setParams] = useSearchParams(),
    [error, setError] = useState(""),
    [revision, setRevision] = useState(0),
    [refreshing, setRefreshing] = useState(false);
  const tenant = params.get("bk_tenant_id") ?? "",
    id = params.get("id") ?? "",
    kind = suppressionKind.safeParse(params.get("kind") ?? "clip"),
    mode = useTimeMode();
  const query = suppressionQuery.safeParse({
    bk_tenant_id: tenant,
    after: params.get("after") ?? "",
    limit: 4,
    ...Object.fromEntries(
      ["policy_id", "event_source_id", "owner_alert_id"]
        .filter((k) => params.get(k))
        .map((k) => [k, params.get(k)]),
    ),
  });
  const valid = query.success && kind.success,
    detailValid =
      valid && suppressionKey.safeParse({ kind: kind.data, id }).success;
  const list = useQuery({
    queryKey: ["suppression-runtime", params.toString()],
    queryFn: ({ signal }) => {
      if (!query.success || !kind.success)
        throw new Error("请填写合法查询范围");
      return listSuppressionRuntime(kind.data, query.data, signal);
    },
    enabled: valid,
  });
  const detail = useQuery({
    queryKey: ["suppression-detail", tenant, kind.data, id],
    queryFn: ({ signal }) => {
      if (!kind.success) throw new Error("方式不合法");
      return getSuppressionRuntime(tenant, kind.data, id, signal);
    },
    enabled: detailValid,
  });
  function search(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    const data = new FormData(event.currentTarget),
      values = Object.fromEntries(
        [...data].map(([k, v]) => [k, String(v).trim()]),
      );
    const k = suppressionKind.safeParse(values.kind),
      q = suppressionQuery.safeParse({
        bk_tenant_id: values.bk_tenant_id,
        ...Object.fromEntries(
          ["policy_id", "event_source_id", "owner_alert_id"]
            .filter((k) => values[k])
            .map((k) => [k, values[k]]),
        ),
      });
    if (
      !k.success ||
      !q.success ||
      (values.id &&
        !suppressionKey.safeParse({ kind: k.data, id: values.id }).success)
    ) {
      setError("请检查租户、方式和运行身份");
      return;
    }
    setError("");
    setParams({ ...values, after: "" });
    setRevision((x) => x + 1);
  }
  async function refresh() {
    setRefreshing(true);
    try {
      const firstPage = new URLSearchParams(params);
      firstPage.delete("after");
      // 返回首页时其缓存可能仍在 fresh 周期内；显式置旧，不能把切换游标当成刷新证据。
      await client.invalidateQueries({
        queryKey: ["suppression-runtime", firstPage.toString()],
        exact: true,
        refetchType: "none",
      });
      if (params.get("after")) {
        setParams(firstPage);
      } else await list.refetch();
      if (detailValid) await detail.refetch();
      await client.invalidateQueries({
        queryKey: ["suppression-members", tenant, kind.data, id],
        refetchType: "none",
      });
      await client.invalidateQueries({
        queryKey: ["suppression-check-history", tenant, kind.data, id],
        refetchType: "none",
      });
      setRevision((x) => x + 1);
    } finally {
      setRefreshing(false);
    }
  }
  const time = (v: number) => formatTime(new Date(v).toISOString(), mode);
  return (
    <div className="policies-page suppression-runtime-page">
      <header className="page-header">
        <div>
          <h1>抑制运行态</h1>
          <p>查看防抖计数、跨来源聚合窗口及当前保留的成员。</p>
          {valid && (
            <Link
              to={
                "/suppression-cleanups?" +
                new URLSearchParams({
                  bk_tenant_id: tenant,
                  ...(query.data.event_source_id
                    ? { event_source_id: query.data.event_source_id }
                    : {}),
                  ...(query.data.owner_alert_id
                    ? { alert_id: query.data.owner_alert_id }
                    : {}),
                })
              }
            >
              查看终态清理历史
            </Link>
          )}
        </div>
        <button
          disabled={
            !valid || refreshing || list.isFetching || detail.isFetching
          }
          onClick={() => void refresh()}
        >
          刷新
        </button>
      </header>
      <form className="merge-filters" key={params.toString()} onSubmit={search}>
        <label>
          租户
          <input name="bk_tenant_id" defaultValue={tenant} required />
        </label>
        <label>
          方式
          <select name="kind" defaultValue={kind.data ?? "clip"}>
            {Object.entries(kinds).map(([k, v]) => (
              <option key={k} value={k}>
                {v}
              </option>
            ))}
          </select>
        </label>
        <label>
          策略 ID
          <input
            name="policy_id"
            defaultValue={params.get("policy_id") ?? ""}
          />
        </label>
        <label>
          防抖 / 聚合主来源
          <input
            name="event_source_id"
            defaultValue={params.get("event_source_id") ?? ""}
          />
        </label>
        <label>
          登记主 Alert ID
          <input
            name="owner_alert_id"
            defaultValue={params.get("owner_alert_id") ?? ""}
          />
        </label>
        <label>
          精确运行 ID
          <input name="id" defaultValue={id} />
        </label>
        <button>查询</button>
      </form>
      {error && <p role="alert">{error}</p>}
      <p className="merge-note">
        查询不会计数、抢占窗口或延长保留时间。缓存可能丢失或到期，空列表不否定
        Event 的历史裁决；主告警是否仍活动、能否处置，应查看对应
        Alert。分页筛选可能返回空页。
      </p>
      {!valid ? (
        <p>请填写合法租户后查询。</p>
      ) : (
        <>
          {list.isPending && <p>正在读取抑制状态…</p>}
          {list.error && (
            <p role="alert">{list.error.message}；游标失效时请刷新回到首页。</p>
          )}
          {list.data && !list.error && (
            <>
              <table className="merge-table" aria-label="当前抑制窗口">
                <thead>
                  <tr>
                    <th>运行身份 / 策略</th>
                    <th>观察到的状态</th>
                    <th>计数 / 主告警</th>
                    <th>观察时间</th>
                    <th>操作</th>
                  </tr>
                </thead>
                <tbody>
                  {list.data.items.map((v) => (
                    <tr key={v.id}>
                      <td>
                        <code>{v.id.slice(0, 16)}…</code>
                        <div>
                          {v.policy.id} / {v.policy.version}
                        </div>
                      </td>
                      <td>{state(v)}</td>
                      <td>
                        {v.kind === "clip" ? (
                          <span>
                            {v.observed_count} / {v.threshold} 次
                          </span>
                        ) : (
                          <Link to={entity(tenant, v.owner_alert_id!)}>
                            {v.owner_alert_id}
                          </Link>
                        )}
                        <div>保留成员：{v.member_count}</div>
                      </td>
                      <td>{time(v.observed_at_ms)}</td>
                      <td>
                        <button
                          onClick={() => {
                            const next = new URLSearchParams(params);
                            next.set("id", v.id);
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
              {!list.data.items.length && <p>本页没有符合条件的窗口。</p>}
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
                  disabled={!list.data.next}
                  onClick={() => {
                    const next = new URLSearchParams(params);
                    next.set("after", list.data!.next);
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
      {id && !detailValid && <p role="alert">运行 ID 与所选方式不匹配。</p>}
      {detailValid && (
        <section className="merge-detail" aria-label="抑制窗口详情">
          {detail.isPending && <p>正在读取窗口…</p>}
          {detail.error && (
            <p role="alert">
              {detail.error.message}；记录可能已到期或清理，历史请查看 Event。
            </p>
          )}
          {detail.data && !detail.error && (
            <>
              <Window value={detail.data} />
              <Members
                key={`${tenant}:${kind.data}:${id}:${detail.data.epoch}:${revision}`}
                value={detail.data}
              />
            </>
          )}
          <SuppressionCheckPanel
            key={tenant + ":" + kind.data + ":" + id + ":" + revision}
            tenant={tenant}
            kind={kind.data!}
            id={id}
            observed={detail.error ? undefined : detail.data}
          />
        </section>
      )}
    </div>
  );
}
function Window({ value: v }: { value: SuppressionWindow }) {
  const mode = useTimeMode(),
    time = (n: number) => formatTime(new Date(n).toISOString(), mode);
  return (
    <>
      <h2>{kinds[v.kind]}详情</h2>
      <p className="suppression-id">{v.id}</p>
      <p>
        <Link
          to={
            "/policies?" +
            new URLSearchParams({
              bk_tenant_id: v.bk_tenant_id,
              type: "suppression",
              id: v.policy.id,
              version: String(v.policy.version),
            })
          }
        >
          策略 {v.policy.id} / {v.policy.version} ↗
        </Link>
      </p>
      <dl className="explorer-facts">
        <div>
          <dt>状态</dt>
          <dd>{state(v)}</dd>
        </div>
        <div>
          <dt>观察时间</dt>
          <dd>{time(v.observed_at_ms)}</dd>
        </div>
        <div>
          <dt>代次</dt>
          <dd>
            <Link to={entity(v.bk_tenant_id, v.epoch, "events")}>
              {v.epoch}
            </Link>
          </dd>
        </div>
        <div>
          <dt>记录剩余保留</dt>
          <dd>约 {Math.ceil(v.retention_ms / 1000)} 秒</dd>
        </div>
      </dl>
      {v.kind === "clip" ? (
        <p>
          来源：{v.event_source_id} · 指纹：{v.fingerprint}
          <br />
          观察时计数：{v.observed_count} / {v.threshold} 次；滑动区间{" "}
          {time(v.observed_at_ms - v.duration_seconds * 1000)} 至{" "}
          {time(v.observed_at_ms)}（含边界）。
        </p>
      ) : (
        <p className="suppression-id">
          分组键：{v.group_key}
          <br />
          固定窗口：{time(v.started_at_ms ?? 0)} 至 {time(v.expires_at_ms!)}
          <br />
          {v.state === "pending" ? "候选" : "登记主"}：
          <Link to={entity(v.bk_tenant_id, v.owner_alert_id!)}>
            {v.owner_alert_id}
          </Link>{" "}
          · 来源：{v.owner_source_id}
        </p>
      )}
      {v.kind === "clip" && v.owner_alert_id && (
        <p>
          登记主：
          <Link to={entity(v.bk_tenant_id, v.owner_alert_id)}>
            {v.owner_alert_id}
          </Link>
        </p>
      )}
      <details>
        <summary>完整运行快照</summary>
        <JsonViewer value={v} />
      </details>
    </>
  );
}
function Members({ value: v }: { value: SuppressionWindow }) {
  const [after, setAfter] = useState(""),
    mode = useTimeMode();
  const query = useQuery({
    queryKey: [
      "suppression-members",
      v.bk_tenant_id,
      v.kind,
      v.id,
      v.epoch,
      after,
    ],
    queryFn: ({ signal }) =>
      getSuppressionMembers(
        v.bk_tenant_id,
        v.kind,
        v.id,
        v.epoch,
        after,
        signal,
      ),
  });
  return (
    <section aria-label="当前保留成员">
      <h3>当前保留成员</h3>
      <p>
        仅显示 Redis 当前保存的 Event
        身份，不代表完整历史。换代或游标失效后请刷新详情。
      </p>
      {query.isPending && <p>正在读取成员…</p>}
      {query.error && <p role="alert">{query.error.message}</p>}
      {query.data && !query.error && (
        <>
          <table className="merge-table">
            <thead>
              <tr>
                <th>Event</th>
                <th>来源 / 指纹</th>
                <th>首次处理时间</th>
              </tr>
            </thead>
            <tbody>
              {query.data.items.map((m) => (
                <tr key={m.event_id}>
                  <td className="suppression-id">
                    <Link to={entity(v.bk_tenant_id, m.event_id, "events")}>
                      {m.event_id}
                    </Link>
                  </td>
                  <td>
                    {m.event_source_id}
                    <br />
                    {m.fingerprint}
                  </td>
                  <td>{formatTime(new Date(m.at_ms).toISOString(), mode)}</td>
                </tr>
              ))}
            </tbody>
          </table>
          {!query.data.items.length && <p>本页没有保留成员。</p>}
          <div className="merge-pagination">
            <button disabled={!after} onClick={() => setAfter("")}>
              成员首页
            </button>
            <button
              disabled={!query.data.next}
              onClick={() => setAfter(query.data!.next)}
            >
              下一页成员
            </button>
          </div>
        </>
      )}
    </section>
  );
}
