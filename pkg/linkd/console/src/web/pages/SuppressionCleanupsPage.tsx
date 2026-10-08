import { useState, type FormEvent } from "react";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { Link, useSearchParams } from "react-router-dom";
import {
  cleanupPage,
  cleanupQuery,
  cleanupRecord,
  type CleanupRecord,
} from "../../shared/suppression-cleanups";
import { consoleURL } from "../base-path";
import { formatTime, useTimeMode } from "../time";
import { JsonViewer } from "../components/JsonViewer";
import "./policies.css";
import "./merge-runtime.css";
import "./suppression-runtime.css";
async function read(path: string, signal: AbortSignal) {
  const r = await fetch(consoleURL("/local-api/suppression-cleanups" + path), {
    signal,
  });
  const body = (await r.json()) as { error?: { message?: string } };
  if (!r.ok) throw new Error(body.error?.message ?? "清理历史读取失败");
  return body;
}
function result(o: NonNullable<CleanupRecord["result"]>["clip"] | undefined) {
  if (!o) return "结果尚未确认";
  if (o.state === "unavailable") return "Redis 不可用，结果未确认";
  if (o.state === "not_applicable") return "不适用";
  return o.removed === 0
    ? "本轮未发现可清理登记"
    : `本轮确认清理 ${o.removed} 项登记`;
}
function entity(r: CleanupRecord) {
  const c = r.cause;
  return (
    "/explore/" +
    (c.alert_id ? "alerts" : "events") +
    "?" +
    new URLSearchParams({
      bk_tenant_id: c.bk_tenant_id,
      detail_tenant: c.bk_tenant_id,
      detail: c.alert_id ?? c.event_id!,
    })
  );
}
export function SuppressionCleanupsPage() {
  const [params, setParams] = useSearchParams(),
    [error, setError] = useState(""),
    [refreshing, setRefreshing] = useState(false),
    client = useQueryClient(),
    mode = useTimeMode();
  const tenant = params.get("bk_tenant_id") ?? "",
    id = params.get("id") ?? "",
    fields = [
      "event_source_id",
      "fingerprint",
      "alert_id",
      "event_id",
      "state",
      "window_kind",
      "window_id",
      "epoch",
    ];
  const parsed = cleanupQuery.safeParse({
    bk_tenant_id: tenant,
    after: params.get("after") ?? "",
    ...Object.fromEntries(
      fields.filter((k) => params.get(k)).map((k) => [k, params.get(k)]),
    ),
  });
  const list = useQuery({
    queryKey: ["suppression-cleanups", params.toString()],
    enabled: parsed.success,
    queryFn: async ({ signal }) => {
      if (!parsed.success) throw new Error("查询范围不合法");
      return cleanupPage.parse(
        await read(
          "?" +
            new URLSearchParams(
              Object.entries(parsed.data).map(([k, v]) => [k, String(v)]),
            ),
          signal,
        ),
      );
    },
  });
  const detail = useQuery({
    queryKey: ["suppression-cleanup", tenant, id],
    enabled: parsed.success && /^[a-f0-9]{64}$/.test(id),
    queryFn: async ({ signal }) =>
      cleanupRecord.parse(
        await read(
          "/" + id + "?" + new URLSearchParams({ bk_tenant_id: tenant }),
          signal,
        ),
      ),
    refetchInterval: (q) => (q.state.data?.state === "pending" ? 5000 : false),
  });
  function search(e: FormEvent<HTMLFormElement>) {
    e.preventDefault();
    const raw = new FormData(e.currentTarget),
      v = Object.fromEntries(
        [...raw].filter(([, v]) => v).map(([k, v]) => [k, String(v).trim()]),
      );
    const q = cleanupQuery.safeParse({
      bk_tenant_id: v.bk_tenant_id,
      ...Object.fromEntries(fields.filter((k) => v[k]).map((k) => [k, v[k]])),
    });
    if (!q.success || (v.id && !/^[a-f0-9]{64}$/.test(v.id))) {
      setError("请检查租户、筛选条件与清理 ID");
      return;
    }
    setError("");
    setParams({
      ...Object.fromEntries(
        Object.entries(q.data).map(([k, v]) => [k, String(v)]),
      ),
      ...(v.id ? { id: v.id } : {}),
    });
  }
  const row = detail.data;
  return (
    <div className="policies-page suppression-runtime-page">
      <header className="page-header">
        <div>
          <h1>抑制清理历史</h1>
          <p>查看终态联动清理的持久记录及结果不确定性。</p>
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
              await client.invalidateQueries({
                queryKey: ["suppression-cleanups"],
                refetchType: "none",
              });
              if (params.get("after")) {
                const next = new URLSearchParams(params);
                next.delete("after");
                setParams(next);
              } else await list.refetch();
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
        记录完成表示诊断已保存；Redis 失败仍可能发生部分清理。当前运行态、历史
        Event 裁决与本页记录分别反映不同事实。
      </p>
      <form className="merge-filters" key={params.toString()} onSubmit={search}>
        {[
          ["bk_tenant_id", "租户"],
          ["event_source_id", "来源 ID"],
          ["fingerprint", "Fingerprint"],
          ["alert_id", "Alert ID"],
          ["event_id", "Event ID"],
          ["id", "精确清理 ID"],
          ["window_id", "窗口 ID"],
          ["epoch", "窗口代次"],
        ].map(([name, label]) => (
          <label key={name}>
            {label}
            <input
              name={name}
              required={name === "bk_tenant_id"}
              defaultValue={params.get(name) ?? ""}
            />
          </label>
        ))}
        <label>
          窗口方式
          <select
            name="window_kind"
            defaultValue={params.get("window_kind") ?? ""}
          >
            <option value="">全部</option>
            <option value="clip">防抖</option>
            <option value="aggregation">关联聚合</option>
          </select>
        </label>
        <label>
          记录阶段
          <select name="state" defaultValue={params.get("state") ?? ""}>
            <option value="">全部</option>
            <option value="pending">结果未完成</option>
            <option value="completed">诊断已保存</option>
          </select>
        </label>
        <button>查询</button>
      </form>
      {error && <p role="alert">{error}</p>}
      {!parsed.success && <p>填写租户后查询。</p>}
      {list.error && <p role="alert">{list.error.message}</p>}
      {list.data && !list.error && (
        <>
          <table className="merge-table" aria-label="抑制清理历史列表">
            <thead>
              <tr>
                <th>来源 / 原因</th>
                <th>防抖计数</th>
                <th>关联聚合</th>
                <th>时间与操作</th>
              </tr>
            </thead>
            <tbody>
              {list.data.items.map((r) => (
                <tr key={r.id}>
                  <td>
                    <Link to={entity(r)}>
                      {r.cause.alert_id ?? r.cause.event_id}
                    </Link>
                    <p>
                      {r.cause.event_source_id} ·{" "}
                      {r.cause.trigger === "alert_terminal"
                        ? `${r.cause.status} / r${r.cause.revision}`
                        : "无 Alert 的终态 Event"}
                    </p>
                  </td>
                  <td>{result(r.result?.clip)}</td>
                  <td>{result(r.result?.aggregation)}</td>
                  <td>
                    {formatTime(r.started_at, mode)}
                    <button
                      onClick={() => {
                        const next = new URLSearchParams(params);
                        next.set("id", r.id);
                        setParams(next);
                      }}
                    >
                      查看记录
                    </button>
                    {r.previous_unconfirmed && <p>此前清理结果未确认</p>}
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
          {!list.data.items.length && (
            <p>本页没有符合条件的记录；有后续页时可继续查询。</p>
          )}
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
      {id && (
        <section className="merge-detail">
          <h2>清理记录详情</h2>
          {!/^[a-f0-9]{64}$/.test(id) && <p role="alert">清理 ID 不合法。</p>}
          {detail.error && <p role="alert">{detail.error.message}</p>}
          {row && !detail.error && (
            <>
              <p>
                {row.state === "pending"
                  ? "结果未完成，不能判断是否发生清理"
                  : "诊断已保存，请分别查看两类结果"}
              </p>
              <p>
                首次记录：{formatTime(row.started_at, mode)} · 完成：
                {row.finished_at ? formatTime(row.finished_at, mode) : "未确认"}
              </p>
              {row.previous_unconfirmed && (
                <p className="merge-note">
                  此前尝试的清理结果未确认。本轮零删除不代表此前没有副作用。
                </p>
              )}
              <p>防抖：{result(row.result?.clip)}</p>
              <p>关联聚合：{result(row.result?.aggregation)}</p>
              <Link to={entity(row)}>
                查看触发清理的{row.cause.alert_id ? " Alert" : " Event"}
              </Link>
              <CleanupWindows key={row.id} row={row} />
              <details>
                <summary>完整清理 JSON</summary>
                <JsonViewer value={row} />
              </details>
            </>
          )}
        </section>
      )}
      <p className="merge-note">
        每页最多四条，按稳定清理 ID
        扫描，不按时间排序。删除数量指登记引用，不是事件数量。明细记录删除时的窗口身份与代次；空结果不证明历史从未抑制。
      </p>
    </div>
  );
}

function CleanupWindows({ row }: { row: CleanupRecord }) {
  const [pages, setPages] = useState({ clip: 0, aggregation: 0 });
  return (
    <section>
      <h3>已确认清理的窗口与代次</h3>
      {(["clip", "aggregation"] as const).map((kind) => {
        const outcome = row.result?.[kind],
          windows =
            outcome?.state === "confirmed" ? (outcome.windows ?? []) : [],
          page = pages[kind];
        return (
          <section key={kind}>
            <h4>
              {kind === "clip" ? "防抖" : "关联聚合"} · {windows.length} 项明细
            </h4>
            {windows.length > 0 ? (
              <>
                <table className="merge-table">
                  <thead>
                    <tr>
                      <th>窗口 ID</th>
                      <th>被清理代次</th>
                    </tr>
                  </thead>
                  <tbody>
                    {windows.slice(page * 16, page * 16 + 16).map((w) => (
                      <tr key={w.id}>
                        <td>
                          <code>{w.id}</code>
                        </td>
                        <td>
                          {w.missing ? "元信息已丢失，代次未知" : w.epoch}
                        </td>
                      </tr>
                    ))}
                  </tbody>
                </table>
                <div className="merge-pagination">
                  <button
                    disabled={page === 0}
                    onClick={() => setPages({ ...pages, [kind]: page - 1 })}
                  >
                    上一页明细
                  </button>
                  <button
                    disabled={(page + 1) * 16 >= windows.length}
                    onClick={() => setPages({ ...pages, [kind]: page + 1 })}
                  >
                    下一页明细
                  </button>
                </div>
              </>
            ) : (
              <p>{result(outcome)}</p>
            )}
          </section>
        );
      })}
    </section>
  );
}
