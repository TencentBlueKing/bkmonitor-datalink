import {
  deliveryMetricQuery,
  deliveryMetricKindSchema,
  deliveryMetricIDs,
  type DeliveryMetricKind,
  type DeliveryMetricQuery,
} from "../shared/delivery-metrics";
import {
  policyPageSchema,
  policyRecordSchema,
  policyReleaseSchema,
  policyPreviewSchema,
  type PolicyKind,
  type PolicyListQuery,
  type PolicyPreviewRequest,
} from "../shared/policies";
import { metricCatalogSchema } from "../shared/metric-catalog";
import { consoleURL } from "./base-path";
import {
  strategyTargetsSchema,
  strategyResultSchema,
  type StrategyQuery,
  strategyBrowseResultSchema,
  type StrategyBrowseQuery,
  strategyAuditSchema,
  type StrategyAuditRequest,
} from "../shared/strategy-index";

export async function startStrategyAudit(query: StrategyAuditRequest) {
  return strategyAuditSchema.parse(
    await request("/local-api/strategy-index/audits", undefined, {
      method: "POST",
      body: JSON.stringify(query),
    }),
  );
}
export async function getStrategyAudit(id: string) {
  return strategyAuditSchema.parse(
    await request(`/local-api/strategy-index/audits/${encodeURIComponent(id)}`),
  );
}
export async function latestStrategyAudit() {
  return strategyAuditSchema
    .nullable()
    .parse(await request("/local-api/strategy-index/audits"));
}
export async function cancelStrategyAudit(id: string) {
  return strategyAuditSchema.parse(
    await request(
      `/local-api/strategy-index/audits/${encodeURIComponent(id)}/cancel`,
      undefined,
      { method: "POST" },
    ),
  );
}

export async function getStrategyTargets() {
  return strategyTargetsSchema.parse(
    await request("/local-api/strategy-index/targets"),
  );
}
export async function browseStrategyIndex(
  query: StrategyBrowseQuery,
  signal?: AbortSignal,
) {
  const params = new URLSearchParams({
    event_source_id: query.event_source_id,
    hook_name: query.hook_name,
    count: String(query.count),
  });
  if (query.cursor) params.set("cursor", query.cursor);
  return strategyBrowseResultSchema.parse(
    await request(`/local-api/strategy-index/browse?${params}`, signal),
  );
}
export async function reconcileStrategyIndex(query: StrategyQuery) {
  return strategyResultSchema.parse(
    await request(
      `/local-api/strategy-index/reconcile?${new URLSearchParams(query)}`,
    ),
  );
}
import {
  dynamicConfigResponseSchema,
  capabilitySchema,
  controlPlaneRuntimeSchema,
  elasticsearchTopologySchema,
  elasticsearchPerformanceSchema,
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

export async function getDynamicConfig({
  signal,
}: { signal?: AbortSignal } = {}) {
  return dynamicConfigResponseSchema.parse(
    await request("/local-api/dynamic-config", signal),
  );
}

export async function getCapabilities(): Promise<Capabilities> {
  return capabilitySchema.parse(await request("/local-api/capabilities"));
}

export async function getPolicyLink(
  query: import("../shared/policy-links").PolicyLinkQuery,
  signal?: AbortSignal,
) {
  const { policyLinkSchema, policyLinkQuerySchema } =
    await import("../shared/policy-links");
  const input = policyLinkQuerySchema.parse(query);
  const params = new URLSearchParams(
    Object.entries(input).filter(([, v]) => v !== undefined) as [
      string,
      string,
    ][],
  );
  const result = policyLinkSchema.parse(
    await request("/local-api/policy-links?" + params, signal),
  );
  if (result.bk_tenant_id !== input.bk_tenant_id || result.type !== input.type)
    throw new Error("配置入口作用域不一致");
  return result;
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

export async function getDeliveryMetrics(
  kind: DeliveryMetricKind,
  input: DeliveryMetricQuery,
  signal?: AbortSignal,
): Promise<MetricsResponse> {
  deliveryMetricKindSchema.parse(kind);
  const ids = deliveryMetricIDs[kind];
  const subject = kind === "action" ? "动作" : "投影";
  const query = deliveryMetricQuery.parse(input);
  const params = new URLSearchParams(
    Object.entries(query).map(([k, v]) => [k, String(v)]),
  );
  const data = metricsResponseSchema.parse(
    await request(`/local-api/${kind}-metrics?${params}`, signal),
  );
  if (
    Date.parse(data.from) !== Date.parse(query.from) ||
    Date.parse(data.to) !== Date.parse(query.to) ||
    data.step !== query.step ||
    data.panels.length !== ids.length ||
    ids.some((id) => data.panels.filter((p) => p.id === id).length !== 1)
  )
    throw new Error(`${subject}指标响应与查询范围不一致`);
  return data;
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
  signal?: AbortSignal;
  rangeSeconds: number;
  instance?: string;
}): Promise<ControlPlaneRuntime> {
  const query = new URLSearchParams({
    range_seconds: String(input.rangeSeconds),
  });
  if (input.instance) query.set("instance", input.instance);
  return controlPlaneRuntimeSchema.parse(
    await request(`/local-api/runtime/control-plane?${query}`, input.signal),
  );
}

export async function getKafkaInfrastructure(): Promise<KafkaInfrastructure> {
  return kafkaInfrastructureSchema.parse(
    await request("/local-api/infrastructure/kafka"),
  );
}

export async function getRedisInfrastructure(
  eventSourceId?: string,
): Promise<RedisInfrastructure> {
  return redisInfrastructureSchema.parse(
    await request(
      `/local-api/infrastructure/redis${eventSourceId ? `?event_source_id=${encodeURIComponent(eventSourceId)}` : ""}`,
    ),
  );
}

export async function getRedisPending(input: {
  eventSourceId?: string;
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
  eventSourceId?: string;
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
  eventSourceId?: string;
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

export async function getElasticsearchPerformance() {
  return elasticsearchPerformanceSchema.parse(
    await request("/local-api/elasticsearch/performance"),
  );
}

export async function searchEntities(
  entity: EntityKind,
  values: Record<string, string | undefined>,
  signal?: AbortSignal,
): Promise<EntityPage> {
  const query = new URLSearchParams();
  for (const [key, value] of Object.entries(values))
    if (value) query.set(key, value);
  return entityPageSchema.parse(
    await request(`/local-api/${entity}?${query}`, signal),
  );
}

export async function getEntityStats(
  entity: EntityKind,
  values: Record<string, string | undefined>,
  signal?: AbortSignal,
): Promise<EntityStats> {
  const query = new URLSearchParams();
  for (const [key, value] of Object.entries(values))
    if (value) query.set(key, value);
  return entityStatsSchema.parse(
    await request(`/local-api/${entity}/stats?${query}`, signal),
  );
}

export async function getEntity(
  entity: EntityKind,
  tenantId: string,
  id: string,
  signal?: AbortSignal,
): Promise<EntityItem> {
  const query = new URLSearchParams({ bk_tenant_id: tenantId });
  return entityItemSchema.parse(
    await request(
      `/local-api/${entity}/${encodeURIComponent(id)}?${query}`,
      signal,
    ),
  );
}

async function request(
  url: string,
  signal?: AbortSignal,
  init: RequestInit = {},
): Promise<unknown> {
  const response = await fetch(consoleURL(url), {
    ...init,
    headers: {
      accept: "application/json",
      ...(init.body ? { "content-type": "application/json" } : {}),
    },
    signal,
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

export async function getMetricCatalog({
  signal,
}: { signal?: AbortSignal } = {}) {
  return metricCatalogSchema.parse(
    await request("/local-api/metrics/catalog", signal),
  );
}

export async function listMergeRuntime(
  query: import("../shared/merge-runtime").MergeQuery,
  signal?: AbortSignal,
) {
  const { mergePage } = await import("../shared/merge-runtime");
  const params = new URLSearchParams(
    Object.entries(query)
      .filter(([, v]) => v !== undefined)
      .map(([k, v]) => [k, String(v)]),
  );
  return mergePage(query.resource).parse(
    await request("/local-api/policy-runtime/merge?" + params, signal),
  );
}
export async function getMergeRuntime(
  tenant: string,
  resource: import("../shared/merge-runtime").MergeResource,
  id: string,
  signal?: AbortSignal,
) {
  const { mergeRow } = await import("../shared/merge-runtime");
  return mergeRow(resource).parse(
    await request(
      "/local-api/policy-runtime/merge/" +
        resource +
        "/" +
        encodeURIComponent(id) +
        "?" +
        new URLSearchParams({ bk_tenant_id: tenant }),
      signal,
    ),
  );
}
export async function getMergeMembers(
  tenant: string,
  id: string,
  after: string,
  signal?: AbortSignal,
) {
  const { mergeSnapshotPage } = await import("../shared/merge-runtime");
  return mergeSnapshotPage.parse(
    await request(
      "/local-api/policy-runtime/merge/decisions/" +
        encodeURIComponent(id) +
        "/members?" +
        new URLSearchParams({ bk_tenant_id: tenant, after }),
      signal,
    ),
  );
}

export async function getMergeSnapshot(
  tenant: string,
  id: string,
  alert: string,
  signal?: AbortSignal,
) {
  const { frozenMergeSnapshot } = await import("../shared/merge-runtime");
  return frozenMergeSnapshot.parse(
    await request(
      "/local-api/policy-runtime/merge/decisions/" +
        encodeURIComponent(id) +
        "/members/" +
        encodeURIComponent(alert) +
        "?" +
        new URLSearchParams({ bk_tenant_id: tenant }),
      signal,
    ),
  );
}

export async function listShieldRuntime(
  query: import("../shared/shield-runtime").ShieldQuery,
  signal?: AbortSignal,
) {
  const { shieldPage } = await import("../shared/shield-runtime");
  const params = new URLSearchParams(
    Object.entries(query)
      .filter(([, v]) => v !== undefined)
      .map(([k, v]) => [k, String(v)]),
  );
  return shieldPage.parse(
    await request("/local-api/policy-runtime/shield/alerts?" + params, signal),
  );
}

export async function listSuppressionRuntime(
  kind: import("../shared/suppression-runtime").SuppressionKind,
  query: import("../shared/suppression-runtime").SuppressionQuery,
  signal?: AbortSignal,
) {
  const { suppressionPage } = await import("../shared/suppression-runtime");
  const params = new URLSearchParams(
    Object.entries(query)
      .filter(([, v]) => v !== undefined)
      .map(([k, v]) => [k, String(v)]),
  );
  return suppressionPage.parse(
    await request(
      "/local-api/policy-runtime/suppression/" + kind + "?" + params,
      signal,
    ),
  );
}
export async function getSuppressionRuntime(
  tenant: string,
  kind: import("../shared/suppression-runtime").SuppressionKind,
  id: string,
  signal?: AbortSignal,
) {
  const { suppressionWindow } = await import("../shared/suppression-runtime");
  return suppressionWindow.parse(
    await request(
      "/local-api/policy-runtime/suppression/" +
        kind +
        "/" +
        encodeURIComponent(id) +
        "?" +
        new URLSearchParams({ bk_tenant_id: tenant }),
      signal,
    ),
  );
}
export async function getSuppressionMembers(
  tenant: string,
  kind: import("../shared/suppression-runtime").SuppressionKind,
  id: string,
  epoch: string,
  after: string,
  signal?: AbortSignal,
) {
  const { suppressionMembers } = await import("../shared/suppression-runtime");
  return suppressionMembers.parse(
    await request(
      "/local-api/policy-runtime/suppression/" +
        kind +
        "/" +
        encodeURIComponent(id) +
        "/members?" +
        new URLSearchParams({ bk_tenant_id: tenant, epoch, after }),
      signal,
    ),
  );
}
export async function getShieldRuntime(
  tenant: string,
  id: string,
  signal?: AbortSignal,
) {
  const { shieldRecord } = await import("../shared/shield-runtime");
  return shieldRecord.parse(
    await request(
      "/local-api/policy-runtime/shield/alerts/" +
        encodeURIComponent(id) +
        "?" +
        new URLSearchParams({ bk_tenant_id: tenant }),
      signal,
    ),
  );
}
export async function getShieldHistory(
  tenant: string,
  id: string,
  after: string,
  signal?: AbortSignal,
) {
  const { shieldHistory } = await import("../shared/shield-runtime");
  return shieldHistory.parse(
    await request(
      "/local-api/policy-runtime/shield/alerts/" +
        encodeURIComponent(id) +
        "/history?" +
        new URLSearchParams({ bk_tenant_id: tenant, after }),
      signal,
    ),
  );
}

export async function listPolicies(
  query: PolicyListQuery,
  signal?: AbortSignal,
) {
  const params = new URLSearchParams({
    bk_tenant_id: query.bk_tenant_id,
    type: query.type,
    after: query.after,
    limit: String(query.limit),
  });
  if (query.is_enable !== undefined) params.set("is_enable", query.is_enable);
  return policyPageSchema.parse(
    await request("/local-api/policies?" + params, signal),
  );
}
export async function getPolicy(
  tenant: string,
  type: PolicyKind,
  id: string,
  signal?: AbortSignal,
) {
  return policyRecordSchema.parse(
    await request(
      "/local-api/policies/" +
        type +
        "/" +
        encodeURIComponent(id) +
        "?" +
        new URLSearchParams({ bk_tenant_id: tenant }),
      signal,
    ),
  );
}
export async function getPolicyRelease(
  tenant: string,
  type: PolicyKind,
  id: string,
  version: number,
  signal?: AbortSignal,
) {
  return policyReleaseSchema.parse(
    await request(
      "/local-api/policies/" +
        type +
        "/" +
        encodeURIComponent(id) +
        "/releases/" +
        version +
        "?" +
        new URLSearchParams({ bk_tenant_id: tenant }),
      signal,
    ),
  );
}
export async function previewPolicy(
  body: PolicyPreviewRequest,
  signal?: AbortSignal,
) {
  return policyPreviewSchema.parse(
    await request("/local-api/policies/preview", signal, {
      method: "POST",
      body: JSON.stringify(body),
    }),
  );
}

export async function getShieldCheck(
  tenant: string,
  id: string,
  signal?: AbortSignal,
) {
  const { shieldLatestCheck } = await import("../shared/shield-checks");
  return shieldLatestCheck.parse(
    await request(
      "/local-api/policy-runtime/shield/alerts/" +
        encodeURIComponent(id) +
        "/check?" +
        new URLSearchParams({ bk_tenant_id: tenant }),
      signal,
    ),
  );
}
export async function listShieldRequests(
  tenant: string,
  id: string,
  after: string,
  signal?: AbortSignal,
) {
  const { shieldCheckRequests } = await import("../shared/shield-checks");
  return shieldCheckRequests.parse(
    await request(
      "/local-api/policy-runtime/shield/alerts/" +
        encodeURIComponent(id) +
        "/requests?" +
        new URLSearchParams({ bk_tenant_id: tenant, after }),
      signal,
    ),
  );
}
export async function getShieldRequest(
  tenant: string,
  id: string,
  requestID: string,
  signal?: AbortSignal,
) {
  const { shieldCheckRequest } = await import("../shared/shield-checks");
  return shieldCheckRequest.parse(
    await request(
      "/local-api/policy-runtime/shield/alerts/" +
        encodeURIComponent(id) +
        "/requests/" +
        requestID +
        "?" +
        new URLSearchParams({ bk_tenant_id: tenant }),
      signal,
    ),
  );
}

export async function listProjectionTasks(
  query: import("../shared/projection-tasks").ProjectionQuery,
  signal?: AbortSignal,
) {
  const { projectionPage } = await import("../shared/projection-tasks");
  return projectionPage.parse(
    await request(
      "/local-api/projection-tasks?" +
        new URLSearchParams(
          Object.entries(query)
            .filter(([, v]) => v !== undefined)
            .map(([k, v]) => [k, String(v)]),
        ),
      signal,
    ),
  );
}
export async function getProjectionTask(
  tenant: string,
  id: string,
  signal?: AbortSignal,
) {
  const { projectionTask } = await import("../shared/projection-tasks");
  return projectionTask.parse(
    await request(
      "/local-api/projection-tasks/" +
        id +
        "?" +
        new URLSearchParams({ bk_tenant_id: tenant }),
      signal,
    ),
  );
}
export async function getProjectionSnapshot(
  tenant: string,
  id: string,
  signal?: AbortSignal,
) {
  const { projectionSnapshot } = await import("../shared/projection-tasks");
  return projectionSnapshot.parse(
    await request(
      "/local-api/projection-tasks/" +
        id +
        "/snapshot?" +
        new URLSearchParams({ bk_tenant_id: tenant }),
      signal,
    ),
  );
}

export async function listActionDeliveries(
  query: import("../shared/action-deliveries").ActionQuery,
  signal?: AbortSignal,
) {
  const { actionPage } = await import("../shared/action-deliveries");
  return actionPage.parse(
    await request(
      "/local-api/action-deliveries?" +
        new URLSearchParams(
          Object.entries(query)
            .filter(([, v]) => v !== undefined)
            .map(([k, v]) => [k, String(v)]),
        ),
      signal,
    ),
  );
}
export async function getActionDelivery(
  tenant: string,
  id: string,
  signal?: AbortSignal,
) {
  const { actionDelivery } = await import("../shared/action-deliveries");
  return actionDelivery.parse(
    await request(
      "/local-api/action-deliveries/" +
        id +
        "?" +
        new URLSearchParams({ bk_tenant_id: tenant }),
      signal,
    ),
  );
}
export async function getActionSnapshot(
  tenant: string,
  id: string,
  signal?: AbortSignal,
) {
  const { actionSnapshot } = await import("../shared/action-deliveries");
  return actionSnapshot.parse(
    await request(
      "/local-api/action-deliveries/" +
        id +
        "/snapshot?" +
        new URLSearchParams({ bk_tenant_id: tenant }),
      signal,
    ),
  );
}

export async function getActionOrder(
  tenant: string,
  id: string,
  signal?: AbortSignal,
) {
  const { actionOrder } = await import("../shared/action-deliveries");
  return actionOrder.parse(
    await request(
      "/local-api/action-deliveries/" +
        id +
        "/order?" +
        new URLSearchParams({ bk_tenant_id: tenant }),
      signal,
    ),
  );
}

export async function getKACAlertLink(
  query: { bk_tenant_id: string; alarm_id: string },
  signal?: AbortSignal,
) {
  const { kacAlertLinkQuery, kacAlertLinkResponse } =
    await import("../shared/kac-alert-link");
  const input = kacAlertLinkQuery.parse(query);
  const result = kacAlertLinkResponse.parse(
    await request(
      "/local-api/kac-alert-link?" + new URLSearchParams(input),
      signal,
    ),
  );
  if (
    result.bk_tenant_id !== input.bk_tenant_id ||
    result.alarm_id !== input.alarm_id
  )
    throw new Error("告警入口作用域不一致");
  return result;
}

export async function simulatePolicy(
  input: import("../shared/policy-simulation").SimulationRequest,
  signal?: AbortSignal,
) {
  const { simulationRequestSchema, simulationResponseSchema } =
    await import("../shared/policy-simulation");
  const q = simulationRequestSchema.parse(input);
  const r = simulationResponseSchema.parse(
    await request("/local-api/policies/simulate", signal, {
      method: "POST",
      body: JSON.stringify(q),
      headers: { "Content-Type": "application/json" },
    }),
  );
  if (
    r.bk_tenant_id !== q.bk_tenant_id ||
    r.type !== q.type ||
    r.id !== (q.spec ? "preview" : q.id) ||
    r.version !== (q.spec ? 0 : q.version)
  )
    throw new Error("模拟作用域不一致");
  return r;
}
export async function getPolicyStatistics(
  input: import("../shared/policy-simulation").StatisticsQuery,
  signal?: AbortSignal,
) {
  const { statisticsQuerySchema, statisticsResponseSchema } =
    await import("../shared/policy-simulation");
  const q = statisticsQuerySchema.parse(input);
  const r = statisticsResponseSchema.parse(
    await request(
      "/local-api/policies/statistics?" +
        new URLSearchParams({
          bk_tenant_id: q.bk_tenant_id,
          type: q.type,
          ids: q.ids.join(","),
          hours: String(q.hours),
        }),
      signal,
    ),
  );
  if (
    r.bk_tenant_id !== q.bk_tenant_id ||
    r.type !== q.type ||
    r.hours !== q.hours ||
    r.items.length !== q.ids.length ||
    r.items.some((v, i) => v.id !== q.ids[i])
  )
    throw new Error("统计作用域不一致");
  return r;
}
