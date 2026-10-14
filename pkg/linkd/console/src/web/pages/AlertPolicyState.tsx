import { JsonViewer } from "../components/JsonViewer";
import { formatTime, useTimeMode } from "../time";
import { display, record } from "./explorer";

const mergeStates: Record<string, string> = {
  none: "未进入合并",
  pending: "等待合并裁决",
  merged: "已建立合并关系",
  released: "已释放",
};
const array = (value: unknown): unknown[] =>
  Array.isArray(value) ? value : [];
const integer = (value: unknown, minimum: number): value is number =>
  typeof value === "number" && Number.isSafeInteger(value) && value >= minimum;

// 历史放行事实不表示当前仍然具备动作资格。
export function AlertPolicyState({
  payload,
}: {
  payload: Record<string, unknown>;
}) {
  const mode = useTimeMode();
  const shield = record(payload.shield);
  const merge = record(payload.merge);
  const admission = record(payload.admission);
  const pending = array(merge.pending);
  const relations = array(merge.relation_ids);
  const bindings = array(shield.bindings);
  const time = (value: unknown) => formatTime(display(value), mode);
  const mergeState =
    payload.merge == null
      ? "未参与合并"
      : merge.role === "aggregate"
        ? merge.relations_ready === true
          ? "合并主告警 · 关系已就绪"
          : "合并主告警 · 等待关系就绪"
        : (mergeStates[display(merge.state)] ?? "合并状态未知");
  return (
    <section className="alert-policy-state" aria-label="策略状态">
      <h3>策略状态</h3>
      <dl className="explorer-facts">
        <div>
          <dt>业务版本</dt>
          <dd>
            {integer(payload.revision, 1) ? payload.revision : "未提供有效版本"}
          </dd>
        </div>
        <div>
          <dt>屏蔽状态</dt>
          <dd>
            {shield.active === true
              ? "屏蔽中"
              : shield.active === false
                ? "未屏蔽"
                : "未提供屏蔽状态"}
          </dd>
        </div>
        <div>
          <dt>合并状态</dt>
          <dd>{mergeState}</dd>
        </div>
        <div>
          <dt>下次屏蔽复查</dt>
          <dd>{time(shield.next_check_at)}</dd>
        </div>
        <div>
          <dt>最近放行</dt>
          <dd>
            {admission.admitted_at
              ? time(admission.admitted_at)
              : "尚无放行记录"}
            {admission.admitted_at ? (
              <div>放行级别：{display(admission.severity)}</div>
            ) : null}
          </dd>
        </div>
        <div>
          <dt>合并引用</dt>
          <dd>
            {pending.length} 个等待窗口 · {relations.length} 条关系
          </dd>
        </div>
      </dl>
      <p className="explorer-context">
        最近放行仅记录历史动作。解除屏蔽或父合并关系后，等待下一条触发事件重新判断处置。
      </p>
      {bindings.length > 0 && (
        <details>
          <summary>屏蔽绑定（{bindings.length}）</summary>
          <JsonViewer value={bindings} />
        </details>
      )}
      {pending.length > 0 && (
        <details>
          <summary>合并等待窗口（{pending.length}）</summary>
          <div className="alert-policy-table">
            <table className="explorer-evaluation-table">
              <thead>
                <tr>
                  <th>策略 / 版本</th>
                  <th>窗口 ID</th>
                  <th>截止时间</th>
                </tr>
              </thead>
              <tbody>
                {pending.map((value, i) => {
                  const wait = record(value);
                  const policy = record(wait.policy);
                  return (
                    <tr key={i}>
                      <td>
                        {display(policy.id)} / {display(policy.version)}
                      </td>
                      <td className="mono">{display(wait.window_id)}</td>
                      <td>{time(wait.deadline)}</td>
                    </tr>
                  );
                })}
              </tbody>
            </table>
          </div>
        </details>
      )}
      {relations.length > 0 && (
        <details>
          <summary>合并关系引用（{relations.length}）</summary>
          <JsonViewer value={relations} />
        </details>
      )}
      {payload.policy_change || payload.merge_change ? (
        <details>
          <summary>待完成的策略变更</summary>
          <JsonViewer
            value={{
              policy_change: payload.policy_change ?? null,
              merge_change: payload.merge_change ?? null,
            }}
          />
        </details>
      ) : null}
    </section>
  );
}
