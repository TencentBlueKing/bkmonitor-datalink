import { mkdtemp, rm, writeFile } from "node:fs/promises";
import { tmpdir } from "node:os";
import path from "node:path";

import { describe, expect, it, vi } from "vitest";

import { loadConfig, redactedConfig } from "./config.js";

describe("Linkd config loader", () => {
  it("redacts global KAC plugin credentials without mutating deployment configuration", async () => {
    const directory = await mkdtemp(path.join(tmpdir(), "linkd-kac-config-"));
    try {
      const configPath = path.join(directory, "linkd.yaml");
      await writeFile(
        configPath,
        `storage:
  repository: mysql
  mysql:
    address: 127.0.0.1:3306
    database: linkd
    username: linkd
blueking:
  enable_multi_tenant_mode: true
  api_url: https://blueking.example
  app_code: linkd
  app_secret: private-cmdb-secret
resources:
  cmdb: {}
plugins:
  kac:
    enabled: true
    alarm_event_index: cw_kac_saas_3.0_alarm_event
    elasticsearch:
      addresses: [https://es.example]
      basic_auth:
        username: linkd
        password: private-kac-es
    action_endpoint: https://kac.example/action
    internal_token: private-delivery-token
`,
      );
      const config = await loadConfig(configPath);
      const shown = redactedConfig(config);
      expect(shown.blueking?.app_secret).toBe("******");
      expect(JSON.stringify(shown)).not.toContain("private-cmdb-secret");
      expect(config.blueking?.app_secret).toBe("private-cmdb-secret");
      expect(shown.plugins?.kac?.internal_token).toBe("******");
      expect(shown.plugins?.kac?.elasticsearch?.basic_auth?.password).toBe(
        "******",
      );
      expect(JSON.stringify(shown)).not.toContain("private-delivery-token");
      expect(JSON.stringify(shown)).not.toContain("private-kac-es");
      expect(config.plugins?.kac?.internal_token).toBe(
        "private-delivery-token",
      );
      shown.plugins!.kac!.elasticsearch!.addresses[0] =
        "https://changed.example";
      expect(config.plugins?.kac?.elasticsearch?.addresses).toEqual([
        "https://es.example",
      ]);
    } finally {
      await rm(directory, { recursive: true, force: true });
    }
  });
  it.each([
    ["omitted control plane", "", true, true, true, false],
    ["empty control plane", "control_plane: {}\n", true, true, true, false],
    [
      "explicit manager",
      "control_plane:\n  redis_stream: {}\n",
      true,
      true,
      true,
      true,
    ],
    [
      "disabled manager",
      "control_plane:\n  redis_stream:\n    enabled: false\n",
      true,
      true,
      false,
      false,
    ],
    ["redis missing", "", false, true, false, false],
    ["lifecycle missing", "", true, false, false, false],
    [
      "disabled without dependencies",
      "control_plane:\n  redis_stream:\n    enabled: false\n",
      false,
      false,
      false,
      false,
    ],
  ])(
    "resolves default Stream management: %s",
    async (_name, controlPlane, redis, lifecycle, enabled, explicit) => {
      const directory = await mkdtemp(
        path.join(tmpdir(), "linkd-stream-defaults-"),
      );
      try {
        const configPath = path.join(directory, "linkd.yaml");
        await writeFile(
          configPath,
          `storage:
  repository: mysql
  mysql:
    address: 127.0.0.1:3306
    database: linkd
    username: linkd
${redis ? "  redis:\n    address: 127.0.0.1:6379\n" : ""}${lifecycle ? "lifecycle: {}\n" : ""}${controlPlane}`,
        );
        const config = await loadConfig(configPath);
        if (enabled) {
          expect(config.redisStreamManager).toEqual({
            explicit,
            reconcileIntervalSeconds: 10,
            operationTimeoutSeconds: 3,
            maxEntries: 100_000,
            trimBatchSize: 10_000,
            maxTrimEntriesPerCycle: 100_000,
          });
        } else {
          expect(config.redisStreamManager).toBeUndefined();
        }
      } finally {
        await rm(directory, { recursive: true, force: true });
      }
    },
  );

  it("rejects an explicitly enabled Stream manager without dependencies", async () => {
    const directory = await mkdtemp(
      path.join(tmpdir(), "linkd-stream-dependencies-"),
    );
    try {
      const configPath = path.join(directory, "linkd.yaml");
      await writeFile(
        configPath,
        `storage:
  repository: mysql
  mysql:
    address: 127.0.0.1:3306
    database: linkd
    username: linkd
control_plane:
  redis_stream:
    enabled: true
`,
      );
      await expect(loadConfig(configPath)).rejects.toThrow(
        "storage.redis and lifecycle are required",
      );
    } finally {
      await rm(directory, { recursive: true, force: true });
    }
  });

  it.each([
    [1, 1, 0, 1],
    [2, 1, 0, 2],
    [3, 1, 0, 3],
    [4, 2, 2, 4],
    [8, 4, 4, 8],
    [32, 16, 16, 32],
    [64, 32, 32, 32],
    [256, 128, 128, 32],
    [511, 128, 128, 32],
    [512, 128, 128, 32],
    [1024, 128, 128, 32],
  ])(
    "derives batch scheduling from concurrency %i",
    async (concurrency, operations, wait, parallel) => {
      const directory = await mkdtemp(path.join(tmpdir(), "linkd-batch-auto-"));
      try {
        const configPath = path.join(directory, "linkd.yaml");
        const yaml = `storage:
  repository: elasticsearch
  elasticsearch:
    addresses: [http://127.0.0.1:9200]
    index_prefix: demo
lifecycle:
  concurrency: ${concurrency}
`;
        await writeFile(configPath, yaml);
        const config = await loadConfig(configPath);
        expect(config.lifecycle?.elasticsearchWriteBatch).toMatchObject({
          max_operations: operations,
          wait_milliseconds: wait,
          read_wait_milliseconds: Math.min(10, wait),
          max_concurrent_batches: parallel,
        });
        for (const key of [
          "max_operations",
          "wait_milliseconds",
          "read_wait_milliseconds",
          "max_concurrent_batches",
        ]) {
          await writeFile(
            configPath,
            yaml + `  elasticsearch_write_batch:\n    ${key}: 1\n`,
          );
          await expect(loadConfig(configPath)).rejects.toThrow();
        }
      } finally {
        await rm(directory, { recursive: true, force: true });
      }
    },
  );

  it("derives Console sources, targets and effective EventSource runtime", async () => {
    const directory = await mkdtemp(
      path.join(tmpdir(), "linkd-console-config-"),
    );
    try {
      const configPath = path.join(directory, "linkd.yaml");
      await writeFile(
        configPath,
        `storage:
  repository: elasticsearch
  elasticsearch:
    addresses: [http://127.0.0.1:9200]
    index_prefix: demo
  redis:
    address: 127.0.0.1:6379
    password: secret
    database: 2
cleaner:
  worker_count: 4
lifecycle: {}
control_plane:
  elasticsearch:
    schema_and_active_reconcile_interval_seconds: 7200
    bucket_reconcile_interval_seconds: 28800
    archive_interval_seconds: 45
    archive_batch_size: 150
    archive_worker_count: 3
  redis_stream:
    reconcile_interval_seconds: 45
    operation_timeout_seconds: 5
    max_entries: 80000
    trim_batch_size: 4000
event_sources:
  - event_source_id: source-a
    enabled: true
    cleaner:
      runtime:
        max_batch_messages: 32
    storage:
      type: kafka
      kafka:
        brokers: [127.0.0.1:9092]
        topic: raw
        consumer_group: cleaner
telemetry:
  metrics:
    exporter: prometheus
    prometheus:
      listen_address: 127.0.0.1:9464
`,
        "utf8",
      );
      const config = await loadConfig(configPath);
      expect(config.entities.events).toBe("elasticsearch");
      expect(config.lifecycle?.elasticsearchWriteBatch).toEqual({
        enabled: true,
        max_operations: 16,
        max_bytes: 4194304,
        wait_milliseconds: 16,
        read_wait_milliseconds: 10,
        max_concurrent_batches: 32,
      });
      expect(config.lifecycle?.signal).toMatchObject({
        maxBatchMessages: 64,
        maxInflightMessages: 64,
      });
      expect(config.lifecycle?.mailbox).toMatchObject({
        maxDrainEvents: 128,
        backpressure: {
          cacheTTLSeconds: 1,
          queryTimeoutSeconds: 1,
          highWatermark: 256,
          lowWatermark: 128,
        },
      });
      expect(config.elasticsearch?.eventTargets).toEqual(["demo-events"]);
      expect(config.eventSources?.[0].runtime.worker_count).toBe(4);
      expect(config.eventSources?.[0].kafka.fetchMaxWaitMilliseconds).toBe(100);
      expect(config.eventSources?.[0].runtime.max_batch_messages).toBe(32);
      expect(config.telemetry?.listenAddress).toBe("127.0.0.1:9464");
      expect(config.redisStreamManager).toEqual({
        explicit: true,
        reconcileIntervalSeconds: 45,
        operationTimeoutSeconds: 5,
        maxEntries: 80_000,
        trimBatchSize: 4_000,
        maxTrimEntriesPerCycle: 40_000,
      });
      expect(config.elasticsearchControlPlane).toEqual({
        explicit: true,
        schemaAndActiveReconcileIntervalSeconds: 7200,
        bucketReconcileIntervalSeconds: 28800,
        archiveIntervalSeconds: 45,
        archiveBatchSize: 150,
        archiveWorkerCount: 3,
      });
      expect(config.elasticsearch?.timePartition).toMatchObject({
        eventBucketDays: 7,
        precreatePastBuckets: 1,
        precreateFutureBuckets: 1,
        maxBucketsPerEntity: 512,
      });
      const redacted = redactedConfig(config);
      expect(JSON.stringify(redacted)).not.toContain("secret");
      expect(redacted.storage.redis).toMatchObject({ password: "******" });
      expect(redacted.controlPlane.elasticsearch).toMatchObject({
        explicit: true,
        archiveBatchSize: 150,
        archiveWorkerCount: 3,
      });
    } finally {
      await rm(directory, { recursive: true, force: true });
    }
  });

  it("loads and redacts Redis Sentinel credentials", async () => {
    const directory = await mkdtemp(
      path.join(tmpdir(), "linkd-console-sentinel-"),
    );
    try {
      const configPath = path.join(directory, "linkd.yaml");
      await writeFile(
        configPath,
        `storage:
  repository: mysql
  mysql:
    address: 127.0.0.1:3306
    database: linkd
    username: linkd
    password: mysql-secret
  redis:
    mode: sentinel
    username: redis-user
    password: redis-secret
    database: 2
    sentinel:
      master_name: linkd-master
      addresses: [sentinel-a.example.com:26379, sentinel-b.example.com:26379]
      username: sentinel-user
      password: sentinel-secret
`,
        "utf8",
      );

      const config = await loadConfig(configPath);
      expect(config.redis).toEqual({
        mode: "sentinel",
        address: undefined,
        username: "redis-user",
        password: "redis-secret",
        database: 2,
        sentinel: {
          masterName: "linkd-master",
          addresses: [
            "sentinel-a.example.com:26379",
            "sentinel-b.example.com:26379",
          ],
          username: "sentinel-user",
          password: "sentinel-secret",
        },
      });
      const serialized = JSON.stringify(redactedConfig(config));
      expect(serialized).not.toContain("redis-secret");
      expect(serialized).not.toContain("sentinel-secret");
      expect(serialized).toContain('"password":"******"');
    } finally {
      await rm(directory, { recursive: true, force: true });
    }
  });

  it("rejects Redis Stream cycle trim budgets outside the command bounds", async () => {
    const directory = await mkdtemp(
      path.join(tmpdir(), "linkd-console-stream-budget-"),
    );
    try {
      for (const maxTrimEntriesPerCycle of [99, 10_001]) {
        const configPath = path.join(directory, "linkd.yaml");
        await writeFile(
          configPath,
          `storage:
  repository: mysql
  mysql:
    address: 127.0.0.1:3306
    database: linkd
    username: linkd
control_plane:
  redis_stream:
    trim_batch_size: 100
    max_trim_entries_per_cycle: ${maxTrimEntriesPerCycle}
`,
          "utf8",
        );
        await expect(loadConfig(configPath)).rejects.toThrow(
          "max_trim_entries_per_cycle",
        );
      }
    } finally {
      await rm(directory, { recursive: true, force: true });
    }
  });

  it("rejects invalid lifecycle mailbox backpressure relationships", async () => {
    const directory = await mkdtemp(
      path.join(tmpdir(), "linkd-console-backpressure-"),
    );
    try {
      for (const backpressure of [
        "cache_ttl_seconds: 2\n        query_timeout_seconds: 3\n        high_watermark: 100\n        low_watermark: 80",
        "cache_ttl_seconds: 3\n        query_timeout_seconds: 1\n        high_watermark: 80\n        low_watermark: 100",
      ]) {
        const configPath = path.join(directory, "linkd.yaml");
        await writeFile(
          configPath,
          `storage:
  repository: mysql
  mysql:
    address: 127.0.0.1:3306
    database: linkd
    username: linkd
lifecycle:
  mailbox:
    backpressure:
        ${backpressure}
`,
          "utf8",
        );
        await expect(loadConfig(configPath)).rejects.toThrow();
      }
    } finally {
      await rm(directory, { recursive: true, force: true });
    }
  });
});

describe("EventSource hooks", () => {
  it("loads multiple outputs without lifecycle.output and redacts their secrets", async () => {
    const directory = await mkdtemp(path.join(tmpdir(), "linkd-hooks-"));
    try {
      const configPath = path.join(directory, "linkd.yaml");
      const yaml = `storage:
  repository: elasticsearch
  elasticsearch:
    addresses: [http://127.0.0.1:9200]
    index_prefix: demo
event_sources:
  - event_source_id: source
    enabled: true
    hooks:
      - name: first
        type: kafka
        config:
          brokers: [kafka:9092]
          topic: first-topic
          security:
            protocol: sasl_plaintext
            sasl: {mechanism: plain, username: user, password: hook-private}
      - name: second
        type: kafka
        config: {brokers: [kafka:9092], topic: second-topic}
      - name: kac-alarm
        type: kac
        config: {brokers: [kafka:9092], topic: kac-alarm-topic}
      - name: active
        type: active-alert-by-strategy
        config:
          redis: {address: 'redis:6379', password: redis-private}
          key_prefix: active
          timeout_milliseconds: 100
    storage:
      type: kafka
      kafka: {brokers: [kafka:9092], topic: raw, consumer_group: cleaner}
`;
      await writeFile(configPath, yaml);
      const config = await loadConfig(configPath);
      expect(config.eventSources?.[0].strategyHooks).toEqual([
        expect.objectContaining({
          name: "active",
          keyPrefix: "active",
          notifyChannel: "active:changes",
          redis: expect.objectContaining({
            password: "redis-private",
            database: 0,
          }),
        }),
      ]);
      expect(config.eventSources?.[0].kafkaHooks?.map((h) => h.name)).toEqual([
        "first",
        "second",
        "kac-alarm",
      ]);
      expect(JSON.stringify(redactedConfig(config))).not.toContain(
        "hook-private",
      );
      expect(JSON.stringify(redactedConfig(config))).not.toContain(
        "redis-private",
      );
      for (const invalid of [
        yaml.replace(
          "key_prefix: active",
          "key_prefix: active\n          notify_channel: custom",
        ),
        yaml + "lifecycle:\n  output: {}\n",
        yaml.replace("timeout_milliseconds", "timeout_seconds"),
        yaml.replace("name: second", "name: first"),
        yaml.replace("timeout_milliseconds: 100", "timeout_milliseconds: 0"),
      ]) {
        await writeFile(configPath, invalid);
        await expect(loadConfig(configPath)).rejects.toThrow();
      }
    } finally {
      await rm(directory, { recursive: true, force: true });
    }
  });
});

it("loads and redacts shared resources without mutating their credentials", async () => {
  const directory = await mkdtemp(path.join(tmpdir(), "linkd-resources-"));
  try {
    const configPath = path.join(directory, "linkd.yaml");
    await writeFile(
      configPath,
      `storage:
  repository: mysql
  mysql: {address: '127.0.0.1:3306', database: linkd, username: reader}
resources:
  mysql: {address: 'kingeye:3306', database: kingeye, username: reader, password: mysql-private}
  onemodel:
    addresses: ['http://onemodel:9200']
    api_key: es-private
    basic_auth: {username: reader, password: basic-private}
  kingeye_display:
    redis:
      mode: sentinel
      password: redis-private
      sentinel: {master_name: master, addresses: ['sentinel:26379'], password: sentinel-private}
  dynamic_group:
    tenants:
      tenant-a:
        key_prefix: 'bk_monitor:'
        redis: {address: 'redis:6379', database: 1, password: group-private}
`,
    );
    const config = await loadConfig(configPath);
    expect(config.resources?.mysql?.password).toBe("mysql-private");
    const text = JSON.stringify(redactedConfig(config));
    for (const secret of [
      "mysql-private",
      "es-private",
      "basic-private",
      "redis-private",
      "sentinel-private",
      "group-private",
    ])
      expect(text).not.toContain(secret);
    expect(config.resources?.onemodel?.api_key).toBe("es-private");
    expect(text).toContain("resources");
  } finally {
    await rm(directory, { recursive: true, force: true });
  }
});

describe("internal JWT configuration", () => {
  it("loads defaults, overrides YAML and excludes secrets from browser config", async () => {
    const directory = await mkdtemp(path.join(tmpdir(), "linkd-jwt-"));
    const configPath = path.join(directory, "linkd.yaml");
    try {
      vi.stubEnv("LINKD_JWT_SECRET_KEY", undefined);
      vi.stubEnv("LINKD_JWT_USERNAME", undefined);
      await writeFile(
        configPath,
        "storage:\n  repository: elasticsearch\n  elasticsearch:\n    addresses: [http://localhost:9200]\ndispatch:\n  jwt:\n    secret_key: yaml-secret\n",
      );
      expect((await loadConfig(configPath)).dispatch?.jwt).toEqual({
        secretKey: "yaml-secret",
        username: "admin",
      });
      vi.stubEnv("LINKD_JWT_SECRET_KEY", "env-secret");
      vi.stubEnv("LINKD_JWT_USERNAME", "service");
      const config = await loadConfig(configPath);
      expect(config.dispatch?.jwt).toEqual({
        secretKey: "env-secret",
        username: "service",
      });
      expect(JSON.stringify(redactedConfig(config))).not.toContain(
        "env-secret",
      );
      vi.stubEnv("LINKD_JWT_SECRET_KEY", "");
      expect((await loadConfig(configPath)).dispatch?.jwt.secretKey).toBe("");
      vi.stubEnv("LINKD_JWT_USERNAME", " ");
      await expect(loadConfig(configPath)).rejects.toThrow("username");
      vi.stubEnv("LINKD_JWT_USERNAME", undefined);
      await writeFile(
        configPath,
        "storage:\n  repository: elasticsearch\n  elasticsearch:\n    addresses: [http://localhost:9200]\ndispatch:\n  api_token: old-token\n",
      );
      await expect(loadConfig(configPath)).rejects.toThrow("api_token");
    } finally {
      vi.unstubAllEnvs();
      await rm(directory, { recursive: true, force: true });
    }
  });
});

it.each([
  "resources:\n  kac_delivery: []\n",
  "plugins:\n  kac:\n    enabled: true\n",
  "plugins:\n  kac:\n    projection_endpoint: https://kac.example/projection\n",
  "plugins:\n  kac:\n    identities: []\n",
  "resources:\n  cmdb:\n    mode: apigw\n",
  "resources:\n  cmdb:\n    identities: []\n",
  "resources:\n  cmdb: {}\n",
  "blueking:\n  app_code: linkd\n",
  "blueking:\n  enable_multi_tenant_mode: 'true'\n",
])(
  "rejects invalid global BlueKing or old CMDB fields: %s",
  async (fragment) => {
    const directory = await mkdtemp(
      path.join(tmpdir(), "linkd-blueking-config-"),
    );
    try {
      const configPath = path.join(directory, "linkd.yaml");
      await writeFile(
        configPath,
        "storage:\n  repository: mysql\n  mysql:\n    address: localhost:3306\n    database: linkd\n    username: linkd\n" +
          fragment,
      );
      await expect(loadConfig(configPath)).rejects.toThrow();
    } finally {
      await rm(directory, { recursive: true, force: true });
    }
  },
);
