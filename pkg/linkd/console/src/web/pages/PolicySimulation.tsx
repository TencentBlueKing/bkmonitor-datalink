import { useEffect, useRef, useState, type FormEvent } from "react";
import { parse as parseYAML } from "yaml";
import type { PolicyRelease } from "../../shared/policies";
import {
  simulationRequestSchema,
  type SimulationResponse,
} from "../../shared/policy-simulation";
import { simulatePolicy } from "../api";
import { JsonViewer } from "../components/JsonViewer";
import { formatTime, useTimeMode } from "../time";

export function PolicySimulation({ release }: { release: PolicyRelease }) {
  const [steps, setSteps] = useState("[]"),
    [eventID, setEventID] = useState(""),
    [at, setAt] = useState(""),
    [seconds, setSeconds] = useState(60);
  const [custom, setCustom] = useState(false),
    [spec, setSpec] = useState(JSON.stringify(release.spec, null, 2));
  const [error, setError] = useState(""),
    [result, setResult] = useState<SimulationResponse>(),
    [busy, setBusy] = useState(false);
  const abort = useRef<AbortController | undefined>(undefined),
    ticket = useRef(0);
  const mode = useTimeMode();
  useEffect(
    () => () => {
      ticket.current++;
      abort.current?.abort();
    },
    [],
  );
  function reset() {
    ticket.current++;
    abort.current?.abort();
    setBusy(false);
    setResult(undefined);
    setError("");
  }
  function parse(raw: string): unknown {
    if (new TextEncoder().encode(raw).byteLength > 3 << 20)
      throw new Error("输入不能超过 3 MiB");
    parseYAML(raw, { uniqueKeys: true, maxAliasCount: 0 });
    return JSON.parse(raw);
  }
  function append(tick: boolean) {
    reset();
    try {
      const input = parse(steps);
      if (!Array.isArray(input) || input.length >= 128)
        throw new Error("最多 128 步");
      const last = input.at(-1) as { at?: string } | undefined;
      const value =
        tick && last?.at
          ? new Date(Date.parse(last.at) + seconds * 1000)
          : new Date(at);
      if (
        !Number.isFinite(value.getTime()) ||
        (!tick && !eventID.trim()) ||
        !Number.isFinite(seconds) ||
        seconds < 0
      )
        throw new Error("请填写合法时间和事件 ID，推进秒数不得为负");
      input.push({
        at: value.toISOString(),
        ...(!tick ? { event_id: eventID.trim() } : {}),
      });
      setSteps(JSON.stringify(input, null, 2));
    } catch (e) {
      setError(e instanceof Error ? e.message : "步骤无效");
    }
  }
  async function run(e: FormEvent) {
    e.preventDefault();
    reset();
    const current = ticket.current;
    const controller = new AbortController();
    abort.current = controller;
    setBusy(true);
    try {
      const input = simulationRequestSchema.parse({
        bk_tenant_id: release.bk_tenant_id,
        type: release.type,
        ...(custom
          ? { spec: parse(spec) }
          : { id: release.id, version: release.version }),
        steps: parse(steps),
      });
      const output = await simulatePolicy(input, controller.signal);
      if (!custom && output.compiled.digest !== release.compiled.digest)
        throw new Error("模拟摘要与选定版本不一致");
      if (current === ticket.current) setResult(output);
    } catch (e) {
      if (current === ticket.current)
        setError(e instanceof Error ? e.message : "模拟失败");
    } finally {
      if (current === ticket.current) setBusy(false);
    }
  }
  if (release.type === "shield") return null;
  return (
    <section className="policy-preview" aria-label="策略状态模拟">
      <h3>计数与窗口模拟</h3>
      <p className="policy-note">
        从空状态开始，复用正式生命周期与策略裁决。仅使用已完成的 Enrich
        结果；每次运行相互隔离，不写生产数据。仅模拟当前策略，目标范围使用当前资源查询结果。
      </p>
      <div className="policy-input-row">
        <label>
          事件 ID
          <input
            aria-label="模拟事件 ID"
            value={eventID}
            onChange={(e) => setEventID(e.target.value)}
          />
        </label>
        <label>
          判定时间（ISO 8601）
          <input
            aria-label="模拟判定时间"
            value={at}
            onChange={(e) => setAt(e.target.value)}
          />
        </label>
        <button type="button" onClick={() => append(false)}>
          添加事件步骤
        </button>
        <label>
          推进秒数
          <input
            aria-label="推进秒数"
            type="number"
            min={0}
            max={2592000}
            value={seconds}
            onChange={(e) => setSeconds(Number(e.target.value))}
          />
        </label>
        <button type="button" onClick={() => append(true)}>
          推进虚拟时间
        </button>
      </div>
      <form onSubmit={run}>
        <label>
          步骤 JSON
          <textarea
            aria-label="模拟步骤 JSON"
            rows={10}
            value={steps}
            onChange={(e) => {
              reset();
              setSteps(e.target.value);
            }}
          />
        </label>
        <p className="policy-note">
          每步包含 at 和 event_id 或完整 event；仅有 at
          表示检查窗口。时间须递增，最多 128 步、跨度 30
          天。同一时刻先检查到期窗口，再处理事件。
        </p>
        <label className="policy-check">
          <input
            type="checkbox"
            checked={custom}
            onChange={(e) => {
              reset();
              setCustom(e.target.checked);
            }}
          />
          使用临时策略配置
        </label>
        {custom && (
          <textarea
            aria-label="模拟临时配置"
            rows={8}
            value={spec}
            onChange={(e) => {
              reset();
              setSpec(e.target.value);
            }}
          />
        )}
        <button disabled={busy} type="submit">
          {busy ? "正在模拟…" : "运行隔离模拟"}
        </button>
        {busy && (
          <button type="button" onClick={reset}>
            取消模拟
          </button>
        )}
      </form>
      {error && (
        <p className="error-banner" role="alert">
          {error}
        </p>
      )}
      {result && (
        <div aria-label="模拟轨迹">
          <p>
            共 {result.steps.length}{" "}
            步。合并成功表示虚拟窗口条件满足，不代表父告警、KAC
            同步或处置已经执行。
          </p>
          {result.steps.map((step, i) => (
            <section className="simulation-step" key={i}>
              <h4>
                {i + 1}. {formatTime(step.at, mode)} ·{" "}
                {step.event_id ?? "推进时间"}
              </h4>
              <p>
                处理结果：<code>{step.outcome}</code> · 虚拟 Alert{" "}
                {step.alerts.length} 个
              </p>

              {step.decision?.suppression?.bypass_reason && (
                <p>抑制绕过：{step.decision.suppression.bypass_reason}</p>
              )}
              {step.decision?.suppression?.evaluations?.map(
                (evaluation, index) => (
                  <div key={index}>
                    {evaluation.steps?.map((decision, number) => (
                      <p key={number}>
                        {evaluation.severity} · {decision.scheme} ·{" "}
                        {decision.count !== undefined
                          ? `计数 ${decision.count} / 阈值 ${decision.threshold}`
                          : decision.outcome}
                        {decision.reason_code && ` · ${decision.reason_code}`}
                      </p>
                    ))}
                  </div>
                ),
              )}
              {step.windows.map((w) => (
                <p key={w.id}>
                  窗口
                  {w.outcome === "succeeded" ? "合并条件满足" : "到期未满足"} ·
                  成员 {w.members.length} · 截止 {formatTime(w.deadline, mode)}
                </p>
              ))}
              <details>
                <summary>计数、策略诊断和状态快照</summary>
                <JsonViewer value={step} />
              </details>
            </section>
          ))}
        </div>
      )}
    </section>
  );
}
