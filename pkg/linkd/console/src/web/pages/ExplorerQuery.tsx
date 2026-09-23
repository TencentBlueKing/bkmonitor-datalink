import { type FormEvent, useState } from "react";
import type { EntityKind } from "../../shared/contracts";
import { useTimeMode } from "../time";
import {
  explorerMeta,
  inputTime,
  parseInputTime,
  rangeOptions,
  valueNames,
  type FilterSpec,
} from "./explorer";

export function ExplorerQuery({
  entity,
  values,
  fields,
  range,
  maxRange,
  maxLimit,
  defaultLimit,
  onApply,
  onReset,
}: {
  entity: EntityKind;
  values: Record<string, string>;
  fields: FilterSpec[];
  range: string;
  maxRange: number;
  maxLimit: number;
  defaultLimit: number;
  onApply: (params: URLSearchParams) => void;
  onReset: () => void;
}) {
  const mode = useTimeMode();
  const [selectedRange, setRange] = useState(range);
  const [error, setError] = useState("");
  const advanced = fields.filter((f) => f.advanced);
  function submit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    setError("");
    const next = new URLSearchParams();
    const data = new FormData(event.currentTarget);
    for (const [key, raw] of data) {
      const value = String(raw).trim();
      if (value && key !== "from" && key !== "to") next.set(key, value);
    }
    try {
      if (selectedRange === "exact") {
        if (!next.get("id"))
          throw new Error("不限时间查询需要填写精确实体 ID。");
      } else {
        let from: string;
        let to: string;
        if (selectedRange === "custom") {
          if (!data.get("from") || !data.get("to"))
            throw new Error("请填写完整的开始和结束时间。");
          from = parseInputTime(String(data.get("from")), mode);
          to = parseInputTime(String(data.get("to")), mode);
        } else {
          const seconds =
            rangeOptions.find(([key]) => key === selectedRange)?.[2] ??
            Math.min(3600, maxRange);
          const now = Date.now();
          from = new Date(now - seconds * 1000).toISOString();
          to = new Date(now).toISOString();
        }
        const duration = Date.parse(to) - Date.parse(from);
        if (duration < 0 || duration > maxRange * 1000)
          throw new Error(
            `时间范围必须为正向且不超过 ${maxRange / 3600} 小时。`,
          );
        next.set("from", from);
        next.set("to", to);
      }
      onApply(next);
    } catch (err) {
      setError(err instanceof Error ? err.message : "时间格式无效");
    }
  }
  return (
    <form className="explorer-query" onSubmit={submit}>
      <div className="explorer-query-title">
        <strong>查询条件</strong>
        <span>字段精确匹配 · {explorerMeta[entity].time}范围</span>
        <button type="button" onClick={onReset}>
          重置
        </button>
      </div>
      <div className="explorer-filter-grid">
        <label>
          租户
          <input name="bk_tenant_id" defaultValue={values.bk_tenant_id ?? ""} />
        </label>
        <label>
          {explorerMeta[entity].singular} ID
          <input name="id" defaultValue={values.id ?? ""} />
        </label>
        <label>
          时间范围
          <select
            name="range"
            value={selectedRange}
            onChange={(e) => setRange(e.target.value)}
          >
            {rangeOptions
              .filter(([, , seconds]) => seconds <= maxRange)
              .map(([key, label]) => (
                <option key={key} value={key}>
                  {label}
                </option>
              ))}
            <option value="custom">自定义范围</option>
            <option value="exact">精确 ID · 不限时间</option>
          </select>
        </label>
        <label>
          时间排序
          <select name="order" defaultValue={values.order ?? "desc"}>
            <option value="desc">最新在前</option>
            <option value="asc">最早在前</option>
          </select>
        </label>
        {fields
          .filter((f) => !f.advanced)
          .map((field) => (
            <QueryField
              key={field.key}
              field={field}
              value={values[field.key]}
            />
          ))}
      </div>
      {selectedRange === "custom" && (
        <div className="explorer-custom-time" key={mode}>
          <label>
            开始时间（{mode === "utc" ? "UTC" : "本地"}）
            <input
              aria-label="开始时间"
              name="from"
              type="datetime-local"
              step="0.001"
              defaultValue={inputTime(values.from ?? "", mode)}
            />
          </label>
          <span>→</span>
          <label>
            结束时间（{mode === "utc" ? "UTC" : "本地"}）
            <input
              aria-label="结束时间"
              name="to"
              type="datetime-local"
              step="0.001"
              defaultValue={inputTime(values.to ?? "", mode)}
            />
          </label>
        </div>
      )}
      {advanced.length > 0 && (
        <details
          className="explorer-advanced"
          open={advanced.some((f) => values[f.key]) || undefined}
        >
          <summary>
            更多条件 · 对象与关联标识
            {advanced.some((f) => values[f.key]) ? "（已应用）" : ""}
          </summary>
          <div className="explorer-filter-grid">
            {advanced.map((field) => (
              <QueryField
                key={field.key}
                field={field}
                value={values[field.key]}
              />
            ))}
          </div>
        </details>
      )}
      <div className="explorer-query-footer">
        <span>条件组合使用 AND；更改后点击查询生效。</span>
        <label>
          每页条数
          <select
            name="limit"
            defaultValue={values.limit ?? String(defaultLimit)}
          >
            {[...new Set([25, 50, 100, 200, defaultLimit])]
              .filter((n) => n <= maxLimit)
              .sort((a, b) => a - b)
              .map((n) => (
                <option key={n}>{n}</option>
              ))}
          </select>
        </label>
        <button className="primary-button" type="submit">
          执行查询
        </button>
      </div>
      {error && (
        <div className="error-banner" role="alert">
          {error}
        </div>
      )}
    </form>
  );
}
function QueryField({
  field,
  value = "",
}: {
  field: FilterSpec;
  value?: string;
}) {
  return (
    <label>
      {field.label}
      {field.options ? (
        <select name={field.key} defaultValue={value}>
          <option value="">全部</option>
          {[...new Set([...field.options, ...(value ? [value] : [])])].map(
            (v) => (
              <option key={v} value={v}>
                {valueNames[v] ? `${valueNames[v]} · ${v}` : v}
              </option>
            ),
          )}
        </select>
      ) : (
        <input name={field.key} defaultValue={value} />
      )}
    </label>
  );
}
