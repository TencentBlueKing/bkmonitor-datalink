import { useState } from "react";
import { useSearchParams } from "react-router-dom";
import { parse } from "yaml";
import { z } from "zod";
import { consoleURL } from "../base-path";

const previewSchema = z.object({
  event_source_version: z.number(),
  config_digest: z.string(),
  enrich_status: z.string(),
  original: z.record(z.string(), z.unknown()),
  effective_alert: z.record(z.string(), z.unknown()),
  enrich: z.record(z.string(), z.unknown()),
  changes: z.array(z.unknown()),
  previous_changes: z.array(z.unknown()),
  trace: z.array(z.unknown()),
});
type Preview = z.infer<typeof previewSchema>;
async function call(path: string, body?: unknown): Promise<unknown> {
  const response = await fetch(consoleURL(path), {
    method: body === undefined ? "GET" : "POST",
    headers: { "Content-Type": "application/json" },
    body: body === undefined ? undefined : JSON.stringify(body),
  });
  const value: unknown = await response.json();
  if (!response.ok) {
    const error = z
      .object({ error: z.object({ message: z.string() }) })
      .safeParse(value);
    throw new Error(
      error.success
        ? error.data.error.message
        : `请求失败（${response.status}）`,
    );
  }
  return value;
}
const sample = `processors:
  - type: fields
    config:
      rules:
        - id: business_labels
          operations:
            - id: assign_labels
              type: assign
              assignments:
                - target: $.labels.environment
                  value: {literal: production}
`;
export function EnrichPreviewPage() {
  const [params] = useSearchParams();
  const [tenant, setTenant] = useState(params.get("bk_tenant_id") ?? "");
  const [source, setSource] = useState(params.get("event_source_id") ?? "");
  const [id, setID] = useState(params.get("alert_id") ?? "");
  const [mode, setMode] = useState<"id" | "json">(id ? "id" : "json");
  const [alert, setAlert] = useState(
    '{\n  "title": "CPU 告警",\n  "content": "IP=10.0.0.8",\n  "labels": {}\n}',
  );
  const [custom, setCustom] = useState(false);
  const [config, setConfig] = useState(sample);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const [result, setResult] = useState<Preview>();
  const [tab, setTab] = useState<
    | "changes"
    | "previous_changes"
    | "trace"
    | "enrich"
    | "original"
    | "effective_alert"
  >("changes");
  async function preview() {
    setBusy(true);
    setError("");
    setResult(undefined);
    try {
      const input =
        mode === "id"
          ? { alert_id: id }
          : { alert: JSON.parse(alert) as unknown };
      const enrich: unknown = custom
        ? parse(config, { maxAliasCount: 0 })
        : undefined;
      if (
        custom &&
        (enrich === null || typeof enrich !== "object" || Array.isArray(enrich))
      )
        throw new Error("丰富配置必须是包含 processors 的对象");
      setResult(
        previewSchema.parse(
          await call("/local-api/enrich/preview", {
            bk_tenant_id: tenant,
            event_source_id: source,
            input,
            ...(custom ? { enrich } : {}),
          }),
        ),
      );
    } catch (e) {
      setError(e instanceof Error ? e.message : "预览失败");
    } finally {
      setBusy(false);
    }
  }
  async function load() {
    setBusy(true);
    setError("");
    try {
      const value = z
        .object({ enrich: z.unknown() })
        .parse(
          await call(`/local-api/enrich/config/${encodeURIComponent(source)}`),
        );
      setConfig(JSON.stringify(value.enrich ?? { processors: [] }, null, 2));
      setCustom(true);
    } catch (e) {
      setError(e instanceof Error ? e.message : "加载失败");
    } finally {
      setBusy(false);
    }
  }
  return (
    <div className="enrich-preview-page">
      <header className="page-heading">
        <div>
          <p className="eyebrow">ENRICH PREVIEW</p>
          <h1>丰富调试</h1>
          <p>
            使用当前 CMDB 数据重新模拟丰富。结果仅供预览，不保存告警或配置。
          </p>
        </div>
      </header>
      <section className="runtime-config-panel">
        <div className="enrich-preview-inputs">
          <label>
            租户
            <input
              aria-label="租户"
              value={tenant}
              onChange={(e) => setTenant(e.target.value)}
            />
          </label>
          <label>
            告警源
            <input
              aria-label="告警源"
              value={source}
              onChange={(e) => setSource(e.target.value)}
            />
          </label>
          <label>
            输入方式
            <select
              aria-label="输入方式"
              value={mode}
              onChange={(e) => setMode(e.target.value as "id" | "json")}
            >
              <option value="id">Alert ID</option>
              <option value="json">Alert JSON</option>
            </select>
          </label>
        </div>
        {mode === "id" ? (
          <label>
            Alert ID
            <input
              aria-label="Alert ID"
              value={id}
              onChange={(e) => setID(e.target.value)}
            />
          </label>
        ) : (
          <label>
            Alert JSON
            <textarea
              aria-label="Alert JSON"
              rows={12}
              value={alert}
              onChange={(e) => setAlert(e.target.value)}
              spellCheck={false}
            />
          </label>
        )}
      </section>
      <section className="runtime-config-panel">
        <h2>丰富配置</h2>
        <label>
          <input
            type="checkbox"
            checked={custom}
            onChange={(e) => setCustom(e.target.checked)}
          />{" "}
          使用临时配置（JSON / YAML）
        </label>
        <button disabled={busy || !source} onClick={() => void load()}>
          加载已发布配置
        </button>
        {custom ? (
          <textarea
            aria-label="丰富配置"
            rows={20}
            value={config}
            onChange={(e) => setConfig(e.target.value)}
            spellCheck={false}
          />
        ) : (
          <p>执行时使用该告警源的当前已发布配置。</p>
        )}
        <p>省略 datasources 时使用告警源配置的连接。</p>
        <button
          className="primary-button"
          disabled={busy || !tenant || !source || (mode === "id" && !id)}
          onClick={() => void preview()}
        >
          {busy ? "正在执行…" : "执行预览"}
        </button>
        {error && <p role="alert">{error}</p>}
      </section>
      {result && (
        <section className="runtime-config-panel">
          <h2>执行结果 · {result.enrich_status}</h2>
          <p>
            来源版本 {result.event_source_version} · 配置摘要{" "}
            {result.config_digest.slice(0, 12)}
          </p>
          <div
            className="enrich-preview-tabs"
            role="tablist"
            aria-label="预览结果"
          >
            {(
              [
                ["changes", "本轮字段差异"],
                ["previous_changes", "与原丰富结果比较"],
                ["trace", "步骤详情"],
                ["enrich", "处理器补丁"],
                ["original", "原始输入"],
                ["effective_alert", "合成结果"],
              ] as const
            ).map(([key, label]) => (
              <button
                role="tab"
                aria-selected={tab === key}
                key={key}
                onClick={() => setTab(key)}
              >
                {label}
              </button>
            ))}
          </div>
          <pre role="tabpanel">{JSON.stringify(result[tab], null, 2)}</pre>
        </section>
      )}
    </div>
  );
}
