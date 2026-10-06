import { createClient, type RedisClientType } from "redis";

import type {
  RedisGroup,
  RedisInfrastructure,
  RedisLeaseResponse,
  RedisMailboxResponse,
  RedisPendingResponse,
  RedisSectionStatus,
} from "../shared/contracts.js";
import type { DevtoolsConfig } from "./config.js";

const maxOverviewKeys = 5_000;
const maxDetailedKeys = 500;
const commandBatchSize = 50;

interface ScanResult {
  keys: string[];
  truncated: boolean;
}

interface CommandResult {
  ok: boolean;
  value?: unknown;
}

export class RedisConnector {
  private client?: RedisClientType;

  constructor(private readonly config: DevtoolsConfig) {}

  async close(): Promise<void> {
    if (this.client?.isOpen) await this.client.quit();
  }

  async inspect(): Promise<RedisInfrastructure> {
    const snapshotAt = new Date().toISOString();
    if (!this.config.redis) return this.unavailable(snapshotAt, "Redis 未配置");
    try {
      const client = await this.getClient();
      const ping = await client.ping();
      const [instance, signalQueue, mailbox, leases] = await Promise.all([
        this.inspectInstance(),
        this.inspectSignalQueue(),
        this.inspectMailboxSummary(),
        this.inspectLeaseSummary(),
      ]);
      const statuses = [
        instance.status,
        signalQueue.status,
        mailbox.status,
        leases.status,
      ];
      const status = statuses.every(
        (value) => value === "available" || value === "empty",
      )
        ? "available"
        : "partial";
      return {
        status,
        snapshotAt,
        connection: {
          status: "available",
          address: this.config.redis.address,
          database: this.config.redis.database,
          ping,
        },
        instance,
        signalQueue,
        mailbox,
        leases,
      };
    } catch {
      return this.unavailable(snapshotAt, "Redis 连接失败");
    }
  }

  async inspectPending(
    requestedGroup: string | undefined,
    limit: number,
  ): Promise<RedisPendingResponse> {
    const snapshotAt = new Date().toISOString();
    const lifecycle = this.config.lifecycle;
    if (!this.config.redis || !lifecycle) {
      return {
        status: "unavailable",
        message: "Redis 或 Lifecycle 未配置",
        snapshotAt,
        total: 0,
        smallestId: null,
        greatestId: null,
        items: [],
        truncated: false,
      };
    }
    try {
      const exists = numeric(
        await this.command(["EXISTS", lifecycle.signal.stream]),
      );
      const group = requestedGroup ?? lifecycle.signal.group;
      const claimMinIdleMilliseconds =
        lifecycle.signal.claimMinIdleSeconds * 1_000;
      if (exists === 0) {
        return {
          status: "empty",
          message: "Signal Stream 尚未创建",
          snapshotAt,
          group,
          total: 0,
          smallestId: null,
          greatestId: null,
          claimMinIdleMilliseconds,
          items: [],
          truncated: false,
        };
      }
      const groups = normalizeRows(
        await this.command(["XINFO", "GROUPS", lifecycle.signal.stream]),
      );
      if (!groups.some((row) => String(row.name ?? "") === group)) {
        return {
          status: "unavailable",
          message: "所选 Consumer Group 不存在",
          snapshotAt,
          group,
          total: 0,
          smallestId: null,
          greatestId: null,
          claimMinIdleMilliseconds,
          items: [],
          truncated: false,
        };
      }
      const [summaryValue, rowsValue] = await Promise.all([
        this.command(["XPENDING", lifecycle.signal.stream, group]),
        this.command([
          "XPENDING",
          lifecycle.signal.stream,
          group,
          "-",
          "+",
          String(limit),
        ]),
      ]);
      const summary = normalizePendingSummary(summaryValue);
      const items = normalizePendingEntries(rowsValue).map((item) => ({
        ...item,
        claimEligible: item.idleMilliseconds >= claimMinIdleMilliseconds,
      }));
      return {
        status: summary.total === 0 ? "empty" : "available",
        message:
          summary.total === 0 ? "当前没有已投递但未确认的 Signal" : undefined,
        snapshotAt,
        group,
        total: summary.total,
        smallestId: summary.smallestId,
        greatestId: summary.greatestId,
        claimMinIdleMilliseconds,
        items,
        truncated: summary.total > items.length,
      };
    } catch {
      return {
        status: "unavailable",
        message: "PEL 查询失败",
        snapshotAt,
        group: requestedGroup ?? lifecycle.signal.group,
        total: 0,
        smallestId: null,
        greatestId: null,
        claimMinIdleMilliseconds: lifecycle.signal.claimMinIdleSeconds * 1_000,
        items: [],
        truncated: false,
      };
    }
  }

  async inspectMailboxes(
    query: string | undefined,
    limit: number,
  ): Promise<RedisMailboxResponse> {
    const snapshotAt = new Date().toISOString();
    const lifecycle = this.config.lifecycle;
    if (!this.config.redis || !lifecycle) {
      return {
        status: "unavailable",
        message: "Redis 或 Lifecycle 未配置",
        snapshotAt,
        scanned: 0,
        scanTruncated: false,
        items: [],
      };
    }
    try {
      const scan = await this.scanKeys(
        `${lifecycle.mailbox.keyPrefix}:*:events`,
        maxDetailedKeys,
      );
      const rows = await mapInBatches(
        scan.keys,
        commandBatchSize,
        async (eventsKey) => {
          const mailboxId = mailboxIDFromEventsKey(
            eventsKey,
            lifecycle.mailbox.keyPrefix,
          );
          const [eventCount, headEventId] = await Promise.all([
            this.command(["LLEN", eventsKey]),
            this.command(["LINDEX", eventsKey, "0"]),
          ]);
          const eventTotal = numeric(eventCount);
          return {
            mailboxId,
            eventCount: eventTotal,
            headEventId:
              typeof headEventId === "string" && headEventId
                ? headEventId
                : null,
          };
        },
      );
      const normalizedQuery = query?.trim().toLocaleLowerCase() ?? "";
      const items = rows
        .filter(
          (row) =>
            !normalizedQuery ||
            row.mailboxId.toLocaleLowerCase().includes(normalizedQuery) ||
            row.headEventId?.toLocaleLowerCase().includes(normalizedQuery),
        )
        .sort(
          (left, right) =>
            right.eventCount - left.eventCount ||
            left.mailboxId.localeCompare(right.mailboxId),
        )
        .slice(0, limit);
      const partial = scan.truncated;
      return {
        status: partial
          ? "partial"
          : items.length === 0
            ? "empty"
            : "available",
        message: scan.truncated
          ? `仅检查前 ${maxDetailedKeys} 个 Mailbox key`
          : items.length === 0
            ? normalizedQuery
              ? "本次扫描没有匹配的 Mailbox"
              : "当前没有待处理 Mailbox"
            : undefined,
        snapshotAt,
        scanned: scan.keys.length,
        scanTruncated: scan.truncated,
        items,
      };
    } catch {
      return {
        status: "unavailable",
        message: "Mailbox 查询失败",
        snapshotAt,
        scanned: 0,
        scanTruncated: false,
        items: [],
      };
    }
  }

  async inspectLeases(
    query: string | undefined,
    limit: number,
  ): Promise<RedisLeaseResponse> {
    const snapshotAt = new Date().toISOString();
    const lifecycle = this.config.lifecycle;
    if (!this.config.redis || !lifecycle) {
      return {
        status: "unavailable",
        message: "Redis 或 Lifecycle 未配置",
        snapshotAt,
        scanned: 0,
        scanTruncated: false,
        items: [],
      };
    }
    try {
      const scan = await this.scanKeys(
        `${lifecycle.lock.keyPrefix}:*`,
        maxDetailedKeys,
      );
      const normalizedQuery = query?.trim().toLocaleLowerCase() ?? "";
      const matchingKeys = scan.keys.filter((key) =>
        leaseIDFromKey(key, lifecycle.lock.keyPrefix)
          .toLocaleLowerCase()
          .includes(normalizedQuery),
      );
      const rows = await mapInBatches(
        matchingKeys,
        commandBatchSize,
        async (key) => {
          const ttl = numeric(await this.command(["PTTL", key]));
          return {
            mailboxId: leaseIDFromKey(key, lifecycle.lock.keyPrefix),
            ttlMilliseconds: ttl >= 0 ? ttl : null,
            expiryState:
              ttl === -1
                ? ("no_expiry" as const)
                : ttl === -2
                  ? ("gone" as const)
                  : ("expiring" as const),
          };
        },
      );
      const items = rows
        .sort(
          (left, right) =>
            (right.ttlMilliseconds ?? -1) - (left.ttlMilliseconds ?? -1) ||
            left.mailboxId.localeCompare(right.mailboxId),
        )
        .slice(0, limit);
      const hasNoExpiry = rows.some((row) => row.expiryState === "no_expiry");
      return {
        status:
          scan.truncated || hasNoExpiry
            ? "partial"
            : items.length === 0
              ? "empty"
              : "available",
        message: scan.truncated
          ? `仅检查前 ${maxDetailedKeys} 个 lease key`
          : hasNoExpiry
            ? "存在没有过期时间的 lease"
            : items.length === 0
              ? "当前没有活跃 lease"
              : undefined,
        snapshotAt,
        scanned: scan.keys.length,
        scanTruncated: scan.truncated,
        items,
      };
    } catch {
      return {
        status: "unavailable",
        message: "Lease 查询失败",
        snapshotAt,
        scanned: 0,
        scanTruncated: false,
        items: [],
      };
    }
  }

  private unavailable(
    snapshotAt: string,
    message: string,
  ): RedisInfrastructure {
    return {
      status: "unavailable",
      snapshotAt,
      connection: {
        status: "unavailable",
        message,
        address: this.config.redis?.address,
        database: this.config.redis?.database,
      },
      instance: { status: "unavailable", message },
      signalQueue: {
        status: "unavailable",
        message: this.config.lifecycle ? message : "Lifecycle 未配置",
        groups: [],
      },
      mailbox: {
        status: "unavailable",
        message: this.config.lifecycle ? message : "Lifecycle 未配置",
        activeMailboxes: null,
        scanTruncated: false,
      },
      leases: {
        status: "unavailable",
        message: this.config.lifecycle ? message : "Lifecycle 未配置",
        activeLeases: null,
        scanTruncated: false,
      },
    };
  }

  private async inspectInstance(): Promise<RedisInfrastructure["instance"]> {
    const names = [
      "server",
      "clients",
      "memory",
      "persistence",
      "replication",
      "stats",
      "keyspace",
    ];
    const results = await Promise.all(
      names.map((name) => this.safeCommand(["INFO", name])),
    );
    const sections = Object.fromEntries(
      names.map((name, index) => [
        name,
        results[index].ok ? parseRedisInfo(results[index].value) : {},
      ]),
    ) as Record<string, Record<string, string>>;
    const succeeded = results.filter((result) => result.ok).length;
    const keyspace = parseKeyspace(
      sections.keyspace[`db${this.config.redis?.database ?? 0}`],
    );
    return {
      status: succeeded === results.length ? "available" : "partial",
      message:
        succeeded === results.length
          ? undefined
          : `INFO ${results.length - succeeded} 个分区不可用`,
      version: sections.server.redis_version,
      mode: sections.server.redis_mode,
      uptimeSeconds: optionalNumber(sections.server.uptime_in_seconds),
      connectedClients: optionalNumber(sections.clients.connected_clients),
      blockedClients: optionalNumber(sections.clients.blocked_clients),
      databaseKeys: keyspace.keys,
      expiringKeys: keyspace.expires,
      averageTtlMilliseconds: keyspace.averageTtlMilliseconds,
      usedMemoryBytes: optionalNumber(sections.memory.used_memory),
      usedMemoryRssBytes: optionalNumber(sections.memory.used_memory_rss),
      peakMemoryBytes: optionalNumber(sections.memory.used_memory_peak),
      maxMemoryBytes: optionalNumber(sections.memory.maxmemory),
      maxMemoryPolicy: sections.memory.maxmemory_policy,
      fragmentationRatio: optionalNumber(
        sections.memory.mem_fragmentation_ratio,
      ),
      loading: optionalBoolean(sections.persistence.loading),
      aofEnabled: optionalBoolean(sections.persistence.aof_enabled),
      rdbChangesSinceLastSave: optionalNumber(
        sections.persistence.rdb_changes_since_last_save,
      ),
      rdbLastSaveTime: optionalNumber(sections.persistence.rdb_last_save_time),
      rdbLastBgsaveStatus: sections.persistence.rdb_last_bgsave_status,
      aofLastWriteStatus: sections.persistence.aof_last_write_status,
      replicationRole: sections.replication.role,
      connectedReplicas: optionalNumber(
        sections.replication.connected_slaves ??
          sections.replication.connected_replicas,
      ),
      masterLinkStatus: sections.replication.master_link_status,
      operationsPerSecond: optionalNumber(
        sections.stats.instantaneous_ops_per_sec,
      ),
      evictedKeys: optionalNumber(sections.stats.evicted_keys),
      rejectedConnections: optionalNumber(sections.stats.rejected_connections),
      totalErrorReplies: optionalNumber(sections.stats.total_error_replies),
    };
  }

  private async inspectSignalQueue(): Promise<
    RedisInfrastructure["signalQueue"]
  > {
    const lifecycle = this.config.lifecycle;
    if (!lifecycle) {
      return {
        status: "unavailable",
        message: "Lifecycle 未配置",
        groups: [],
      };
    }
    try {
      const exists = numeric(
        await this.command(["EXISTS", lifecycle.signal.stream]),
      );
      if (exists === 0) {
        return {
          status: "empty",
          message: "Signal Stream 尚未创建",
          streamKey: lifecycle.signal.stream,
          expectedGroup: lifecycle.signal.group,
          claimMinIdleSeconds: lifecycle.signal.claimMinIdleSeconds,
          stream: {
            exists: false,
            length: 0,
            entriesAdded: 0,
            memoryBytes: null,
            firstEntryId: null,
            lastGeneratedId: null,
            oldestEntryAgeSeconds: null,
            groupsCount: 0,
            maxEntries: this.config.redisStreamManager?.maxEntries ?? null,
            entriesAboveMax: null,
          },
          groups: [],
        };
      }
      const [streamResult, groupsResult, memoryResult] = await Promise.all([
        this.safeCommand(["XINFO", "STREAM", lifecycle.signal.stream]),
        this.safeCommand(["XINFO", "GROUPS", lifecycle.signal.stream]),
        this.safeCommand(["MEMORY", "USAGE", lifecycle.signal.stream]),
      ]);
      const stream = normalizeMap(streamResult.value);
      const groupRows = groupsResult.ok
        ? normalizeRows(groupsResult.value)
        : [];
      let consumerFailure = false;
      const groups = await mapInBatches(
        groupRows,
        commandBatchSize,
        async (row): Promise<RedisGroup> => {
          const name = String(row.name ?? "");
          const consumers = await this.safeCommand([
            "XINFO",
            "CONSUMERS",
            lifecycle.signal.stream,
            name,
          ]);
          if (!consumers.ok) consumerFailure = true;
          const consumerRows = consumers.ok
            ? normalizeRows(consumers.value)
            : [];
          return {
            name,
            expected: name === lifecycle.signal.group,
            consumersCount: nonnegativeNumber(row.consumers),
            pending: nonnegativeNumber(row.pending),
            lastDeliveredId: String(row["last-delivered-id"] ?? ""),
            entriesRead: nullableNonnegativeNumber(row["entries-read"]),
            lag: nullableNonnegativeNumber(row.lag),
            consumersStatus: consumers.ok
              ? consumerRows.length === 0
                ? "empty"
                : "available"
              : "unavailable",
            consumers: consumerRows.map((consumer) => ({
              name: String(consumer.name ?? ""),
              pending: nonnegativeNumber(consumer.pending),
              idleMilliseconds: nullableNonnegativeNumber(consumer.idle),
              inactiveMilliseconds: nullableNonnegativeNumber(
                consumer.inactive,
              ),
            })),
          };
        },
      );
      const expectedGroupPresent = groups.some((group) => group.expected);
      const maxEntries = this.config.redisStreamManager?.maxEntries ?? null;
      const counters = normalizeStreamCounters(stream, maxEntries);
      const firstEntryId = streamEntryID(stream["first-entry"]);
      const status: RedisSectionStatus =
        !streamResult.ok ||
        !groupsResult.ok ||
        consumerFailure ||
        !expectedGroupPresent
          ? "partial"
          : "available";
      return {
        status,
        message:
          groupsResult.ok && !expectedGroupPresent
            ? "配置的 Lifecycle Consumer Group 不存在"
            : status === "partial"
              ? "部分 Stream 状态不可用"
              : undefined,
        streamKey: lifecycle.signal.stream,
        expectedGroup: lifecycle.signal.group,
        claimMinIdleSeconds: lifecycle.signal.claimMinIdleSeconds,
        stream: {
          exists: true,
          length: counters.length,
          entriesAdded: counters.entriesAdded,
          memoryBytes: memoryResult.ok
            ? nullableNonnegativeNumber(memoryResult.value)
            : null,
          firstEntryId,
          lastGeneratedId:
            typeof stream["last-generated-id"] === "string"
              ? stream["last-generated-id"]
              : null,
          oldestEntryAgeSeconds: streamIDAgeSeconds(firstEntryId),
          groupsCount: counters.groupsCount,
          maxEntries,
          entriesAboveMax: counters.entriesAboveMax,
        },
        groups,
      };
    } catch {
      return {
        status: "unavailable",
        message: "Signal Stream 查询失败",
        streamKey: lifecycle.signal.stream,
        expectedGroup: lifecycle.signal.group,
        claimMinIdleSeconds: lifecycle.signal.claimMinIdleSeconds,
        groups: [],
      };
    }
  }

  private async inspectMailboxSummary(): Promise<
    RedisInfrastructure["mailbox"]
  > {
    const lifecycle = this.config.lifecycle;
    if (!lifecycle) {
      return {
        status: "unavailable",
        message: "Lifecycle 未配置",
        activeMailboxes: null,
        scanTruncated: false,
      };
    }
    const scanResult = await this.scanKeys(
      `${lifecycle.mailbox.keyPrefix}:*:events`,
      maxOverviewKeys,
    )
      .then((value) => ({ ok: true, value }) as const)
      .catch(() => ({ ok: false }) as const);
    const scan = scanResult.ok ? scanResult.value : undefined;
    const partial = !scan || scan.truncated;
    const empty = !partial && scan.keys.length === 0;
    return {
      status: partial ? "partial" : empty ? "empty" : "available",
      message: !scan
        ? "Mailbox key 扫描不可用"
        : scan.truncated
          ? `Mailbox 数量至少为 ${scan.keys.length}`
          : empty
            ? "当前没有待处理 Mailbox"
            : undefined,
      activeMailboxes: scan?.keys.length ?? null,
      scanTruncated: scan?.truncated ?? false,
      maxPendingPerMailbox: lifecycle.mailbox.maxPending,
      maxDrainEvents: lifecycle.mailbox.maxDrainEvents,
    };
  }

  private async inspectLeaseSummary(): Promise<RedisInfrastructure["leases"]> {
    const lifecycle = this.config.lifecycle;
    if (!lifecycle) {
      return {
        status: "unavailable",
        message: "Lifecycle 未配置",
        activeLeases: null,
        scanTruncated: false,
      };
    }
    try {
      const scan = await this.scanKeys(
        `${lifecycle.lock.keyPrefix}:*`,
        maxOverviewKeys,
      );
      return {
        status: scan.truncated
          ? "partial"
          : scan.keys.length === 0
            ? "empty"
            : "available",
        message: scan.truncated
          ? `Lease 数量至少为 ${scan.keys.length}`
          : scan.keys.length === 0
            ? "当前没有活跃 lease"
            : undefined,
        activeLeases: scan.keys.length,
        scanTruncated: scan.truncated,
        ttlSeconds: lifecycle.lock.ttlSeconds,
        renewIntervalSeconds: lifecycle.lock.renewIntervalSeconds,
      };
    } catch {
      return {
        status: "unavailable",
        message: "Lease key 扫描不可用",
        activeLeases: null,
        scanTruncated: false,
        ttlSeconds: lifecycle.lock.ttlSeconds,
        renewIntervalSeconds: lifecycle.lock.renewIntervalSeconds,
      };
    }
  }

  private async getClient(): Promise<RedisClientType> {
    if (this.client?.isReady) return this.client;
    const config = this.config.redis!;
    const url = new URL(`redis://${config.address}/${config.database}`);
    if (config.username) url.username = config.username;
    if (config.password) url.password = config.password;
    this.client = createClient({
      url: url.toString(),
      socket: {
        connectTimeout: this.config.query.timeoutMilliseconds,
        reconnectStrategy: false,
      },
    });
    this.client.on("error", () => undefined);
    await this.client.connect();
    return this.client;
  }

  private async command(values: string[]): Promise<unknown> {
    return this.getClient().then((client) => client.sendCommand(values));
  }

  private async safeCommand(values: string[]): Promise<CommandResult> {
    try {
      return { ok: true, value: await this.command(values) };
    } catch {
      return { ok: false };
    }
  }

  private async scanKeys(
    pattern: string,
    maximum: number,
  ): Promise<ScanResult> {
    let cursor = "0";
    const keys = new Set<string>();
    let truncated = false;
    do {
      const response = await this.command([
        "SCAN",
        cursor,
        "MATCH",
        pattern,
        "COUNT",
        "200",
      ]);
      if (!Array.isArray(response) || response.length < 2)
        throw new Error("invalid SCAN response");
      cursor = String(response[0]);
      const page = Array.isArray(response[1]) ? response[1] : [];
      for (const key of page) {
        if (!keys.has(String(key)) && keys.size >= maximum) {
          truncated = true;
          break;
        }
        keys.add(String(key));
      }
      if (keys.size >= maximum && cursor !== "0") truncated = true;
      if (truncated) break;
    } while (cursor !== "0");
    return { keys: [...keys], truncated };
  }
}

export function normalizeMap(value: unknown): Record<string, unknown> {
  if (value && typeof value === "object" && !Array.isArray(value)) {
    return { ...(value as Record<string, unknown>) };
  }
  if (!Array.isArray(value)) return {};
  const result: Record<string, unknown> = {};
  for (let index = 0; index + 1 < value.length; index += 2) {
    result[String(value[index])] = value[index + 1];
  }
  return result;
}

export function parseRedisInfo(value: unknown): Record<string, string> {
  if (typeof value !== "string") return {};
  const result: Record<string, string> = {};
  for (const rawLine of value.split(/\r?\n/)) {
    if (!rawLine || rawLine.startsWith("#")) continue;
    const separator = rawLine.indexOf(":");
    if (separator <= 0) continue;
    result[rawLine.slice(0, separator)] = rawLine.slice(separator + 1);
  }
  return result;
}

export function normalizeStreamCounters(
  value: unknown,
  maxEntries: number | null,
): {
  length: number | null;
  entriesAdded: number | null;
  groupsCount: number | null;
  entriesAboveMax: number | null;
} {
  const stream = normalizeMap(value);
  const length = nullableNonnegativeNumber(stream.length);
  return {
    length,
    entriesAdded: nullableNonnegativeNumber(stream["entries-added"]),
    groupsCount: nullableNonnegativeNumber(stream.groups),
    entriesAboveMax:
      maxEntries === null || length === null
        ? null
        : Math.max(0, length - maxEntries),
  };
}

export function normalizePendingEntries(value: unknown): Array<{
  id: string;
  consumer: string;
  idleMilliseconds: number;
  deliveryCount: number;
}> {
  if (!Array.isArray(value)) return [];
  return value.flatMap((row) => {
    if (!Array.isArray(row) || row.length < 4) return [];
    return [
      {
        id: String(row[0]),
        consumer: String(row[1]),
        idleMilliseconds: nonnegativeNumber(row[2]),
        deliveryCount: Math.max(1, nonnegativeNumber(row[3])),
      },
    ];
  });
}

function normalizeRows(value: unknown): Array<Record<string, unknown>> {
  if (!Array.isArray(value)) return [];
  return value.map((row) => normalizeMap(row));
}

function normalizePendingSummary(value: unknown): {
  total: number;
  smallestId: string | null;
  greatestId: string | null;
} {
  if (!Array.isArray(value))
    return { total: 0, smallestId: null, greatestId: null };
  return {
    total: nonnegativeNumber(value[0]),
    smallestId: typeof value[1] === "string" ? value[1] : null,
    greatestId: typeof value[2] === "string" ? value[2] : null,
  };
}

function parseKeyspace(value: string | undefined): {
  keys?: number;
  expires?: number;
  averageTtlMilliseconds?: number;
} {
  if (!value) return {};
  const fields = Object.fromEntries(
    value.split(",").map((field) => {
      const [key, raw] = field.split("=", 2);
      return [key, raw];
    }),
  );
  return {
    keys: optionalNumber(fields.keys),
    expires: optionalNumber(fields.expires),
    averageTtlMilliseconds: optionalNumber(fields.avg_ttl),
  };
}

function streamEntryID(value: unknown): string | null {
  if (!Array.isArray(value) || value.length === 0) return null;
  return typeof value[0] === "string" ? value[0] : null;
}

function streamIDAgeSeconds(id: string | null): number | null {
  if (!id) return null;
  const milliseconds = Number(id.split("-", 1)[0]);
  if (!Number.isFinite(milliseconds) || milliseconds < 0) return null;
  return Math.max(0, (Date.now() - milliseconds) / 1_000);
}

function mailboxIDFromEventsKey(key: string, prefix: string): string {
  const start = `${prefix}:`;
  const suffix = ":events";
  if (!key.startsWith(start) || !key.endsWith(suffix)) return key;
  return key.slice(start.length, -suffix.length);
}

function leaseIDFromKey(key: string, prefix: string): string {
  const start = `${prefix}:`;
  return key.startsWith(start) ? key.slice(start.length) : key;
}

function numeric(value: unknown): number {
  const parsed = Number(value);
  if (!Number.isFinite(parsed)) throw new Error("Redis numeric value invalid");
  return parsed;
}

function optionalNumber(value: unknown): number | undefined {
  if (value === undefined || value === null || value === "") return undefined;
  const parsed = Number(value);
  return Number.isFinite(parsed) && parsed >= 0 ? parsed : undefined;
}

function nonnegativeNumber(value: unknown): number {
  return optionalNumber(value) ?? 0;
}

function nullableNonnegativeNumber(value: unknown): number | null {
  if (value === null || value === undefined) return null;
  const parsed = Number(value);
  return Number.isFinite(parsed) && parsed >= 0 ? parsed : null;
}

function optionalBoolean(value: unknown): boolean | undefined {
  if (value === undefined) return undefined;
  if (value === "1") return true;
  if (value === "0") return false;
  return undefined;
}

async function mapInBatches<Input, Output>(
  inputs: Input[],
  batchSize: number,
  mapper: (input: Input) => Promise<Output>,
): Promise<Output[]> {
  const outputs: Output[] = [];
  for (let start = 0; start < inputs.length; start += batchSize) {
    outputs.push(
      ...(await Promise.all(
        inputs.slice(start, start + batchSize).map(mapper),
      )),
    );
  }
  return outputs;
}
