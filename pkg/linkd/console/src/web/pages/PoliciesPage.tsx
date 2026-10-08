import { PolicySimulation } from "./PolicySimulation";
import { PolicyStatistics } from "./PolicyStatistics";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { type FormEvent, useEffect, useRef, useState } from "react";
import { useSearchParams } from "react-router-dom";
import { parse as parseYAML } from "yaml";
import {
  policyKindSchema,
  policyListQuerySchema,
  policyPreviewRequestSchema,
  type PolicyKind,
  type PolicyRecord,
  type PolicyRelease,
  type PolicyPreview,
} from "../../shared/policies";
import {
  getPolicy,
  getPolicyRelease,
  listPolicies,
  previewPolicy,
} from "../api";
import { JsonViewer } from "../components/JsonViewer";
import { PolicyConfigLink } from "../components/PolicyConfigLink";
import { formatTime, useTimeMode } from "../time";
import { display, inputTime, parseInputTime } from "./explorer";
import "./policies.css";

const names: Record<PolicyKind, string> = {
  suppression: "事件抑制",
  shield: "告警屏蔽",
  merge: "告警合并",
};
const positiveVersion = (raw: string | null) => {
  const n = Number(raw);
  return Number.isSafeInteger(n) && n > 0 ? n : undefined;
};
const reasonNames: Record<string, string> = {
  inactive: "配置停用或当前不在生效时段",
  terminal_bypass: "终态输入跳过策略",
  outside_business_scope: "不在业务范围",
  outside_target_model: "对象模型不匹配",
  outside_target_instances: "不在目标实例中",
  business_field_missing: "缺少业务字段",
  instance_field_missing: "缺少实例身份",
  business_scope_unavailable: "业务范围查询不可用",
  target_resolution_failed: "目标解析失败",
  condition_unavailable: "条件依赖或字段不可求值",
  conditions_not_matched: "条件未命中",
  group_field_missing: "缺少分组字段",
  group_field_unavailable: "分组字段不可求值",
  target_reader_unavailable: "目标查询未配置",
};
function outcome(evaluated: boolean, matched: boolean) {
  return !evaluated ? "无法判定" : matched ? "匹配" : "未匹配";
}
function canonical(value: unknown): string | undefined {
  const normalize = (v: unknown): unknown => {
    if (Array.isArray(v)) return v.map(normalize);
    if (v !== null && typeof v === "object")
      return Object.fromEntries(
        Object.entries(v)
          .sort(([a], [b]) => a.localeCompare(b))
          .map(([k, x]) => [k, normalize(x)]),
      );
    return v;
  };
  return JSON.stringify(normalize(value));
}

export function PoliciesPage() {
  const [params, setParams] = useSearchParams();
  const client = useQueryClient();
  const tenant = params.get("bk_tenant_id") ?? "";
  const kind = policyKindSchema.catch("suppression").parse(params.get("type"));
  const enabled = params.get("is_enable");
  const scope = policyListQuerySchema.safeParse({
    bk_tenant_id: tenant,
    type: kind,
    after: params.get("after") ?? "",
    limit: 8,
    ...(enabled === "true" || enabled === "false"
      ? { is_enable: enabled }
      : {}),
  });
  const id = params.get("id") ?? "";
  const [formError, setFormError] = useState("");
  const list = useQuery({
    queryKey: ["policy-list", tenant, kind, enabled, params.get("after")],
    queryFn: ({ signal }) => {
      if (!scope.success) throw new Error("请选择合法租户");
      return listPolicies(scope.data, signal);
    },
    enabled: scope.success,
  });
  const detail = useQuery({
    queryKey: ["policy-detail", tenant, kind, id],
    queryFn: ({ signal }) => getPolicy(tenant, kind, id, signal),
    enabled: scope.success && /^[a-zA-Z0-9_-]{1,80}$/.test(id),
  });
  function search(e: FormEvent<HTMLFormElement>) {
    e.preventDefault();
    const data = new FormData(e.currentTarget);
    const next = {
      bk_tenant_id: String(data.get("tenant") ?? "").trim(),
      type: String(data.get("type")),
      after: "",
      limit: 8,
      ...(data.get("enabled")
        ? { is_enable: String(data.get("enabled")) }
        : {}),
    };
    const parsed = policyListQuerySchema.safeParse(next);
    if (!parsed.success) {
      setFormError("租户为必填，使用字母、数字、下划线或连字符，最多 64 字符");
      return;
    }
    setFormError("");
    const query = new URLSearchParams({
      bk_tenant_id: parsed.data.bk_tenant_id,
      type: parsed.data.type,
    });
    if (parsed.data.is_enable) query.set("is_enable", parsed.data.is_enable);
    setParams(query);
    void client.invalidateQueries({ queryKey: ["policy-list"] });
  }
  function select(row: PolicyRecord) {
    const next = new URLSearchParams(params);
    next.set("id", row.id);
    next.delete("version");
    setParams(next);
  }
  return (
    <div className="policies-page">
      <header className="page-heading">
        <div>
          <p className="eyebrow">ALARM POLICIES</p>
          <h1>告警策略</h1>
          <p>
            按租户查看跨来源策略，核对发布版本并解释匹配结果。配置由 KAC 维护。
          </p>
        </div>
        <button
          onClick={() => {
            void list.refetch();
            if (id) {
              void detail.refetch();
              void client.invalidateQueries({
                queryKey: ["policy-config-link", tenant, kind, id],
              });
            }
          }}
          disabled={!scope.success}
        >
          刷新策略
        </button>
      </header>
      <form
        className="policy-filters"
        key={tenant + kind + enabled}
        onSubmit={search}
      >
        <label>
          租户
          <input
            name="tenant"
            defaultValue={tenant}
            aria-label="策略租户"
            autoComplete="off"
          />
        </label>
        <label>
          策略类型
          <select name="type" defaultValue={kind} aria-label="策略类型">
            {Object.entries(names).map(([key, value]) => (
              <option value={key} key={key}>
                {value}
              </option>
            ))}
          </select>
        </label>
        <label>
          配置启用
          <select
            name="enabled"
            defaultValue={enabled ?? ""}
            aria-label="配置启用"
          >
            <option value="">全部</option>
            <option value="true">启用</option>
            <option value="false">停用 / 删除 / 未发布</option>
          </select>
        </label>
        <button type="submit">查询策略</button>
      </form>
      {formError && (
        <p className="error-banner" role="alert">
          {formError}
        </p>
      )}
      {!scope.success ? (
        <p className="policy-empty">
          填写租户后查询策略。策略可跨 EventSource 生效。
        </p>
      ) : (
        <div className="policy-layout">
          <section className="policy-list" aria-label="策略列表">
            <h2>
              {names[kind]}{" "}
              <small>本页 {list.data?.items.length ?? "—"} 条</small>
            </h2>
            {list.isError && (
              <p className="error-banner" role="alert">
                {list.error.message}
              </p>
            )}
            {list.isFetching && <p role="status">正在读取策略…</p>}
            {list.data && (
              <>
                {!list.data.items.length && (
                  <p className="policy-empty">
                    {list.data.next
                      ? "当前扫描页没有符合筛选的策略，可继续下一页。"
                      : "本页没有策略。"}
                  </p>
                )}
                {list.data.items.map((row) => (
                  <button
                    className={
                      "policy-row" + (id === row.id ? " selected" : "")
                    }
                    key={row.id}
                    onClick={() => select(row)}
                    aria-pressed={id === row.id}
                  >
                    <strong>
                      {display(
                        row.spec?.name ?? row.pending?.spec.name ?? row.id,
                      )}
                    </strong>
                    <span className="mono">{row.id}</span>
                    <span>
                      {row.deleted
                        ? "已删除"
                        : row.spec?.is_enable === true
                          ? "配置启用"
                          : "配置未启用"}{" "}
                      · 已发布 v{row.published}
                      {row.pending ? " · 发布待完成" : ""}
                    </span>
                    <small>业务范围 {display(row.spec?.space_code)}</small>
                  </button>
                ))}
                <div className="policy-pagination">
                  <button
                    disabled={!params.get("after")}
                    onClick={() => {
                      const next = new URLSearchParams(params);
                      next.delete("after");
                      setParams(next);
                    }}
                  >
                    首页
                  </button>
                  <button
                    disabled={!list.data.next || list.isFetching}
                    onClick={() => {
                      const next = new URLSearchParams(params);
                      next.set("after", list.data!.next);
                      setParams(next);
                    }}
                  >
                    下一页
                  </button>
                </div>
              </>
            )}
            {list.data && (
              <PolicyStatistics
                tenant={tenant}
                kind={kind}
                ids={list.data.items.map((row) => row.id)}
              />
            )}
          </section>
          <section className="policy-detail" aria-label="策略详情">
            {!id && (
              <p className="policy-empty">选择策略，查看配置与只读匹配预览。</p>
            )}
            {detail.isFetching && <p role="status">正在读取策略详情…</p>}
            {detail.isError && (
              <p className="error-banner" role="alert">
                {detail.error.message}
              </p>
            )}
            {detail.data && (
              <PolicyDetail
                key={tenant + ":" + kind + ":" + id}
                record={detail.data}
                version={positiveVersion(params.get("version"))}
                onVersion={(value) => {
                  const next = new URLSearchParams(params);
                  next.set("version", String(value));
                  setParams(next);
                }}
              />
            )}
          </section>
        </div>
      )}
    </div>
  );
}

function PolicyDetail({
  record,
  version,
  onVersion,
}: {
  record: PolicyRecord;
  version?: number;
  onVersion: (version: number) => void;
}) {
  const mode = useTimeMode();
  const selected = version ?? record.published;
  const [compare, setCompare] = useState<number>();
  const release = useQuery({
    queryKey: [
      "policy-release",
      record.bk_tenant_id,
      record.type,
      record.id,
      selected,
    ],
    queryFn: ({ signal }) =>
      getPolicyRelease(
        record.bk_tenant_id,
        record.type,
        record.id,
        selected,
        signal,
      ),
    enabled: selected > 0,
  });
  const previous = useQuery({
    queryKey: [
      "policy-release",
      record.bk_tenant_id,
      record.type,
      record.id,
      compare,
    ],
    queryFn: ({ signal }) =>
      getPolicyRelease(
        record.bk_tenant_id,
        record.type,
        record.id,
        compare!,
        signal,
      ),
    enabled: compare !== undefined,
  });
  const changed =
    release.data && previous.data
      ? [
          ...new Set([
            ...Object.keys(previous.data.spec),
            ...Object.keys(release.data.spec),
          ]),
        ]
          .sort()
          .filter(
            (key) =>
              canonical(previous.data!.spec[key]) !==
              canonical(release.data!.spec[key]),
          )
      : [];
  return (
    <>
      <h2>
        {display(record.spec?.name ?? record.pending?.spec.name ?? record.id)}
      </h2>
      <p className="mono">
        {record.bk_tenant_id} / {record.id}
      </p>
      <PolicyConfigLink record={record} />
      <dl className="policy-facts">
        <div>
          <dt>编辑版本</dt>
          <dd>v{record.revision}</dd>
        </div>
        <div>
          <dt>已发布版本</dt>
          <dd>{record.published ? "v" + record.published : "尚未发布"}</dd>
        </div>
        <div>
          <dt>发布状态</dt>
          <dd>{record.pending ? "待完成" : "已完成"}</dd>
        </div>
      </dl>
      <p className="policy-note">
        已发布表示配置可读取，不能据此确认每个 Worker
        已应用。时间与目标条件在实际处理时判定。
      </p>
      {record.pending && (
        <details>
          <summary>待发布 v{record.pending.version}</summary>
          <JsonViewer value={record.pending} />
        </details>
      )}
      {record.published > 0 && (
        <form
          className="policy-version-form"
          key={selected}
          onSubmit={(e) => {
            e.preventDefault();
            const n = positiveVersion(
              String(new FormData(e.currentTarget).get("version")),
            );
            if (n && n <= record.published) {
              setCompare(undefined);
              onVersion(n);
            }
          }}
        >
          <label>
            查看版本
            <input
              name="version"
              aria-label="查看版本"
              type="number"
              min={1}
              max={record.published}
              defaultValue={selected}
              required
            />
          </label>
          <button type="submit">读取版本</button>
        </form>
      )}
      {release.isFetching && <p role="status">正在读取发布版本…</p>}
      {release.isError && (
        <p className="error-banner" role="alert">
          {release.error.message}
        </p>
      )}
      {release.data && (
        <>
          <div className="policy-release-heading">
            <h3>发布 v{release.data.version}</h3>
            <span>
              {formatTime(release.data.created_at, mode)}
              {release.data.deleted ? " · 删除版本" : ""}
            </span>
          </div>
          <dl className="policy-facts">
            <div>
              <dt>时区</dt>
              <dd>{release.data.compiled.timezone}</dd>
            </div>
            <div>
              <dt>条件组</dt>
              <dd>{release.data.compiled.condition_groups}</dd>
            </div>
            <div>
              <dt>目标选择器</dt>
              <dd>{release.data.compiled.target_selectors}</dd>
            </div>
          </dl>
          <details>
            <summary>配置与编译摘要</summary>
            <JsonViewer
              value={{
                spec: release.data.spec,
                compiled: release.data.compiled,
                operation_id: release.data.operation_id,
              }}
            />
          </details>
          {record.type === "shield" &&
            release.data.spec.shield_type === "rely_shield" && (
              <p className="policy-note">
                目标范围仅限制主告警。子告警按业务范围、被屏蔽条件、时间范围与关系条件匹配。
              </p>
            )}
          <form
            className="policy-version-form"
            onSubmit={(e) => {
              e.preventDefault();
              const n = positiveVersion(
                String(new FormData(e.currentTarget).get("compare")),
              );
              if (n && n <= record.published) setCompare(n);
            }}
          >
            <label>
              对比版本
              <input
                name="compare"
                aria-label="对比版本"
                type="number"
                min={1}
                max={record.published}
                defaultValue={Math.max(1, selected - 1)}
                required
              />
            </label>
            <button type="submit">对比配置</button>
          </form>
          {previous.isFetching && <p role="status">正在读取对比版本…</p>}
          {previous.isError && (
            <p className="error-banner" role="alert">
              {previous.error.message}
            </p>
          )}
          {previous.data && (
            <section aria-label="版本对比">
              <h3>
                v{previous.data.version} → v{release.data.version}
              </h3>
              <p>
                {changed.length
                  ? "变化字段：" + changed.join("、")
                  : "策略配置相同"}
                {previous.data.deleted !== release.data.deleted
                  ? "；删除标记发生变化"
                  : ""}
              </p>
              <div className="policy-diff">
                <JsonViewer value={previous.data.spec} />
                <JsonViewer value={release.data.spec} />
              </div>
            </section>
          )}
          <PolicySimulation
            key={
              "simulation:" +
              release.data.version +
              ":" +
              release.data.compiled.digest
            }
            release={release.data}
          />
          <PolicyPreviewForm
            key={release.data.version + ":" + release.data.compiled.digest}
            release={release.data}
          />
        </>
      )}
    </>
  );
}

function PolicyPreviewForm({ release }: { release: PolicyRelease }) {
  const mode = useTimeMode();
  const [inputKind, setInputKind] = useState<
    "event_id" | "alert_id" | "event" | "alert"
  >("event_id");
  const [input, setInput] = useState("");
  const [severity, setSeverity] = useState("");
  const [at, setAt] = useState("");
  const [rely, setRely] = useState(false);
  const [origin, setOrigin] = useState("");
  const [custom, setCustom] = useState(false);
  const [spec, setSpec] = useState("");
  const [result, setResult] = useState<PolicyPreview>();
  const [error, setError] = useState("");
  const [busy, setBusy] = useState(false);
  const operation = useRef(0);
  const abort = useRef<AbortController | undefined>(undefined);
  useEffect(
    () => () => {
      operation.current++;
      abort.current?.abort();
    },
    [],
  );
  const reset = () => {
    operation.current++;
    abort.current?.abort();
    setResult(undefined);
    setError("");
    setBusy(false);
  };
  function json(text: string): unknown {
    if (new TextEncoder().encode(text).byteLength > 2 << 20)
      throw new Error("单份 JSON 输入不能超过 2 MiB");
    const value: unknown = JSON.parse(text);
    parseYAML(text, { maxAliasCount: 0, uniqueKeys: true });
    return value;
  }
  async function run(e: FormEvent) {
    e.preventDefault();
    reset();
    const ticket = operation.current;
    const controller = new AbortController();
    abort.current = controller;
    setBusy(true);
    try {
      const body = policyPreviewRequestSchema.parse({
        bk_tenant_id: release.bk_tenant_id,
        type: release.type,
        ...(custom
          ? { spec: json(spec) }
          : { id: release.id, version: release.version }),
        [inputKind]: inputKind.endsWith("_id") ? input.trim() : json(input),
        ...(severity.trim() ? { severity: severity.trim() } : {}),
        ...(at ? { at } : {}),
        ...(rely
          ? {
              rely: true,
              ...(origin.trim() ? { origin_alert_id: origin.trim() } : {}),
            }
          : {}),
      });
      if (new TextEncoder().encode(JSON.stringify(body)).byteLength > 3 << 20)
        throw new Error("预览请求不能超过 3 MiB");
      const response = await previewPolicy(body, controller.signal);
      if (!custom && response.compiled.digest !== release.compiled.digest)
        throw new Error("预览编译摘要与选定版本不一致");
      if (operation.current === ticket) setResult(response);
    } catch (e) {
      if (operation.current === ticket)
        setError(e instanceof Error ? e.message : "预览失败");
    } finally {
      if (operation.current === ticket) setBusy(false);
    }
  }
  return (
    <section className="policy-preview" aria-label="策略匹配预览">
      <h3>只读匹配预览</h3>
      <p className="policy-note">
        使用输入已保存的丰富结果，不重新运行
        Enrich。预览不计数、不占窗口、不建立屏蔽或合并关系。
      </p>
      <form onSubmit={run}>
        <div className="policy-input-row">
          <label>
            输入方式
            <select
              aria-label="策略预览输入方式"
              value={inputKind}
              onChange={(e) => {
                reset();
                setInput("");
                setInputKind(e.target.value as typeof inputKind);
              }}
            >
              <option value="event_id">Event ID</option>
              <option value="alert_id">Alert ID</option>
              <option value="event">Event JSON</option>
              <option value="alert">Alert JSON</option>
            </select>
          </label>
          <label>
            等级（留空返回全部）
            <input
              aria-label="预览等级"
              value={severity}
              onChange={(e) => {
                reset();
                setSeverity(e.target.value);
              }}
            />
          </label>
          <label>
            判定时间（{mode === "utc" ? "UTC" : "本地时间"}，留空使用当前时间）
            <input
              aria-label="预览判定时间"
              type="datetime-local"
              value={inputTime(at, mode)}
              onChange={(e) => {
                reset();
                setAt(
                  e.target.value ? parseInputTime(e.target.value, mode) : "",
                );
              }}
            />
          </label>
        </div>
        <label>
          {inputKind.endsWith("_id")
            ? "输入 ID"
            : "完整领域 JSON（含租户与丰富结果）"}
          {inputKind.endsWith("_id") ? (
            <input
              aria-label="预览输入 ID"
              value={input}
              onChange={(e) => {
                reset();
                setInput(e.target.value);
              }}
              required
            />
          ) : (
            <textarea
              aria-label="预览输入 JSON"
              value={input}
              onChange={(e) => {
                reset();
                setInput(e.target.value);
              }}
              required
            />
          )}
        </label>
        {release.type === "shield" &&
          release.spec.shield_type === "rely_shield" && (
            <>
              <label className="policy-check">
                <input
                  type="checkbox"
                  checked={rely}
                  onChange={(e) => {
                    reset();
                    setRely(e.target.checked);
                  }}
                />
                匹配被屏蔽条件（rely_policy）
              </label>
              {rely && (
                <label>
                  关系查询的主 Alert ID
                  <input
                    aria-label="依赖主 Alert ID"
                    value={origin}
                    onChange={(e) => {
                      reset();
                      setOrigin(e.target.value);
                    }}
                  />
                </label>
              )}
            </>
          )}
        <label className="policy-check">
          <input
            type="checkbox"
            checked={custom}
            onChange={(e) => {
              reset();
              setCustom(e.target.checked);
              if (e.target.checked)
                setSpec(JSON.stringify(release.spec, null, 2));
            }}
          />
          使用临时策略配置（不保存）
        </label>
        {custom && (
          <textarea
            aria-label="临时策略 JSON"
            value={spec}
            onChange={(e) => {
              reset();
              setSpec(e.target.value);
            }}
          />
        )}
        <div className="policy-actions">
          <button type="submit" disabled={busy || !input.trim()}>
            {busy ? "正在匹配…" : "执行只读匹配"}
          </button>
          {busy && (
            <button type="button" onClick={reset}>
              取消预览
            </button>
          )}
        </div>
      </form>
      {error && (
        <p className="error-banner" role="alert">
          {error}
        </p>
      )}
      {result && (
        <section aria-label="匹配结果">
          <h4>
            {result.version ? "发布 v" + result.version : "临时配置"} ·
            仅匹配判定
          </h4>
          <p>{formatTime(result.at, mode)}</p>
          {result.evaluations.map((evaluation) => (
            <article className="policy-verdict" key={evaluation.severity}>
              <h4>
                {evaluation.severity}{" "}
                <span
                  className={
                    evaluation.evaluated
                      ? evaluation.matched
                        ? "policy-match"
                        : ""
                      : "policy-unknown"
                  }
                >
                  {outcome(evaluation.evaluated, evaluation.matched)}
                </span>
              </h4>
              {evaluation.reason && (
                <p>
                  {reasonNames[evaluation.reason] ?? evaluation.reason}{" "}
                  <code>{evaluation.reason}</code>
                </p>
              )}
              {evaluation.group_key && (
                <p className="mono">分组键 {evaluation.group_key}</p>
              )}
              {evaluation.groups.map((group) => (
                <section key={group.index}>
                  <h5>
                    条件组 {group.index + 1} ·{" "}
                    {outcome(group.evaluated, group.matched)}
                  </h5>
                  <div className="policy-table-scroll">
                    <table>
                      <thead>
                        <tr>
                          <th>条件 ID</th>
                          <th>结果</th>
                          <th>原因</th>
                        </tr>
                      </thead>
                      <tbody>
                        {group.conditions.map((condition) => (
                          <tr key={condition.id}>
                            <td>{condition.id}</td>
                            <td>
                              {outcome(condition.evaluated, condition.matched)}
                            </td>
                            <td>{condition.reason ?? "—"}</td>
                          </tr>
                        ))}
                      </tbody>
                    </table>
                  </div>
                </section>
              ))}
            </article>
          ))}
        </section>
      )}
    </section>
  );
}
