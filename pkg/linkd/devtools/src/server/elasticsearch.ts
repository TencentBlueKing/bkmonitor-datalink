import type {
  ElasticsearchTopology,
  EntityItem,
  EntityKind,
  EntityPage,
  SearchParams,
} from "../shared/contracts.js";
import type { DevtoolsConfig } from "./config.js";
import { decodeCursor, encodeCursor, queryHash } from "./cursor.js";

interface SearchHit {
  _index?: string;
  _source: Record<string, unknown> & {
    bk_tenant_id: string;
    event_id?: string;
    alert_id?: string;
    log_id?: string;
    received_at?: string;
    update_at?: string;
    created_time?: string;
    processing?: Record<string, unknown>;
  };
  sort: Array<string | number>;
}

interface SearchResponse {
  pit_id?: string;
  hits: { hits: SearchHit[] };
}

interface StatsResponse {
  hits: { total?: number | { value: number } };
  aggregations?: Record<
    string,
    {
      buckets?: Array<{
        key?: string | number;
        key_as_string?: string;
        doc_count: number;
      }>;
    }
  >;
}

interface CountResponse {
  count?: number;
}

interface ResolveResponse {
  indices?: Array<{ name: string; aliases?: string[] }>;
  aliases?: Array<{ name: string; indices?: string[] }>;
  data_streams?: Array<{ name: string; backing_indices?: string[] }>;
}

interface CatIndexRow {
  health?: string;
  status?: string;
  index: string;
  pri?: string;
  rep?: string;
  "docs.count"?: string;
  "store.size"?: string;
}

interface ClusterInfoResponse {
  cluster_name?: string;
  version?: { number?: string };
}

interface ClusterHealthResponse {
  status?: string;
  number_of_nodes?: number;
  active_shards?: number;
  unassigned_shards?: number;
}

interface MappingResponse {
  [index: string]: { mappings?: { properties?: Record<string, unknown> } };
}

interface AliasResponse {
  [index: string]: { aliases?: Record<string, { is_write_index?: boolean }> };
}

interface TemplateMetadata {
  schema?: string;
  entity?: string;
  schema_version?: number;
}

interface TemplateResponse {
  index_templates?: Array<{
    name: string;
    index_template?: {
      index_patterns?: string[];
      _meta?: TemplateMetadata;
    };
  }>;
}

export class ElasticsearchConnector {
  private readonly baseUrl: string;
  private readonly timeoutMilliseconds: number;
  private readonly headers: Record<string, string>;
  private readonly targets: Record<EntityKind, string[]>;
  private readonly indexPrefix?: string;
  private readonly resolvedTargets = new Map<string, ResolveResponse>();

  constructor(config: DevtoolsConfig) {
    if (!config.elasticsearch)
      throw new Error("elasticsearch is not configured");
    this.baseUrl = config.elasticsearch.baseUrl.replace(/\/$/, "");
    this.timeoutMilliseconds = config.query.timeoutMilliseconds;
    this.headers = authHeaders(config.elasticsearch.auth);
    this.targets = {
      events: config.elasticsearch.eventTargets,
      alerts: config.elasticsearch.alertTargets,
      "alert-logs": config.elasticsearch.alertLogTargets,
    };
    this.indexPrefix = config.elasticsearch.indexPrefix;
  }

  async archiveBacklog(): Promise<{
    status: "available" | "unavailable";
    message?: string;
    backlog: number | null;
  }> {
    if (!this.indexPrefix) {
      return {
        status: "unavailable",
        message: "Elasticsearch index_prefix 未配置",
        backlog: null,
      };
    }
    try {
      const target = `${this.indexPrefix}-alerts-active`;
      const response = await this.request<CountResponse>(
        `/${escapeTarget(target)}/_count?ignore_unavailable=true`,
        {
          method: "POST",
          body: JSON.stringify({
            query: {
              bool: { must_not: [{ term: { status: "active" } }] },
            },
          }),
        },
      );
      return {
        status: "available",
        backlog: Math.max(0, Math.trunc(response.count ?? 0)),
      };
    } catch {
      return {
        status: "unavailable",
        message: "待归档 Alert 查询失败",
        backlog: null,
      };
    }
  }

  async search(entity: EntityKind, params: SearchParams): Promise<EntityPage> {
    const targets = this.targets[entity];
    await this.validateTargets(targets);
    const queryIdentity = { ...params, cursor: undefined };
    let pitId: string;
    let searchAfter: Array<string | number> | undefined;
    if (params.cursor) {
      const cursor = decodeCursor(params.cursor, entity, queryIdentity);
      if (cursor.kind !== "elasticsearch" || !cursor.pitId)
        throw new Error("invalid elasticsearch cursor");
      pitId = cursor.pitId;
      searchAfter = cursor.values;
    } else {
      pitId = await this.openPIT(targets);
    }

    const fields = entityFields(entity);
    const filters = buildFilters(entity, params, fields);
    const body: Record<string, unknown> = {
      size: entity === "alerts" ? (params.limit + 1) * 2 : params.limit + 1,
      track_total_hits: false,
      query: { bool: { filter: filters } },
      pit: { id: pitId, keep_alive: "1m" },
      sort: [
        { [fields.time]: "desc" },
        { bk_tenant_id: "asc" },
        { [fields.id]: "asc" },
        { _shard_doc: "asc" },
      ],
      timeout: `${this.timeoutMilliseconds}ms`,
    };
    if (searchAfter) body.search_after = searchAfter;
    const response = await this.request<SearchResponse>("/_search", {
      method: "POST",
      body: JSON.stringify(body),
    });
    const rawHits = response.hits.hits;
    const groups =
      entity === "alerts"
        ? groupAlertHits(rawHits)
        : rawHits.map((hit) => ({ hit, cursorSort: hit.sort }));
    const visibleGroups = groups.slice(0, params.limit);
    const visible = visibleGroups.map((group) => group.hit);
    const items = visible.map((hit) => hitToItem(entity, hit));
    let nextCursor: string | undefined;
    if (groups.length > params.limit && visible.length > 0) {
      const last = visibleGroups.at(-1)!;
      nextCursor = encodeCursor({
        version: 1,
        kind: "elasticsearch",
        entity,
        queryHash: queryHash(queryIdentity),
        values: last.cursorSort,
        pitId: response.pit_id ?? pitId,
        from: params.from,
        to: params.to,
      });
    } else {
      await this.closePIT(response.pit_id ?? pitId);
    }
    return {
      items,
      nextCursor,
      source: "elasticsearch",
      warnings:
        entity === "alerts" && groups.length < rawHits.length
          ? ["检测到归档过渡副本，已优先展示 AlertHistory。"]
          : [],
    };
  }

  async detail(
    entity: EntityKind,
    tenantId: string,
    id: string,
  ): Promise<EntityItem | undefined> {
    const targets = this.targets[entity];
    await this.validateTargets(targets);
    const fields = entityFields(entity);
    const body = {
      size: entity === "alerts" ? 2 : 1,
      track_total_hits: false,
      query: {
        bool: {
          filter: [
            { term: { bk_tenant_id: tenantId } },
            { term: { [fields.id]: id } },
          ],
        },
      },
      sort: [{ [fields.time]: "desc" }, { _shard_doc: "asc" }],
      timeout: `${this.timeoutMilliseconds}ms`,
    };
    const response = await this.request<SearchResponse>(
      `/${targets.map(escapeTarget).join(",")}/_search`,
      {
        method: "POST",
        body: JSON.stringify(body),
      },
    );
    const hit =
      entity === "alerts"
        ? groupAlertHits(response.hits.hits)[0]?.hit
        : response.hits.hits[0];
    return hit ? hitToItem(entity, hit) : undefined;
  }

  async stats(entity: EntityKind, params: SearchParams) {
    const targets = this.targets[entity];
    await this.validateTargets(targets);
    const fields = entityFields(entity);
    const bucketSeconds = statsBucketSeconds(params);
    const facetFields: Array<{ name: string; field: string }> =
      entity === "events"
        ? [
            { name: "event_source_id", field: "event_source_id" },
            { name: "processing_state", field: "processing.state" },
          ]
        : entity === "alerts"
          ? ["status", "event_source_id", "severity"].map((field) => ({
              name: field,
              field,
            }))
          : ["operation_kind", "operator_kind"].map((field) => ({
              name: field,
              field,
            }));
    const aggregations: Record<string, unknown> = {
      timeline: {
        date_histogram: {
          field: fields.time,
          fixed_interval: `${bucketSeconds}s`,
          min_doc_count: 0,
          ...(params.from && params.to
            ? { extended_bounds: { min: params.from, max: params.to } }
            : {}),
        },
      },
    };
    for (const facet of facetFields)
      aggregations[`facet_${facet.name}`] = {
        terms: { field: facet.field, size: 100 },
      };
    const response = await this.request<StatsResponse>(
      `/${targets.map(escapeTarget).join(",")}/_search`,
      {
        method: "POST",
        body: JSON.stringify({
          size: 0,
          track_total_hits: true,
          query: { bool: { filter: buildFilters(entity, params, fields) } },
          aggs: aggregations,
          timeout: `${this.timeoutMilliseconds}ms`,
        }),
      },
    );
    const total =
      typeof response.hits.total === "number"
        ? response.hits.total
        : (response.hits.total?.value ?? 0);
    return {
      entity,
      source: "elasticsearch" as const,
      total,
      timeline: (response.aggregations?.timeline?.buckets ?? []).map(
        (bucket) => ({
          timestamp:
            bucket.key_as_string ??
            new Date(Number(bucket.key ?? 0)).toISOString(),
          count: bucket.doc_count,
        }),
      ),
      facets: facetFields.map((facet) => ({
        name: facet.name,
        values: (
          response.aggregations?.[`facet_${facet.name}`]?.buckets ?? []
        ).map((bucket) => ({
          value: String(bucket.key ?? ""),
          count: bucket.doc_count,
        })),
      })),
      warnings:
        entity === "alerts"
          ? ["Alert 归档短暂重叠期间，聚合统计可能重复计数。"]
          : [],
    };
  }

  async topology(): Promise<ElasticsearchTopology> {
    const configured = (
      Object.entries(this.targets) as Array<[EntityKind, string[]]>
    ).filter(([, targets]) => targets.length > 0);
    if (configured.length === 0)
      throw new Error("no Elasticsearch targets are configured");

    const [clusterInfo, clusterHealth, resolutions] = await Promise.all([
      this.optionalRequest<ClusterInfoResponse>("/"),
      this.optionalRequest<ClusterHealthResponse>("/_cluster/health"),
      Promise.all(
        configured.map(async ([entity, targets]) => ({
          entity,
          targets,
          resolved: await this.resolveTargets(targets),
        })),
      ),
    ]);

    const indexEntities = new Map<string, Set<EntityKind>>();
    const aliasIndices = new Map<string, Set<string>>();
    const targetGroups = resolutions.map(({ entity, targets, resolved }) => {
      const indices = physicalIndices(resolved);
      const aliases = resolvedAliases(resolved);
      for (const index of indices) addToSet(indexEntities, index, entity);
      for (const alias of resolved.aliases ?? []) {
        for (const index of alias.indices ?? [])
          addToSet(aliasIndices, alias.name, index);
      }
      for (const index of resolved.indices ?? []) {
        for (const alias of index.aliases ?? [])
          addToSet(aliasIndices, alias, index.name);
      }
      return {
        entity,
        configuredTargets: targets,
        indices,
        aliases,
      };
    });
    const physical = [...indexEntities.keys()].sort();
    if (physical.length === 0) {
      const templateValues = await Promise.all(
        configured
          .flatMap(([, targets]) => targets)
          .map((target) =>
            this.optionalRequest<TemplateResponse>(
              `/_index_template/${escapeTarget(target)}`,
            ),
          ),
      );
      return {
        cluster: {
          name: clusterInfo?.cluster_name ?? "unknown",
          version: clusterInfo?.version?.number ?? "unknown",
          status: clusterHealth?.status ?? "unknown",
          numberOfNodes: clusterHealth?.number_of_nodes ?? 0,
          activeShards: clusterHealth?.active_shards ?? 0,
          unassignedShards: clusterHealth?.unassigned_shards ?? 0,
        },
        targets: targetGroups,
        indices: [],
        aliases: [],
        templates: templateValues
          .flatMap((value) => value?.index_templates ?? [])
          .map((value) => ({
            name: value.name,
            indexPatterns: value.index_template?.index_patterns ?? [],
            schema: templateSchema(value.index_template?._meta),
          }))
          .sort((left, right) => left.name.localeCompare(right.name)),
      };
    }
    const metadataChunks = await Promise.all(
      chunkValues(physical, 128).map(async (indices) => {
        const targetPath = indices.map(escapeTarget).join(",");
        const [rows, mappings, aliases] = await Promise.all([
          this.optionalRequest<CatIndexRow[]>(
            `/_cat/indices/${targetPath}?format=json&bytes=b&h=health,status,index,pri,rep,docs.count,store.size`,
          ),
          this.optionalRequest<MappingResponse>(`/${targetPath}/_mapping`),
          this.optionalRequest<AliasResponse>(`/${targetPath}/_alias`),
        ]);
        return { rows, mappings, aliases };
      }),
    );
    const templateValues = await Promise.all(
      configured
        .flatMap(([, targets]) => targets)
        .map((target) =>
          this.optionalRequest<TemplateResponse>(
            `/_index_template/${escapeTarget(target)}`,
          ),
        ),
    );
    const rowsValue = metadataChunks.flatMap((chunk) => chunk.rows ?? []);
    const mappings = Object.assign(
      {},
      ...metadataChunks.map((chunk) => chunk.mappings ?? {}),
    ) as MappingResponse;
    const aliasDetails = Object.assign(
      {},
      ...metadataChunks.map((chunk) => chunk.aliases ?? {}),
    ) as AliasResponse;
    const rows: CatIndexRow[] = rowsValue.length
      ? rowsValue
      : physical.map((index): CatIndexRow => ({ index }));
    for (const [index, value] of Object.entries(aliasDetails ?? {})) {
      for (const alias of Object.keys(value.aliases ?? {}))
        addToSet(aliasIndices, alias, index);
    }
    const indices = rows
      .map((row) => ({
        name: row.index,
        health: row.health ?? "unknown",
        status: row.status ?? "unknown",
        primaryShards: nonNegativeNumber(row.pri),
        replicaShards: nonNegativeNumber(row.rep),
        docsCount: nonNegativeNumber(row["docs.count"]),
        storeBytes: nonNegativeNumber(row["store.size"]),
        aliases: [...aliasIndices.entries()]
          .filter(([, names]) => names.has(row.index))
          .map(([name]) => name)
          .sort(),
        entities: [...(indexEntities.get(row.index) ?? [])].sort(),
        mappingFields: Object.keys(
          mappings?.[row.index]?.mappings?.properties ?? {},
        ).sort(),
      }))
      .sort((left, right) => left.name.localeCompare(right.name));
    const aliases = [...aliasIndices.entries()]
      .map(([name, names]) => ({
        name,
        indices: [...names].sort(),
        entities: uniqueEntities(
          [...names].flatMap((index) => [...(indexEntities.get(index) ?? [])]),
        ),
        writeIndex: [...names].find(
          (index) =>
            aliasDetails?.[index]?.aliases?.[name]?.is_write_index === true,
        ),
      }))
      .sort((left, right) => left.name.localeCompare(right.name));

    return {
      cluster: {
        name: clusterInfo?.cluster_name ?? "unknown",
        version: clusterInfo?.version?.number ?? "unknown",
        status: clusterHealth?.status ?? "unknown",
        numberOfNodes: clusterHealth?.number_of_nodes ?? 0,
        activeShards: clusterHealth?.active_shards ?? 0,
        unassignedShards: clusterHealth?.unassigned_shards ?? 0,
      },
      targets: targetGroups,
      indices,
      aliases,
      templates: templateValues
        .flatMap((value) => value?.index_templates ?? [])
        .map((value) => ({
          name: value.name,
          indexPatterns: value.index_template?.index_patterns ?? [],
          schema: templateSchema(value.index_template?._meta),
        }))
        .sort((left, right) => left.name.localeCompare(right.name)),
    };
  }

  private async openPIT(targets: string[]): Promise<string> {
    const response = await this.request<{ id: string }>(
      `/${targets.map(escapeTarget).join(",")}/_pit?keep_alive=1m&ignore_unavailable=true`,
      { method: "POST" },
    );
    if (!response.id) throw new Error("Elasticsearch returned an empty PIT id");
    return response.id;
  }

  private async closePIT(id: string): Promise<void> {
    try {
      await this.request("/_pit", {
        method: "DELETE",
        body: JSON.stringify({ id }),
      });
    } catch {
      // PIT 有固定 keep_alive；关闭失败不能把已经成功的只读结果变成错误。
    }
  }

  private async validateTargets(targets: string[]): Promise<void> {
    const resolved = await this.resolveTargets(targets);
    const physical = physicalIndices(resolved);
    if (physical.length === 0) {
      throw new Error(
        "no matching Elasticsearch indices are currently available",
      );
    }
  }

  private async resolveTargets(targets: string[]): Promise<ResolveResponse> {
    const key = targets.join(",");
    const cached = this.resolvedTargets.get(key);
    if (cached) return cached;
    for (const target of targets) {
      if (
        !/^[a-z0-9][a-z0-9._*-]{0,254}$/.test(target) ||
        target.includes("..")
      ) {
        throw new Error(`invalid Elasticsearch target: ${target}`);
      }
    }
    const resolved = await this.request<ResolveResponse>(
      `/_resolve/index/${targets.map(escapeTarget).join(",")}?expand_wildcards=open`,
    );
    this.resolvedTargets.set(key, resolved);
    return resolved;
  }

  private async request<T = unknown>(
    pathname: string,
    init: RequestInit = {},
  ): Promise<T> {
    const response = await fetch(`${this.baseUrl}${pathname}`, {
      ...init,
      headers: {
        accept: "application/json",
        "content-type": "application/json",
        ...this.headers,
        ...init.headers,
      },
      signal: AbortSignal.timeout(this.timeoutMilliseconds),
    });
    if (!response.ok)
      throw new Error(
        `Elasticsearch request failed with status ${response.status}`,
      );
    return (await response.json()) as T;
  }

  private async optionalRequest<T>(pathname: string): Promise<T | undefined> {
    try {
      return await this.request<T>(pathname);
    } catch {
      // 集群健康和容量属于增强信息；只具备 read/view_index_metadata 的账号
      // 仍应能看到白名单 target 到物理索引的解析关系。
      return undefined;
    }
  }
}

function entityFields(entity: EntityKind) {
  if (entity === "events") return { id: "event_id", time: "received_at" };
  if (entity === "alerts") return { id: "alert_id", time: "update_at" };
  return { id: "log_id", time: "created_time" };
}

function templateSchema(
  metadata: TemplateMetadata | undefined,
): string | undefined {
  if (!metadata) return undefined;
  if (metadata.schema) return metadata.schema;
  if (metadata.entity && metadata.schema_version !== undefined)
    return `${metadata.entity}:v${metadata.schema_version}`;
  return undefined;
}

function buildFilters(
  entity: EntityKind,
  params: SearchParams,
  fields: ReturnType<typeof entityFields>,
): Array<Record<string, unknown>> {
  const filters: Array<Record<string, unknown>> = [];
  if (params.tenantId)
    filters.push({ term: { bk_tenant_id: params.tenantId } });
  if (params.id) filters.push({ term: { [fields.id]: params.id } });
  if (params.from || params.to) {
    filters.push({
      range: {
        [fields.time]: {
          ...(params.from ? { gte: params.from } : {}),
          ...(params.to ? { lte: params.to } : {}),
        },
      },
    });
  }
  if (entity === "events") {
    if (params.state)
      filters.push({ term: { "processing.state": params.state } });
    if (params.eventSourceId)
      filters.push({ term: { event_source_id: params.eventSourceId } });
    if (params.relatedAlertId)
      filters.push({ term: { related_alert_id: params.relatedAlertId } });
  }
  if (entity === "alerts") {
    if (params.status) filters.push({ term: { status: params.status } });
    if (params.eventSourceId)
      filters.push({ term: { event_source_id: params.eventSourceId } });
    if (params.fingerprint)
      filters.push({ term: { fingerprint: params.fingerprint } });
    if (params.severity) filters.push({ term: { severity: params.severity } });
  }
  if (entity === "alert-logs") {
    if (params.alertId) filters.push({ term: { alert_id: params.alertId } });
    if (params.operationKind)
      filters.push({ term: { operation_kind: params.operationKind } });
    if (params.operatorKind)
      filters.push({ term: { operator_kind: params.operatorKind } });
  }
  return filters;
}

function statsBucketSeconds(params: SearchParams): number {
  const from = params.from
    ? new Date(params.from).getTime()
    : Date.now() - 3600_000;
  const to = params.to ? new Date(params.to).getTime() : Date.now();
  return Math.max(60, Math.ceil((to - from) / 1000 / 240));
}

function hitToItem(entity: EntityKind, hit: SearchHit): EntityItem {
  const source = hit._source;
  const fields = entityFields(entity);
  const id = String(source[fields.id as keyof typeof source] ?? "");
  const timestamp = String(source[fields.time as keyof typeof source] ?? "");
  if (!id || !timestamp)
    throw new Error(`Elasticsearch ${entity} document lacks id or time field`);
  return {
    tenantId: source.bk_tenant_id,
    id,
    timestamp,
    summary:
      entity === "events"
        ? {
            ...pick(source, [
              "action",
              "event_source_id",
              "severity",
              "related_alert_id",
              "title",
            ]),
            ...(source.processing ?? {}),
          }
        : entity === "alerts"
          ? pick(source, [
              "status",
              "event_source_id",
              "severity",
              "enrich_status",
              "title",
              "latest_event_id",
            ])
          : pick(source, ["alert_id", "operation_kind", "operator_kind"]),
    payload:
      entity === "events" && source.processing
        ? { ...withoutProcessing(source), _processing: source.processing }
        : { ...source },
  };
}

function groupAlertHits(
  hits: SearchHit[],
): Array<{ hit: SearchHit; cursorSort: Array<string | number> }> {
  const groups: Array<{
    hit: SearchHit;
    cursorSort: Array<string | number>;
    identity: string;
  }> = [];
  for (const hit of hits) {
    const identity = `${hit._source.bk_tenant_id}\u0000${String(hit._source.alert_id ?? "")}`;
    const previous = groups.at(-1);
    if (!previous || previous.identity !== identity) {
      groups.push({ hit, cursorSort: hit.sort, identity });
      continue;
    }
    previous.cursorSort = hit.sort;
    if (hit._index?.includes("-alert-history-")) previous.hit = hit;
  }
  return groups.map(({ hit, cursorSort }) => ({ hit, cursorSort }));
}

function withoutProcessing(
  source: SearchHit["_source"],
): Record<string, unknown> {
  const event: Record<string, unknown> = { ...source };
  delete event.processing;
  return event;
}

function pick(
  payload: Record<string, unknown>,
  keys: string[],
): Record<string, unknown> {
  return Object.fromEntries(
    keys
      .filter((key) => payload[key] !== undefined)
      .map((key) => [key, payload[key]]),
  );
}

function escapeTarget(target: string): string {
  return encodeURIComponent(target).replaceAll("%2A", "*");
}

function physicalIndices(response: ResolveResponse): string[] {
  const names = new Set<string>();
  for (const index of response.indices ?? []) names.add(index.name);
  for (const alias of response.aliases ?? [])
    for (const index of alias.indices ?? []) names.add(index);
  for (const stream of response.data_streams ?? [])
    for (const index of stream.backing_indices ?? []) names.add(index);
  return [...names].sort();
}

function resolvedAliases(response: ResolveResponse): string[] {
  const names = new Set<string>();
  for (const alias of response.aliases ?? []) names.add(alias.name);
  for (const index of response.indices ?? [])
    for (const alias of index.aliases ?? []) names.add(alias);
  return [...names].sort();
}

function addToSet<K, V>(map: Map<K, Set<V>>, key: K, value: V): void {
  const values = map.get(key) ?? new Set<V>();
  values.add(value);
  map.set(key, values);
}

function uniqueEntities(values: EntityKind[]): EntityKind[] {
  return [...new Set(values)].sort();
}

function nonNegativeNumber(value: string | undefined): number {
  const parsed = Number(value ?? 0);
  return Number.isFinite(parsed) && parsed >= 0 ? parsed : 0;
}

function chunkValues<T>(values: T[], size: number): T[][] {
  const chunks: T[][] = [];
  for (let index = 0; index < values.length; index += size)
    chunks.push(values.slice(index, index + size));
  return chunks;
}

function authHeaders(auth: {
  apiKey?: string;
  username?: string;
  password?: string;
}): Record<string, string> {
  if (auth.apiKey) return { authorization: `ApiKey ${auth.apiKey}` };
  if (auth.username)
    return {
      authorization: `Basic ${Buffer.from(`${auth.username}:${auth.password ?? ""}`).toString("base64")}`,
    };
  return {};
}
