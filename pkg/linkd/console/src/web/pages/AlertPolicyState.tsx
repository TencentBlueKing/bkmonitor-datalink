import { JsonViewer } from "../components/JsonViewer";
import { Link } from "react-router-dom";
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

// 水位缺失/非法时显示未知，不把空值当成零或把状态同步当成新的处置资格。
export function AlertPolicyState({
  payload,
}: {
  payload: Record<string, unknown>;
}) {
  const mode = useTimeMode();
  const shield = record(payload.shield);
  const merge = record(payload.merge);
  const admission = record(payload.admission);
  const actionPending = record(payload.action_pending);
  const targets = Object.entries(
    record(record(payload.projection).targets),
  ).sort(([a], [b]) => a.localeCompare(b));
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
    <section className="alert-policy-state" aria-label="策略与投影状态">
      <h3>策略与投影状态</h3>
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
      <h4>动作入队</h4>
      <p>
        {payload.action_pending == null
          ? "当前无入队待办；这不表示接收端已经受理。"
          : integer(actionPending.revision, 1) &&
              actionPending.revision === payload.revision
            ? "原获准动作尚待确认全部目标入队，后续业务变更需先补齐此意图。"
            : "入队待办格式异常，请核对原始记录。"}
      </p>
      {payload.action_pending != null && (
        <details>
          <summary>原动作入队意图</summary>
          <JsonViewer value={payload.action_pending} />
        </details>
      )}
      {typeof payload.bk_tenant_id === "string" &&
        /^[a-zA-Z0-9_-]{1,64}$/.test(payload.bk_tenant_id) &&
        typeof payload.alert_id === "string" &&
        payload.alert_id.length > 0 &&
        payload.alert_id.length <= 160 && (
          <Link
            to={
              "/action-deliveries?" +
              new URLSearchParams({
                bk_tenant_id: payload.bk_tenant_id,
                alert_id: payload.alert_id,
              })
            }
          >
            查看此告警的动作投递
          </Link>
        )}
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
      <h4>投影同步</h4>
      {!targets.length ? (
        <p className="explorer-context">
          {payload.projection == null ? "未提供投影水位" : "未绑定投影目标"}
        </p>
      ) : (
        <div className="alert-policy-table">
          <table
            className="explorer-evaluation-table"
            aria-label="投影目标水位"
          >
            <thead>
              <tr>
                <th>目标</th>
                <th>目标来源版本</th>
                <th>动作投递</th>
                <th>要求版本</th>
                <th>已确认版本</th>
                <th>同步状态</th>
                <th>最近确认</th>
              </tr>
            </thead>
            <tbody>
              {targets.map(([id, raw]) => {
                const target = record(raw);
                const valid =
                  integer(payload.revision, 1) &&
                  integer(target.source_version, 1) &&
                  integer(target.required_revision, 1) &&
                  integer(target.synced_revision, 0) &&
                  target.required_revision <= payload.revision &&
                  target.synced_revision <= target.required_revision &&
                  (target.synced_revision === 0
                    ? target.synced_at == null
                    : typeof target.synced_at === "string" &&
                      Number.isFinite(Date.parse(target.synced_at)));
                const synchronized =
                  valid && target.required_revision === target.synced_revision;
                return (
                  <tr key={id}>
                    <td>{id}</td>
                    <td>{display(target.source_version)}</td>
                    <td>
                      {target.action_enabled === true
                        ? "启用"
                        : target.action_enabled == null ||
                            target.action_enabled === false
                          ? "仅同步状态"
                          : "未知"}
                    </td>
                    <td>{display(target.required_revision)}</td>
                    <td>{display(target.synced_revision)}</td>
                    <td>
                      {!valid ? "水位异常" : synchronized ? "已确认" : "待同步"}
                    </td>
                    <td>{time(target.synced_at)}</td>
                  </tr>
                );
              })}
            </tbody>
          </table>
        </div>
      )}
    </section>
  );
}
