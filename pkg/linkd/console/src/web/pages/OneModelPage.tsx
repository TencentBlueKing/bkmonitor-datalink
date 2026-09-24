import { useEffect, useRef, useState } from "react";
import { useSearchParams } from "react-router-dom";
import { z } from "zod";
import { consoleURL } from "../base-path";
import { JsonViewer } from "../components/JsonViewer";
import { useReportPageQueryFailure } from "../navigation";
import "./onemodel.css";

const resultSchema = z.object({
  items: z.array(
    z
      .object({
        bk_tenant_id: z.string(),
        model_id: z.string(),
        model_inst_id: z.string(),
        entity_uid: z.string(),
        display_name: z.unknown().optional(),
        attributes: z.record(z.string(), z.unknown()).optional(),
      })
      .passthrough(),
  ),
  next_cursor: z.string().optional(),
  elapsed_milliseconds: z.number(),
});
type Result = z.infer<typeof resultSchema>;
type Item = Result["items"][number];
interface Attribute {
  field: string;
  type: string;
  operator: string;
  value: string;
}
const emptyAttribute: Attribute = {
  field: "",
  type: "keyword",
  operator: "eq",
  value: "",
};

async function call(
  operation: string,
  body: unknown,
  signal?: AbortSignal,
): Promise<unknown> {
  const response = await fetch(consoleURL(`/local-api/onemodel/${operation}`), {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify(body),
    signal,
  });
  const value: unknown = await response.json();
  if (!response.ok) {
    const error = z
      .object({ error: z.object({ message: z.string() }) })
      .safeParse(value);
    throw new Error(
      error.success
        ? error.data.error.message
        : `查询失败（${response.status}）`,
    );
  }
  return value;
}

function attributeFilter(
  attribute: Attribute,
): Record<string, unknown> | undefined {
  if (!attribute.field.trim()) return undefined;
  let value: unknown = attribute.value;
  if (["exists", "not_exists"].includes(attribute.operator)) value = undefined;
  else if (["in", "not_in"].includes(attribute.operator)) {
    value = JSON.parse(attribute.value) as unknown;
    if (!Array.isArray(value))
      throw new Error("in / not_in 的值必须是 JSON 数组");
  } else if (["long", "double", "boolean"].includes(attribute.type)) {
    value = JSON.parse(attribute.value) as unknown;
    if (
      attribute.type === "boolean"
        ? typeof value !== "boolean"
        : typeof value !== "number"
    )
      throw new Error("属性值与选择的类型不符");
    if (attribute.type === "long" && !Number.isSafeInteger(value))
      throw new Error("long 值必须是安全范围内的整数");
  }
  const leaf = {
    field: attribute.field.startsWith("attributes.")
      ? attribute.field
      : `attributes.${attribute.field}`,
    type: attribute.type,
    operator:
      attribute.operator === "not_exists" ? "exists" : attribute.operator,
    ...(value === undefined ? {} : { value }),
  };
  return attribute.operator === "not_exists" ? { not: leaf } : leaf;
}

export function OneModelPage() {
  const [params] = useSearchParams();
  const [tenant, setTenant] = useState(params.get("bk_tenant_id") ?? "");
  const [mode, setMode] = useState<"search" | "related">("search");
  const [model, setModel] = useState(params.get("model_id") ?? "");
  const [id, setID] = useState(params.get("model_inst_id") ?? "");
  const [limit, setLimit] = useState(50);
  const [attributes, setAttributes] = useState<Attribute[]>([
    { ...emptyAttribute },
  ]);
  const [advanced, setAdvanced] = useState(false);
  const [filterJSON, setFilterJSON] = useState("{}");
  const [rootModel, setRootModel] = useState("");
  const [rootID, setRootID] = useState("");
  const [relation, setRelation] = useState("");
  const [direction, setDirection] = useState("out");
  const [result, setResult] = useState<Result>();
  const [detail, setDetail] = useState<Item>();
  const [showJSON, setShowJSON] = useState(false);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const [page, setPage] = useState(1);
  const epoch = useRef(0);
  const active = useRef<AbortController | undefined>(undefined);
  const cursor = useRef<{ bk_tenant_id: string; cursor: string } | undefined>(
    undefined,
  );
  useReportPageQueryFailure(Boolean(error));

  function releaseCursor() {
    const current = cursor.current;
    cursor.current = undefined;
    if (current) void call("close", current).catch(() => undefined);
  }
  // 取消和代次检查同时使用：取消已经抵达控制面的查询，拒绝网络竞态中的旧结果。
  function reset() {
    epoch.current++;
    active.current?.abort();
    releaseCursor();
    setBusy(false);
    setResult(undefined);
    setDetail(undefined);
    setError("");
    setPage(1);
  }
  useEffect(
    () => () => {
      epoch.current++;
      active.current?.abort();
      const current = cursor.current;
      cursor.current = undefined;
      if (current) void call("close", current).catch(() => undefined);
    },
    [],
  );

  function where() {
    const clauses: unknown[] = [];
    if (id.trim())
      clauses.push({
        field: "model_inst_id",
        type: "keyword",
        operator: "eq",
        value: id.trim(),
      });
    if (advanced) {
      const filter: unknown = JSON.parse(filterJSON);
      if (!filter || typeof filter !== "object" || Array.isArray(filter))
        throw new Error("过滤 JSON 必须是对象");
      if (Object.keys(filter).length) clauses.push(filter);
    } else
      for (const attr of attributes) {
        const filter = attributeFilter(attr);
        if (filter) clauses.push(filter);
      }
    return clauses.length === 0
      ? {}
      : clauses.length === 1
        ? clauses[0]
        : { all: clauses };
  }
  async function query(next = false) {
    if (!next) reset();
    const generation = ++epoch.current;
    const abort = new AbortController();
    active.current?.abort();
    active.current = abort;
    setBusy(true);
    setError("");
    setDetail(undefined);
    const requestTenant = tenant.trim();
    try {
      if (!requestTenant || !model.trim()) throw new Error("请填写租户和模型");
      if (
        mode === "related" &&
        (!rootModel.trim() || !rootID.trim() || !relation.trim())
      )
        throw new Error("请填写起点模型、起点实例 ID 和关系");
      const target = {
        model_id: model.trim(),
        where: where(),
        limit: mode === "search" ? limit : 1024,
      };
      const body =
        mode === "search"
          ? {
              bk_tenant_id: requestTenant,
              ...target,
              ...(next && cursor.current
                ? { cursor: cursor.current.cursor }
                : {}),
            }
          : {
              bk_tenant_id: requestTenant,
              roots: [
                { model_id: rootModel.trim(), model_inst_id: rootID.trim() },
              ],
              relation: relation.trim(),
              direction,
              query: target,
            };
      const data = resultSchema.parse(await call(mode, body, abort.signal));
      if (generation !== epoch.current) {
        if (data.next_cursor)
          void call("close", {
            bk_tenant_id: requestTenant,
            cursor: data.next_cursor,
          }).catch(() => undefined);
        return;
      }
      cursor.current = data.next_cursor
        ? { bk_tenant_id: requestTenant, cursor: data.next_cursor }
        : undefined;
      setResult(data);
      setPage((p) => (next ? p + 1 : 1));
    } catch (cause) {
      if (generation === epoch.current) {
        releaseCursor();
        setResult(undefined);
        setError(cause instanceof Error ? cause.message : "查询失败");
      }
    } finally {
      if (generation === epoch.current) setBusy(false);
    }
  }
  function fromInstance(item: Item) {
    reset();
    setMode("related");
    setTenant(item.bk_tenant_id);
    setRootModel(item.model_id);
    setRootID(item.model_inst_id);
    setModel("");
    setID("");
    setAttributes([{ ...emptyAttribute }]);
    setAdvanced(false);
    setFilterJSON("{}");
  }
  const editAttribute = (
    index: number,
    key: keyof Attribute,
    value: string,
  ) => {
    reset();
    setAttributes((rows) =>
      rows.map((row, i) => (i === index ? { ...row, [key]: value } : row)),
    );
  };

  return (
    <section className="onemodel-page">
      <header className="page-heading">
        <div>
          <p className="eyebrow">ONEMODEL EXPLORER</p>
          <h1>OneModel 查询</h1>
          <p>按租户查看实例与关联，使用与 CMDB 丰富相同的查询规则。</p>
        </div>
      </header>
      <div className="onemodel-tabs" role="tablist" aria-label="查询方式">
        <button
          role="tab"
          aria-selected={mode === "search"}
          onClick={() => {
            reset();
            setMode("search");
          }}
        >
          实例查询
        </button>
        <button
          role="tab"
          aria-selected={mode === "related"}
          onClick={() => {
            reset();
            setMode("related");
          }}
        >
          关联查询
        </button>
      </div>
      <form
        className="runtime-config-panel"
        onSubmit={(e) => {
          e.preventDefault();
          void query();
        }}
      >
        <div className="onemodel-grid">
          <label>
            租户
            <input
              aria-label="租户"
              value={tenant}
              onChange={(e) => {
                reset();
                setTenant(e.target.value);
              }}
            />
          </label>
          <label>
            {mode === "search" ? "模型" : "目标模型"}
            <input
              aria-label={mode === "search" ? "模型" : "目标模型"}
              value={model}
              onChange={(e) => {
                reset();
                setModel(e.target.value);
              }}
            />
          </label>
          <label>
            实例 ID
            <input
              aria-label="实例 ID"
              value={id}
              onChange={(e) => {
                reset();
                setID(e.target.value);
              }}
            />
          </label>
          {mode === "search" && (
            <label>
              每页条数
              <select
                aria-label="每页条数"
                value={limit}
                onChange={(e) => {
                  reset();
                  setLimit(Number(e.target.value));
                }}
              >
                {[20, 50, 100, 200].map((n) => (
                  <option key={n}>{n}</option>
                ))}
              </select>
            </label>
          )}
        </div>
        {mode === "related" && (
          <>
            <div className="onemodel-grid">
              <label>
                起点模型
                <input
                  aria-label="起点模型"
                  value={rootModel}
                  onChange={(e) => {
                    reset();
                    setRootModel(e.target.value);
                  }}
                />
              </label>
              <label>
                起点实例 ID
                <input
                  aria-label="起点实例 ID"
                  value={rootID}
                  onChange={(e) => {
                    reset();
                    setRootID(e.target.value);
                  }}
                />
              </label>
              <label>
                关系
                <input
                  aria-label="关系"
                  value={relation}
                  onChange={(e) => {
                    reset();
                    setRelation(e.target.value);
                  }}
                />
              </label>
              <label>
                方向
                <select
                  aria-label="方向"
                  value={direction}
                  onChange={(e) => {
                    reset();
                    setDirection(e.target.value);
                  }}
                >
                  <option value="out">出向</option>
                  <option value="in">入向</option>
                  <option value="both">双向</option>
                </select>
              </label>
            </div>
            <p className="muted">
              关联查询返回完整结果，最多 1024 个实例；超限时请收窄条件。
            </p>
          </>
        )}
        <div className="onemodel-filter-heading">
          <h2>属性过滤</h2>
          <label>
            <input
              type="checkbox"
              checked={advanced}
              onChange={(e) => {
                reset();
                setAdvanced(e.target.checked);
              }}
            />
            高级 JSON
          </label>
        </div>
        {advanced ? (
          <label>
            过滤 JSON
            <textarea
              aria-label="过滤 JSON"
              rows={8}
              spellCheck={false}
              value={filterJSON}
              onChange={(e) => {
                reset();
                setFilterJSON(e.target.value);
              }}
            />
            <span className="muted">
              使用 all / any / not 组合类型化条件；租户由上方字段固定。
            </span>
          </label>
        ) : (
          <>
            {attributes.map((attr, index) => (
              <div className="onemodel-attribute" key={index}>
                <label>
                  属性名
                  <input
                    aria-label={`属性名 ${index + 1}`}
                    value={attr.field}
                    onChange={(e) =>
                      editAttribute(index, "field", e.target.value)
                    }
                  />
                </label>
                <label>
                  类型
                  <select
                    aria-label={`属性类型 ${index + 1}`}
                    value={attr.type}
                    onChange={(e) =>
                      editAttribute(index, "type", e.target.value)
                    }
                  >
                    {[
                      "keyword",
                      "long",
                      "double",
                      "boolean",
                      "datetime",
                      "ip",
                    ].map((v) => (
                      <option key={v}>{v}</option>
                    ))}
                  </select>
                </label>
                <label>
                  运算符
                  <select
                    aria-label={`运算符 ${index + 1}`}
                    value={attr.operator}
                    onChange={(e) =>
                      editAttribute(index, "operator", e.target.value)
                    }
                  >
                    {[
                      "eq",
                      "ne",
                      "in",
                      "not_in",
                      "gt",
                      "gte",
                      "lt",
                      "lte",
                      "exists",
                      "not_exists",
                    ].map((v) => (
                      <option key={v}>{v}</option>
                    ))}
                  </select>
                </label>
                <label>
                  值
                  <input
                    aria-label={`属性值 ${index + 1}`}
                    disabled={["exists", "not_exists"].includes(attr.operator)}
                    value={attr.value}
                    onChange={(e) =>
                      editAttribute(index, "value", e.target.value)
                    }
                  />
                </label>
                <button
                  type="button"
                  aria-label={`删除条件 ${index + 1}`}
                  onClick={() => {
                    reset();
                    setAttributes((rows) => rows.filter((_, i) => i !== index));
                  }}
                >
                  删除
                </button>
              </div>
            ))}
            <button
              type="button"
              disabled={attributes.length >= 32}
              onClick={() => {
                reset();
                setAttributes([...attributes, { ...emptyAttribute }]);
              }}
            >
              添加条件
            </button>
          </>
        )}
        <div className="onemodel-actions">
          <button type="submit" className="primary-button" disabled={busy}>
            {busy ? "查询中…" : "执行查询"}
          </button>
          {busy && (
            <button type="button" onClick={reset}>
              取消查询
            </button>
          )}
        </div>
      </form>
      {error && (
        <div className="error-banner" role="alert">
          {error}
        </div>
      )}
      {result && (
        <section className="runtime-config-panel" aria-label="查询结果">
          <div className="onemodel-filter-heading">
            <h2>查询结果 · {result.items.length} 条</h2>
            <span>
              第 {page} 页 · {result.elapsed_milliseconds} ms
            </span>
            <button onClick={() => setShowJSON(!showJSON)}>
              {showJSON ? "表格视图" : "JSON 视图"}
            </button>
          </div>
          {result.items.length === 0 && <p>没有匹配的实例。</p>}
          {showJSON ? (
            <JsonViewer value={result.items} />
          ) : (
            <div className="table-scroll">
              <table className="explorer-table">
                <thead>
                  <tr>
                    <th>实例 ID</th>
                    <th>名称</th>
                    <th>模型</th>
                    <th>操作</th>
                  </tr>
                </thead>
                <tbody>
                  {result.items.map((item) => (
                    <tr key={item.entity_uid}>
                      <td>
                        <button onClick={() => setDetail(item)}>
                          {item.model_inst_id}
                        </button>
                      </td>
                      <td>{String(item.display_name ?? "—")}</td>
                      <td>{item.model_id}</td>
                      <td>
                        <button onClick={() => fromInstance(item)}>
                          查询关联
                        </button>
                      </td>
                    </tr>
                  ))}
                </tbody>
              </table>
            </div>
          )}
          <div className="onemodel-actions">
            <button
              disabled={busy || !result.next_cursor}
              onClick={() => void query(true)}
            >
              下一页
            </button>
            <button disabled={busy} onClick={() => void query()}>
              重新查询
            </button>
          </div>
        </section>
      )}
      {detail && (
        <section className="runtime-config-panel">
          <h2>实例详情 · {detail.model_inst_id}</h2>
          <JsonViewer value={detail} />
        </section>
      )}
    </section>
  );
}
