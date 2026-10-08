import { useEffect, useState } from "react";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import {
  mergeRetryCommand,
  mergeRetryRequest,
  mergeRetryPage,
  mergeControlPoint,
  type MergeRetryCommand,
} from "../../shared/merge-retries";
import { consoleURL } from "../base-path";
import { formatTime, useTimeMode } from "../time";
import { JsonViewer } from "../components/JsonViewer";
import "./merge-retry.css";
const outcomes = {
  advanced: "原进度已推进",
  unchanged: "本次无需推进",
  superseded: "原版本已变化，未接续旧操作",
  failed: "本次执行失败",
};
const states = {
  pending: "等待执行结果",
  completed: "本次操作已完成",
  superseded: "原版本已变化",
  failed: "本次执行失败",
};
async function read(url: string, signal: AbortSignal) {
  const response = await fetch(consoleURL(url), { signal }),
    body = (await response.json()) as { error?: { message?: string } };
  if (!response.ok) throw new Error(body.error?.message ?? "合并请求读取失败");
  return body;
}
export function MergeRetryPanel({
  tenant,
  kind,
  id,
}: {
  tenant: string;
  kind: "decisions" | "relations";
  id: string;
}) {
  const client = useQueryClient(),
    mode = useTimeMode(),
    base =
      "/local-api/policy-runtime/merge/" + kind + "/" + encodeURIComponent(id),
    key = "linkd:merge-request:" + tenant + ":" + kind + ":" + id;
  const [command, setCommand] = useState<MergeRetryCommand | undefined>(() => {
      try {
        const c = mergeRetryCommand.safeParse(
          JSON.parse(sessionStorage.getItem(key) ?? "null"),
        );
        return c.success && c.data.bk_tenant_id === tenant ? c.data : undefined;
      } catch {
        return undefined;
      }
    }),
    [selected, setSelected] = useState(() => {
      try {
        const id = sessionStorage.getItem(key + ":result") ?? "";
        return /^[a-f0-9]{64}$/.test(id) ? id : "";
      } catch {
        return "";
      }
    }),
    [reason, setReason] = useState(command?.reason ?? ""),
    [after, setAfter] = useState(""),
    [sending, setSending] = useState(false),
    [error, setError] = useState("");
  const point = useQuery({
    queryKey: ["merge-control", tenant, kind, id],
    queryFn: async ({ signal }) =>
      mergeControlPoint.parse(
        await read(
          base + "/control?" + new URLSearchParams({ bk_tenant_id: tenant }),
          signal,
        ),
      ),
  });
  const observed = point.error ? undefined : point.data;
  const history = useQuery({
    queryKey: ["merge-retry-history", tenant, kind, id, after],
    queryFn: async ({ signal }) =>
      mergeRetryPage.parse(
        await read(
          base +
            "/requests?" +
            new URLSearchParams({ bk_tenant_id: tenant, after, limit: "4" }),
          signal,
        ),
      ),
  });
  const detail = useQuery({
    queryKey: ["merge-retry-request", tenant, kind, id, selected],
    enabled: Boolean(selected),
    queryFn: async ({ signal }) =>
      mergeRetryRequest.parse(
        await read(
          base +
            "/requests/" +
            selected +
            "?" +
            new URLSearchParams({ bk_tenant_id: tenant }),
          signal,
        ),
      ),
    refetchInterval: (q) => (q.state.data?.state === "pending" ? 1000 : false),
  });
  const request = detail.data;
  useEffect(() => {
    if (request && request.state !== "pending") {
      void client.invalidateQueries({
        queryKey: ["merge-retry-history", tenant, kind, id],
      });
      void client.invalidateQueries({
        queryKey: ["merge-control", tenant, kind, id],
      });
      if (request.result) {
        void client.invalidateQueries({ queryKey: ["merge-runtime"] });
        void client.invalidateQueries({
          queryKey: ["merge-detail", tenant, kind, id],
        });
      }
    }
  }, [request, tenant, kind, id, client]);
  function select(value: string) {
    sessionStorage.setItem(key + ":result", value);
    setSelected(value);
  }
  async function submit() {
    setSending(true);
    setError("");
    try {
      const c =
        command ??
        mergeRetryCommand.parse({
          bk_tenant_id: tenant,
          expected_token: observed?.token,
          operation_id: crypto.randomUUID(),
          reason,
        });
      sessionStorage.setItem(key, JSON.stringify(c));
      setCommand(c);
      const response = await fetch(consoleURL(base + "/requests"), {
          method: "POST",
          headers: { "Content-Type": "application/json" },
          body: JSON.stringify(c),
          signal: AbortSignal.timeout(20000),
        }),
        body = (await response.json()) as unknown;
      if (!response.ok) {
        if ([400, 401, 403, 404, 409].includes(response.status)) {
          sessionStorage.removeItem(key);
          setCommand(undefined);
          void client.invalidateQueries({
            queryKey: ["merge-detail", tenant, kind, id],
          });
        }
        throw new Error(
          (body as { error?: { message?: string } }).error?.message ??
            "结果未确认，请重试同一操作。",
        );
      }
      const r = mergeRetryRequest.parse(body);
      if (
        r.command.kind !== kind ||
        r.command.target_id !== id ||
        Object.entries(c).some(
          ([k, v]) => r.command[k as keyof typeof r.command] !== v,
        )
      )
        throw new Error("返回命令不一致，请重试同一操作。");
      sessionStorage.removeItem(key);
      setCommand(undefined);
      select(r.id);
      setAfter("");
      client.setQueryData(["merge-retry-request", tenant, kind, id, r.id], r);
      void client.invalidateQueries({
        queryKey: ["merge-retry-history", tenant, kind, id],
      });
    } catch (e) {
      setError(e instanceof Error ? e.message : "结果未确认，请重试同一操作。");
    } finally {
      setSending(false);
    }
  }
  return (
    <section className="merge-retry" aria-label="合并受控操作">
      <h2>{kind === "decisions" ? "接续裁决" : "检查父子关系"}</h2>
      <p>
        固定当前持久版本，复用后台正式步骤。不会重置业务结果、重选成员或新建另一个父告警；父人工关闭后的检查只解除关系。
      </p>
      <p>接续可能继续创建主告警、释放原告警或更新关系，处置沿用原准入规则。</p>
      {point.error && <p role="alert">{point.error.message}</p>}
      {observed && (
        <p>
          当前阶段：{observed.phase} · 观察时间：
          {formatTime(observed.updated_at, mode)}
          {observed.complete ? " · 原任务已完成，无需再次接续。" : ""}
          {observed.outcome === "failed"
            ? " · 业务条件未满足，原结果不会被重置。"
            : ""}
        </p>
      )}
      <form
        onSubmit={(e) => {
          e.preventDefault();
          void submit();
        }}
      >
        <label>
          操作原因
          <textarea
            required
            value={reason}
            disabled={sending || Boolean(command)}
            onChange={(e) => setReason(e.target.value)}
          />
        </label>
        <button
          disabled={
            sending ||
            (!command &&
              (request?.state === "pending" ||
                !observed ||
                observed.complete ||
                !reason.trim()))
          }
        >
          {sending
            ? "正在提交…"
            : command
              ? "重试同一操作"
              : kind === "decisions"
                ? "请求接续裁决"
                : "请求检查关系"}
        </button>
      </form>
      {command && (
        <p>待确认操作：{command.operation_id}。保留原版本和原因重试。</p>
      )}
      {!observed && !command && <p>当前控制点不可用，仍可查询既有请求结果。</p>}
      {error && <p role="alert">{error}</p>}
      {detail.error && <p role="alert">{detail.error.message}</p>}
      {request && !detail.error && (
        <section className="merge-retry-result">
          <h3>{states[request.state]}</h3>
          <p>
            操作人：{request.command.operator_id} · 原因：
            {request.command.reason}
          </p>
          <p>
            固定版本：<code>{request.command.expected_token}</code>
          </p>
          <p>
            {formatTime(request.created_at, mode)} · {request.id}
          </p>
          {request.previous_unconfirmed && (
            <p className="merge-note">
              此前执行结果未确认；本轮进度不变或版本变化不能证明此前没有副作用。
            </p>
          )}
          {request.state === "pending" && (
            <p>命令已受理，结果等待后台任务确认。</p>
          )}
          {request.result && (
            <>
              <strong>{outcomes[request.result.outcome]}</strong>
              <p>
                原因：<code>{request.result.reason}</code> ·{" "}
                {formatTime(request.result.checked_at, mode)}
              </p>
              <p>
                {request.result.step_attempted
                  ? "已调用正式有界步骤，失败时可能已有部分进度。"
                  : "本次未调用业务步骤。"}
              </p>
              {request.result.before && (
                <p>执行前阶段：{request.result.before.phase}</p>
              )}
              {request.result.after && (
                <p>
                  执行后阶段：{request.result.after.phase} ·{" "}
                  {request.result.after.complete
                    ? "原任务已完成"
                    : "原任务仍可能有后续步骤"}
                </p>
              )}
              <details>
                <summary>完整操作结果</summary>
                <JsonViewer value={request} />
              </details>
            </>
          )}
        </section>
      )}
      <h3>该记录的操作请求</h3>
      {history.error && <p role="alert">{history.error.message}</p>}
      {history.data && !history.error && (
        <>
          <table className="merge-table">
            <thead>
              <tr>
                <th>创建时间 / 原版本</th>
                <th>操作人 / 阶段</th>
                <th>操作</th>
              </tr>
            </thead>
            <tbody>
              {history.data.items.map((r) => (
                <tr key={r.id}>
                  <td>
                    {formatTime(r.created_at, mode)}
                    <p>{r.command.expected_token}</p>
                  </td>
                  <td>
                    {r.command.operator_id}
                    <p>{states[r.state]}</p>
                  </td>
                  <td>
                    <button onClick={() => select(r.id)}>查看请求</button>
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
          {history.data.items.length === 0 && <p>本页暂无请求。</p>}
          <div className="merge-pagination">
            <button disabled={!after} onClick={() => setAfter("")}>
              请求首页
            </button>
            <button
              disabled={!history.data.next}
              onClick={() => setAfter(history.data.next)}
            >
              下一页请求
            </button>
            <button
              onClick={async () => {
                await client.invalidateQueries({
                  queryKey: ["merge-retry-history", tenant, kind, id],
                  refetchType: "none",
                });
                if (after) setAfter("");
                else await history.refetch();
                if (selected) await detail.refetch();
              }}
            >
              刷新操作记录
            </button>
          </div>
        </>
      )}
    </section>
  );
}
