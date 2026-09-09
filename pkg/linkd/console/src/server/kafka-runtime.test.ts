import { afterEach, beforeEach, expect, it, vi } from "vitest";
import { KafkaConnector } from "./kafka.js";
import type { ConsoleConfig } from "./config.js";

const mock = vi.hoisted(() => ({
  clients: [] as Array<Record<string, unknown>>,
  factory: vi.fn(),
}));
vi.mock("kafkajs", async (importOriginal) => {
  const actual = await importOriginal<typeof import("kafkajs")>();
  return {
    ...actual,
    Kafka: class {
      constructor(config: Record<string, unknown>) {
        mock.clients.push(config);
      }
      admin() {
        return mock.factory();
      }
    },
  };
});
const config = {
  dispatch: {
    url: "http://control-plane:8080",
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
const source = {
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
  },
};
function admin() {
  return {
    connect: vi.fn(async () => undefined),
    disconnect: vi.fn(async () => undefined),
    describeCluster: vi.fn(async () => ({
      clusterId: "cluster",
      controller: 1,
      brokers: [{ nodeId: 1, host: "broker", port: 9092 }],
    })),
    fetchTopicMetadata: vi.fn(async () => ({
      topics: [
        {
          name: "raw",
          partitions: [
            { partitionId: 0, leader: 1, replicas: [1, 2], isr: [1, 2] },
          ],
        },
      ],
    })),
    fetchTopicOffsets: vi.fn(async () => [
      { partition: 0, low: "0", high: "900719925474099312345" },
    ]),
    describeGroups: vi.fn(async () => ({
      groups: [
        {
          groupId: "cleaner",
          state: "Stable",
          protocol: "range",
          members: [
            {
              memberId: "member",
              clientId: "linkd-test",
              clientHost: "worker",
              memberAssignment: Buffer.from([
                0, 0, 0, 0, 0, 1, 0, 3, 114, 97, 119, 0, 0, 0, 1, 0, 0, 0, 0, 0,
                0, 0, 0,
              ]),
            },
          ],
        },
      ],
    })),
    fetchOffsets: vi.fn(async () => [
      {
        topic: "raw",
        partitions: [{ partition: 0, offset: "900719925474099312342" }],
      },
    ]),
  };
}
beforeEach(() => {
  mock.clients = [];
  mock.factory.mockReset();
  mock.factory.mockImplementation(admin);
  vi.stubGlobal(
    "fetch",
    vi.fn(async () => new Response(JSON.stringify([source]))),
  );
});
afterEach(() => vi.unstubAllGlobals());
it("queries real metadata and offsets with complete credentials without exposing them", async () => {
  const connector = new KafkaConnector(config);
  const result = await connector.inspectRuntime();
  expect(mock.clients[0].sasl).toEqual({
    mechanism: "plain",
    username: "reader",
    password: "private-kafka-password",
  });
  expect(result.kafka.resources[0].partitions[0]).toMatchObject({
    leader: 1,
    isr: [1, 2],
    replicas: [1, 2],
    highOffset: "900719925474099312345",
    committedOffset: "900719925474099312342",
    lag: "3",
    members: ["member"],
    status: "available",
  });
  expect(result.kafka.resources[0].group?.state).toBe("Stable");
  expect(JSON.stringify(result)).not.toContain("private-kafka-password");
  expect(JSON.stringify(result)).not.toContain("admin-token");
});
it("retains unknown offsets as partial and does not infer a healthy partition", async () => {
  const client = admin();
  client.fetchOffsets.mockResolvedValue([
    { topic: "raw", partitions: [{ partition: 0, offset: "-1" }] },
  ]);
  mock.factory.mockReturnValue(client);
  const result = await new KafkaConnector(config).inspect();
  expect(result.resources[0].partitions[0]).toMatchObject({
    status: "partial",
    committedOffset: undefined,
    lag: undefined,
  });
  expect(client.disconnect).toHaveBeenCalledOnce();
});
it("reports failed queries without reflecting credentials or stale scheduler status", async () => {
  const client = admin();
  client.connect.mockRejectedValue(new Error("private-kafka-password"));
  mock.factory.mockReturnValue(client);
  const result = await new KafkaConnector(config).inspect();
  expect(result.resources[0]).toMatchObject({
    status: "unavailable",
    partitions: [],
    message: expect.stringContaining("Kafka 查询失败"),
  });
  expect(JSON.stringify(result)).not.toContain("private-kafka-password");
  expect(client.disconnect).toHaveBeenCalledOnce();
});
it("shares concurrent refreshes and caps Admin connections at four", async () => {
  vi.stubGlobal(
    "fetch",
    vi.fn(
      async () =>
        new Response(
          JSON.stringify(
            Array.from({ length: 9 }, (_, i) => ({
              ...source,
              id: `source-${i}`,
            })),
          ),
        ),
    ),
  );
  let active = 0,
    peak = 0;
  mock.factory.mockImplementation(() => {
    const client = admin();
    client.connect.mockImplementation(async () => {
      active++;
      peak = Math.max(peak, active);
      await new Promise((resolve) => setTimeout(resolve, 5));
    });
    client.disconnect.mockImplementation(async () => {
      active--;
    });
    return client;
  });
  const connector = new KafkaConnector(config);
  await Promise.all([connector.inspect(), connector.inspectRuntime()]);
  expect(mock.clients).toHaveLength(9);
  expect(peak).toBe(4);
  expect(active).toBe(0);
  await connector.inspect();
  expect(mock.clients).toHaveLength(18);
});
it("queries named output hooks using their independent credentials", async () => {
  vi.stubGlobal(
    "fetch",
    vi.fn(
      async () =>
        new Response(
          JSON.stringify([
            {
              ...source,
              spec: {
                ...source.spec,
                hooks: [
                  {
                    name: "archive",
                    type: "kafka",
                    config: {
                      brokers: ["output:9092"],
                      topic: "raw",
                      security: {
                        ...security,
                        sasl: { ...security.sasl, password: "output-secret" },
                      },
                    },
                  },
                ],
              },
            },
          ]),
        ),
    ),
  );
  const result = await new KafkaConnector(config).inspect();
  expect(mock.clients[1].sasl).toMatchObject({ password: "output-secret" });
  expect(result.resources[1]).toMatchObject({
    kind: "output",
    hookName: "archive",
    status: "available",
  });
  expect(JSON.stringify(result)).not.toContain("output-secret");
});

it("passes inline TLS material only to the Kafka client", async () => {
  const tls = {
    ca_pem: "private-ca",
    client_cert_pem: "private-cert",
    client_key_pem: "private-key",
    server_name: "kafka.example.com",
    insecure_skip_verify: false,
  };
  vi.stubGlobal(
    "fetch",
    vi.fn(
      async () =>
        new Response(
          JSON.stringify([
            {
              ...source,
              spec: {
                ...source.spec,
                storage: {
                  type: "kafka",
                  kafka: {
                    ...source.spec.storage.kafka,
                    security: { protocol: "ssl", tls },
                  },
                },
              },
            },
          ]),
        ),
    ),
  );
  const result = await new KafkaConnector(config).inspectRuntime();
  expect(mock.clients[0].ssl).toEqual({
    ca: ["private-ca"],
    cert: "private-cert",
    key: "private-key",
    servername: "kafka.example.com",
    rejectUnauthorized: true,
  });
  expect(JSON.stringify(result)).not.toContain("private-key");
  expect(JSON.stringify(result)).not.toContain("private-cert");
});

it("isolates failed sources and releases the refresh after configuration errors", async () => {
  const connector = new KafkaConnector(config);
  vi.stubGlobal(
    "fetch",
    vi
      .fn()
      .mockResolvedValueOnce(new Response("unavailable", { status: 503 }))
      .mockResolvedValueOnce(
        new Response(JSON.stringify([source, { ...source, id: "second" }])),
      ),
  );
  await expect(connector.inspect()).rejects.toThrow(
    "source configuration unavailable",
  );
  const failed = admin();
  failed.connect.mockRejectedValue(new Error("offline"));
  mock.factory.mockReturnValueOnce(failed).mockImplementation(admin);
  const result = await connector.inspect();
  expect(result.status).toBe("partial");
  expect(result.resources.map((r) => r.status)).toEqual([
    "unavailable",
    "available",
  ]);
});
