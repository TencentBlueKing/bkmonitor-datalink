import { z } from "zod";
import { consoleURL } from "../base-path";

const placementSchema = z
  .object({
    replicas: z.union([z.literal("all"), z.number()]).default("all"),
    selector: z.record(z.string(), z.string()).default({}),
  })
  .passthrough();

// 只解析页面需要的字段；表单往返必须保留 Cleaner、Enrich、Hooks 等完整配置。
export const sourceSpecSchema = z
  .object({
    event_source_id: z.string(),
    related_tenant_id: z.string().optional(),
    enabled: z.boolean(),
    cleaner: z.object({ type: z.string().optional() }).passthrough().optional(),
    fingerprint_mode: z.string().optional(),
    fingerprint_field: z.string().optional(),
    fingerprint_fields: z.array(z.string()).optional(),
    scheduling: z
      .object({ cleaner: placementSchema, lifecycle: placementSchema })
      .passthrough(),
    storage: z
      .object({
        type: z.literal("kafka"),
        kafka: z
          .object({
            brokers: z.array(z.string()),
            topic: z.string(),
            consumer_group: z.string(),
            security: z.unknown().optional(),
          })
          .passthrough(),
      })
      .passthrough(),
  })
  .passthrough();

export const sourceRecordSchema = z.object({
  id: z.string(),
  revision: z.number(),
  published: z.number(),
  deleted: z.boolean(),
  spec: sourceSpecSchema,
});
export type SourceSpec = z.infer<typeof sourceSpecSchema>;
export type SourceRecord = z.infer<typeof sourceRecordSchema>;
export type SourceRole = "cleaner" | "lifecycle";
export const sourceRoles: SourceRole[] = ["cleaner", "lifecycle"];

export const schedulingSchema = z.object({
  workers: z.record(
    z.string(),
    z.object({
      id: z.string(),
      roles: z.array(z.string()),
      labels: z.record(z.string(), z.string()).nullish(),
      require_explicit_selector: z.boolean(),
    }),
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
export type Scheduling = z.infer<typeof schedulingSchema>;

export class SourceRequestError extends Error {
  constructor(
    message: string,
    readonly status: number,
  ) {
    super(message);
  }
}

export async function sourceRequest(
  path: string,
  method = "GET",
  body?: unknown,
  signal?: AbortSignal,
) {
  const response = await fetch(consoleURL(path), {
    method,
    headers: { "Content-Type": "application/json" },
    body: body === undefined ? undefined : JSON.stringify(body),
    signal,
  });
  const value: unknown = await response.json().catch(() => null);
  if (!response.ok) {
    const error = z
      .object({ error: z.object({ message: z.string() }) })
      .safeParse(value);
    throw new SourceRequestError(
      error.success
        ? error.data.error.message
        : `请求失败（${response.status}）`,
      response.status,
    );
  }
  return value;
}

export function sourcePath(id: string) {
  return `/local-api/event-sources/${encodeURIComponent(id)}`;
}

export async function listSources(
  signal?: AbortSignal,
): Promise<SourceRecord[]> {
  const all: SourceRecord[] = [];
  let after = "";
  for (let page = 0; page < 100; page++) {
    const rows = z
      .array(sourceRecordSchema)
      .parse(
        await sourceRequest(
          `/local-api/event-sources?limit=100&after=${encodeURIComponent(after)}`,
          "GET",
          undefined,
          signal,
        ),
      );
    all.push(...rows);
    if (rows.length < 100)
      return all.sort((a, b) => (a.id < b.id ? -1 : a.id > b.id ? 1 : 0));
    const next = rows.at(-1)!.id;
    if (next === after) throw new Error("来源分页未前进，请重新刷新");
    after = next;
  }
  throw new Error("来源超过页面展示上限（10000 条）");
}

export function editableSpec(record?: SourceRecord): SourceSpec {
  const spec: SourceSpec = record
    ? structuredClone(record.spec)
    : {
        event_source_id: "",
        enabled: true,
        cleaner: { type: "standard" },
        scheduling: {
          cleaner: { replicas: "all", selector: {} },
          lifecycle: { replicas: "all", selector: {} },
        },
        storage: {
          type: "kafka",
          kafka: { brokers: [], topic: "", consumer_group: "" },
        },
      };
  // 输入 Kafka 省略整个 security 才会保留原凭据。Hook/Enrich 使用各自已有的占位符保留契约。
  if (record) delete spec.storage.kafka.security;
  return spec;
}

export interface PlacementForm {
  replicas: string;
  selector: Array<{ key: string; value: string }>;
}
export interface SourceForm {
  id: string;
  tenant: string;
  enabled: boolean;
  brokers: string;
  topic: string;
  group: string;
  cleaner: PlacementForm;
  lifecycle: PlacementForm;
}
export function specToForm(spec: SourceSpec): SourceForm {
  const placement = (role: SourceRole): PlacementForm => ({
    replicas: String(spec.scheduling[role].replicas),
    selector: Object.entries(spec.scheduling[role].selector).map(
      ([key, value]) => ({ key, value }),
    ),
  });
  return {
    id: spec.event_source_id,
    tenant: spec.related_tenant_id ?? "",
    enabled: spec.enabled,
    brokers: spec.storage.kafka.brokers.join("\n"),
    topic: spec.storage.kafka.topic,
    group: spec.storage.kafka.consumer_group,
    cleaner: placement("cleaner"),
    lifecycle: placement("lifecycle"),
  };
}

export class SourceValidationError extends Error {
  constructor(
    readonly field: string,
    message: string,
  ) {
    super(message);
  }
}
function invalid(field: string, message: string): never {
  throw new SourceValidationError(field, message);
}
const bytes = (value: string) => new TextEncoder().encode(value).length;

export function formToSpec(form: SourceForm, original: SourceSpec): SourceSpec {
  const placement = (
    role: SourceRole,
  ): SourceSpec["scheduling"][SourceRole] => {
    const value = form[role];
    if (value.replicas !== "all" && !/^(0|[1-9]\d*)$/.test(value.replicas))
      invalid(
        `scheduling.${role}.replicas`,
        "副本数必须是 all 或 0–10000 的整数",
      );
    const replicas = value.replicas === "all" ? "all" : Number(value.replicas);
    if (replicas !== "all" && replicas > 10000)
      invalid(`scheduling.${role}.replicas`, "副本数不能超过 10000");
    const keys = new Set<string>();
    for (const row of value.selector) {
      if (keys.has(row.key))
        invalid(`scheduling.${role}.selector`, "标签键不能重复");
      keys.add(row.key);
    }
    return {
      ...original.scheduling[role],
      replicas,
      selector: Object.fromEntries(
        value.selector.map(({ key, value }) => [key, value]),
      ),
    };
  };
  return {
    ...original,
    event_source_id: form.id,
    ...(form.tenant || original.related_tenant_id !== undefined
      ? { related_tenant_id: form.tenant }
      : {}),
    enabled: form.enabled,
    storage: {
      ...original.storage,
      kafka: {
        ...original.storage.kafka,
        brokers: form.brokers.split(/\r?\n/).filter((line) => line !== ""),
        topic: form.topic,
        consumer_group: form.group,
      },
    },
    scheduling: {
      ...original.scheduling,
      cleaner: placement("cleaner"),
      lifecycle: placement("lifecycle"),
    },
  };
}

export function parseSourceJSON(text: string): SourceSpec {
  let value: unknown;
  try {
    value = JSON.parse(text);
  } catch (error) {
    // 不显示 SyntaxError 自带的原文片段，避免把用户输入的凭据复制到错误提示。
    const message = error instanceof Error ? error.message : "";
    const position = /position (\d+)/.exec(message);
    const lines = text
      .slice(0, position ? Number(position[1]) : text.length)
      .split("\n");
    invalid(
      "json",
      `JSON 语法错误：第 ${lines.length} 行，第 ${lines.at(-1)!.length + 1} 列附近，请检查引号、逗号和括号`,
    );
  }
  const result = sourceSpecSchema.safeParse(value);
  if (!result.success) {
    const issue = result.error.issues[0];
    invalid(
      issue.path.join("."),
      `字段 ${issue.path.join(".") || "配置"} 缺失或类型不正确`,
    );
  }
  return result.data;
}

export function validateSource(
  spec: SourceSpec,
  previous?: SourceRecord,
): void {
  if (!/^[a-zA-Z0-9_-]{1,32}$/.test(spec.event_source_id))
    invalid(
      "event_source_id",
      "来源 ID 必须为 1–32 位字母、数字、下划线或连字符",
    );
  if (
    spec.related_tenant_id &&
    !/^[a-zA-Z0-9_-]{1,64}$/.test(spec.related_tenant_id)
  )
    invalid(
      "related_tenant_id",
      "关联租户必须为至多 64 位字母、数字、下划线或连字符",
    );
  const kafka = spec.storage.kafka;
  if (
    !kafka.brokers.length ||
    kafka.brokers.some(
      (b) => !b.trim() || b !== b.trim() || /\s|\p{Cc}/u.test(b),
    )
  )
    invalid(
      "storage.kafka.brokers",
      "至少填写一个有效 Broker，每行一个地址，不包含空白或控制字符",
    );
  const brokers = new Set<string>();
  for (const broker of kafka.brokers) {
    const parts = /^(?:\[([^\]]+)\]|([^:[\]]+)):(\+?\d+)$/.exec(broker);
    if (!parts || Number(parts[3]) < 1 || Number(parts[3]) > 65535)
      invalid(
        "storage.kafka.brokers",
        "Broker 必须为 host:port，端口范围为 1–65535；IPv6 地址使用方括号",
      );
    let host = (parts[1] ?? parts[2]).toLowerCase();
    if (host.includes(":")) {
      // URL 用于归一化标准 IPv6 写法；保留 Go 允许但 URL 不支持的 zone 等主机形式。
      try {
        host = new URL(`http://[${host}]:${Number(parts[3])}`).hostname;
      } catch {
        /* 以原主机名交给控制面做最终校验。 */
      }
    }
    const canonical = `${host}:${Number(parts[3])}`;
    if (brokers.has(canonical))
      invalid("storage.kafka.brokers", "Broker 地址不能重复");
    brokers.add(canonical);
  }
  if (
    !/^[a-zA-Z0-9._-]{1,249}$/.test(kafka.topic) ||
    [".", ".."].includes(kafka.topic)
  )
    invalid(
      "storage.kafka.topic",
      "Topic 必须为 1–249 位字母、数字、点、下划线或连字符，不能为 . 或 ..",
    );
  if (
    !kafka.consumer_group ||
    kafka.consumer_group !== kafka.consumer_group.trim() ||
    /\p{Cc}/u.test(kafka.consumer_group)
  )
    invalid(
      "storage.kafka.consumer_group",
      "Consumer group 必填，不能包含首尾空白或控制字符",
    );
  for (const role of sourceRoles) {
    const { replicas, selector } = spec.scheduling[role];
    if (
      replicas !== "all" &&
      (!Number.isInteger(replicas) || replicas < 0 || replicas > 10000)
    )
      invalid(
        `scheduling.${role}.replicas`,
        "副本数必须是 all 或 0–10000 的整数",
      );
    if (
      Object.keys(selector).length > 32 ||
      Object.entries(selector).some(
        ([k, v]) => !k.trim() || bytes(k) > 128 || bytes(v) > 256,
      )
    )
      invalid(
        `scheduling.${role}.selector`,
        "最多 32 个标签；键不能为空且不超过 128 字节，值不超过 256 字节",
      );
  }
  if (previous) {
    const identity = (s: SourceSpec) => [
      s.event_source_id,
      s.related_tenant_id ?? "",
      s.cleaner?.type || "standard",
      s.fingerprint_mode || "field",
      s.fingerprint_field ||
        ((s.fingerprint_mode || "field") === "field" &&
        !s.fingerprint_fields?.length
          ? "source_alert_id"
          : ""),
      s.fingerprint_fields ?? [],
      s.storage.type,
      s.storage.kafka.brokers,
      s.storage.kafka.topic,
      s.storage.kafka.consumer_group,
    ];
    if (
      JSON.stringify(identity(spec)) !== JSON.stringify(identity(previous.spec))
    )
      invalid(
        "identity",
        "已有来源的 ID、关联租户、Kafka 订阅、Cleaner 类型及指纹配置不可修改，请新建来源",
      );
  }
}

export function hasMetadataSuccess(value?: string): boolean {
  return Boolean(
    value && !value.startsWith("0001-") && Number.isFinite(Date.parse(value)),
  );
}
