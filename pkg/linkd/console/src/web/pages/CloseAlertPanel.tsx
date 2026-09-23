import { useState } from "react";
import { z } from "zod";
import type { EntityItem } from "../../shared/contracts";
import { consoleURL } from "../base-path";

const commandSchema = z.object({
  bk_tenant_id: z.string(),
  operation_id: z.string().uuid(),
  reason: z.string(),
  effective_at: z.string().datetime(),
});
type Command = z.infer<typeof commandSchema>;

export function CloseAlertPanel({
  item,
  onClosed,
}: {
  item: EntityItem;
  onClosed: (alert: Record<string, unknown>) => void;
}) {
  const storageKey = `linkd:close:${encodeURIComponent(item.tenantId)}:${encodeURIComponent(item.id)}`;
  const [command, setCommand] = useState<Command | undefined>(() => {
    try {
      const parsed = commandSchema.safeParse(
        JSON.parse(sessionStorage.getItem(storageKey) ?? "null"),
      );
      return parsed.success && parsed.data.bk_tenant_id === item.tenantId
        ? parsed.data
        : undefined;
    } catch {
      return undefined;
    }
  });
  const [open, setOpen] = useState(Boolean(command));
  const [reason, setReason] = useState(command?.reason ?? "");
  const [pending, setPending] = useState(false);
  const [error, setError] = useState("");
  const [success, setSuccess] = useState(false);
  const active = item.payload.status === "active";
  async function close() {
    setPending(true);
    setError("");
    try {
      if (
        !reason.trim() ||
        new TextEncoder().encode(reason.trim()).length > 256
      )
        throw new Error("请填写关闭原因，最多 256 字节。");
      const request = command ?? {
        bk_tenant_id: item.tenantId,
        operation_id: crypto.randomUUID(),
        reason: reason.trim(),
        effective_at: new Date().toISOString(),
      };
      // 一次明确用户操作只生成一次幂等键；超时或重新打开详情仍复用原命令。
      sessionStorage.setItem(storageKey, JSON.stringify(request));
      setCommand(request);
      const response = await fetch(
        consoleURL(`/local-api/alerts/${encodeURIComponent(item.id)}/close`),
        {
          method: "POST",
          headers: { "content-type": "application/json" },
          body: JSON.stringify(request),
          signal: AbortSignal.timeout(20000),
        },
      );
      const data = (await response.json()) as {
        alert?: Record<string, unknown>;
        error?: { message?: string };
      };
      if (!response.ok && [400, 401, 403, 404, 503].includes(response.status)) {
        sessionStorage.removeItem(storageKey);
        setCommand(undefined);
      }
      if (!response.ok || !data.alert)
        throw new Error(
          data.error?.message ?? "关闭结果尚未确认，请重试同一操作。",
        );
      sessionStorage.removeItem(storageKey);
      setCommand(undefined);
      setSuccess(true);
      setOpen(false);
      onClosed(data.alert);
    } catch (err) {
      setError(err instanceof Error ? err.message : "关闭失败");
    } finally {
      setPending(false);
    }
  }
  return (
    <section className="explorer-close" aria-label="主动关闭告警">
      <div className="explorer-query-title">
        <strong>告警操作</strong>
        <span>
          {active
            ? "关闭当前生命周期，保留关联事件和操作记录。"
            : "此告警已进入终态。"}
        </span>
        {!open && active && (
          <button className="explorer-danger" onClick={() => setOpen(true)}>
            主动关闭
          </button>
        )}
      </div>
      {success && (
        <p role="status">
          告警已关闭。Hook
          的下游处理结果请查看操作流水；存储刷新前流水可能稍后可见。
        </p>
      )}
      {open && (
        <form
          onSubmit={(event) => {
            event.preventDefault();
            void close();
          }}
        >
          <p>
            将关闭租户 <strong>{item.tenantId}</strong> 的告警{" "}
            <code>{item.id}</code>，并执行当前已发布来源的 FinalHook。
          </p>
          <label>
            关闭原因
            <textarea
              value={reason}
              disabled={Boolean(command) || pending}
              required
              onChange={(e) => setReason(e.target.value)}
            />
          </label>
          {command && (
            <p className="muted">
              待确认操作：{command.operation_id}
              。重试保持原原因和时间；即使状态已关闭，也可补齐未完成流水。
            </p>
          )}
          {error && (
            <p className="error-banner" role="alert">
              {error}
            </p>
          )}
          <div className="explorer-close-actions">
            <button
              type="button"
              disabled={pending}
              onClick={() => setOpen(false)}
            >
              收起
            </button>
            <button
              className="explorer-danger"
              type="submit"
              disabled={pending || (!active && !command)}
            >
              {pending
                ? "正在关闭…"
                : command
                  ? "重试同一关闭操作"
                  : "确认关闭告警"}
            </button>
          </div>
        </form>
      )}
      {!open && command && (
        <button onClick={() => setOpen(true)}>继续待确认的关闭操作</button>
      )}
    </section>
  );
}
