import { z } from "zod";
import type { ConsoleConfig } from "./config.js";
import type { KafkaInfrastructure } from "../shared/contracts.js";

const outputHookSchema = z.object({
  name: z.string(),
  type: z.string(),
  config: z.object({
    brokers: z.array(z.string()).optional(),
    topic: z.string().optional(),
    client_id: z.string().optional(),
  }),
});
const recordSchema = z.object({
  id: z.string(),
  deleted: z.boolean(),
  spec: z.object({
    enabled: z.boolean(),
    hooks: z.array(outputHookSchema).default([]),
    cleaner: z.object({
      type: z.string(),
      runtime: z.record(z.string(), z.number()).optional(),
    }),
    storage: z.object({
      kafka: z.object({
        brokers: z.array(z.string()),
        topic: z.string(),
        consumer_group: z.string(),
      }),
    }),
  }),
});
const stateSchema = z.object({
  metadata: z.record(
    z.string(),
    z.object({ partitions: z.number().int().min(0), error: z.string() }),
  ),
  tasks: z.record(
    z.string(),
    z.object({
      id: z.string(),
      source: z.string(),
      role: z.string(),
      worker: z.string(),
      phase: z.string(),
      epoch: z.number(),
      partitions: z.array(z.string()).optional(),
    }),
  ),
});
// 输入 Kafka 诊断使用控制面探测及 worker 实际分配，避免拿脱敏凭据重新连接。
export async function sourceRuntime(config: ConsoleConfig) {
  const dispatch = config.dispatch;
  if (!dispatch) throw new Error("dispatch is not configured");
  async function get(path: string) {
    const response = await fetch(`${dispatch!.url.replace(/\/$/, "")}${path}`, {
      headers: { Authorization: `Bearer ${dispatch!.apiToken}` },
      signal: AbortSignal.timeout(config.query.timeoutMilliseconds),
    });
    if (!response.ok) throw new Error("source runtime unavailable");
    return response.json() as Promise<unknown>;
  }
  const state = stateSchema.parse(await get("/api/v1/runtime"));
  const records: z.infer<typeof recordSchema>[] = [];
  let after = "";
  for (let page = 0; page < 100; page++) {
    const batch = z
      .array(recordSchema)
      .parse(
        await get(
          `/api/v1/event-sources?limit=100&after=${encodeURIComponent(after)}`,
        ),
      );
    records.push(...batch);
    if (batch.length < 100) break;
    after = batch.at(-1)!.id;
  }
  const sources = records.filter((r) => !r.deleted);
  const resources: KafkaInfrastructure["resources"] = sources.map((record) => {
    const source = record.spec;
    const metadata = state.metadata[record.id];
    const tasks = Object.values(state.tasks).filter(
      (t) =>
        t.source === record.id && t.role === "cleaner" && t.phase === "running",
    );
    const assignments = tasks.map((t) => ({
      memberId: t.id,
      clientId: `linkd-${t.id}-${t.epoch}`,
      clientHost: t.worker,
      partitions: (t.partitions ?? [])
        .map((p) => Number(p.slice(p.lastIndexOf("/") + 1)))
        .filter(Number.isInteger),
    }));
    return {
      kind: "input",
      eventSourceId: record.id,
      status: metadata?.partitions ? "available" : "unavailable",
      message:
        metadata?.error ||
        "来自调度器元数据与 worker 分配；offset/ISR 未在此重复探测",
      brokers: source.storage.kafka.brokers,
      topic: source.storage.kafka.topic,
      consumerGroup: source.storage.kafka.consumer_group,
      group: {
        state: "scheduler snapshot",
        protocol: "consumer group",
        members: assignments,
      },
      partitions: Array.from(
        { length: metadata?.partitions ?? 0 },
        (_, partition) => ({
          partition,
          leader: null,
          replicas: [],
          isr: [],
          status: "available" as const,
          issues: [],
          members: assignments
            .filter((m) => m.partitions.includes(partition))
            .map((m) => m.memberId),
        }),
      ),
      issues: [],
    };
  });
  // 管理接口只返回脱敏配置，不能用星号凭据连接输出集群；如实展示声明的目标。
  for (const record of sources) {
    for (const hook of record.spec.hooks) {
      if (hook.type !== "kafka" || !hook.config.brokers || !hook.config.topic)
        continue;
      resources.push({
        kind: "output",
        eventSourceId: record.id,
        hookName: hook.name,
        brokers: hook.config.brokers,
        topic: hook.config.topic,
        clientId: hook.config.client_id,
        status: "unavailable",
        message: "来自 EventSource hook 配置；输出集群 metadata 未探测",
        partitions: [],
        issues: [],
      });
    }
  }
  return {
    eventSources: sources.map((r) => ({
      eventSourceId: r.id,
      enabled: r.spec.enabled,
      cleanerType: r.spec.cleaner.type,
      runtime: r.spec.cleaner.runtime ?? {},
      kafka: {
        ...r.spec.storage.kafka,
        consumerGroup: r.spec.storage.kafka.consumer_group,
      },
    })),
    kafka: {
      status:
        resources.length > 0 && resources.every((r) => r.status === "available")
          ? "available"
          : resources.some((r) => r.status === "available")
            ? "partial"
            : "unavailable",
      resources,
    } satisfies KafkaInfrastructure,
  };
}
