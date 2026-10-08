import { MergeRetryPanel } from "./MergeRetryPanel";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { type FormEvent, useEffect, useState } from "react";
import { Link, useSearchParams } from "react-router-dom";
import {
  mergePhase,
  mergeQuery,
  mergeResource,
  type MergeResource,
  type MergeRow,
} from "../../shared/merge-runtime";
import {
  getMergeMembers,
  getMergeRuntime,
  getMergeSnapshot,
  listMergeRuntime,
} from "../api";
import { JsonViewer } from "../components/JsonViewer";
import { formatTime, useTimeMode } from "../time";
import "./policies.css";
import "./merge-runtime.css";

const names: Record<MergeResource, string> = {
  windows: "临时窗口",
  decisions: "持久化裁决",
  relations: "Alert 父子关系",
};
const phases: Record<string, string> = {
  capturing: "捕获成员快照",
  prepared: "父事件已准备",
  waiting_parent: "等待真实父告警",
  linking: "建立关系",
  ending: "解除关系",
  releasing: "释放原告警",
  completed: "执行完成",
  preparing: "关系准备中",
  ready: "关系生效",
  ended: "关系已结束",
  pending: "未确认",
  linked: "已关联",
  terminal: "已观察到终态",
};
function entityLink(tenant: string, id: string, entity = "alerts") {
  return (
    "/explore/" +
    entity +
    "?" +
    new URLSearchParams({
      bk_tenant_id: tenant,
      detail_tenant: tenant,
      detail: id,
    })
  );
}
function status(row: MergeRow) {
  if ("phase" in row) return phases[row.phase];
  if ("state" in row) return phases[row.state];
  return row.frozen ? "窗口已冻结" : "窗口累计中";
}
export function MergeRuntimePage() {
  const client = useQueryClient();
  const [refreshing, setRefreshing] = useState(false);
  const [revision, setRevision] = useState(0);
  const [params, setParams] = useSearchParams();
  const tenant = params.get("bk_tenant_id") ?? "";
  const resource = mergeResource
    .catch("decisions")
    .parse(params.get("resource"));
  const id = params.get("id") ?? "";
  const query = mergeQuery.safeParse({
    bk_tenant_id: tenant,
    resource,
    after: params.get("after") ?? "",
    limit: 4,
    ...(params.get("policy_id") ? { policy_id: params.get("policy_id") } : {}),
    ...(params.get("phase") ? { phase: params.get("phase") } : {}),
    ...(params.get("alert_id") ? { alert_id: params.get("alert_id") } : {}),
  });
  const [error, setError] = useState("");
  const list = useQuery({
    queryKey: ["merge-runtime", params.toString()],
    queryFn: ({ signal }) => {
      if (!query.success) throw new Error("请填写查询范围");
      return listMergeRuntime(query.data, signal);
    },
    enabled: query.success,
  });
  const detail = useQuery({
    queryKey: ["merge-detail", tenant, resource, id],
    queryFn: ({ signal }) => getMergeRuntime(tenant, resource, id, signal),
    enabled: /^[a-zA-Z0-9_-]{1,64}$/.test(tenant) && /^[a-f0-9]{64}$/.test(id),
  });
  const mode = useTimeMode();
  function search(e: FormEvent<HTMLFormElement>) {
    e.preventDefault();
    const data = new FormData(e.currentTarget);
    const next = {
      bk_tenant_id: String(data.get("tenant") ?? "").trim(),
      resource,
      after: "",
      limit: 4,
      ...(data.get("policy")
        ? { policy_id: String(data.get("policy")).trim() }
        : {}),
      ...(data.get("phase") ? { phase: String(data.get("phase")) } : {}),
      ...(resource === "relations"
        ? { alert_id: String(data.get("alert") ?? "").trim() }
        : {}),
    };
    const parsed = mergeQuery.safeParse(next),
      lookup = String(data.get("id") ?? "").trim();
    if (!parsed.success || (lookup && !/^[a-f0-9]{64}$/.test(lookup))) {
      setError("请填写合法租户、查询范围和 64 位十六进制运行记录 ID");
      return;
    }
    setError("");
    setParams({
      ...Object.fromEntries(
        Object.entries(parsed.data).map(([k, v]) => [k, String(v)]),
      ),
      ...(lookup ? { id: lookup } : {}),
    });
  }
  function navigate(resource: MergeResource, id: string, alert?: string) {
    setParams({
      bk_tenant_id: tenant,
      resource,
      id,
      ...(alert ? { alert_id: alert } : {}),
    });
  }
  return (
    <div className="policies-page merge-runtime-page">
      <header className="page-header">
        <div>
          <h1>合并运行态</h1>
          <p>按租户检查窗口、裁决进度与真实父子关系。</p>
        </div>
        <button
          type="button"
          onClick={async () => {
            setRefreshing(true);
            try {
              await Promise.all([
                list.refetch(),
                ...(id ? [detail.refetch()] : []),
              ]);
              if (id)
                await client.invalidateQueries({
                  queryKey: ["merge-members", tenant, id],
                });
              await Promise.all(
                [
                  "merge-control",
                  "merge-retry-history",
                  "merge-retry-request",
                ].map((key) =>
                  client.invalidateQueries({
                    queryKey: [key, tenant, resource, id],
                  }),
                ),
              );
              setRevision((v) => v + 1);
            } finally {
              setRefreshing(false);
            }
          }}
          disabled={
            !query.success || refreshing || list.isFetching || detail.isFetching
          }
        >
          刷新
        </button>
      </header>
      <nav className="merge-tabs" aria-label="合并运行态类型">
        {mergeResource.options.map((value) => (
          <button
            key={value}
            aria-pressed={resource === value}
            onClick={() => setParams({ bk_tenant_id: tenant, resource: value })}
          >
            {names[value]}
          </button>
        ))}
      </nav>
      <form className="merge-filters" key={params.toString()} onSubmit={search}>
        <label>
          租户
          <input name="tenant" defaultValue={tenant} required />
        </label>
        <label>
          策略 ID
          <input name="policy" defaultValue={params.get("policy_id") ?? ""} />
        </label>
        {resource === "decisions" && (
          <label>
            执行阶段
            <select name="phase" defaultValue={params.get("phase") ?? ""}>
              <option value="">全部阶段</option>
              {mergePhase.options.map((p) => (
                <option key={p} value={p}>
                  {phases[p]}
                </option>
              ))}
            </select>
          </label>
        )}
        {resource === "relations" && (
          <label>
            Alert ID
            <input
              name="alert"
              defaultValue={params.get("alert_id") ?? ""}
              required
            />
          </label>
        )}
        <label>
          精确记录 ID
          <input name="id" defaultValue={id} />
        </label>
        <button type="submit">查询</button>
      </form>
      {error && <p role="alert">{error}</p>}
      <p className="merge-note">
        {resource === "windows"
          ? "窗口是 Redis 临时状态；消失可能表示正常清理、到期或数据丢失，不能据此认定合并失败。首次命中组不等于最终裁决结果。"
          : resource === "decisions"
            ? "裁决包含已完成记录。窗口结果与执行阶段分别展示；成功裁决不代表父告警已创建或已获处置资格。"
            : "关系状态独立于父子生命周期。父人工关闭只解除关系，不恢复子告警，也不自动补发处置。"}
      </p>
      {!query.success ? (
        <p>请填写租户{resource === "relations" ? "和 Alert ID" : ""}后查询。</p>
      ) : (
        <>
          {list.isPending && <p>正在读取{names[resource]}…</p>}
          {list.error && <p role="alert">{list.error.message}</p>}
          {list.data && !list.error && (
            <>
              <table className="merge-table" aria-label={names[resource]}>
                <thead>
                  <tr>
                    <th>记录 ID / 策略</th>
                    <th>状态与进度</th>
                    <th>时间</th>
                    <th>操作</th>
                  </tr>
                </thead>
                <tbody>
                  {list.data.items.map((row) => (
                    <tr key={row.id}>
                      <td>
                        <code>{row.id}</code>
                        <Link
                          to={
                            "/policies?" +
                            new URLSearchParams({
                              bk_tenant_id: tenant,
                              type: "merge",
                              id: row.policy.id,
                              version: String(row.policy.version),
                            })
                          }
                        >
                          {row.policy.id} · v{row.policy.version}
                        </Link>
                      </td>
                      <td>
                        <strong>{status(row)}</strong>
                        <p>
                          {"phase" in row
                            ? `窗口裁决：${row.outcome === "succeeded" ? "满足条件" : "条件未满足"}；成员 ${row.member_offset}/${row.wait_member_count}`
                            : "state" in row
                              ? `固定成员 ${row.members.length}；解除 ${row.end_offset}/${row.wait_member_ids.length}`
                              : `已提交 ${row.committed_count}/${row.member_count}；${row.cyclic ? "周期" : "非周期"}`}
                        </p>
                      </td>
                      <td>
                        {formatTime(
                          "deadline" in row ? row.deadline : row.updated_at,
                          mode,
                        )}
                        {"cyclic" in row && !row.frozen && (
                          <WindowRemaining deadline={row.deadline} />
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
              {list.data.items.length === 0 && (
                <p>
                  本页没有匹配记录。{list.data.next ? "仍可继续下一页。" : ""}
                </p>
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
        </>
      )}
      {id && (
        <section className="merge-detail" aria-label="运行记录详情">
          <h2>{names[resource]}详情</h2>
          <code>{id}</code>
          {detail.isPending && <p>正在读取详情…</p>}
          {detail.error && (
            <p role="alert">
              {detail.error.message}。
              {resource === "windows"
                ? "请结合持久化裁决和 Alert 等待记录检查，不将窗口缺失视为合并失败。"
                : ""}
            </p>
          )}
          {detail.data && !detail.error && (
            <RuntimeDetail
              key={tenant + resource + id}
              row={detail.data}
              navigate={navigate}
            />
          )}
        </section>
      )}
      {resource !== "windows" &&
        /^[a-zA-Z0-9_-]{1,64}$/.test(tenant) &&
        /^[a-f0-9]{64}$/.test(id) && (
          <MergeRetryPanel
            key={`${tenant}:${resource}:${id}:${revision}`}
            tenant={tenant}
            kind={resource}
            id={id}
          />
        )}
    </div>
  );
}
function WindowRemaining({ deadline }: { deadline: string }) {
  const [now, setNow] = useState(() => Date.now());
  useEffect(() => {
    const timer = setInterval(() => setNow(Date.now()), 1000);
    return () => clearInterval(timer);
  }, []);
  const seconds = Math.max(0, Math.ceil((Date.parse(deadline) - now) / 1000));
  return (
    <p>{seconds ? `距窗口截止 ${seconds} 秒` : "已到截止时间，等待裁决确认"}</p>
  );
}

function RuntimeDetail({
  row,
  navigate,
}: {
  row: MergeRow;
  navigate: (resource: MergeResource, id: string, alert?: string) => void;
}) {
  const [memberAfter, setMemberAfter] = useState("");
  const [snapshotID, setSnapshotID] = useState("");
  const snapshot = useQuery({
    queryKey: ["merge-snapshot", row.bk_tenant_id, row.id, snapshotID],
    queryFn: ({ signal }) =>
      getMergeSnapshot(row.bk_tenant_id, row.id, snapshotID, signal),
    enabled: "phase" in row && Boolean(snapshotID),
  });
  const members = useQuery({
    queryKey: ["merge-members", row.bk_tenant_id, row.id, memberAfter],
    queryFn: ({ signal }) =>
      getMergeMembers(row.bk_tenant_id, row.id, memberAfter, signal),
    enabled: "phase" in row,
  });
  const mode = useTimeMode(),
    tenant = row.bk_tenant_id;
  return (
    <>
      <div className="merge-detail-summary">
        <strong>{status(row)}</strong>
        <span>
          策略 {row.policy.id} · v{row.policy.version}
        </span>
        {"deadline" in row && (
          <span>截止时间 {formatTime(row.deadline, mode)}</span>
        )}
      </div>
      {"phase" in row ? (
        <>
          <p>
            窗口结果：{row.outcome === "succeeded" ? "满足条件" : "条件未满足"}{" "}
            · 快照捕获 {row.capture_offset}/{row.member_count} · 等待成员处理{" "}
            {row.member_offset}/{row.wait_member_count}
          </p>
          <p>
            诊断原因：{row.reason_code ?? "无"} · 窗口清理确认：
            {row.window_finished ? "已确认" : "未确认"}
          </p>
          <div className="merge-links">
            <button onClick={() => navigate("windows", row.window_id)}>
              查看临时窗口
            </button>
            {row.parent_event_id && (
              <Link to={entityLink(tenant, row.parent_event_id, "events")}>
                父 Event
              </Link>
            )}
            {row.parent_alert_id && (
              <>
                <Link to={entityLink(tenant, row.parent_alert_id)}>
                  父 Alert
                </Link>
                <button
                  onClick={() =>
                    navigate("relations", row.id, row.parent_alert_id)
                  }
                >
                  查看父子关系
                </button>
              </>
            )}
          </div>
          <h3>固定成员快照</h3>
          <p>
            这里显示首次捕获时的状态；点击 Alert
            查看当前状态。未捕获的成员不会用实时快照替代。
          </p>
          {members.error && <p role="alert">{members.error.message}</p>}
          {members.data && (
            <>
              <table className="merge-table" aria-label="固定成员快照">
                <thead>
                  <tr>
                    <th>Alert</th>
                    <th>来源</th>
                    <th>当时状态 / 等级</th>
                    <th>丰富</th>
                  </tr>
                </thead>
                <tbody>
                  {members.data.items.map((m) => (
                    <tr key={m.alert_id}>
                      <td>
                        <Link to={entityLink(tenant, m.alert_id)}>
                          {m.alert_id}
                        </Link>
                        <button onClick={() => setSnapshotID(m.alert_id)}>
                          查看冻结快照
                        </button>
                      </td>
                      <td>{m.event_source_id}</td>
                      <td>
                        {m.status} / {m.severity} · r{m.revision}
                      </td>
                      <td>{m.enrich_status}</td>
                    </tr>
                  ))}
                </tbody>
              </table>
              <div className="merge-pagination">
                <button
                  disabled={!memberAfter}
                  onClick={() => setMemberAfter("")}
                >
                  成员首页
                </button>
                <button
                  disabled={!members.data.next}
                  onClick={() => setMemberAfter(members.data.next)}
                >
                  下一页成员
                </button>
              </div>
              {snapshotID && (
                <section className="merge-frozen">
                  <h3>冻结成员 Alert 快照</h3>
                  <p>{snapshotID} · 这是合并模板使用的首次捕获数据。</p>
                  <button onClick={() => setSnapshotID("")}>关闭快照</button>
                  {snapshot.isPending && <p>正在读取冻结快照…</p>}
                  {snapshot.error && (
                    <p role="alert">{snapshot.error.message}</p>
                  )}
                  {snapshot.data && !snapshot.error && (
                    <JsonViewer value={snapshot.data.alert} />
                  )}
                </section>
              )}
            </>
          )}
        </>
      ) : "state" in row ? (
        <>
          <p>
            索引建立 {row.index_offset}/{row.members.length + 1} · 解除处理{" "}
            {row.end_offset}/{row.wait_member_ids.length} · 原因{" "}
            {row.end_reason ?? "无"}
          </p>
          <div className="merge-links">
            <Link to={entityLink(tenant, row.parent_alert_id)}>
              查看父 Alert
            </Link>
            <button onClick={() => navigate("decisions", row.id)}>
              查看原始裁决
            </button>
          </div>
          <table className="merge-table" aria-label="关系成员">
            <thead>
              <tr>
                <th>子 Alert</th>
                <th>建联状态</th>
              </tr>
            </thead>
            <tbody>
              {row.members.map((m) => (
                <tr key={m.alert_id}>
                  <td>
                    <Link to={entityLink(tenant, m.alert_id)}>
                      {m.alert_id}
                    </Link>
                  </td>
                  <td>{phases[m.state]}</td>
                </tr>
              ))}
            </tbody>
          </table>
          <p>
            linked 记录建联结果，关系结束后不代表仍在抑制；是否活动由对应 Alert
            的生命周期字段确定。
          </p>
        </>
      ) : (
        <>
          <p>
            已提交成员 {row.committed_count}/{row.member_count} ·{" "}
            {row.cyclic ? "周期窗口到期裁决" : "非周期窗口可提前裁决"}
          </p>
          <div className="merge-groups">
            {row.group_counts.map((n, i) => (
              <span key={i}>
                条件组 {i + 1}：{n} 个首次命中成员
              </span>
            ))}
          </div>
          {row.frozen && (
            <button
              onClick={() => navigate("decisions", row.frozen!.operation_id)}
            >
              查看持久化裁决
            </button>
          )}
          <table className="merge-table" aria-label="窗口成员">
            <thead>
              <tr>
                <th>Alert / Event</th>
                <th>来源 / 等级</th>
                <th>首次条件组</th>
                <th>提交状态</th>
              </tr>
            </thead>
            <tbody>
              {row.members?.map((m) => (
                <tr key={m.main.alert_id}>
                  <td>
                    <Link to={entityLink(tenant, m.main.alert_id)}>
                      {m.main.alert_id}
                    </Link>
                    <Link to={entityLink(tenant, m.main.event_id, "events")}>
                      {m.main.event_id}
                    </Link>
                  </td>
                  <td>
                    {m.main.event_source_id} / {m.main.severity}
                  </td>
                  <td>{m.groups.map((g) => g + 1).join("、")}</td>
                  <td>{m.committed ? "Alert 已提交" : "尚未确认"}</td>
                </tr>
              ))}
            </tbody>
          </table>
        </>
      )}
      <details>
        <summary>查看运行记录 JSON</summary>
        <JsonViewer value={row} />
      </details>
    </>
  );
}
