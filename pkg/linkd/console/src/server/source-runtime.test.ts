import { afterEach, expect, it, vi } from "vitest";
import type { ConsoleConfig } from "./config.js";
import { sourceRuntime } from "./source-runtime.js";

afterEach(() => vi.unstubAllGlobals());

it("discovers zero or multiple named outputs without connecting with redacted credentials", async () => {
  const spec = {
    enabled: true,
    cleaner: { type: "standard" },
    storage: {
      kafka: {
        brokers: ["input:9092"],
        topic: "raw",
        consumer_group: "cleaner",
      },
    },
  };
  const records = [
    { id: "empty", deleted: false, spec },
    {
      id: "source",
      deleted: false,
      spec: {
        ...spec,
        hooks: [
          {
            name: "one",
            type: "kafka",
            config: {
              brokers: ["output:9092"],
              topic: "alerts",
              security: { sasl: { password: "******" } },
            },
          },
          {
            name: "two",
            type: "kafka",
            config: { brokers: ["output:9092"], topic: "alerts" },
          },
          {
            name: "active",
            type: "active-alert-by-strategy",
            config: { redis: { password: "******" }, key_prefix: "active" },
          },
        ],
      },
    },
  ];
  const fetcher = vi.fn(
    async (url: string) =>
      new Response(
        JSON.stringify(
          url.includes("/runtime") ? { metadata: {}, tasks: {} } : records,
        ),
      ),
  );
  vi.stubGlobal("fetch", fetcher);
  const result = await sourceRuntime({
    dispatch: {
      url: "http://localhost:8080",
      apiToken: "token",
      deployment: "test",
    },
    query: { timeoutMilliseconds: 1000 },
  } as ConsoleConfig);
  const outputs = result.kafka.resources.filter((r) => r.kind === "output");
  expect(outputs.map((r) => [r.eventSourceId, r.hookName, r.topic])).toEqual([
    ["source", "one", "alerts"],
    ["source", "two", "alerts"],
  ]);
  expect(
    outputs.every(
      (r) => r.status === "unavailable" && r.partitions.length === 0,
    ),
  ).toBe(true);
  expect(JSON.stringify(result)).not.toContain("******");
  expect(fetcher).toHaveBeenCalledTimes(2);
});
