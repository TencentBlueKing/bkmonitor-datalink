import {
  capabilitySchema,
  controlPlaneRuntimeSchema,
  elasticsearchTopologySchema,
  kafkaInfrastructureSchema,
  entityItemSchema,
  entityPageSchema,
  metricsResponseSchema,
  redisInfrastructureSchema,
  redisLeaseResponseSchema,
  redisMailboxResponseSchema,
  redisPendingResponseSchema,
  runtimeResponseSchema,
  configResponseSchema,
  entityStatsSchema,
  type Capabilities,
  type ControlPlaneRuntime,
  type ElasticsearchTopology,
  type KafkaInfrastructure,
  type EntityItem,
  type EntityKind,
  type EntityPage,
  type MetricsResponse,
  type RedisInfrastructure,
  type RedisLeaseResponse,
  type RedisMailboxResponse,
  type RedisPendingResponse,
  type RuntimeResponse,
  type ConfigResponse,
  type EntityStats,
} from "../shared/contracts";

export async function getCapabilities(): Promise<Capabilities> {
  return capabilitySchema.parse(await request("/local-api/capabilities"));
}

export async function getMetrics(input: {
  from: Date;
  to: Date;
  step: number;
  calculationWindowSeconds?: number;
  instance?: string;
  eventSourceId?: string;
  partition?: number;
}): Promise<MetricsResponse> {
  const query = new URLSearchParams({
    from: input.from.toISOString(),
    to: input.to.toISOString(),
    step: String(input.step),
  });
  if (input.calculationWindowSeconds !== undefined) {
    query.set(
      "calculation_window_seconds",
      String(input.calculationWindowSeconds),
    );
  }
  if (input.instance) query.set("instance", input.instance);
  if (input.eventSourceId) query.set("event_source_id", input.eventSourceId);
  if (input.partition !== undefined)
    query.set("partition", String(input.partition));
  return metricsResponseSchema.parse(
    await request(`/local-api/metrics?${query}`),
  );
}

export async function getRuntimeProcesses(): Promise<RuntimeResponse> {
  return runtimeResponseSchema.parse(
    await request("/local-api/runtime/processes"),
  );
}

export async function getCleanerRuntime(): Promise<RuntimeResponse> {
  return runtimeResponseSchema.parse(
    await request("/local-api/runtime/cleaner"),
  );
}

export async function getLifecycleRuntime(): Promise<RuntimeResponse> {
  return runtimeResponseSchema.parse(
    await request("/local-api/runtime/lifecycle"),
  );
}

export async function getControlPlaneRuntime(input: {
  rangeSeconds: number;
  instance?: string;
}): Promise<ControlPlaneRuntime> {
  const query = new URLSearchParams({
    range_seconds: String(input.rangeSeconds),
  });
  if (input.instance) query.set("instance", input.instance);
  return controlPlaneRuntimeSchema.parse(
    await request(`/local-api/runtime/control-plane?${query}`),
  );
}

export async function getKafkaInfrastructure(): Promise<KafkaInfrastructure> {
  return kafkaInfrastructureSchema.parse(
    await request("/local-api/infrastructure/kafka"),
  );
}

export async function getRedisInfrastructure(): Promise<RedisInfrastructure> {
  return redisInfrastructureSchema.parse(
    await request("/local-api/infrastructure/redis"),
  );
}

export async function getRedisPending(input: {
  group?: string;
  limit?: number;
}): Promise<RedisPendingResponse> {
  const query = new URLSearchParams();
  if (input.group) query.set("group", input.group);
  query.set("limit", String(input.limit ?? 50));
  return redisPendingResponseSchema.parse(
    await request(`/local-api/infrastructure/redis/pending?${query}`),
  );
}

export async function getRedisMailboxes(input: {
  query?: string;
  limit?: number;
}): Promise<RedisMailboxResponse> {
  const query = new URLSearchParams();
  if (input.query) query.set("query", input.query);
  query.set("limit", String(input.limit ?? 50));
  return redisMailboxResponseSchema.parse(
    await request(`/local-api/infrastructure/redis/mailboxes?${query}`),
  );
}

export async function getRedisLeases(input: {
  query?: string;
  limit?: number;
}): Promise<RedisLeaseResponse> {
  const query = new URLSearchParams();
  if (input.query) query.set("query", input.query);
  query.set("limit", String(input.limit ?? 50));
  return redisLeaseResponseSchema.parse(
    await request(`/local-api/infrastructure/redis/leases?${query}`),
  );
}

export async function getConfigSummary(): Promise<ConfigResponse> {
  return configResponseSchema.parse(await request("/local-api/config"));
}

export async function getElasticsearchTopology(): Promise<ElasticsearchTopology> {
  return elasticsearchTopologySchema.parse(
    await request("/local-api/elasticsearch/topology"),
  );
}

export async function searchEntities(
  entity: EntityKind,
  values: Record<string, string | undefined>,
): Promise<EntityPage> {
  const query = new URLSearchParams();
  for (const [key, value] of Object.entries(values))
    if (value) query.set(key, value);
  return entityPageSchema.parse(await request(`/local-api/${entity}?${query}`));
}

export async function getEntityStats(
  entity: EntityKind,
  values: Record<string, string | undefined>,
): Promise<EntityStats> {
  const query = new URLSearchParams();
  for (const [key, value] of Object.entries(values))
    if (value) query.set(key, value);
  return entityStatsSchema.parse(
    await request(`/local-api/${entity}/stats?${query}`),
  );
}

export async function getEntity(
  entity: EntityKind,
  tenantId: string,
  id: string,
): Promise<EntityItem> {
  const query = new URLSearchParams({ bk_tenant_id: tenantId });
  return entityItemSchema.parse(
    await request(`/local-api/${entity}/${encodeURIComponent(id)}?${query}`),
  );
}

async function request(url: string): Promise<unknown> {
  const response = await fetch(url, {
    headers: { accept: "application/json" },
  });
  const data = (await response.json()) as unknown;
  if (!response.ok) {
    const message =
      typeof data === "object" && data && "error" in data
        ? String(
            (data as { error?: { message?: string } }).error?.message ??
              "请求失败",
          )
        : "请求失败";
    throw new Error(message);
  }
  return data;
}
