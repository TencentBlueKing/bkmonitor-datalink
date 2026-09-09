import { mkdtemp, rm, writeFile } from "node:fs/promises";
import { tmpdir } from "node:os";
import path from "node:path";

import { describe, expect, it } from "vitest";

import { loadConfig, redactedConfig } from "./config.js";

describe("Linkd config loader", () => {
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
      expect(config.eventSources?.[0].kafkaHooks?.map((h) => h.name)).toEqual([
        "first",
        "second",
      ]);
      expect(JSON.stringify(redactedConfig(config))).not.toContain(
        "hook-private",
      );
      expect(JSON.stringify(redactedConfig(config))).not.toContain(
        "redis-private",
      );
      for (const invalid of [
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
