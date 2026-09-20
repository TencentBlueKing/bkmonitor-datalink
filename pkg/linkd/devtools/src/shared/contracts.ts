import { z } from "zod";

export const entityKindSchema = z.enum(["events", "alerts", "alert-logs"]);
export type EntityKind = z.infer<typeof entityKindSchema>;

export const sourceKindSchema = z.enum(["mysql", "elasticsearch"]);
export type SourceKind = z.infer<typeof sourceKindSchema>;

export const capabilitySchema = z.object({
  version: z.string(),
  metrics: z.object({
    configured: z.boolean(),
    source: z.literal("prometheus"),
  }),
  entities: z.record(
    entityKindSchema,
    z.object({
      source: sourceKindSchema,
      filters: z.array(z.string()),
    }),
  ),
  storage: z.object({
    elasticsearch: z.object({ configured: z.boolean() }),
  }),
  infrastructure: z
    .object({
      kafka: z.object({ configured: z.boolean() }),
      redis: z.object({ configured: z.boolean() }),
    })
    .optional(),
  limits: z.object({
    defaultRangeSeconds: z.number(),
    maxRangeSeconds: z.number(),
    defaultLimit: z.number(),
    maxLimit: z.number(),
  }),
});
export type Capabilities = z.infer<typeof capabilitySchema>;

export const entityItemSchema = z.object({
  tenantId: z.string(),
  id: z.string(),
  timestamp: z.string(),
  summary: z.record(z.string(), z.unknown()),
  payload: z.record(z.string(), z.unknown()),
});
export type EntityItem = z.infer<typeof entityItemSchema>;

export const entityPageSchema = z.object({
  items: z.array(entityItemSchema),
  nextCursor: z.string().optional(),
  source: sourceKindSchema,
  warnings: z.array(z.string()).default([]),
});
export type EntityPage = z.infer<typeof entityPageSchema>;

export const metricPointSchema = z.tuple([z.number(), z.number().nullable()]);
export const metricSeriesSchema = z.object({
  name: z.string(),
  labels: z.record(z.string(), z.string()),
  points: z.array(metricPointSchema),
});
export type MetricSeries = z.infer<typeof metricSeriesSchema>;

export const metricPanelSchema = z.object({
  id: z.string(),
  title: z.string(),
  unit: z.string(),
  kind: z.enum(["line", "area", "stat"]),
  status: z.enum(["available", "unavailable"]),
  message: z.string().optional(),
  series: z.array(metricSeriesSchema),
});
export type MetricPanel = z.infer<typeof metricPanelSchema>;

export const metricsResponseSchema = z.object({
  from: z.string(),
  to: z.string(),
  step: z.number(),
  panels: z.array(metricPanelSchema),
});
export type MetricsResponse = z.infer<typeof metricsResponseSchema>;

export const availabilitySchema = z.enum([
  "available",
  "partial",
  "unavailable",
]);

export const runtimeResponseSchema = z
  .object({ status: availabilitySchema })
  .passthrough();
export type RuntimeResponse = z.infer<typeof runtimeResponseSchema>;

export const configResponseSchema = z.object({
  configPath: z.string().optional(),
  repository: sourceKindSchema,
  telemetry: z.record(z.string(), z.unknown()).optional(),
  storage: z.record(z.string(), z.unknown()),
  eventSources: z.array(z.record(z.string(), z.unknown())),
  lifecycle: z.record(z.string(), z.unknown()).optional(),
  controlPlane: z.record(z.string(), z.unknown()).optional(),
});
export type ConfigResponse = z.infer<typeof configResponseSchema>;

export const entityStatsSchema = z.object({
  entity: entityKindSchema,
  source: sourceKindSchema,
  total: z.number(),
  timeline: z.array(z.object({ timestamp: z.string(), count: z.number() })),
  facets: z.array(
    z.object({
      name: z.string(),
      values: z.array(z.object({ value: z.string(), count: z.number() })),
    }),
  ),
  warnings: z.array(z.string()).default([]),
});
export type EntityStats = z.infer<typeof entityStatsSchema>;

export const elasticsearchTargetGroupSchema = z.object({
  entity: entityKindSchema,
  configuredTargets: z.array(z.string()),
  indices: z.array(z.string()),
  aliases: z.array(z.string()),
});

export const elasticsearchIndexSchema = z.object({
  name: z.string(),
  health: z.string(),
  status: z.string(),
  primaryShards: z.number(),
  replicaShards: z.number(),
  docsCount: z.number(),
  storeBytes: z.number(),
  aliases: z.array(z.string()),
  entities: z.array(entityKindSchema),
  mappingFields: z.array(z.string()).default([]),
});

export const elasticsearchAliasSchema = z.object({
  name: z.string(),
  indices: z.array(z.string()),
  entities: z.array(entityKindSchema),
  writeIndex: z.string().optional(),
});

export const elasticsearchTopologySchema = z.object({
  cluster: z.object({
    name: z.string(),
    version: z.string(),
    status: z.string(),
    numberOfNodes: z.number(),
    activeShards: z.number(),
    unassignedShards: z.number(),
  }),
  targets: z.array(elasticsearchTargetGroupSchema),
  indices: z.array(elasticsearchIndexSchema),
  aliases: z.array(elasticsearchAliasSchema),
  templates: z
    .array(
      z.object({
        name: z.string(),
        indexPatterns: z.array(z.string()),
        schema: z.string().optional(),
      }),
    )
    .default([]),
});
export type ElasticsearchTopology = z.infer<typeof elasticsearchTopologySchema>;

export const kafkaIssueSchema = z.object({
  code: z.enum([
    "leader_missing",
    "isr_incomplete",
    "group_empty",
    "group_dead",
    "group_rebalancing",
    "group_unknown",
    "owner_missing",
    "committed_missing",
  ]),
  message: z.string(),
  partition: z.number().int().nonnegative().optional(),
});
export type KafkaIssue = z.infer<typeof kafkaIssueSchema>;

export const kafkaPartitionSchema = z.object({
  partition: z.number().int().nonnegative(),
  leader: z.number().int().nullable(),
  replicas: z.array(z.number().int()),
  isr: z.array(z.number().int()),
  lowOffset: z.string().optional(),
  highOffset: z.string().optional(),
  committedOffset: z.string().optional(),
  lag: z.string().optional(),
  members: z.array(z.string()).optional(),
  status: availabilitySchema,
  issues: z.array(kafkaIssueSchema),
});
export type KafkaPartition = z.infer<typeof kafkaPartitionSchema>;

export const kafkaGroupSchema = z.object({
  state: z.string(),
  protocol: z.string(),
  members: z.array(
    z.object({
      memberId: z.string(),
      clientId: z.string(),
      clientHost: z.string(),
      partitions: z.array(z.number().int().nonnegative()),
    }),
  ),
});
export type KafkaGroup = z.infer<typeof kafkaGroupSchema>;

export const kafkaResourceSchema = z.object({
  kind: z.enum(["input", "output"]),
  eventSourceId: z.string().optional(),
  status: availabilitySchema,
  message: z.string().optional(),
  brokers: z.array(z.string()),
  topic: z.string(),
  clientId: z.string().optional(),
  consumerGroup: z.string().optional(),
  cluster: z
    .object({
      id: z.string(),
      controller: z.number().int().nullable(),
      brokers: z.array(
        z.object({
          nodeId: z.number().int(),
          host: z.string(),
          port: z.number().int(),
        }),
      ),
    })
    .optional(),
  group: kafkaGroupSchema.optional(),
  partitions: z.array(kafkaPartitionSchema),
  issues: z.array(kafkaIssueSchema),
});
export type KafkaResource = z.infer<typeof kafkaResourceSchema>;

export const kafkaInfrastructureSchema = z.object({
  status: availabilitySchema,
  resources: z.array(kafkaResourceSchema),
});
export type KafkaInfrastructure = z.infer<typeof kafkaInfrastructureSchema>;

export const redisSectionStatusSchema = z.enum([
  "available",
  "partial",
  "unavailable",
  "empty",
]);
export type RedisSectionStatus = z.infer<typeof redisSectionStatusSchema>;

const redisSectionBaseSchema = z.object({
  status: redisSectionStatusSchema,
  message: z.string().optional(),
});

export const redisConsumerSchema = z.object({
  name: z.string(),
  pending: z.number().int().nonnegative(),
  idleMilliseconds: z.number().nonnegative().nullable(),
  inactiveMilliseconds: z.number().nonnegative().nullable(),
});
export type RedisConsumer = z.infer<typeof redisConsumerSchema>;

export const redisGroupSchema = z.object({
  name: z.string(),
  expected: z.boolean(),
  consumersCount: z.number().int().nonnegative(),
  pending: z.number().int().nonnegative(),
  lastDeliveredId: z.string(),
  entriesRead: z.number().int().nonnegative().nullable(),
  lag: z.number().int().nonnegative().nullable(),
  consumersStatus: redisSectionStatusSchema,
  consumers: z.array(redisConsumerSchema),
});
export type RedisGroup = z.infer<typeof redisGroupSchema>;

export const redisInfrastructureSchema = z.object({
  status: availabilitySchema,
  snapshotAt: z.string().datetime(),
  connection: redisSectionBaseSchema.extend({
    address: z.string().optional(),
    database: z.number().int().nonnegative().optional(),
    ping: z.string().optional(),
  }),
  instance: redisSectionBaseSchema.extend({
    version: z.string().optional(),
    mode: z.string().optional(),
    uptimeSeconds: z.number().nonnegative().optional(),
    connectedClients: z.number().int().nonnegative().optional(),
    blockedClients: z.number().int().nonnegative().optional(),
    databaseKeys: z.number().int().nonnegative().optional(),
    expiringKeys: z.number().int().nonnegative().optional(),
    averageTtlMilliseconds: z.number().nonnegative().optional(),
    usedMemoryBytes: z.number().nonnegative().optional(),
    usedMemoryRssBytes: z.number().nonnegative().optional(),
    peakMemoryBytes: z.number().nonnegative().optional(),
    maxMemoryBytes: z.number().nonnegative().optional(),
    maxMemoryPolicy: z.string().optional(),
    fragmentationRatio: z.number().nonnegative().optional(),
    loading: z.boolean().optional(),
    aofEnabled: z.boolean().optional(),
    rdbChangesSinceLastSave: z.number().int().nonnegative().optional(),
    rdbLastSaveTime: z.number().nonnegative().optional(),
    rdbLastBgsaveStatus: z.string().optional(),
    aofLastWriteStatus: z.string().optional(),
    replicationRole: z.string().optional(),
    connectedReplicas: z.number().int().nonnegative().optional(),
    masterLinkStatus: z.string().optional(),
    operationsPerSecond: z.number().nonnegative().optional(),
    evictedKeys: z.number().int().nonnegative().optional(),
    rejectedConnections: z.number().int().nonnegative().optional(),
    totalErrorReplies: z.number().int().nonnegative().optional(),
  }),
  signalQueue: redisSectionBaseSchema.extend({
    streamKey: z.string().optional(),
    expectedGroup: z.string().optional(),
    claimMinIdleSeconds: z.number().positive().optional(),
    stream: z
      .object({
        exists: z.boolean(),
        length: z.number().int().nonnegative().nullable(),
        entriesAdded: z.number().int().nonnegative().nullable(),
        memoryBytes: z.number().nonnegative().nullable(),
        firstEntryId: z.string().nullable(),
        lastGeneratedId: z.string().nullable(),
        oldestEntryAgeSeconds: z.number().nonnegative().nullable(),
        groupsCount: z.number().int().nonnegative().nullable(),
        maxEntries: z.number().int().positive().nullable(),
        entriesAboveMax: z.number().int().nonnegative().nullable(),
      })
      .optional(),
    groups: z.array(redisGroupSchema),
  }),
  mailbox: redisSectionBaseSchema.extend({
    activeMailboxes: z.number().int().nonnegative().nullable(),
    scanTruncated: z.boolean(),
    maxPendingPerMailbox: z.number().int().positive().optional(),
    maxDrainEvents: z.number().int().positive().optional(),
  }),
  leases: redisSectionBaseSchema.extend({
    activeLeases: z.number().int().nonnegative().nullable(),
    scanTruncated: z.boolean(),
    ttlSeconds: z.number().positive().optional(),
    renewIntervalSeconds: z.number().positive().optional(),
  }),
});
export type RedisInfrastructure = z.infer<typeof redisInfrastructureSchema>;

export const redisPendingResponseSchema = redisSectionBaseSchema.extend({
  snapshotAt: z.string().datetime(),
  group: z.string().optional(),
  total: z.number().int().nonnegative(),
  smallestId: z.string().nullable(),
  greatestId: z.string().nullable(),
  claimMinIdleMilliseconds: z.number().positive().optional(),
  items: z.array(
    z.object({
      id: z.string(),
      consumer: z.string(),
      idleMilliseconds: z.number().nonnegative(),
      deliveryCount: z.number().int().positive(),
      claimEligible: z.boolean(),
    }),
  ),
  truncated: z.boolean(),
});
export type RedisPendingResponse = z.infer<typeof redisPendingResponseSchema>;

export const redisMailboxResponseSchema = redisSectionBaseSchema.extend({
  snapshotAt: z.string().datetime(),
  scanned: z.number().int().nonnegative(),
  scanTruncated: z.boolean(),
  items: z.array(
    z.object({
      mailboxId: z.string(),
      eventCount: z.number().int().nonnegative(),
      headEventId: z.string().nullable(),
    }),
  ),
});
export type RedisMailboxResponse = z.infer<typeof redisMailboxResponseSchema>;

export const redisLeaseResponseSchema = redisSectionBaseSchema.extend({
  snapshotAt: z.string().datetime(),
  scanned: z.number().int().nonnegative(),
  scanTruncated: z.boolean(),
  items: z.array(
    z.object({
      mailboxId: z.string(),
      ttlMilliseconds: z.number().nullable(),
      expiryState: z.enum(["expiring", "no_expiry", "gone"]),
    }),
  ),
});
export type RedisLeaseResponse = z.infer<typeof redisLeaseResponseSchema>;

export const controlPlaneTaskIdSchema = z.enum([
  "elasticsearch-schema-and-active-reconciler",
  "elasticsearch-bucket-manager",
  "elasticsearch-alert-archiver",
  "redis-stream-manager",
]);
export type ControlPlaneTaskId = z.infer<typeof controlPlaneTaskIdSchema>;

export const controlPlaneTaskDefinitionSchema = z.object({
  id: controlPlaneTaskIdSchema,
  enabled: z.boolean(),
  dependsOn: z.array(controlPlaneTaskIdSchema),
  intervalSeconds: z.number().int().positive(),
  configSource: z.enum(["explicit", "default", "disabled"]),
  settings: z.record(
    z.string(),
    z.union([z.string(), z.number(), z.boolean()]),
  ),
});
export type ControlPlaneTaskDefinition = z.infer<
  typeof controlPlaneTaskDefinitionSchema
>;

const runtimeSeriesValueSchema = z.object({
  labels: z.record(z.string(), z.string()),
  value: z.number().nullable(),
  timestamp: z.number(),
});

export const controlPlaneRuntimeSchema = z.object({
  status: availabilitySchema,
  snapshotAt: z.string().datetime(),
  tasks: z.array(controlPlaneTaskDefinitionSchema),
  processes: z.object({
    status: availabilitySchema,
    message: z.string().optional(),
    items: z.array(
      z.object({
        instance: z.string(),
        job: z.string(),
        serviceInstanceId: z.string(),
        role: z.string(),
        version: z.string(),
        up: z.boolean(),
      }),
    ),
  }),
  metrics: z.object({
    status: availabilitySchema,
    message: z.string().optional(),
    series: z.record(z.string(), z.array(runtimeSeriesValueSchema)),
  }),
  archive: z.object({
    status: availabilitySchema,
    message: z.string().optional(),
    backlog: z.number().int().nonnegative().nullable(),
  }),
  redis: z.object({
    status: availabilitySchema,
    message: z.string().optional(),
    streamExists: z.boolean().nullable(),
    expectedGroupPresent: z.boolean().nullable(),
    entries: z.number().int().nonnegative().nullable(),
    maxEntries: z.number().int().positive().nullable(),
    entriesAboveMax: z.number().int().nonnegative().nullable(),
    pending: z.number().int().nonnegative().nullable(),
    maxLag: z.number().int().nullable(),
  }),
});
export type ControlPlaneRuntime = z.infer<typeof controlPlaneRuntimeSchema>;

export const errorResponseSchema = z.object({
  error: z.object({
    code: z.string(),
    message: z.string(),
    requestId: z.string(),
  }),
});

export interface SearchParams {
  tenantId?: string;
  id?: string;
  from?: string;
  to?: string;
  state?: string;
  status?: string;
  eventSourceId?: string;
  relatedAlertId?: string;
  fingerprint?: string;
  severity?: string;
  alertId?: string;
  operationKind?: string;
  operatorKind?: string;
  limit: number;
  cursor?: string;
}
