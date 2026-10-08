import { useEffect, useState } from "react";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { Link } from "react-router-dom";
import {
  suppressionCheckCommand,
  suppressionCheckRequest,
  suppressionCheckPage,
  type SuppressionCheckCommand,
} from "../../shared/suppression-checks";
import type { SuppressionWindow } from "../../shared/suppression-runtime";
import { consoleURL } from "../base-path";
import { formatTime, useTimeMode } from "../time";
import { JsonViewer } from "../components/JsonViewer";
import "./suppression-check.css";
const outcomes = {
  retained: "窗口保留",
  cleared: "原窗口已清理",
  absent: "窗口已不存在",
  superseded: "窗口已换代，未按旧命令清理",
  failed: "检查失败",
};
const states = {
  pending: "等待检查结果",
  completed: "检查已完成",
  superseded: "原命令已失效",
  failed: "检查失败",
};
async function read(url: string, signal: AbortSignal) {
  const response = await fetch(consoleURL(url), { signal }),
    body = (await response.json()) as { error?: { message?: string } };
  if (!response.ok) throw new Error(body.error?.message ?? "对账请求读取失败");
  return body;
}
export function SuppressionCheckPanel({
  tenant,
  kind,
  id,
  observed,
}: {
  tenant: string;
  kind: "clip" | "aggregation";
  id: string;
  observed?: SuppressionWindow;
}) {
  const client = useQueryClient(),
    mode = useTimeMode(),
    base =
      "/local-api/policy-runtime/suppression/" +
      kind +
      "/" +
      encodeURIComponent(id),
    key = "linkd:suppression-check:" + tenant + ":" + kind + ":" + id;
  const [command, setCommand] = useState<SuppressionCheckCommand | undefined>(
      () => {
        try {
          const c = suppressionCheckCommand.safeParse(
            JSON.parse(sessionStorage.getItem(key) ?? "null"),
          );
          return c.success && c.data.bk_tenant_id === tenant
            ? c.data
            : undefined;
        } catch {
          return undefined;
        }
      },
    ),
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
  const history = useQuery({
    queryKey: ["suppression-check-history", tenant, kind, id, after],
    queryFn: async ({ signal }) =>
      suppressionCheckPage.parse(
        await read(
          base +
            "/requests?" +
            new URLSearchParams({ bk_tenant_id: tenant, after, limit: "4" }),
          signal,
        ),
      ),
  });
  const detail = useQuery({
    queryKey: ["suppression-check-request", tenant, kind, id, selected],
    enabled: Boolean(selected),
    queryFn: async ({ signal }) =>
      suppressionCheckRequest.parse(
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
        queryKey: ["suppression-check-history", tenant, kind, id],
      });
      if (request.result?.changed) {
        void client.invalidateQueries({ queryKey: ["suppression-runtime"] });
        void client.invalidateQueries({
          queryKey: ["suppression-detail", tenant, kind, id],
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
        suppressionCheckCommand.parse({
          bk_tenant_id: tenant,
          expected_epoch: observed?.epoch,
          expected_owner_alert_id: observed?.owner_alert_id ?? "",
          operation_id: crypto.randomUUID(),
          reason,
        });
      sessionStorage.setItem(key, JSON.stringify(c));
      setCommand(c);
      const response = await fetch(consoleURL(base + "/reconcile"), {
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
            queryKey: ["suppression-detail", tenant, kind, id],
          });
        }
        throw new Error(
          (body as { error?: { message?: string } }).error?.message ??
            "结果未确认，请重试同一对账操作。",
        );
      }
      const r = suppressionCheckRequest.parse(body);
      if (
        r.command.kind !== kind ||
        r.command.window_id !== id ||
        Object.entries(c).some(
          ([k, v]) => r.command[k as keyof typeof r.command] !== v,
        )
      )
        throw new Error("返回命令不一致，请重试同一对账操作。");
      sessionStorage.removeItem(key);
      setCommand(undefined);
      select(r.id);
      setAfter("");
      client.setQueryData(
        ["suppression-check-request", tenant, kind, id, r.id],
        r,
      );
      void client.invalidateQueries({
        queryKey: ["suppression-check-history", tenant, kind, id],
      });
    } catch (e) {
      setError(
        e instanceof Error ? e.message : "结果未确认，请重试同一对账操作。",
      );
    } finally {
      setSending(false);
    }
  }
  return (
    <section className="suppression-check" aria-label="抑制受控对账">
      <h2>受控对账</h2>
      <p>
        固定当前 owner 和代次复核真实告警。未绑定的防抖计数保持不变，已失效的
        owner 登记才会移除；不重新计数或补发处置。
      </p>
      <form
        onSubmit={(e) => {
          e.preventDefault();
          void submit();
        }}
      >
        <label>
          对账原因
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
              (request?.state === "pending" || !observed || !reason.trim()))
          }
        >
          {sending
            ? "正在提交…"
            : command
              ? "重试同一对账操作"
              : "提交受控对账"}
        </button>
      </form>
      {command && (
        <p>
          待确认操作：{command.operation_id}。保留原 owner、代次和原因重试。
        </p>
      )}
      {!observed && !command && (
        <p>当前窗口未读取或已消失，仍可查询既有请求结果。</p>
      )}
      {error && <p role="alert">{error}</p>}
      {detail.error && <p role="alert">{detail.error.message}</p>}
      {request && !detail.error && (
        <section className="suppression-check-result">
          <h3>{states[request.state]}</h3>
          <p>
            操作人：{request.command.operator_id} · 原因：
            {request.command.reason}
          </p>
          <p>
            固定代次：<code>{request.command.expected_epoch}</code> · owner：
            {request.command.expected_owner_alert_id || "未绑定"}
          </p>
          <p>
            {formatTime(request.created_at, mode)} · {request.id}
          </p>
          {request.previous_unconfirmed && (
            <p className="merge-note">
              此前执行结果未确认，当前空窗口不能证明此前没有发生清理。
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
                {request.result.changed
                  ? "本次已确认删除原窗口。"
                  : "本次没有确认删除；失败时仍可能存在未确认的副作用。"}
              </p>
              {request.result.owner_revision && (
                <p>
                  实际 owner：r{request.result.owner_revision} ·{" "}
                  {request.result.owner_status}
                </p>
              )}
              <details>
                <summary>完整对账结果</summary>
                <JsonViewer value={request} />
              </details>
            </>
          )}
        </section>
      )}
      <h3>该窗口的对账请求</h3>
      {history.error && <p role="alert">{history.error.message}</p>}
      {history.data && !history.error && (
        <>
          <table className="merge-table">
            <thead>
              <tr>
                <th>创建时间 / 原代次</th>
                <th>操作人 / 阶段</th>
                <th>操作</th>
              </tr>
            </thead>
            <tbody>
              {history.data.items.map((r) => (
                <tr key={r.id}>
                  <td>
                    {formatTime(r.created_at, mode)}
                    <p>{r.command.expected_epoch}</p>
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
                  queryKey: ["suppression-check-history", tenant, kind, id],
                  refetchType: "none",
                });
                if (after) setAfter("");
                else await history.refetch();
                if (selected) await detail.refetch();
              }}
            >
              刷新对账记录
            </button>
          </div>
        </>
      )}
      <Link
        to={
          "/suppression-cleanups?" +
          new URLSearchParams({
            bk_tenant_id: tenant,
            window_kind: kind,
            window_id: id,
            ...(observed ? { epoch: observed.epoch } : {}),
          })
        }
      >
        查看该窗口的终态清理明细
      </Link>
    </section>
  );
}
