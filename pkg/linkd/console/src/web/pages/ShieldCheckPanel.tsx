import { useEffect, useState } from "react";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { Link } from "react-router-dom";
import {
  shieldCheckCommand,
  shieldCheckRequest,
  type ShieldCheck,
  type ShieldCheckCommand,
  type ShieldCheckRequest,
} from "../../shared/shield-checks";
import type { ShieldRecord } from "../../shared/shield-runtime";
import { getShieldCheck, getShieldRequest, listShieldRequests } from "../api";
import { consoleURL } from "../base-path";
import { JsonViewer } from "../components/JsonViewer";
import { formatTime, useTimeMode } from "../time";
const outcomes = {
  failed: "检查失败",
  superseded: "版本变化，请求已失效",
  inactive: "当前无需解除",
  retained: "保留屏蔽",
  changed: "屏蔽状态已更新",
  partial: "部分条件未能检查",
};
const states = {
  pending: "等待执行",
  completed: "已完成",
  failed: "失败",
  superseded: "已失效",
};
function CheckResult({ value }: { value: ShieldCheck }) {
  const mode = useTimeMode(),
    r = value.report;
  const [stepPage, setStepPage] = useState(0);
  const steps = r.decision?.steps ?? [],
    lastPage = Math.max(0, Math.ceil(steps.length / 16) - 1),
    page = Math.min(stepPage, lastPage);
  return (
    <div className="shield-check-result">
      <strong>{outcomes[r.outcome]}</strong>
      <p>
        {formatTime(value.finished_at, mode)} ·{" "}
        {value.trigger === "timer"
          ? "定时复查"
          : value.trigger === "hint"
            ? "事件提示复查"
            : "手动复查"}{" "}
        · 观察版本 {r.observed_revision || "未读取"} →{" "}
        {r.result_revision || "未知"}
      </p>
      {value.error_code && (
        <p role="status">
          原因：<code>{value.error_code}</code>
          。失败可能发生在部分状态已保存之后，请结合当前 Alert 查看。
        </p>
      )}
      <p>
        {r.observed_revision === 0 ? (
          "本次未能读取 Alert，绑定数量未知。"
        ) : (
          <>
            本次确认的状态变更：{r.changed ? "有" : "无"} · 检查结束时剩余{" "}
            {r.remaining_bindings} 条绑定
          </>
        )}
      </p>
      {steps.slice(page * 16, page * 16 + 16).map((step, i) => (
        <p key={`${page}:${i}:${step.binding_id ?? ""}`}>
          <span>{step.from_binding ? "既有绑定" : "候选规则"} · </span>
          <Link
            to={
              "/policies?" +
              new URLSearchParams({
                bk_tenant_id: value.bk_tenant_id,
                type: "shield",
                id: step.policy.id,
                version: String(step.policy.version),
              })
            }
          >
            {step.policy.id} · v{step.policy.version}
          </Link>
          ：
          {
            {
              retained: "保留",
              released: "解除",
              skipped: step.from_binding
                ? "检查未完成，保留关系"
                : "未能评估，跳过候选",
              bound: "建立时间屏蔽",
              not_matched: "未命中",
            }[step.outcome]
          }
          {step.reason_code && (
            <>
              {" "}
              · <code>{step.reason_code}</code>
            </>
          )}
        </p>
      ))}
      {steps.length > 16 && (
        <nav aria-label="检查步骤分页">
          <button
            type="button"
            disabled={page === 0}
            onClick={() => setStepPage(page - 1)}
          >
            检查步骤上一页
          </button>
          <span>
            {page + 1} / {lastPage + 1} · 共 {steps.length} 条步骤
          </span>
          <button
            type="button"
            disabled={page === lastPage}
            onClick={() => setStepPage(page + 1)}
          >
            检查步骤下一页
          </button>
        </nav>
      )}
    </div>
  );
}
export function ShieldCheckPanel({ row }: { row: ShieldRecord }) {
  const client = useQueryClient(),
    tenant = row.bk_tenant_id,
    id = row.alert_id,
    storageKey =
      "linkd:shield-check:" +
      encodeURIComponent(tenant) +
      ":" +
      encodeURIComponent(id);
  const [command, setCommand] = useState<ShieldCheckCommand | undefined>(() => {
    try {
      const c = shieldCheckCommand.safeParse(
        JSON.parse(sessionStorage.getItem(storageKey) ?? "null"),
      );
      return c.success && c.data.bk_tenant_id === tenant ? c.data : undefined;
    } catch {
      return undefined;
    }
  });
  const [reason, setReason] = useState(command?.reason ?? ""),
    [sending, setSending] = useState(false),
    [error, setError] = useState(""),
    [receipt, setReceipt] = useState<ShieldCheckRequest>(),
    [after, setAfter] = useState("");
  const mode = useTimeMode();
  const latest = useQuery({
    queryKey: ["shield-check", tenant, id],
    queryFn: ({ signal }) => getShieldCheck(tenant, id, signal),
  });
  const history = useQuery({
    queryKey: ["shield-requests", tenant, id, after],
    queryFn: ({ signal }) => listShieldRequests(tenant, id, after, signal),
  });
  const requested = useQuery({
    queryKey: ["shield-request", tenant, id, receipt?.id],
    queryFn: ({ signal }) => getShieldRequest(tenant, id, receipt!.id, signal),
    enabled: Boolean(receipt),
    refetchInterval: (q) => (q.state.data?.state === "pending" ? 2000 : false),
  });
  const result = requested.data ?? receipt,
    terminal = result && result.state !== "pending";
  useEffect(() => {
    if (!terminal) return;
    sessionStorage.removeItem(storageKey);
    void client.invalidateQueries({ queryKey: ["shield-detail", tenant, id] });
    void client.invalidateQueries({ queryKey: ["shield-runtime"] });
    void client.invalidateQueries({ queryKey: ["shield-check", tenant, id] });
    void client.invalidateQueries({
      queryKey: ["shield-requests", tenant, id],
    });
  }, [terminal, storageKey, client, tenant, id]);
  async function submit() {
    setSending(true);
    setError("");
    try {
      const c =
        command ??
        shieldCheckCommand.parse({
          bk_tenant_id: tenant,
          operation_id: crypto.randomUUID(),
          expected_revision: row.revision,
          reason,
        });
      sessionStorage.setItem(storageKey, JSON.stringify(c));
      setCommand(c);
      const response = await fetch(
        consoleURL(
          "/local-api/policy-runtime/shield/alerts/" +
            encodeURIComponent(id) +
            "/reconcile",
        ),
        {
          method: "POST",
          headers: { "Content-Type": "application/json" },
          body: JSON.stringify(c),
          signal: AbortSignal.timeout(20000),
        },
      );
      const raw = (await response.json()) as unknown;
      if (!response.ok) {
        if ([400, 401, 403, 404, 409].includes(response.status)) {
          sessionStorage.removeItem(storageKey);
          setCommand(undefined);
          if (response.status === 409)
            void client.invalidateQueries({
              queryKey: ["shield-detail", tenant, id],
            });
        }
        const data = raw as { error?: { message?: string } };
        throw new Error(
          data.error?.message ?? "提交结果未确认，请重试同一操作。",
        );
      }
      const r = shieldCheckRequest.parse(raw);
      if (
        r.command.alert_id !== id ||
        Object.entries(c).some(
          ([key, v]) => r.command[key as keyof typeof r.command] !== v,
        )
      )
        throw new Error("返回命令不一致，请重试同一操作。");
      setReceipt(r);
      setAfter("");
      void client.invalidateQueries({
        queryKey: ["shield-requests", tenant, id],
      });
    } catch (err) {
      setError(
        err instanceof Error ? err.message : "提交结果未确认，请重试同一操作。",
      );
    } finally {
      setSending(false);
    }
  }
  return (
    <section className="shield-check-panel" aria-label="屏蔽复查">
      <h3>最近一次复查</h3>
      <p>这是已保存的检查记录；当前绑定和状态以 Alert 详情为准。</p>
      {latest.isPending && <p>正在读取复查记录…</p>}
      {latest.error && <p role="alert">{latest.error.message}</p>}
      {latest.data &&
        !latest.error &&
        (latest.data.check ? (
          <CheckResult
            key={latest.data.check.started_at + latest.data.check.finished_at}
            value={latest.data.check}
          />
        ) : (
          <p>尚无复查记录。</p>
        ))}
      <h3>手动复查</h3>
      <p>
        依据当前条件重新检查屏蔽，仅同步状态；解除后等待下一条触发 Event
        判断处置。
      </p>
      {result && (
        <div role="status">
          <strong>请求{states[result.state]}</strong>
          <p>
            操作 {result.command.operation_id} · 提交版本 r
            {result.command.expected_revision}
          </p>
          {result.result && (
            <CheckResult
              key={result.result.started_at + result.result.finished_at}
              value={result.result}
            />
          )}
        </div>
      )}
      {requested.error && (
        <p role="alert">
          请求状态读取失败：{requested.error.message}
          。保留原命令，可刷新或重试查询。
        </p>
      )}
      {terminal ? (
        <button
          onClick={() => {
            setReceipt(undefined);
            setCommand(undefined);
            setReason("");
            setError("");
          }}
        >
          准备新的复查
        </button>
      ) : (
        <form
          onSubmit={(e) => {
            e.preventDefault();
            void submit();
          }}
        >
          <label>
            复查原因
            <textarea
              value={reason}
              required
              disabled={Boolean(command) || sending}
              onChange={(e) => setReason(e.target.value)}
            />
          </label>
          <p>
            提交版本 r{command?.expected_revision ?? row.revision}
            {command && (
              <> · 待确认操作 {command.operation_id}，重试保留原版本和原因。</>
            )}
          </p>
          <button
            disabled={
              sending ||
              result?.state === "pending" ||
              (!command && !reason.trim())
            }
          >
            {sending ? "正在提交…" : command ? "重试同一复查操作" : "提交复查"}
          </button>
        </form>
      )}
      {error && <p role="alert">{error}</p>}
      <h3>复查请求记录</h3>
      <p>按请求身份分页，包含排队、完成和失效的记录。</p>
      {history.isPending && <p>正在读取请求…</p>}
      {history.error && <p role="alert">{history.error.message}</p>}
      {history.data && !history.error && (
        <>
          {history.data.items.length === 0 && <p>本页无复查请求。</p>}
          {history.data.items.map((r) => (
            <article className="shield-check-history" key={r.id}>
              <strong>
                {states[r.state]} · r{r.command.expected_revision}
              </strong>
              <p>
                {formatTime(r.created_at, mode)} · {r.command.operator_id} ·{" "}
                {r.command.reason}
              </p>
              <code>{r.command.operation_id}</code>
              {r.result && (
                <CheckResult
                  key={r.result.started_at + r.result.finished_at}
                  value={r.result}
                />
              )}
              <details>
                <summary>请求详情</summary>
                <JsonViewer value={r} />
              </details>
            </article>
          ))}
          <div className="merge-pagination">
            <button disabled={!after} onClick={() => setAfter("")}>
              请求首页
            </button>
            <button
              disabled={!history.data.next || history.isFetching}
              onClick={() => setAfter(history.data.next)}
            >
              后续请求
            </button>
          </div>
        </>
      )}
    </section>
  );
}
