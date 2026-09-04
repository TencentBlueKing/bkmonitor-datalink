import { fileURLToPath } from "node:url";

import fastifyStatic from "@fastify/static";
import Fastify, { type FastifyInstance } from "fastify";
import { z } from "zod";

import {
  type ControlPlaneTaskDefinition,
  entityKindSchema,
  type EntityKind,
  type EntityPage,
} from "../shared/contracts.js";
import {
  loadConfig,
  publicConfig,
  redactedConfig,
  type DevtoolsConfig,
} from "./config.js";
import { ElasticsearchConnector } from "./elasticsearch.js";
import { KafkaConnector } from "./kafka.js";
import { MysqlConnector } from "./mysql.js";
import { PrometheusConnector } from "./prometheus.js";
import { parseSearchQuery } from "./query.js";
import { RedisConnector } from "./redis.js";

const detailQuerySchema = z.object({
  bk_tenant_id: z.string().min(1).max(1024),
});
const metricQuerySchema = z.object({
  from: z.string().datetime(),
  to: z.string().datetime(),
  step: z.coerce.number().int().min(1).max(3600),
  calculation_window_seconds: z.coerce
    .number()
    .int()
    .min(15)
    .max(3600)
    .default(60),
  instance: z.string().max(512).optional(),
  event_source_id: z.string().max(128).optional(),
  partition: z.coerce.number().int().nonnegative().optional(),
});
const redisDetailQuerySchema = z.object({
  query: z.string().trim().max(128).optional(),
  limit: z.coerce.number().int().min(1).max(100).default(50),
});
const redisPendingQuerySchema = z.object({
  group: z.string().trim().min(1).max(256).optional(),
  limit: z.coerce.number().int().min(1).max(100).default(50),
});
const controlPlaneQuerySchema = z.object({
  range_seconds: z.coerce.number().int().min(60).max(604800).default(3600),
  instance: z.string().trim().max(512).optional(),
});

export async function createApp(
  configOverride?: DevtoolsConfig,
): Promise<FastifyInstance> {
  const config = configOverride ?? (await loadConfig());
  const app = Fastify({
    logger: true,
    bodyLimit: 64 * 1024,
  });
  const mysqlConnector = config.mysql ? new MysqlConnector(config) : undefined;
  const elasticsearchConnector = config.elasticsearch
    ? new ElasticsearchConnector(config)
    : undefined;
  const prometheusConnector = new PrometheusConnector(config);
  const kafkaConnector = new KafkaConnector(config);
  const redisConnector = new RedisConnector(config);

  app.setErrorHandler((error, request, reply) => {
    const cause =
      error instanceof Error ? error : new Error("unknown local API error");
    request.log.warn({ err: { name: cause.name } }, "local API request failed");
    const invalid =
      cause instanceof z.ZodError ||
      cause.message.includes("cursor") ||
      cause.message.includes("must");
    void reply.status(invalid ? 400 : 502).send({
      error: {
        code: invalid ? "invalid_argument" : "data_source_error",
        message: invalid
          ? cause.message
          : "数据源查询失败，请检查本机连接配置和服务状态。",
        requestId: request.id,
      },
    });
  });

  app.get("/local-api/capabilities", async () => publicConfig(config));
  app.get("/local-api/config", async () => redactedConfig(config));
  app.get("/local-api/runtime/processes", async () =>
    prometheusConnector.processes(),
  );
  app.get("/local-api/runtime/cleaner", async () => {
    const [processes, metrics, kafka] = await Promise.all([
      prometheusConnector.processes(),
      prometheusConnector.cleanerSnapshot(),
      kafkaConnector.inspect(),
    ]);
    return {
      status: combinedStatus(processes, metrics, kafka),
      eventSources: redactedConfig(config).eventSources,
      processes,
      metrics,
      kafka,
    };
  });
  app.get("/local-api/runtime/lifecycle", async () => {
    const [processes, metrics, redis] = await Promise.all([
      prometheusConnector.processes(),
      prometheusConnector.lifecycleSnapshot(),
      redisConnector.inspect(),
    ]);
    return {
      status: combinedStatus(processes, metrics, redis),
      config: redactedConfig(config).lifecycle,
      processes,
      metrics,
      redis,
    };
  });
  app.get("/local-api/runtime/control-plane", async (request) => {
    const query = controlPlaneQuerySchema.parse(request.query);
    if (query.range_seconds > config.query.maxRangeSeconds) {
      throw new Error(
        `range_seconds must not exceed ${config.query.maxRangeSeconds}`,
      );
    }
    const tasks = controlPlaneTasks(config);
    const elasticsearchEnabled = tasks
      .filter((task) => task.id.startsWith("elasticsearch-"))
      .some((task) => task.enabled);
    const redisEnabled = tasks.some(
      (task) => task.id === "redis-stream-manager" && task.enabled,
    );
    const [allProcesses, metrics, archive, redis] = await Promise.all([
      prometheusConnector.processes(),
      prometheusConnector.controlPlaneSnapshot(
        query.range_seconds,
        query.instance,
      ),
      elasticsearchEnabled && elasticsearchConnector
        ? elasticsearchConnector.archiveBacklog()
        : Promise.resolve({
            status: "unavailable" as const,
            message: "Elasticsearch 控制面任务未启用",
            backlog: null,
          }),
      redisEnabled ? redisConnector.inspect() : Promise.resolve(undefined),
    ]);
    const processes = controlPlaneProcesses(allProcesses);
    const redisSummary = redisTaskSummary(redis, redisEnabled);
    const enabledSources: Array<Record<string, unknown>> = [processes, metrics];
    if (elasticsearchEnabled) enabledSources.push(archive);
    if (redisEnabled) enabledSources.push(redisSummary);
    return {
      status:
        tasks.some((task) => task.enabled) && enabledSources.length > 0
          ? combinedStatus(...enabledSources)
          : "unavailable",
      snapshotAt: new Date().toISOString(),
      tasks,
      processes,
      metrics,
      archive,
      redis: redisSummary,
    };
  });
  app.get("/local-api/infrastructure/kafka", async () =>
    kafkaConnector.inspect(),
  );
  app.get("/local-api/infrastructure/redis", async () =>
    redisConnector.inspect(),
  );
  app.get("/local-api/infrastructure/redis/pending", async (request) => {
    const query = redisPendingQuerySchema.parse(request.query);
    return redisConnector.inspectPending(query.group, query.limit);
  });
  app.get("/local-api/infrastructure/redis/mailboxes", async (request) => {
    const query = redisDetailQuerySchema.parse(request.query);
    return redisConnector.inspectMailboxes(query.query, query.limit);
  });
  app.get("/local-api/infrastructure/redis/leases", async (request) => {
    const query = redisDetailQuerySchema.parse(request.query);
    return redisConnector.inspectLeases(query.query, query.limit);
  });
  app.get("/local-api/metrics", async (request) => {
    const query = metricQuerySchema.parse(request.query);
    const from = new Date(query.from);
    const to = new Date(query.to);
    if (to.getTime() - from.getTime() > config.query.maxRangeSeconds * 1000) {
      throw new Error(
        `metrics range must not exceed ${config.query.maxRangeSeconds} seconds`,
      );
    }
    return prometheusConnector.panels(from, to, query.step, {
      instance: query.instance,
      eventSourceId: query.event_source_id,
      partition: query.partition,
      calculationWindowSeconds: query.calculation_window_seconds,
    });
  });
  app.get("/local-api/elasticsearch/topology", async () => {
    if (!elasticsearchConnector)
      throw new Error("elasticsearch source is unavailable");
    return elasticsearchConnector.topology();
  });

  for (const entity of entityKindSchema.options) {
    app.get(`/local-api/${entity}`, async (request): Promise<EntityPage> => {
      const params = parseSearchQuery(request.query, entity, config);
      return searchEntity(
        entity,
        params,
        config,
        mysqlConnector,
        elasticsearchConnector,
      );
    });
    app.get(`/local-api/${entity}/stats`, async (request) => {
      const params = parseSearchQuery(request.query, entity, config);
      const source = entitySource(config, entity);
      if (source === "mysql") {
        if (!mysqlConnector) throw new Error("mysql source is unavailable");
        return mysqlConnector.stats(entity, params);
      }
      if (!elasticsearchConnector)
        throw new Error("elasticsearch source is unavailable");
      return elasticsearchConnector.stats(entity, params);
    });
    app.get(`/local-api/${entity}/:id`, async (request, reply) => {
      const { id } = z
        .object({ id: z.string().min(1).max(1024) })
        .parse(request.params);
      const { bk_tenant_id: tenantId } = detailQuerySchema.parse(request.query);
      const item = await detailEntity(
        entity,
        tenantId,
        id,
        config,
        mysqlConnector,
        elasticsearchConnector,
      );
      if (!item)
        return reply.status(404).send({
          error: {
            code: "not_found",
            message: "对象不存在",
            requestId: request.id,
          },
        });
      return item;
    });
  }

  app.addHook("onClose", async () => {
    await mysqlConnector?.close();
    await redisConnector.close();
  });

  if (process.env.NODE_ENV === "production") {
    await app.register(fastifyStatic, {
      root: fileURLToPath(new URL("../../dist", import.meta.url)),
      wildcard: false,
    });
    app.setNotFoundHandler((request, reply) => {
      if (request.raw.url?.startsWith("/local-api/")) {
        return reply.status(404).send({
          error: {
            code: "not_found",
            message: "接口不存在",
            requestId: request.id,
          },
        });
      }
      return reply.sendFile("index.html");
    });
  }
  return app;
}

function combinedStatus(
  ...values: Array<Record<string, unknown>>
): "available" | "partial" | "unavailable" {
  const statuses = values.map((value) => value.status);
  if (statuses.every((status) => status === "available")) return "available";
  if (statuses.every((status) => status === "unavailable"))
    return "unavailable";
  return "partial";
}

function controlPlaneTasks(
  config: DevtoolsConfig,
): ControlPlaneTaskDefinition[] {
  const elasticsearchEnabled =
    config.entities.events === "elasticsearch" && Boolean(config.elasticsearch);
  const elasticsearch = config.elasticsearchControlPlane ?? {
    explicit: false,
    schemaAndActiveReconcileIntervalSeconds: 3600,
    bucketReconcileIntervalSeconds: 21600,
    archiveIntervalSeconds: 30,
    archiveBatchSize: 1000,
    archiveWorkerCount: 4,
  };
  const partition = config.elasticsearch?.timePartition ?? {
    eventBucketDays: 7,
    alertHistoryBucketDays: 7,
    alertLogBucketDays: 7,
    precreatePastBuckets: 1,
    precreateFutureBuckets: 1,
    maxBucketsPerEntity: 512,
    maxFutureSkewSeconds: 300,
  };
  const elasticsearchSource = elasticsearchEnabled
    ? elasticsearch.explicit
      ? ("explicit" as const)
      : ("default" as const)
    : ("disabled" as const);
  const redis = config.redisStreamManager ?? {
    reconcileIntervalSeconds: 60,
    operationTimeoutSeconds: 10,
    maxEntries: 100000,
    trimBatchSize: 10000,
  };
  return [
    {
      id: "elasticsearch-schema-and-active-reconciler",
      enabled: elasticsearchEnabled,
      dependsOn: [],
      intervalSeconds: elasticsearch.schemaAndActiveReconcileIntervalSeconds,
      configSource: elasticsearchSource,
      settings: {
        schemaAndActiveReconcileIntervalSeconds:
          elasticsearch.schemaAndActiveReconcileIntervalSeconds,
      },
    },
    {
      id: "elasticsearch-bucket-manager",
      enabled: elasticsearchEnabled,
      dependsOn: ["elasticsearch-schema-and-active-reconciler"],
      intervalSeconds: elasticsearch.bucketReconcileIntervalSeconds,
      configSource: elasticsearchSource,
      settings: {
        eventBucketDays: partition.eventBucketDays,
        alertHistoryBucketDays: partition.alertHistoryBucketDays,
        alertLogBucketDays: partition.alertLogBucketDays,
        precreatePastBuckets: partition.precreatePastBuckets,
        precreateFutureBuckets: partition.precreateFutureBuckets,
        maxBucketsPerEntity: partition.maxBucketsPerEntity,
      },
    },
    {
      id: "elasticsearch-alert-archiver",
      enabled: elasticsearchEnabled,
      dependsOn: ["elasticsearch-bucket-manager"],
      intervalSeconds: elasticsearch.archiveIntervalSeconds,
      configSource: elasticsearchSource,
      settings: {
        archiveIntervalSeconds: elasticsearch.archiveIntervalSeconds,
        archiveBatchSize: elasticsearch.archiveBatchSize,
        archiveWorkerCount: elasticsearch.archiveWorkerCount,
      },
    },
    {
      id: "redis-stream-manager",
      enabled: Boolean(config.redisStreamManager),
      dependsOn: [],
      intervalSeconds: redis.reconcileIntervalSeconds,
      configSource: config.redisStreamManager ? "explicit" : "disabled",
      settings: {
        reconcileIntervalSeconds: redis.reconcileIntervalSeconds,
        operationTimeoutSeconds: redis.operationTimeoutSeconds,
        maxEntries: redis.maxEntries,
        trimBatchSize: redis.trimBatchSize,
        stream: config.lifecycle?.signal.stream ?? "",
        group: config.lifecycle?.signal.group ?? "",
      },
    },
  ];
}

function controlPlaneProcesses(value: Record<string, unknown>) {
  const items = Array.isArray(value.items)
    ? value.items.filter((item) => {
        if (!item || typeof item !== "object") return false;
        const role = (item as { role?: unknown }).role;
        return role === "control-plane" || role === "all-in-one";
      })
    : [];
  return { ...value, items };
}

function redisTaskSummary(
  value: Awaited<ReturnType<RedisConnector["inspect"]>> | undefined,
  enabled: boolean,
) {
  if (!enabled || !value) {
    return {
      status: "unavailable" as const,
      message: "Redis Stream 管理任务未启用",
      streamExists: null,
      expectedGroupPresent: null,
      entries: null,
      maxEntries: null,
      entriesAboveMax: null,
      pending: null,
      maxLag: null,
    };
  }
  const groups = value.signalQueue.groups;
  const stream = value.signalQueue.stream;
  const knownLags = groups.map((group) => group.lag);
  const maxLag = knownLags.some((lag) => lag === null)
    ? null
    : Math.max(0, ...knownLags.map((lag) => lag ?? 0));
  return {
    status:
      value.signalQueue.status === "unavailable"
        ? ("unavailable" as const)
        : value.signalQueue.status === "partial"
          ? ("partial" as const)
          : ("available" as const),
    message: value.signalQueue.message,
    streamExists: stream?.exists ?? null,
    expectedGroupPresent: stream
      ? groups.some((group) => group.expected)
      : null,
    entries: stream?.length ?? null,
    maxEntries: stream?.maxEntries ?? null,
    entriesAboveMax: stream?.entriesAboveMax ?? null,
    pending: stream
      ? groups.reduce((total, group) => total + group.pending, 0)
      : null,
    maxLag,
  };
}

async function searchEntity(
  entity: EntityKind,
  params: ReturnType<typeof parseSearchQuery>,
  config: DevtoolsConfig,
  mysqlConnector?: MysqlConnector,
  elasticsearchConnector?: ElasticsearchConnector,
) {
  const source = entitySource(config, entity);
  if (source === "mysql") {
    if (!mysqlConnector) throw new Error("mysql source is unavailable");
    return mysqlConnector.search(entity, params);
  }
  if (!elasticsearchConnector)
    throw new Error("elasticsearch source is unavailable");
  return elasticsearchConnector.search(entity, params);
}

async function detailEntity(
  entity: EntityKind,
  tenantId: string,
  id: string,
  config: DevtoolsConfig,
  mysqlConnector?: MysqlConnector,
  elasticsearchConnector?: ElasticsearchConnector,
) {
  const source = entitySource(config, entity);
  if (source === "mysql") {
    if (!mysqlConnector) throw new Error("mysql source is unavailable");
    return mysqlConnector.detail(entity, tenantId, id);
  }
  if (!elasticsearchConnector)
    throw new Error("elasticsearch source is unavailable");
  return elasticsearchConnector.detail(entity, tenantId, id);
}

function entitySource(config: DevtoolsConfig, entity: EntityKind) {
  if (entity === "events") return config.entities.events;
  if (entity === "alerts") return config.entities.alerts;
  return config.entities.alertLogs;
}
