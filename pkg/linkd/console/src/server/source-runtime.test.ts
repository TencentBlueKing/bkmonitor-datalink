import { afterEach, expect, it, vi } from "vitest";
import { redactedConfig, type ConsoleConfig } from "./config.js";
import { loadRuntimeSources } from "./source-runtime.js";

afterEach(() => vi.unstubAllGlobals());
const config = {
  dispatch: {
    url: "http://localhost:8080",
    apiToken: "admin-token",
    deployment: "test",
  },
  query: { timeoutMilliseconds: 1000 },
  entities: {
    events: "elasticsearch",
    alerts: "elasticsearch",
    alertLogs: "elasticsearch",
  },
} as ConsoleConfig;
const security = {
  protocol: "sasl_plaintext",
  sasl: {
    mechanism: "plain",
    username: "reader",
    password: "private-kafka-password",
  },
};
const records = [
  {
    id: "test",
    deleted: false,
    spec: {
      enabled: true,
      storage: {
        type: "kafka",
        kafka: {
          brokers: ["broker:9092"],
          topic: "raw",
          consumer_group: "cleaner",
          security,
        },
      },
      hooks: [
        {
          name: "output",
          type: "kafka",
          config: { brokers: ["out:9092"], topic: "alerts", security },
        },
      ],
    },
  },
];

it("loads complete input and output credentials only on the server", async () => {
  const fetcher = vi.fn(async () => new Response(JSON.stringify(records)));
  vi.stubGlobal("fetch", fetcher);
  const sources = await loadRuntimeSources(config);
  expect(sources[0].kafka.security.sasl?.password).toBe(
    "private-kafka-password",
  );
  expect(sources[0].kafkaHooks?.[0].connection.security.sasl?.password).toBe(
    "private-kafka-password",
  );
  expect(fetcher).toHaveBeenCalledWith(
    expect.stringContaining("include_secrets=true"),
    expect.objectContaining({
      headers: { Authorization: "Bearer admin-token" },
      cache: "no-store",
    }),
  );
  expect(
    JSON.stringify(redactedConfig({ ...config, eventSources: sources })),
  ).not.toContain("private-kafka-password");
});
it("paginates and omits deleted sources", async () => {
  const first = Array.from({ length: 100 }, (_, i) => ({
    ...records[0],
    id: `source-${i}`,
    deleted: true,
  }));
  const fetcher = vi
    .fn()
    .mockResolvedValueOnce(new Response(JSON.stringify(first)))
    .mockResolvedValueOnce(new Response(JSON.stringify(records)));
  vi.stubGlobal("fetch", fetcher);
  expect(
    (await loadRuntimeSources(config)).map((s) => s.eventSourceId),
  ).toEqual(["test"]);
  expect(fetcher.mock.calls[1][0]).toContain("after=source-99");
});
it("does not expose a rejected configuration or upstream error payload", async () => {
  vi.stubGlobal(
    "fetch",
    vi.fn(async () => new Response("private-kafka-password", { status: 403 })),
  );
  await expect(loadRuntimeSources(config)).rejects.toThrow(
    "source configuration unavailable (403)",
  );
  vi.stubGlobal(
    "fetch",
    vi.fn(
      async () =>
        new Response(
          JSON.stringify([
            {
              ...records[0],
              spec: {
                ...records[0].spec,
                storage: { type: "private-kafka-password" },
              },
            },
          ]),
        ),
    ),
  );
  await expect(loadRuntimeSources(config)).rejects.toThrow(
    "source configuration invalid",
  );
});
it("supports an empty source list", async () => {
  vi.stubGlobal(
    "fetch",
    vi.fn(async () => new Response("[]")),
  );
  expect(await loadRuntimeSources(config)).toEqual([]);
});
