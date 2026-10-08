import { useState } from "react";
import { Link } from "react-router-dom";
import { JsonViewer } from "../components/JsonViewer";
import { formatTime, useTimeMode } from "../time";
import { display, record } from "./explorer";

const outcomes: Record<string, string> = {
  passed: "通过门槛",
  suppressed: "被抑制",
  not_matched: "未命中",
  skipped: "跳过",
  reserved: "候选占位，待放行确认",
  released: "占位已释放",
};
const schemes: Record<string, string> = {
  clip: "防抖",
  aggregation: "关联聚合",
};
const array = (v: unknown): unknown[] => (Array.isArray(v) ? v : []);
const positive = (v: unknown): v is number =>
  typeof v === "number" && Number.isSafeInteger(v) && v > 0;

// 只展示 Event 已保存的裁决，不读取或改变 Redis 计数，也不从这里推断 Alert 当前资格。
export function EventSuppressionState({
  payload,
  tenant,
  onNavigate,
}: {
  payload: Record<string, unknown>;
  tenant: string;
  onNavigate?: () => void;
}) {
  const suppression = record(
    record(record(payload._processing).policy_decision).suppression,
  );
  const evaluations = array(suppression.evaluations);
  return (
    <section className="alert-policy-state" aria-label="事件抑制记录">
      <h3>事件抑制记录</h3>
      <p className="explorer-context">
        以下是本 Event 当时保存的裁决，计数和窗口不是当前 Redis
        状态。后续屏蔽、合并及处置资格请查看关联 Alert。
      </p>
      {suppression.bypass_reason === "active_alert" ? (
        <p>
          已关联活动 Alert，绕过防抖和关联聚合。
          <AlertReference
            tenant={tenant}
            id={suppression.active_alert_id}
            onNavigate={onNavigate}
          />
        </p>
      ) : suppression.bypass_reason === "no_trigger" ? (
        <p>没有触发判定，不执行新告警抑制。</p>
      ) : suppression.bypass_reason ? (
        <p>未知绕过原因：{display(suppression.bypass_reason)}</p>
      ) : evaluations.length ? (
        evaluations.map((value, i) => (
          <Evaluation
            key={i}
            value={value}
            tenant={tenant}
            onNavigate={onNavigate}
          />
        ))
      ) : (
        <p className="muted">尚无已保存的抑制裁决。</p>
      )}
    </section>
  );
}

function AlertReference({
  tenant,
  id,
  onNavigate,
}: {
  tenant: string;
  id: unknown;
  onNavigate?: () => void;
}) {
  return tenant && typeof id === "string" && id ? (
    <Link
      onClick={onNavigate}
      to={
        "/explore/alerts?" + new URLSearchParams({ bk_tenant_id: tenant, id })
      }
    >
      {id} ↗
    </Link>
  ) : (
    <span>未提供关联 Alert</span>
  );
}

function Evaluation({
  value,
  tenant,
  onNavigate,
}: {
  value: unknown;
  tenant: string;
  onNavigate?: () => void;
}) {
  const e = record(value);
  const steps = array(e.steps);
  const [offset, setOffset] = useState(0);
  const mode = useTimeMode();
  const start = Math.min(
    offset,
    Math.max(0, Math.ceil(steps.length / 16) - 1) * 16,
  );
  const millis = (v: unknown) =>
    typeof v === "number" && Number.isSafeInteger(v) && v >= 0 && v <= 2 ** 46
      ? formatTime(new Date(v).toISOString(), mode)
      : "未提供有效时间";
  const state =
    e.suppressed === true
      ? "被抑制"
      : e.suppressed === false
        ? "未被抑制"
        : "结果未知";
  return (
    <section aria-label={"抑制判定 " + display(e.severity)}>
      <h4>
        {display(e.severity)} · {state}
      </h4>
      {e.reason_code ? <p>原因：{display(e.reason_code)}</p> : null}
      {e.related_alert_id ? (
        <p>
          关联主：
          <AlertReference
            tenant={tenant}
            id={e.related_alert_id}
            onNavigate={onNavigate}
          />
        </p>
      ) : null}
      {!steps.length ? (
        <p className="muted">没有执行步骤。</p>
      ) : (
        <>
          <div className="alert-policy-table">
            <table
              className="explorer-evaluation-table"
              aria-label={"抑制步骤 " + display(e.severity)}
            >
              <thead>
                <tr>
                  <th>策略 / 版本</th>
                  <th>方式 / 结果</th>
                  <th>当时的计数或窗口</th>
                  <th>原因与证据</th>
                </tr>
              </thead>
              <tbody>
                {steps.slice(start, start + 16).map((raw, i) => {
                  const step = record(raw),
                    policy = record(step.policy),
                    window = record(step.window);
                  const countValid =
                    typeof step.counter_id === "string" &&
                    step.counter_id.length > 0 &&
                    positive(step.count) &&
                    positive(step.threshold) &&
                    positive(step.duration_seconds);
                  return (
                    <tr key={start + i}>
                      <td>
                        {typeof policy.id === "string" &&
                        policy.id &&
                        positive(policy.version) &&
                        tenant ? (
                          <Link
                            onClick={onNavigate}
                            to={
                              "/policies?" +
                              new URLSearchParams({
                                bk_tenant_id: tenant,
                                type: "suppression",
                                id: policy.id,
                                version: String(policy.version),
                              })
                            }
                          >
                            {policy.id} / {policy.version} ↗
                          </Link>
                        ) : (
                          "未提供有效策略引用"
                        )}
                      </td>
                      <td>
                        <span>
                          {schemes[display(step.scheme)] ??
                            display(step.scheme)}
                        </span>
                        <br />
                        <span>
                          {outcomes[display(step.outcome)] ??
                            "未知结果：" + display(step.outcome)}
                        </span>
                      </td>
                      <td>
                        {step.scheme === "clip" ? (
                          countValid ? (
                            <>
                              {display(step.count)} / {display(step.threshold)}{" "}
                              次 · {display(step.duration_seconds)} 秒
                              <div>{millis(step.evaluated_at_ms)}</div>
                            </>
                          ) : (
                            "未提供有效计数"
                          )
                        ) : step.window ? (
                          <>
                            <div>
                              {step.outcome === "suppressed"
                                ? "关联主"
                                : "候选"}
                              ：
                              <AlertReference
                                tenant={tenant}
                                id={window.owner_alert_id}
                                onNavigate={onNavigate}
                              />
                            </div>
                            <div>窗口起点：{millis(window.started_at_ms)}</div>
                            <div>固定截止：{millis(window.expires_at_ms)}</div>
                          </>
                        ) : (
                          "未建立窗口"
                        )}
                      </td>
                      <td>
                        {display(step.reason_code)}
                        <details>
                          <summary>裁决证据</summary>
                          <JsonViewer value={step} />
                        </details>
                      </td>
                    </tr>
                  );
                })}
              </tbody>
            </table>
          </div>
          {steps.length > 16 && (
            <div className="explorer-heading-actions">
              <button
                disabled={start === 0}
                onClick={() => setOffset(start - 16)}
              >
                上页步骤
              </button>
              <span>
                {start + 1}–{Math.min(start + 16, steps.length)} /{" "}
                {steps.length}
              </span>
              <button
                disabled={start + 16 >= steps.length}
                onClick={() => setOffset(start + 16)}
              >
                下页步骤
              </button>
            </div>
          )}
        </>
      )}
    </section>
  );
}
