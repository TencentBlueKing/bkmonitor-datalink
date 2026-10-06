import { mkdtemp, rm, writeFile } from "node:fs/promises";
import { tmpdir } from "node:os";
import path from "node:path";

import { describe, expect, it } from "vitest";

import { loadConfig, redactedConfig } from "./config.js";

describe("Linkd config loader", () => {
  it("derives DevTools sources, targets and effective EventSource runtime", async () => {
    const directory = await mkdtemp(
      path.join(tmpdir(), "linkd-devtools-config-"),
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
lifecycle:
  output:
    kafka:
      brokers: [127.0.0.1:9092]
      topic: output
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
      expect(config.elasticsearch?.eventTargets).toEqual(["demo-events"]);
      expect(config.eventSources?.[0].runtime.worker_count).toBe(4);
      expect(config.eventSources?.[0].runtime.max_batch_messages).toBe(32);
      expect(config.telemetry?.listenAddress).toBe("127.0.0.1:9464");
      expect(config.redisStreamManager).toEqual({
        reconcileIntervalSeconds: 45,
        operationTimeoutSeconds: 5,
        maxEntries: 80_000,
        trimBatchSize: 4_000,
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

  it("rejects invalid lifecycle mailbox backpressure relationships", async () => {
    const directory = await mkdtemp(
      path.join(tmpdir(), "linkd-devtools-backpressure-"),
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
  output:
    kafka:
      brokers: [127.0.0.1:9092]
      topic: output
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
