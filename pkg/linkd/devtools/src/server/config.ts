import { readFile } from "node:fs/promises";
import { isIP } from "node:net";
import path from "node:path";
import { parseArgs } from "node:util";

import { parse } from "yaml";
import { z } from "zod";

const cleanerRuntimeSchema = z
  .object({
    worker_count: z.number().int().positive(),
    max_batch_messages: z.number().int().positive(),
    max_batch_bytes: z.number().int().positive(),
    batch_wait_milliseconds: z.number().int().positive(),
    max_concurrent_batches: z.number().int().positive(),
    max_inflight_messages: z.number().int().positive(),
    max_inflight_bytes: z.number().int().positive(),
    max_inflight_per_lane: z.number().int().positive(),
    resume_inflight_per_lane: z.number().int().positive(),
    process_timeout_seconds: z.number().int().positive(),
    retry_max_attempts: z.number().int().positive(),
    retry_max_elapsed_seconds: z.number().int().positive(),
    shutdown_drain_timeout_seconds: z.number().int().positive(),
  })
  .partial()
  .default({});

const kafkaSecuritySchema = z
  .object({
    protocol: z
      .enum(["plaintext", "ssl", "sasl_plaintext", "sasl_ssl"])
      .default("plaintext"),
    tls: z
      .object({
        ca_file: z.string().optional(),
        ca_pem: z.string().optional(),
        client_cert_file: z.string().optional(),
        client_key_file: z.string().optional(),
        client_cert_pem: z.string().optional(),
        client_key_pem: z.string().optional(),
        server_name: z.string().optional(),
        insecure_skip_verify: z.boolean().default(false),
      })
      .passthrough()
      .optional(),
    sasl: z
      .object({
        mechanism: z.enum(["plain", "scram_sha_256", "scram_sha_512"]),
        username: z.string(),
        password: z.string(),
      })
      .optional(),
  })
  .passthrough()
  .default({ protocol: "plaintext" });

const kafkaConfigSchema = z.object({
  brokers: z.array(z.string().min(1)).min(1),
  topic: z.string().min(1),
  consumer_group: z.string().min(1).optional(),
  client_id: z.string().optional(),
  security: kafkaSecuritySchema,
});

const lifecycleMailboxBackpressureSchema = z
  .object({
    cache_ttl_seconds: z.number().int().min(1).max(60).default(3),
    query_timeout_seconds: z.number().int().positive().default(1),
    high_watermark: z.number().int().positive().default(100_000),
    low_watermark: z.number().int().positive().default(80_000),
  })
  .superRefine((value, context) => {
    if (value.query_timeout_seconds > value.cache_ttl_seconds) {
      context.addIssue({
        code: "custom",
        path: ["query_timeout_seconds"],
        message: "must not exceed cache_ttl_seconds",
      });
    }
    if (value.low_watermark >= value.high_watermark) {
      context.addIssue({
        code: "custom",
        path: ["low_watermark"],
        message: "must be less than high_watermark",
      });
    }
  });

const linkdConfigSchema = z
  .object({
    storage: z
      .object({
        repository: z.enum(["mysql", "elasticsearch"]),
        mysql: z
          .object({
            address: z.string().min(1),
            database: z.string().min(1),
            username: z.string().min(1),
            password: z.string().default(""),
          })
          .optional(),
        elasticsearch: z
          .object({
            addresses: z.array(z.string().url()).min(1),
            index_prefix: z.string().min(1).default("linkd"),
            time_partition: z
              .object({
                event_bucket_days: z.number().int().positive().default(7),
                alert_history_bucket_days: z
                  .number()
                  .int()
                  .positive()
                  .default(7),
                alert_log_bucket_days: z.number().int().positive().default(7),
                precreate_past_buckets: z
                  .number()
                  .int()
                  .nonnegative()
                  .default(1),
                precreate_future_buckets: z
                  .number()
                  .int()
                  .positive()
                  .default(1),
                max_buckets_per_entity: z
                  .number()
                  .int()
                  .positive()
                  .default(512),
                max_future_skew_seconds: z
                  .number()
                  .int()
                  .nonnegative()
                  .default(300),
              })
              .default({
                event_bucket_days: 7,
                alert_history_bucket_days: 7,
                alert_log_bucket_days: 7,
                precreate_past_buckets: 1,
                precreate_future_buckets: 1,
                max_buckets_per_entity: 512,
                max_future_skew_seconds: 300,
              }),
            api_key: z.string().optional(),
            basic_auth: z
              .object({ username: z.string(), password: z.string() })
              .optional(),
          })
          .optional(),
        redis: z
          .object({
            address: z.string().min(1),
            username: z.string().optional(),
            password: z.string().optional(),
            database: z.number().int().nonnegative().default(0),
          })
          .optional(),
      })
      .passthrough(),
    cleaner: cleanerRuntimeSchema,
    lifecycle: z
      .object({
        concurrency: z.number().int().positive().default(8),
        process_timeout_seconds: z.number().int().positive().default(30),
        retry_max_attempts: z.number().int().positive().default(3),
        retry_max_elapsed_seconds: z.number().int().positive().default(120),
        signal: z
          .object({
            stream: z.string().default("linkd:lifecycle:signals"),
            group: z.string().default("linkd-lifecycle"),
            consumer_prefix: z.string().default("linkd-lifecycle"),
            claim_min_idle_seconds: z.number().int().positive().default(300),
          })
          .default({
            stream: "linkd:lifecycle:signals",
            group: "linkd-lifecycle",
            consumer_prefix: "linkd-lifecycle",
            claim_min_idle_seconds: 300,
          }),
        mailbox: z
          .object({
            key_prefix: z.string().default("linkd:lifecycle:mailbox"),
            max_pending: z.number().int().positive().default(128),
            max_drain_events: z.number().int().positive().default(512),
            backpressure: lifecycleMailboxBackpressureSchema.default({
              cache_ttl_seconds: 3,
              query_timeout_seconds: 1,
              high_watermark: 100_000,
              low_watermark: 80_000,
            }),
          })
          .default({
            key_prefix: "linkd:lifecycle:mailbox",
            max_pending: 128,
            max_drain_events: 512,
            backpressure: {
              cache_ttl_seconds: 3,
              query_timeout_seconds: 1,
              high_watermark: 100_000,
              low_watermark: 80_000,
            },
          }),
        lock: z
          .object({
            key_prefix: z.string().default("linkd:lifecycle:lock"),
            ttl_seconds: z.number().int().positive().default(60),
            renew_interval_seconds: z.number().int().positive().default(20),
          })
          .default({
            key_prefix: "linkd:lifecycle:lock",
            ttl_seconds: 60,
            renew_interval_seconds: 20,
          }),
        output: z.object({ kafka: kafkaConfigSchema }).passthrough(),
      })
      .passthrough()
      .optional(),
    control_plane: z
      .object({
        elasticsearch: z
          .object({
            schema_and_active_reconcile_interval_seconds: z
              .number()
              .int()
              .positive()
              .default(3600),
            bucket_reconcile_interval_seconds: z
              .number()
              .int()
              .positive()
              .default(21600),
            archive_interval_seconds: z.number().int().positive().default(30),
            archive_batch_size: z.number().int().positive().default(1000),
            archive_worker_count: z.number().int().positive().default(4),
          })
          .optional(),
        redis_stream: z
          .object({
            reconcile_interval_seconds: z.number().int().positive().default(60),
            operation_timeout_seconds: z.number().int().positive().default(10),
            max_entries: z.number().int().positive().default(100_000),
            trim_batch_size: z.number().int().positive().default(10_000),
          })
          .optional(),
      })
      .passthrough()
      .optional(),
    telemetry: z
      .object({
        metrics: z.object({
          exporter: z.string().optional(),
          prometheus: z
            .object({ listen_address: z.string().optional() })
            .default({}),
        }),
      })
      .passthrough()
      .optional(),
    event_sources: z
      .array(
        z
          .object({
            event_source_id: z.string().min(1),
            enabled: z.boolean(),
            cleaner: z
              .object({
                type: z.string().default("standard"),
                runtime: cleanerRuntimeSchema.optional(),
              })
              .default({ type: "standard" }),
            storage: z.object({
              type: z.literal("kafka"),
              kafka: kafkaConfigSchema.extend({
                consumer_group: z.string().min(1),
              }),
            }),
          })
          .passthrough(),
      )
      .default([]),
  })
  .passthrough();

export type CleanerRuntime = Required<z.infer<typeof cleanerRuntimeSchema>>;
export type KafkaSecurity = z.infer<typeof kafkaSecuritySchema>;

interface AuthConfig {
  apiKey?: string;
  username?: string;
  password?: string;
}

export interface KafkaConnection {
  brokers: string[];
  topic: string;
  consumerGroup?: string;
  clientId?: string;
  security: KafkaSecurity;
}

export interface EventSourceConfig {
  eventSourceId: string;
  enabled: boolean;
  cleanerType: string;
  runtime: CleanerRuntime;
  kafka: KafkaConnection & { consumerGroup: string };
}

export interface DevtoolsConfig {
  configPath?: string;
  server: { host: string; port: number };
  query: {
    defaultRangeSeconds: number;
    maxRangeSeconds: number;
    defaultLimit: number;
    maxLimit: number;
    timeoutMilliseconds: number;
  };
  prometheus?: { baseUrl: string; auth: AuthConfig };
  mysql?: {
    host: string;
    port: number;
    database: string;
    username: string;
    password: string;
    connectionLimit: number;
  };
  elasticsearch?: {
    baseUrl: string;
    baseUrls?: string[];
    auth: AuthConfig;
    eventTargets: string[];
    alertTargets: string[];
    alertLogTargets: string[];
    indexPrefix?: string;
    timePartition?: {
      eventBucketDays: number;
      alertHistoryBucketDays: number;
      alertLogBucketDays: number;
      precreatePastBuckets: number;
      precreateFutureBuckets: number;
      maxBucketsPerEntity: number;
      maxFutureSkewSeconds: number;
    };
  };
  redis?: {
    address: string;
    username?: string;
    password?: string;
    database: number;
  };
  lifecycle?: {
    concurrency: number;
    processTimeoutSeconds: number;
    retryMaxAttempts: number;
    retryMaxElapsedSeconds: number;
    signal: {
      stream: string;
      group: string;
      consumerPrefix: string;
      claimMinIdleSeconds: number;
    };
    mailbox: {
      keyPrefix: string;
      maxPending: number;
      maxDrainEvents: number;
      backpressure: {
        cacheTTLSeconds: number;
        queryTimeoutSeconds: number;
        highWatermark: number;
        lowWatermark: number;
      };
    };
    lock: {
      keyPrefix: string;
      ttlSeconds: number;
      renewIntervalSeconds: number;
    };
    outputKafka: KafkaConnection;
  };
  redisStreamManager?: {
    reconcileIntervalSeconds: number;
    operationTimeoutSeconds: number;
    maxEntries: number;
    trimBatchSize: number;
  };
  elasticsearchControlPlane?: {
    explicit: boolean;
    schemaAndActiveReconcileIntervalSeconds: number;
    bucketReconcileIntervalSeconds: number;
    archiveIntervalSeconds: number;
    archiveBatchSize: number;
    archiveWorkerCount: number;
  };
  telemetry?: { listenAddress?: string };
  eventSources?: EventSourceConfig[];
  entities: {
    alerts: "mysql" | "elasticsearch";
    events: "mysql" | "elasticsearch";
    alertLogs: "mysql" | "elasticsearch";
  };
}

export async function loadConfig(
  configPathOverride?: string,
): Promise<DevtoolsConfig> {
  const configPath = path.resolve(
    configPathOverride ??
      process.env.LINKD_CONFIG ??
      cliConfigPath() ??
      "../configs/linkd.yaml",
  );
  const decoded = linkdConfigSchema.parse(
    parse(await readFile(configPath, "utf8")) as unknown,
  );
  const configDir = path.dirname(configPath);
  const cleanerDefaults = withCleanerDefaults(decoded.cleaner);
  const repository = decoded.storage.repository;
  const query = {
    defaultRangeSeconds: envInteger(
      "LINKD_DEVTOOLS_DEFAULT_RANGE_SECONDS",
      3600,
      60,
      604800,
    ),
    maxRangeSeconds: envInteger(
      "LINKD_DEVTOOLS_MAX_RANGE_SECONDS",
      604800,
      3600,
      604800,
    ),
    defaultLimit: envInteger("LINKD_DEVTOOLS_DEFAULT_LIMIT", 50, 1, 200),
    maxLimit: envInteger("LINKD_DEVTOOLS_MAX_LIMIT", 200, 1, 200),
    timeoutMilliseconds: envInteger(
      "LINKD_DEVTOOLS_TIMEOUT_MILLISECONDS",
      5000,
      100,
      30000,
    ),
  };
  if (
    query.defaultRangeSeconds > query.maxRangeSeconds ||
    query.defaultLimit > query.maxLimit
  ) {
    throw new Error(
      "DevTools default query limits must not exceed maximum limits",
    );
  }

  const prometheusUrl = process.env.LINKD_DEVTOOLS_PROMETHEUS_URL;
  const config: DevtoolsConfig = {
    configPath,
    server: {
      host: process.env.LINKD_DEVTOOLS_HOST ?? "127.0.0.1",
      port: envInteger("LINKD_DEVTOOLS_PORT", 4399, 1, 65535),
    },
    query,
    prometheus: prometheusUrl
      ? {
          baseUrl: new URL(prometheusUrl).toString().replace(/\/$/, ""),
          auth: {
            apiKey: process.env.LINKD_DEVTOOLS_PROMETHEUS_API_KEY,
            username: process.env.LINKD_DEVTOOLS_PROMETHEUS_USERNAME,
            password: process.env.LINKD_DEVTOOLS_PROMETHEUS_PASSWORD,
          },
        }
      : undefined,
    entities: { alerts: repository, events: repository, alertLogs: repository },
    telemetry: {
      listenAddress: decoded.telemetry?.metrics.prometheus.listen_address,
    },
    eventSources: decoded.event_sources.map((source) => ({
      eventSourceId: source.event_source_id,
      enabled: source.enabled,
      cleanerType: source.cleaner.type,
      runtime: withCleanerDefaults({
        ...cleanerDefaults,
        ...(source.cleaner.runtime ?? {}),
      }),
      kafka: normalizeKafka(
        source.storage.kafka,
        configDir,
      ) as KafkaConnection & { consumerGroup: string },
    })),
  };
  validateLoopback(config.server.host);

  if (decoded.storage.mysql) {
    const address = splitAddress(decoded.storage.mysql.address);
    config.mysql = {
      ...address,
      database: decoded.storage.mysql.database,
      username: decoded.storage.mysql.username,
      password:
        process.env.LINKD_DEVTOOLS_MYSQL_PASSWORD ??
        decoded.storage.mysql.password,
      connectionLimit: 5,
    };
  }
  if (decoded.storage.elasticsearch) {
    const storage = decoded.storage.elasticsearch;
    config.elasticsearch = {
      baseUrl: storage.addresses[0].replace(/\/$/, ""),
      baseUrls: storage.addresses.map((value) => value.replace(/\/$/, "")),
      auth: {
        apiKey:
          process.env.LINKD_DEVTOOLS_ELASTICSEARCH_API_KEY ?? storage.api_key,
        username: storage.basic_auth?.username,
        password:
          process.env.LINKD_DEVTOOLS_ELASTICSEARCH_PASSWORD ??
          storage.basic_auth?.password,
      },
      eventTargets: [`${storage.index_prefix}-events`],
      alertTargets: [`${storage.index_prefix}-alerts`],
      alertLogTargets: [`${storage.index_prefix}-alert-logs`],
      indexPrefix: storage.index_prefix,
      timePartition: {
        eventBucketDays: storage.time_partition.event_bucket_days,
        alertHistoryBucketDays:
          storage.time_partition.alert_history_bucket_days,
        alertLogBucketDays: storage.time_partition.alert_log_bucket_days,
        precreatePastBuckets: storage.time_partition.precreate_past_buckets,
        precreateFutureBuckets: storage.time_partition.precreate_future_buckets,
        maxBucketsPerEntity: storage.time_partition.max_buckets_per_entity,
        maxFutureSkewSeconds: storage.time_partition.max_future_skew_seconds,
      },
    };
    if (repository === "elasticsearch") {
      const manager = decoded.control_plane?.elasticsearch;
      config.elasticsearchControlPlane = {
        explicit: Boolean(manager),
        schemaAndActiveReconcileIntervalSeconds:
          manager?.schema_and_active_reconcile_interval_seconds ?? 3600,
        bucketReconcileIntervalSeconds:
          manager?.bucket_reconcile_interval_seconds ?? 21600,
        archiveIntervalSeconds: manager?.archive_interval_seconds ?? 30,
        archiveBatchSize: manager?.archive_batch_size ?? 1000,
        archiveWorkerCount: manager?.archive_worker_count ?? 4,
      };
    }
  }
  if (decoded.storage.redis) {
    config.redis = {
      ...decoded.storage.redis,
      password:
        process.env.LINKD_DEVTOOLS_REDIS_PASSWORD ??
        decoded.storage.redis.password,
    };
  }
  if (decoded.lifecycle) {
    const lifecycle = decoded.lifecycle;
    config.lifecycle = {
      concurrency: lifecycle.concurrency,
      processTimeoutSeconds: lifecycle.process_timeout_seconds,
      retryMaxAttempts: lifecycle.retry_max_attempts,
      retryMaxElapsedSeconds: lifecycle.retry_max_elapsed_seconds,
      signal: {
        stream: lifecycle.signal.stream,
        group: lifecycle.signal.group,
        consumerPrefix: lifecycle.signal.consumer_prefix,
        claimMinIdleSeconds: lifecycle.signal.claim_min_idle_seconds,
      },
      mailbox: {
        keyPrefix: lifecycle.mailbox.key_prefix,
        maxPending: lifecycle.mailbox.max_pending,
        maxDrainEvents: lifecycle.mailbox.max_drain_events,
        backpressure: {
          cacheTTLSeconds: lifecycle.mailbox.backpressure.cache_ttl_seconds,
          queryTimeoutSeconds:
            lifecycle.mailbox.backpressure.query_timeout_seconds,
          highWatermark: lifecycle.mailbox.backpressure.high_watermark,
          lowWatermark: lifecycle.mailbox.backpressure.low_watermark,
        },
      },
      lock: {
        keyPrefix: lifecycle.lock.key_prefix,
        ttlSeconds: lifecycle.lock.ttl_seconds,
        renewIntervalSeconds: lifecycle.lock.renew_interval_seconds,
      },
      outputKafka: normalizeKafka(lifecycle.output.kafka, configDir),
    };
  }
  if (decoded.control_plane?.redis_stream) {
    const manager = decoded.control_plane.redis_stream;
    config.redisStreamManager = {
      reconcileIntervalSeconds: manager.reconcile_interval_seconds,
      operationTimeoutSeconds: manager.operation_timeout_seconds,
      maxEntries: manager.max_entries,
      trimBatchSize: manager.trim_batch_size,
    };
  }
  validateSources(config);
  return config;
}

function cliConfigPath(): string | undefined {
  const parsed = parseArgs({
    options: { config: { type: "string" } },
    allowPositionals: true,
    strict: false,
  });
  return typeof parsed.values.config === "string"
    ? parsed.values.config
    : undefined;
}

function normalizeKafka(
  value: z.infer<typeof kafkaConfigSchema>,
  configDir: string,
): KafkaConnection {
  const security = structuredClone(value.security);
  if (security.tls) {
    for (const key of [
      "ca_file",
      "client_cert_file",
      "client_key_file",
    ] as const) {
      const candidate = security.tls[key];
      if (candidate && !path.isAbsolute(candidate))
        security.tls[key] = path.resolve(configDir, candidate);
    }
  }
  return {
    brokers: [...value.brokers],
    topic: value.topic,
    consumerGroup: value.consumer_group,
    clientId: value.client_id,
    security,
  };
}

function withCleanerDefaults(
  value: z.infer<typeof cleanerRuntimeSchema>,
): CleanerRuntime {
  const defaults: CleanerRuntime = {
    worker_count: 8,
    max_batch_messages: 128,
    max_batch_bytes: 4 << 20,
    batch_wait_milliseconds: 20,
    max_concurrent_batches: 2,
    max_inflight_messages: 512,
    max_inflight_bytes: 16 << 20,
    max_inflight_per_lane: 256,
    resume_inflight_per_lane: 128,
    process_timeout_seconds: 30,
    retry_max_attempts: 3,
    retry_max_elapsed_seconds: 120,
    shutdown_drain_timeout_seconds: 30,
  };
  return { ...defaults, ...value };
}

function splitAddress(address: string): { host: string; port: number } {
  const separator = address.lastIndexOf(":");
  if (separator < 1) throw new Error(`invalid host:port address: ${address}`);
  const host = address.slice(0, separator).replace(/^\[|\]$/g, "");
  const port = Number(address.slice(separator + 1));
  if (!host || !Number.isInteger(port) || port < 1 || port > 65535) {
    throw new Error(`invalid host:port address: ${address}`);
  }
  return { host, port };
}

function envInteger(
  name: string,
  fallback: number,
  minimum: number,
  maximum: number,
): number {
  const value = process.env[name];
  if (value === undefined) return fallback;
  const parsed = Number(value);
  if (!Number.isInteger(parsed) || parsed < minimum || parsed > maximum) {
    throw new Error(
      `${name} must be an integer between ${minimum} and ${maximum}`,
    );
  }
  return parsed;
}

function validateLoopback(host: string): void {
  if (host === "localhost") return;
  const ip = isIP(host);
  if (
    (ip === 4 && host === "127.0.0.1") ||
    (ip === 6 && (host === "::1" || host === "0:0:0:0:0:0:0:1"))
  )
    return;
  throw new Error("server host must be a loopback address");
}

function validateSources(config: DevtoolsConfig): void {
  const repository = config.entities.events;
  if (repository === "mysql" && !config.mysql)
    throw new Error("storage.mysql is required by storage.repository");
  if (repository === "elasticsearch" && !config.elasticsearch) {
    throw new Error("storage.elasticsearch is required by storage.repository");
  }
}

export function publicConfig(config: DevtoolsConfig) {
  const source = config.entities.events;
  return {
    version: "0.2.0",
    metrics: {
      configured: Boolean(config.prometheus),
      source: "prometheus" as const,
    },
    entities: {
      events: {
        source,
        filters: [
          "tenantId",
          "id",
          "from",
          "to",
          "state",
          "eventSourceId",
          "relatedAlertId",
        ],
      },
      alerts: {
        source,
        filters: [
          "tenantId",
          "id",
          "from",
          "to",
          "status",
          "eventSourceId",
          "fingerprint",
          "severity",
        ],
      },
      "alert-logs": {
        source,
        filters: [
          "tenantId",
          "id",
          "from",
          "to",
          "alertId",
          "operationKind",
          "operatorKind",
        ],
      },
    },
    storage: { elasticsearch: { configured: Boolean(config.elasticsearch) } },
    infrastructure: {
      kafka: {
        configured: Boolean(config.eventSources?.length || config.lifecycle),
      },
      redis: { configured: Boolean(config.redis) },
    },
    limits: config.query,
  };
}

export function redactedConfig(config: DevtoolsConfig) {
  return {
    configPath: config.configPath,
    repository: config.entities.events,
    telemetry: config.telemetry,
    storage: {
      mysql: config.mysql
        ? {
            host: config.mysql.host,
            port: config.mysql.port,
            database: config.mysql.database,
            username: config.mysql.username,
            password: config.mysql.password ? "******" : "",
          }
        : undefined,
      elasticsearch: config.elasticsearch
        ? {
            addresses: config.elasticsearch.baseUrls ?? [
              config.elasticsearch.baseUrl,
            ],
            indexPrefix: config.elasticsearch.indexPrefix,
            auth:
              config.elasticsearch.auth.apiKey ||
              config.elasticsearch.auth.password
                ? "configured"
                : "none",
            timePartition: config.elasticsearch.timePartition,
          }
        : undefined,
      redis: config.redis
        ? {
            address: config.redis.address,
            database: config.redis.database,
            username: config.redis.username,
            password: config.redis.password ? "******" : "",
          }
        : undefined,
    },
    eventSources:
      config.eventSources?.map((source) => ({
        eventSourceId: source.eventSourceId,
        enabled: source.enabled,
        cleanerType: source.cleanerType,
        runtime: source.runtime,
        kafka: {
          brokers: source.kafka.brokers,
          topic: source.kafka.topic,
          consumerGroup: source.kafka.consumerGroup,
          security: source.kafka.security.protocol,
        },
      })) ?? [],
    lifecycle: config.lifecycle
      ? {
          concurrency: config.lifecycle.concurrency,
          signal: config.lifecycle.signal,
          mailbox: config.lifecycle.mailbox,
          lock: config.lifecycle.lock,
          outputKafka: {
            brokers: config.lifecycle.outputKafka.brokers,
            topic: config.lifecycle.outputKafka.topic,
            clientId: config.lifecycle.outputKafka.clientId,
            security: config.lifecycle.outputKafka.security.protocol,
          },
        }
      : undefined,
    controlPlane: {
      elasticsearch: config.elasticsearchControlPlane,
      redisStream: config.redisStreamManager,
    },
  };
}
