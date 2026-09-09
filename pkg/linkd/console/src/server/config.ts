import { readFile } from "node:fs/promises";
import path from "node:path";
import { parseArgs } from "node:util";

import { parse } from "yaml";
import { z } from "zod";
import { normalizeBasePath } from "../shared/base-path.js";
import {
  loadServerAccess,
  validateServerAccess,
  type ServerAccess,
} from "./auth.js";

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
    cache_ttl_seconds: z.number().int().min(1).max(60).default(1),
    query_timeout_seconds: z.number().int().positive().default(1),
    high_watermark: z.number().int().positive().optional(),
    low_watermark: z.number().int().positive().optional(),
  })
  .superRefine((value, context) => {
    if (value.query_timeout_seconds > value.cache_ttl_seconds) {
      context.addIssue({
        code: "custom",
        path: ["query_timeout_seconds"],
        message: "must not exceed cache_ttl_seconds",
      });
    }
    if (
      value.low_watermark !== undefined &&
      value.high_watermark !== undefined &&
      value.low_watermark >= value.high_watermark
    ) {
      context.addIssue({
        code: "custom",
        path: ["low_watermark"],
        message: "must be less than high_watermark",
      });
    }
  });

const redisConfigSchema = z
  .object({
    mode: z.enum(["standalone", "sentinel"]).default("standalone"),
    address: z.string().min(1).optional(),
    username: z.string().optional(),
    password: z.string().optional(),
    database: z.number().int().nonnegative().default(0),
    sentinel: z
      .object({
        master_name: z.string().min(1),
        addresses: z.array(z.string().min(1)).min(1),
        username: z.string().optional(),
        password: z.string().optional(),
      })
      .optional(),
  })
  .superRefine((value, context) => {
    if (value.mode === "standalone") {
      if (!value.address) {
        context.addIssue({
          code: "custom",
          path: ["address"],
          message: "is required in standalone mode",
        });
      }
      if (value.sentinel) {
        context.addIssue({
          code: "custom",
          path: ["sentinel"],
          message: "must be omitted in standalone mode",
        });
      }
      return;
    }
    if (value.address) {
      context.addIssue({
        code: "custom",
        path: ["address"],
        message: "must be omitted in sentinel mode",
      });
    }
    if (!value.sentinel) {
      context.addIssue({
        code: "custom",
        path: ["sentinel"],
        message: "is required in sentinel mode",
      });
    }
  });

const hookSchema = z.discriminatedUnion("type", [
  z
    .object({
      name: z.string().regex(/^[a-zA-Z0-9][a-zA-Z0-9_-]{0,63}$/),
      type: z.literal("kafka"),
      config: kafkaConfigSchema
        .omit({ consumer_group: true })
        .extend({
          max_message_bytes: z
            .number()
            .int()
            .positive()
            .max(2147483647)
            .optional(),
        })
        .strict(),
    })
    .strict(),
  z
    .object({
      name: z.string().regex(/^[a-zA-Z0-9][a-zA-Z0-9_-]{0,63}$/),
      type: z.literal("active-alert-by-strategy"),
      config: z
        .object({
          redis: redisConfigSchema,
          key_prefix: z
            .string()
            .min(1)
            .max(256)
            .refine((v) => v.trim() === v),
          timeout_milliseconds: z
            .number()
            .int()
            .positive()
            .max(9223372036854)
            .default(1000),
        })
        .strict(),
    })
    .strict(),
]);
const hooksSchema = z
  .array(hookSchema)
  .max(16)
  .default([])
  .refine(
    (hooks) => new Set(hooks.map((h) => h.name)).size === hooks.length,
    "hook names must be unique",
  );

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
            number_of_shards: z.number().int().min(1).max(1024).optional(),
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
        redis: redisConfigSchema.optional(),
      })
      .passthrough(),
    cleaner: cleanerRuntimeSchema,
    lifecycle: z
      .object({
        concurrency: z.number().int().min(1).max(1024).default(32),
        elasticsearch_write_batch: z
          .object({
            enabled: z.boolean().default(true),
            max_bytes: z
              .number()
              .int()
              .min(1048576)
              .max(16777216)
              .default(4194304),
          })
          .strict()
          .prefault({}),
        process_timeout_seconds: z.number().int().positive().default(30),
        retry_max_attempts: z.number().int().positive().default(3),
        retry_max_elapsed_seconds: z.number().int().positive().default(120),
        signal: z
          .object({
            stream: z.string().default("linkd:lifecycle:signals"),
            group: z.string().default("linkd-lifecycle"),
            consumer_prefix: z.string().default("linkd-lifecycle"),
            claim_min_idle_seconds: z.number().int().positive().default(300),
            max_batch_messages: z.number().int().min(1).max(4096).default(64),
            max_inflight_messages: z.number().int().min(1).max(4096).optional(),
          })
          .superRefine((value, context) => {
            if (
              value.max_inflight_messages !== undefined &&
              value.max_inflight_messages < value.max_batch_messages
            ) {
              context.addIssue({
                code: "custom",
                path: ["max_inflight_messages"],
                message: "must not be less than max_batch_messages",
              });
            }
          })
          .default({
            stream: "linkd:lifecycle:signals",
            group: "linkd-lifecycle",
            consumer_prefix: "linkd-lifecycle",
            claim_min_idle_seconds: 300,
            max_batch_messages: 64,
          }),
        mailbox: z
          .object({
            key_prefix: z.string().default("linkd:lifecycle:mailbox"),
            max_pending: z.number().int().positive().default(128),
            max_drain_events: z.number().int().positive().default(128),
            backpressure: lifecycleMailboxBackpressureSchema.default({
              cache_ttl_seconds: 1,
              query_timeout_seconds: 1,
            }),
          })
          .default({
            key_prefix: "linkd:lifecycle:mailbox",
            max_pending: 128,
            max_drain_events: 128,
            backpressure: {
              cache_ttl_seconds: 1,
              query_timeout_seconds: 1,
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
        output: z.never().optional(),
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
            archive_interval_seconds: z.number().int().positive().default(5),
            archive_batch_size: z.number().int().positive().default(1000),
            archive_worker_count: z.number().int().positive().default(1),
          })
          .optional(),
        redis_stream: z
          .object({
            reconcile_interval_seconds: z.number().int().positive().default(10),
            operation_timeout_seconds: z.number().int().positive().default(3),
            max_entries: z.number().int().positive().default(100_000),
            trim_batch_size: z.number().int().positive().default(10_000),
            max_trim_entries_per_cycle: z.number().int().positive().optional(),
          })
          .superRefine((value, context) => {
            const maxTrimEntriesPerCycle =
              value.max_trim_entries_per_cycle ?? value.trim_batch_size * 10;
            if (maxTrimEntriesPerCycle < value.trim_batch_size) {
              context.addIssue({
                code: "custom",
                path: ["max_trim_entries_per_cycle"],
                message: "must not be less than trim_batch_size",
              });
            }
            if (maxTrimEntriesPerCycle > value.trim_batch_size * 100) {
              context.addIssue({
                code: "custom",
                path: ["max_trim_entries_per_cycle"],
                message: "must not exceed 100 times trim_batch_size",
              });
            }
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
            hooks: hooksSchema,
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
                fetch_max_wait_milliseconds: z
                  .number()
                  .int()
                  .min(10)
                  .max(5000)
                  .default(100),
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
  kafkaHooks?: Array<{ name: string; connection: KafkaConnection }>;
  runtime: CleanerRuntime;
  kafka: KafkaConnection & {
    consumerGroup: string;
    fetchMaxWaitMilliseconds?: number;
  };
}

export interface ConsoleConfig {
  dispatch?: { url: string; apiToken: string; deployment: string };
  configPath?: string;
  server: {
    host: string;
    port: number;
    basePath?: string;
    access?: ServerAccess;
  };
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
    mode: "standalone" | "sentinel";
    address?: string;
    username?: string;
    password?: string;
    database: number;
    sentinel?: {
      masterName: string;
      addresses: string[];
      username?: string;
      password?: string;
    };
  };
  lifecycle?: {
    elasticsearchWriteBatch?: {
      enabled: boolean;
      max_operations: number;
      max_bytes: number;
      wait_milliseconds: number;
      read_wait_milliseconds: number;
      max_concurrent_batches: number;
    };
    concurrency: number;
    processTimeoutSeconds: number;
    retryMaxAttempts: number;
    retryMaxElapsedSeconds: number;
    signal: {
      stream: string;
      group: string;
      consumerPrefix: string;
      claimMinIdleSeconds: number;
      maxBatchMessages: number;
      maxInflightMessages: number;
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
  };
  redisStreamManager?: {
    reconcileIntervalSeconds: number;
    operationTimeoutSeconds: number;
    maxEntries: number;
    trimBatchSize: number;
    maxTrimEntriesPerCycle: number;
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
): Promise<ConsoleConfig> {
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
      "LINKD_CONSOLE_DEFAULT_RANGE_SECONDS",
      3600,
      60,
      604800,
    ),
    maxRangeSeconds: envInteger(
      "LINKD_CONSOLE_MAX_RANGE_SECONDS",
      604800,
      3600,
      604800,
    ),
    defaultLimit: envInteger("LINKD_CONSOLE_DEFAULT_LIMIT", 50, 1, 200),
    maxLimit: envInteger("LINKD_CONSOLE_MAX_LIMIT", 200, 1, 200),
    timeoutMilliseconds: envInteger(
      "LINKD_CONSOLE_TIMEOUT_MILLISECONDS",
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
      "Console default query limits must not exceed maximum limits",
    );
  }

  const prometheusUrl = process.env.LINKD_CONSOLE_PROMETHEUS_URL;
  const dispatch = z
    .object({
      url: z.string().url().default("http://127.0.0.1:8090"),
      api_token: z.string().default(""),
      deployment: z.string().default("default"),
    })
    .parse(decoded.dispatch ?? {});
  const config: ConsoleConfig = {
    dispatch: {
      url: process.env.LINKD_CONTROL_PLANE_URL ?? dispatch.url,
      apiToken: process.env.LINKD_API_TOKEN ?? dispatch.api_token,
      deployment: dispatch.deployment,
    },
    configPath,
    server: {
      host: process.env.LINKD_CONSOLE_HOST ?? "127.0.0.1",
      port: envInteger("LINKD_CONSOLE_PORT", 4399, 1, 65535),
      basePath: normalizeBasePath(process.env.LINKD_CONSOLE_BASE_PATH),
      access: loadServerAccess(),
    },
    query,
    prometheus: prometheusUrl
      ? {
          baseUrl: new URL(prometheusUrl).toString().replace(/\/$/, ""),
          auth: {
            apiKey: process.env.LINKD_CONSOLE_PROMETHEUS_API_KEY,
            username: process.env.LINKD_CONSOLE_PROMETHEUS_USERNAME,
            password: process.env.LINKD_CONSOLE_PROMETHEUS_PASSWORD,
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
      kafkaHooks: source.hooks
        .filter((h) => h.type === "kafka")
        .map((h) => ({
          name: h.name,
          connection: normalizeKafka(h.config, configDir),
        })),
      runtime: withCleanerDefaults({
        ...cleanerDefaults,
        ...(source.cleaner.runtime ?? {}),
      }),
      kafka: {
        ...normalizeKafka(source.storage.kafka, configDir),
        consumerGroup: source.storage.kafka.consumer_group,
        fetchMaxWaitMilliseconds:
          source.storage.kafka.fetch_max_wait_milliseconds,
      },
    })),
  };
  validateServerAccess(
    config.server.host,
    config.server.access ?? { mode: "local" },
  );

  if (decoded.storage.mysql) {
    const address = splitAddress(decoded.storage.mysql.address);
    config.mysql = {
      ...address,
      database: decoded.storage.mysql.database,
      username: decoded.storage.mysql.username,
      password:
        process.env.LINKD_CONSOLE_MYSQL_PASSWORD ??
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
          process.env.LINKD_CONSOLE_ELASTICSEARCH_API_KEY ?? storage.api_key,
        username: storage.basic_auth?.username,
        password:
          process.env.LINKD_CONSOLE_ELASTICSEARCH_PASSWORD ??
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
        archiveIntervalSeconds: manager?.archive_interval_seconds ?? 5,
        archiveBatchSize: manager?.archive_batch_size ?? 1000,
        archiveWorkerCount: manager?.archive_worker_count ?? 1,
      };
    }
  }
  if (decoded.storage.redis) {
    const storage = decoded.storage.redis;
    config.redis = {
      mode: storage.mode,
      address: storage.address,
      username: storage.username,
      password: process.env.LINKD_CONSOLE_REDIS_PASSWORD ?? storage.password,
      database: storage.database,
      sentinel: storage.sentinel
        ? {
            masterName: storage.sentinel.master_name,
            addresses: [...storage.sentinel.addresses],
            username: storage.sentinel.username,
            password:
              process.env.LINKD_CONSOLE_REDIS_SENTINEL_PASSWORD ??
              storage.sentinel.password,
          }
        : undefined,
    };
  }
  if (decoded.lifecycle) {
    const lifecycle = decoded.lifecycle;
    const signalInflightMessages =
      lifecycle.signal.max_inflight_messages ??
      Math.max(lifecycle.concurrency * 2, lifecycle.signal.max_batch_messages);
    const backpressureHighWatermark =
      lifecycle.mailbox.backpressure.high_watermark ??
      signalInflightMessages * 4;
    const backpressureLowWatermark =
      lifecycle.mailbox.backpressure.low_watermark ??
      signalInflightMessages * 2;
    if (backpressureLowWatermark >= backpressureHighWatermark) {
      throw new Error(
        "lifecycle.mailbox.backpressure.low_watermark must be less than high_watermark",
      );
    }
    // 与 Go WithDefaults 保持同一推导规则；只读展示，不接受独立调度参数。
    const batchOperations = Math.min(
      128,
      Math.max(1, Math.floor(lifecycle.concurrency / 2)),
    );
    config.lifecycle = {
      elasticsearchWriteBatch: {
        ...lifecycle.elasticsearch_write_batch,
        max_operations: batchOperations,
        wait_milliseconds: batchOperations === 1 ? 0 : batchOperations,
        read_wait_milliseconds:
          batchOperations === 1 ? 0 : Math.min(10, batchOperations),
        max_concurrent_batches: Math.min(32, lifecycle.concurrency),
      },
      concurrency: lifecycle.concurrency,
      processTimeoutSeconds: lifecycle.process_timeout_seconds,
      retryMaxAttempts: lifecycle.retry_max_attempts,
      retryMaxElapsedSeconds: lifecycle.retry_max_elapsed_seconds,
      signal: {
        stream: lifecycle.signal.stream,
        group: lifecycle.signal.group,
        consumerPrefix: lifecycle.signal.consumer_prefix,
        claimMinIdleSeconds: lifecycle.signal.claim_min_idle_seconds,
        maxBatchMessages: lifecycle.signal.max_batch_messages,
        maxInflightMessages: signalInflightMessages,
      },
      mailbox: {
        keyPrefix: lifecycle.mailbox.key_prefix,
        maxPending: lifecycle.mailbox.max_pending,
        maxDrainEvents: lifecycle.mailbox.max_drain_events,
        backpressure: {
          cacheTTLSeconds: lifecycle.mailbox.backpressure.cache_ttl_seconds,
          queryTimeoutSeconds:
            lifecycle.mailbox.backpressure.query_timeout_seconds,
          highWatermark: backpressureHighWatermark,
          lowWatermark: backpressureLowWatermark,
        },
      },
      lock: {
        keyPrefix: lifecycle.lock.key_prefix,
        ttlSeconds: lifecycle.lock.ttl_seconds,
        renewIntervalSeconds: lifecycle.lock.renew_interval_seconds,
      },
    };
  }
  if (decoded.control_plane?.redis_stream) {
    const manager = decoded.control_plane.redis_stream;
    config.redisStreamManager = {
      reconcileIntervalSeconds: manager.reconcile_interval_seconds,
      operationTimeoutSeconds: manager.operation_timeout_seconds,
      maxEntries: manager.max_entries,
      trimBatchSize: manager.trim_batch_size,
      maxTrimEntriesPerCycle:
        manager.max_trim_entries_per_cycle ?? manager.trim_batch_size * 10,
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

function validateSources(config: ConsoleConfig): void {
  const repository = config.entities.events;
  if (repository === "mysql" && !config.mysql)
    throw new Error("storage.mysql is required by storage.repository");
  if (repository === "elasticsearch" && !config.elasticsearch) {
    throw new Error("storage.elasticsearch is required by storage.repository");
  }
}

export function publicConfig(config: ConsoleConfig) {
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

export function redactedConfig(config: ConsoleConfig) {
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
            mode: config.redis.mode,
            address: config.redis.address,
            database: config.redis.database,
            username: config.redis.username,
            password: config.redis.password ? "******" : "",
            sentinel: config.redis.sentinel
              ? {
                  masterName: config.redis.sentinel.masterName,
                  addresses: config.redis.sentinel.addresses,
                  username: config.redis.sentinel.username,
                  password: config.redis.sentinel.password ? "******" : "",
                }
              : undefined,
          }
        : undefined,
    },
    eventSources:
      config.eventSources?.map((source) => ({
        eventSourceId: source.eventSourceId,
        enabled: source.enabled,
        cleanerType: source.cleanerType,
        kafkaHooks: source.kafkaHooks?.map((h) => ({
          name: h.name,
          brokers: h.connection.brokers,
          topic: h.connection.topic,
          clientId: h.connection.clientId,
          security: h.connection.security.protocol,
        })),
        runtime: source.runtime,
        kafka: {
          brokers: source.kafka.brokers,
          topic: source.kafka.topic,
          consumerGroup: source.kafka.consumerGroup,
          fetchMaxWaitMilliseconds: source.kafka.fetchMaxWaitMilliseconds,
          security: source.kafka.security.protocol,
        },
      })) ?? [],
    lifecycle: config.lifecycle
      ? {
          concurrency: config.lifecycle.concurrency,
          elasticsearchWriteBatch: config.lifecycle.elasticsearchWriteBatch,
          signal: config.lifecycle.signal,
          mailbox: config.lifecycle.mailbox,
          lock: config.lifecycle.lock,
        }
      : undefined,
    controlPlane: {
      elasticsearch: config.elasticsearchControlPlane,
      redisStream: config.redisStreamManager,
    },
  };
}
