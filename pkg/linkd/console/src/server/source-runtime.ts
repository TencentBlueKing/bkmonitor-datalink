import path from "node:path";
import { z } from "zod";
import {
  normalizeEventSources,
  type ConsoleConfig,
  type EventSourceConfig,
} from "./config.js";

const recordSchema = z.object({
  id: z.string(),
  deleted: z.boolean(),
  spec: z.record(z.string(), z.unknown()),
});

// 管理 token 仅在 Console 服务端使用；完整配置不通过浏览器管理代理透传。
export async function loadRuntimeSources(
  config: ConsoleConfig,
): Promise<EventSourceConfig[]> {
  const dispatch = config.dispatch;
  if (!dispatch?.apiToken) throw new Error("dispatch is not configured");
  const records: z.infer<typeof recordSchema>[] = [];
  let after = "";
  for (let page = 0; page < 100; page++) {
    const response = await fetch(
      `${dispatch.url.replace(/\/$/, "")}/api/v1/event-sources?limit=100&after=${encodeURIComponent(after)}&include_secrets=true`,
      {
        headers: { Authorization: `Bearer ${dispatch.apiToken}` },
        signal: AbortSignal.timeout(config.query.timeoutMilliseconds),
        cache: "no-store",
      },
    );
    if (!response.ok)
      throw new Error(`source configuration unavailable (${response.status})`);
    const batch = z.array(recordSchema).parse(await response.json());
    records.push(...batch);
    if (batch.length < 100) {
      try {
        return normalizeEventSources(
          records
            .filter((r) => !r.deleted)
            .map((r) => ({ ...r.spec, event_source_id: r.id })),
          path.dirname(config.configPath ?? "linkd.yaml"),
        );
      } catch {
        throw new Error("source configuration invalid");
      }
    }
    after = batch.at(-1)!.id;
  }
  throw new Error("source configuration exceeds 10000 records");
}
