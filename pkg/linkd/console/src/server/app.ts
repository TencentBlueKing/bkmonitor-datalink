import { registerOneModelRoutes } from "./onemodel.js";
import { registerSourceRoutes } from "./sources.js";
import { registerCloseAlert } from "./close-alert.js";
import Fastify, { type FastifyInstance } from "fastify";
import { z } from "zod";

import {
  controlPlaneCatalogSchema,
  entityKindSchema,
  type EntityKind,
  type EntityPage,
} from "../shared/contracts.js";
import {
  loadConfig,
  publicConfig,
  redactedConfig,
  type ConsoleConfig,
} from "./config.js";
import { ElasticsearchConnector } from "./elasticsearch.js";
import { KafkaConnector } from "./kafka.js";
import { MysqlConnector } from "./mysql.js";
import { PrometheusConnector } from "./prometheus.js";
import { parseSearchQuery } from "./query.js";
import { RedisConnector } from "./redis.js";
import { registerBasicAuth, validateServerAccess } from "./auth.js";
import { readBuildInfo } from "./version.js";
import { normalizeBasePath } from "../shared/base-path.js";
import { registerWeb } from "./web.js";
import { StrategyIndexConnector } from "./strategy-index.js";
import { StrategyAuditJobs } from "./strategy-audit.js";
import {
  strategyQuerySchema,
  strategyBrowseQuerySchema,
  strategyAuditRequestSchema,
} from "../shared/strategy-index.js";

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
  event_source_id: z.string().optional(),
  query: z.string().trim().max(128).optional(),
  limit: z.coerce.number().int().min(1).max(100).default(50),
});
const redisPendingQuerySchema = z.object({
  event_source_id: z.string().optional(),
  group: z.string().trim().min(1).max(256).optional(),
  limit: z.coerce.number().int().min(1).max(100).default(50),
});
const controlPlaneQuerySchema = z.object({
  range_seconds: z.coerce.number().int().min(60).max(604800).default(3600),
  instance: z.string().trim().max(512).optional(),
});

export async function createApp(
  configOverride?: ConsoleConfig,
): Promise<FastifyInstance> {
  const config = configOverride ?? (await loadConfig());
  const access = config.server.access ?? { mode: "local" as const };
  validateServerAccess(config.server.host, access);
  const app = Fastify({
    logger: { redact: ["req.headers.authorization"] },
    bodyLimit: 1024 * 1024,
  });
  registerBasicAuth(app, access);
  const basePath = normalizeBasePath(config.server.basePath);
  await app.register(
    async (routes) => registerConsoleRoutes(routes, config, basePath),
    { prefix: basePath },
  );
  return app;
}

async function registerConsoleRoutes(
  app: FastifyInstance,
  config: ConsoleConfig,
  basePath: string,
): Promise<void> {
  app.get("/local-api/version", () => readBuildInfo());
  registerCloseAlert(app, config);
  const mysqlConnector = config.mysql ? new MysqlConnector(config) : undefined;
  const elasticsearchConnector = config.elasticsearch
    ? new ElasticsearchConnector(config)
    : undefined;
  const prometheusConnector = new PrometheusConnector(config);
  const kafkaConnector = new KafkaConnector(config);
  const redisConnector = new RedisConnector(config);
  const strategyIndex = new StrategyIndexConnector(
    config,
    config.entities.alerts === "mysql"
      ? mysqlConnector
      : elasticsearchConnector,
  );
  const audits = new StrategyAuditJobs(
    (query, signal) => strategyIndex.auditTarget(query, signal),
    config.entities.alerts === "mysql"
      ? mysqlConnector
      : elasticsearchConnector,
    Math.min(15000, config.query.timeoutMilliseconds),
  );
  app.get("/local-api/strategy-index/audits", async (_request, reply) => {
    reply.header("Cache-Control", "no-store");
    return audits.latest();
  });
  app.post("/local-api/strategy-index/audits", async (request, reply) => {
    reply.header("Cache-Control", "no-store");
    return audits.start(strategyAuditRequestSchema.parse(request.body));
  });
  app.get("/local-api/strategy-index/audits/:id", async (request, reply) => {
    reply.header("Cache-Control", "no-store");
    return audits.get(z.object({ id: z.uuid() }).parse(request.params).id);
  });
  app.post(
    "/local-api/strategy-index/audits/:id/cancel",
    async (request, reply) => {
      reply.header("Cache-Control", "no-store");
      return audits.cancel(z.object({ id: z.uuid() }).parse(request.params).id);
    },
  );

  for (const operation of ["targets", "reconcile", "browse"] as const) {
    app.get(
      `/local-api/strategy-index/${operation}`,
      async (request, reply) => {
        reply.header("Cache-Control", "no-store");
        const abort = new AbortController();
        const canceled = () => abort.abort();
        const disconnected = () => {
          if (!reply.raw.writableEnded) abort.abort();
        };
        request.raw.once("aborted", canceled);
        reply.raw.once("close", disconnected);
        try {
          return await (operation === "targets"
            ? strategyIndex.targets(abort.signal)
            : operation === "browse"
              ? strategyIndex.browse(
                  strategyBrowseQuerySchema.parse(request.query),
                  abort.signal,
                )
              : strategyIndex.inspect(
                  strategyQuerySchema.parse(request.query),
                  abort.signal,
                ));
        } finally {
          request.raw.off("aborted", canceled);
          reply.raw.off("close", disconnected);
        }
      },
    );
  }

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

  async function sourceRedis(query: unknown): Promise<RedisConnector> {
    if (!config.dispatch?.apiToken || !config.lifecycle) return redisConnector;
    const { event_source_id } = z
      .object({ event_source_id: z.string().optional() })
      .parse(query ?? {});
    const response = await fetch(
      `${config.dispatch.url.replace(/\/$/, "")}/api/v1/runtime`,
      {
        headers: { Authorization: `Bearer ${config.dispatch.apiToken}` },
        signal: AbortSignal.timeout(config.query.timeoutMilliseconds),
      },
    );
    if (!response.ok) throw new Error("source routing unavailable");
    const state = z
      .object({
        routes: z.record(
          z.string(),
          z.object({
            stream: z.string(),
            mailbox_prefix: z.string(),
            lock_prefix: z.string(),
          }),
        ),
      })
      .parse(await response.json());
    const id = event_source_id || Object.keys(state.routes).sort()[0];
    const route = id ? state.routes[id] : undefined;
    if (!route) throw new Error("event source routing unavailable");
    const selected = structuredClone(config);
    selected.lifecycle!.signal.stream = route.stream;
    selected.lifecycle!.mailbox.keyPrefix = route.mailbox_prefix;
    selected.lifecycle!.lock.keyPrefix = route.lock_prefix;
    return new RedisConnector(selected);
  }
  registerSourceRoutes(app, config);
  registerOneModelRoutes(app, config);
  app.get("/local-api/capabilities", async () => publicConfig(config));
  app.get("/local-api/config", async () => redactedConfig(config));
  app.get("/local-api/runtime/processes", async () =>
    prometheusConnector.processes(),
  );
  app.get("/local-api/runtime/cleaner", async () => {
    const [processes, metrics, runtime] = await Promise.all([
      prometheusConnector.processes(),
      prometheusConnector.cleanerSnapshot(),
      kafkaConnector.inspectRuntime(),
    ]);
    return {
      status: combinedStatus(processes, metrics, runtime.kafka),
      eventSources: runtime.eventSources,
      processes,
      metrics,
      kafka: runtime.kafka,
    };
  });
  app.get("/local-api/runtime/lifecycle", async (request) => {
    const [processes, metrics, redis] = await Promise.all([
      prometheusConnector.processes(),
      prometheusConnector.lifecycleSnapshot(),
      (await sourceRedis(request.query)).inspect(),
    ]);
    return {
      status: combinedStatus(processes, metrics, redis),
      config: redactedConfig(config).lifecycle,
      processes,
      metrics,
      redis,
    };
  });
  app.get("/local-api/runtime/control-plane", async (request, reply) => {
    const query = controlPlaneQuerySchema.parse(request.query);
    if (query.range_seconds > config.query.maxRangeSeconds) {
      throw new Error(
        `range_seconds must not exceed ${config.query.maxRangeSeconds}`,
      );
    }
    reply.header("Cache-Control", "no-store");
    if (!config.dispatch?.apiToken)
      return reply
        .code(503)
        .send({ error: { message: "请配置 dispatch.url 与 api_token" } });
    const abort = new AbortController();
    const disconnected = () => {
      if (!reply.raw.writableEnded) abort.abort();
    };
    reply.raw.once("close", disconnected);
    try {
      const response = await fetch(
        `${config.dispatch.url.replace(/\/$/, "")}/api/v1/control-plane/tasks`,
        {
          headers: { Authorization: `Bearer ${config.dispatch.apiToken}` },
          signal: AbortSignal.any([
            abort.signal,
            AbortSignal.timeout(config.query.timeoutMilliseconds),
          ]),
        },
      );
      if (!response.ok)
        return reply.code(502).send({
          error: {
            message: `控制面任务状态不可用（${response.status}），请确认控制面版本与连接配置`,
          },
        });
      const catalog = controlPlaneCatalogSchema.parse(await response.json());
      const elasticsearchEnabled = catalog.tasks.some(
        (task) => task.id === "elasticsearch-alert-archiver" && task.enabled,
      );
      const [allProcesses, metrics, archive] = await Promise.all([
        prometheusConnector.processes(),
        prometheusConnector.controlPlaneSnapshot(query.range_seconds),
        elasticsearchEnabled && elasticsearchConnector
          ? elasticsearchConnector.archiveBacklog()
          : Promise.resolve({
              status: "unavailable" as const,
              message: "待归档查询未配置",
              backlog: null,
            }),
      ]);
      return {
        ...catalog,
        status: "available",
        processes: controlPlaneProcesses(allProcesses),
        metrics,
        archive,
      };
    } catch {
      return reply.code(502).send({
        error: {
          message: "控制面任务状态读取失败或响应不完整，保留上次快照",
        },
      });
    } finally {
      reply.raw.off("close", disconnected);
    }
  });
  app.get("/local-api/infrastructure/kafka", async () =>
    kafkaConnector.inspect(),
  );
  app.get("/local-api/infrastructure/redis", async (request) =>
    (await sourceRedis(request.query)).inspect(),
  );
  app.get("/local-api/infrastructure/redis/pending", async (request) => {
    const query = redisPendingQuerySchema.parse(request.query);
    return (await sourceRedis(query)).inspectPending(query.group, query.limit);
  });
  app.get("/local-api/infrastructure/redis/mailboxes", async (request) => {
    const query = redisDetailQuerySchema.parse(request.query);
    return (await sourceRedis(query)).inspectMailboxes(
      query.query,
      query.limit,
    );
  });
  app.get("/local-api/infrastructure/redis/leases", async (request) => {
    const query = redisDetailQuerySchema.parse(request.query);
    return (await sourceRedis(query)).inspectLeases(query.query, query.limit);
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
  app.get("/local-api/elasticsearch/performance", async () => {
    if (!elasticsearchConnector)
      return {
        status: "unavailable",
        sampledAt: new Date().toISOString(),
        nodes: [],
        message: "Elasticsearch 未配置",
      };
    return elasticsearchConnector.performance();
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
    await audits.close();
    await mysqlConnector?.close();
    await redisConnector.close();
  });

  if (process.env.NODE_ENV === "production") {
    await registerWeb(app, basePath);
  }
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

async function searchEntity(
  entity: EntityKind,
  params: ReturnType<typeof parseSearchQuery>,
  config: ConsoleConfig,
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
  config: ConsoleConfig,
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

function entitySource(config: ConsoleConfig, entity: EntityKind) {
  if (entity === "events") return config.entities.events;
  if (entity === "alerts") return config.entities.alerts;
  return config.entities.alertLogs;
}
