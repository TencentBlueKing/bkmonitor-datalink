import { consoleURL } from "../base-path";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { useState } from "react";
import { z } from "zod";

const placement = z.object({
  replicas: z
    .union([z.literal("all"), z.number().int().nonnegative()])
    .default("all"),
  selector: z.record(z.string(), z.string()).default({}),
});
const specSchema = z
  .object({
    event_source_id: z.string(),
    enabled: z.boolean(),
    scheduling: z.object({ cleaner: placement, lifecycle: placement }),
    storage: z.object({
      type: z.literal("kafka"),
      kafka: z
        .object({
          brokers: z.array(z.string()),
          topic: z.string(),
          consumer_group: z.string(),
          security: z.unknown().optional(),
        })
        .passthrough(),
    }),
  })
  .passthrough();
const recordSchema = z.object({
  id: z.string(),
  revision: z.number(),
  published: z.number(),
  deleted: z.boolean(),
  spec: specSchema,
});
type Source = z.infer<typeof recordSchema>;
const runtimeSchema = z.object({
  workers: z.record(
    z.string(),
    z
      .object({
        id: z.string(),
        roles: z.array(z.string()),
        labels: z.record(z.string(), z.string()).nullable(),
        require_explicit_selector: z.boolean(),
      })
      .passthrough(),
  ),
  tasks: z.record(
    z.string(),
    z.object({
      id: z.string(),
      source: z.string(),
      role: z.string(),
      worker: z.string(),
      version: z.number(),
      phase: z.string(),
      partitions: z.array(z.string()).optional(),
      error: z.string().optional(),
    }),
  ),
  statuses: z
    .array(
      z.object({
        source: z.string(),
        role: z.string(),
        matching: z.number(),
        target: z.number(),
        running: z.number(),
        reason: z.string().optional(),
        metadata: z
          .object({
            partitions: z.number(),
            success: z.string(),
            error: z.string(),
          })
          .optional(),
      }),
    )
    .nullable(),
});
async function call(path: string, method = "GET", body?: unknown) {
  const response = await fetch(consoleURL(path), {
    method,
    headers: { "Content-Type": "application/json" },
    body: body === undefined ? undefined : JSON.stringify(body),
  });
  const value: unknown = await response.json();
  if (!response.ok) {
    const error = z
      .object({ error: z.object({ message: z.string() }) })
      .safeParse(value);
    throw new Error(
      error.success ? error.data.error.message : `请求失败 ${response.status}`,
    );
  }
  return value;
}
async function listSources() {
  const all: Source[] = [];
  let after = "";
  for (let page = 0; page < 100; page++) {
    const rows = z
      .array(recordSchema)
      .parse(
        await call(
          `/local-api/event-sources?limit=100&after=${encodeURIComponent(after)}`,
        ),
      );
    all.push(...rows);
    if (rows.length < 100) return all;
    after = rows.at(-1)!.id;
  }
  throw new Error("来源超过页面展示上限");
}
function freshSpec() {
  return {
    event_source_id: "",
    enabled: true,
    cleaner: { type: "standard" },
    scheduling: {
      cleaner: { replicas: "all" as const, selector: {} },
      lifecycle: { replicas: "all" as const, selector: {} },
    },
    storage: {
      type: "kafka" as const,
      kafka: { brokers: [] as string[], topic: "", consumer_group: "" },
    },
  };
}

export function EventSourcesPage() {
  const cache = useQueryClient();
  const records = useQuery({
    queryKey: ["event-sources"],
    queryFn: listSources,
    refetchInterval: 5000,
  });
  const runtime = useQuery({
    queryKey: ["scheduling"],
    queryFn: async () =>
      runtimeSchema.parse(await call("/local-api/scheduling")),
    refetchInterval: 3000,
  });
  const [selected, setSelected] = useState<Source>();
  const [text, setText] = useState(() => JSON.stringify(freshSpec(), null, 2));
  const [error, setError] = useState("");
  const [busy, setBusy] = useState(false);
  function edit(source?: Source) {
    setSelected(source);
    const spec = source ? structuredClone(source.spec) : freshSpec();
    if ("security" in spec.storage.kafka) delete spec.storage.kafka.security;
    setText(JSON.stringify(spec, null, 2));
    setError("");
  }
  async function save(remove = false) {
    setBusy(true);
    setError("");
    try {
      const spec = specSchema.parse(JSON.parse(text));
      await call(
        `/local-api/event-sources/${encodeURIComponent(spec.event_source_id)}`,
        remove ? "DELETE" : "PUT",
        remove
          ? { expected_revision: selected?.revision ?? 0 }
          : { expected_revision: selected?.revision ?? 0, spec },
      );
      setSelected(undefined);
      setText(JSON.stringify(freshSpec(), null, 2));
      await cache.invalidateQueries({ queryKey: ["event-sources"] });
      await cache.invalidateQueries({ queryKey: ["scheduling"] });
    } catch (cause) {
      setError(cause instanceof Error ? cause.message : "保存失败");
    } finally {
      setBusy(false);
    }
  }
  return (
    <section>
      <div className="page-heading">
        <div>
          <p className="eyebrow">EVENT SOURCES</p>
          <h1>事件来源与任务调度</h1>
          <p>
            配置由控制面管理。all 匹配全部 worker，Cleaner 自动受 Kafka
            partition 数限制。
          </p>
        </div>
        <button onClick={() => edit()}>新增来源</button>
      </div>
      {(records.error || runtime.error || error) && (
        <div role="alert" className="error-banner">
          {error || records.error?.message || runtime.error?.message}
        </div>
      )}
      <article className="runtime-config-panel">
        <h2>来源配置</h2>
        <table>
          <thead>
            <tr>
              <th>来源</th>
              <th>开关</th>
              <th>发布版本</th>
              <th>Cleaner 数量 / 标签</th>
              <th>Lifecycle 数量 / 标签</th>
              <th>操作</th>
            </tr>
          </thead>
          <tbody>
            {records.data?.map((source) => (
              <tr key={source.id}>
                <td>{source.id}</td>
                <td>
                  {source.deleted
                    ? "已删除"
                    : source.spec.enabled
                      ? "启用"
                      : "停用"}
                </td>
                <td>{source.published}</td>
                <td>
                  {source.spec.scheduling.cleaner.replicas} /{" "}
                  {JSON.stringify(source.spec.scheduling.cleaner.selector)}
                </td>
                <td>
                  {source.spec.scheduling.lifecycle.replicas} /{" "}
                  {JSON.stringify(source.spec.scheduling.lifecycle.selector)}
                </td>
                <td>
                  <button onClick={() => edit(source)}>编辑</button>
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      </article>
      <article className="runtime-config-panel">
        <h2>{selected ? `编辑 ${selected.id}` : "新增来源"}</h2>
        <p>
          可修改 enabled、各角色 replicas 和 selector。security
          未提交时保留原凭据；只有需要轮换时才显式填写。
        </p>
        <label htmlFor="source-spec">EventSource 配置</label>
        <textarea
          id="source-spec"
          value={text}
          onChange={(event) => setText(event.target.value)}
          rows={22}
          style={{ width: "100%", fontFamily: "monospace" }}
        />
        <button disabled={busy} onClick={() => void save()}>
          保存并发布
        </button>
        {selected && !selected.deleted && (
          <button disabled={busy} onClick={() => void save(true)}>
            删除来源
          </button>
        )}
      </article>
      <article className="runtime-config-panel">
        <h2>调度目标</h2>
        <table>
          <thead>
            <tr>
              <th>来源 / 角色</th>
              <th>匹配</th>
              <th>Partition 上限</th>
              <th>目标 / 运行</th>
              <th>最近探测成功</th>
              <th>状态</th>
            </tr>
          </thead>
          <tbody>
            {runtime.data?.statuses?.map((s) => (
              <tr key={`${s.source}/${s.role}`}>
                <td>
                  {s.source} / {s.role}
                </td>
                <td>{s.matching}</td>
                <td>{s.metadata?.partitions ?? "—"}</td>
                <td>
                  {s.target} / {s.running}
                </td>
                <td>{s.metadata?.success ?? "—"}</td>
                <td>{s.reason || "正常"}</td>
              </tr>
            ))}
          </tbody>
        </table>
      </article>
      <article className="runtime-config-panel">
        <h2>实际任务</h2>
        <table>
          <thead>
            <tr>
              <th>来源 / 角色</th>
              <th>Worker</th>
              <th>版本</th>
              <th>阶段</th>
              <th>Partitions</th>
            </tr>
          </thead>
          <tbody>
            {Object.values(runtime.data?.tasks ?? {}).map((t) => (
              <tr key={t.id}>
                <td>
                  {t.source} / {t.role}
                </td>
                <td>{t.worker}</td>
                <td>{t.version}</td>
                <td>
                  {t.phase}
                  {t.error ? `：${t.error}` : ""}
                </td>
                <td>{t.partitions?.join(", ") || "—"}</td>
              </tr>
            ))}
          </tbody>
        </table>
      </article>
      <article className="runtime-config-panel">
        <h2>Worker 标签与角色</h2>
        {Object.values(runtime.data?.workers ?? {}).map((w) => (
          <p key={w.id}>
            {w.id} · {w.roles.join(", ")} · {JSON.stringify(w.labels ?? {})} ·{" "}
            {w.require_explicit_selector ? "必须显式匹配" : "接受空选择器"}
          </p>
        ))}
      </article>
    </section>
  );
}
