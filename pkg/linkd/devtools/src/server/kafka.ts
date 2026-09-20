import { readFile } from "node:fs/promises";

import {
  AssignerProtocol,
  Kafka,
  logLevel,
  type Admin,
  type KafkaConfig,
} from "kafkajs";

import type {
  KafkaGroup,
  KafkaInfrastructure,
  KafkaIssue,
  KafkaPartition,
  KafkaResource,
} from "../shared/contracts.js";
import type {
  DevtoolsConfig,
  EventSourceConfig,
  KafkaConnection,
} from "./config.js";

// KafkaConnector 只创建 Admin client，并且只调用 metadata/group/offset 查询接口。
export class KafkaConnector {
  constructor(private readonly config: DevtoolsConfig) {}

  async inspect(): Promise<KafkaInfrastructure> {
    const resources = await Promise.all([
      ...(this.config.eventSources ?? []).map((source) =>
        this.inspectInput(source),
      ),
      ...(this.config.lifecycle
        ? [this.inspectOutput(this.config.lifecycle.outputKafka)]
        : []),
    ]);
    return {
      status: kafkaInspectionStatus(resources),
      resources,
    };
  }

  private async inspectInput(
    source: EventSourceConfig,
  ): Promise<KafkaResource> {
    return this.inspectResource("input", source.kafka, source.eventSourceId);
  }

  private async inspectOutput(
    connection: KafkaConnection,
  ): Promise<KafkaResource> {
    return this.inspectResource("output", connection);
  }

  private async inspectResource(
    kind: "input" | "output",
    connection: KafkaConnection,
    eventSourceId?: string,
  ): Promise<KafkaResource> {
    const base: KafkaResource = {
      kind,
      eventSourceId,
      status: "unavailable",
      brokers: connection.brokers,
      topic: connection.topic,
      clientId: kind === "output" ? connection.clientId : undefined,
      consumerGroup: kind === "input" ? connection.consumerGroup : undefined,
      partitions: [],
      issues: [],
    };
    let admin: Admin | undefined;
    try {
      const kafka = new Kafka(
        await kafkaClientConfig(
          connection,
          this.config.query.timeoutMilliseconds,
        ),
      );
      admin = kafka.admin();
      await admin.connect();
      const [cluster, metadata, offsets] = await Promise.all([
        admin.describeCluster(),
        admin.fetchTopicMetadata({ topics: [connection.topic] }),
        admin.fetchTopicOffsets(connection.topic),
      ]);
      const topic = metadata.topics.find(
        (item) => item.name === connection.topic,
      );
      if (!topic)
        throw new Error(
          `Kafka topic ${connection.topic} was not returned by metadata`,
        );
      const partitions: KafkaPartition[] = topic.partitions
        .map((partition) => {
          const offset = offsets.find(
            (item) => item.partition === partition.partitionId,
          );
          return {
            partition: partition.partitionId,
            leader: partition.leader >= 0 ? partition.leader : null,
            replicas: partition.replicas,
            isr: partition.isr,
            lowOffset: knownOffset(offset?.low),
            highOffset: knownOffset(offset?.high ?? offset?.offset),
            status: "available" as const,
            issues: [],
          };
        })
        .sort((left, right) => left.partition - right.partition);
      let group: KafkaGroup | undefined;
      if (kind === "input" && connection.consumerGroup) {
        const [descriptions, committed] = await Promise.all([
          admin.describeGroups([connection.consumerGroup]),
          admin.fetchOffsets({
            groupId: connection.consumerGroup,
            topics: [connection.topic],
            resolveOffsets: false,
          }),
        ]);
        const description = descriptions.groups.find(
          (item) => item.groupId === connection.consumerGroup,
        );
        const members = (description?.members ?? []).map((member) => {
          const assignment = AssignerProtocol.MemberAssignment.decode(
            member.memberAssignment,
          );
          const assigned = assignment?.assignment[connection.topic] ?? [];
          return {
            memberId: member.memberId,
            clientId: member.clientId,
            clientHost: member.clientHost,
            partitions: assigned,
          };
        });
        group = {
          state: description?.state ?? "unknown",
          protocol: description?.protocol ?? "unknown",
          members,
        };
        const topicOffsets =
          committed.find((item) => item.topic === connection.topic)
            ?.partitions ?? [];
        for (const partition of partitions) {
          const committedOffset = topicOffsets.find(
            (item) => item.partition === partition.partition,
          )?.offset;
          partition.committedOffset = knownOffset(committedOffset);
          partition.lag = offsetLag(partition.highOffset, committedOffset);
          partition.members = members
            .filter((member) => member.partitions.includes(partition.partition))
            .map((member) => member.memberId);
        }
      }
      const analysis = analyzeKafkaResource(kind, group, partitions);
      return {
        ...base,
        status: analysis.status,
        cluster: {
          id: cluster.clusterId,
          controller: cluster.controller,
          brokers: cluster.brokers,
        },
        group,
        partitions: analysis.partitions,
        issues: analysis.issues,
      };
    } catch (error) {
      return { ...base, message: safeMessage(error) };
    } finally {
      await admin?.disconnect().catch(() => undefined);
    }
  }
}

export function kafkaInspectionStatus(
  resources: Array<Pick<KafkaResource, "status">>,
): KafkaInfrastructure["status"] {
  if (resources.length === 0) return "unavailable";
  if (resources.every((resource) => resource.status === "available"))
    return "available";
  if (resources.every((resource) => resource.status === "unavailable"))
    return "unavailable";
  return "partial";
}

export function analyzeKafkaResource(
  kind: KafkaResource["kind"],
  group: KafkaGroup | undefined,
  partitions: KafkaPartition[],
): Pick<KafkaResource, "status" | "issues" | "partitions"> {
  const groupIssues: KafkaIssue[] = [];
  if (kind === "input") {
    if (!group || group.state.toLowerCase() === "unknown") {
      groupIssues.push({
        code: "group_unknown",
        message: "无法确认 consumer group 状态。",
      });
    } else if (group.state === "Empty") {
      groupIssues.push({
        code: "group_empty",
        message: "Consumer group 当前没有活跃成员。",
      });
    } else if (group.state === "Dead") {
      groupIssues.push({
        code: "group_dead",
        message: "Consumer group 已进入 Dead 状态。",
      });
    } else if (group.state.toLowerCase().includes("rebalance")) {
      groupIssues.push({
        code: "group_rebalancing",
        message: `Consumer group 正在 ${group.state}。`,
      });
    }
  }

  const analyzedPartitions = partitions.map((partition) => {
    const issues: KafkaIssue[] = [];
    if (partition.leader === null || partition.leader < 0) {
      issues.push({
        code: "leader_missing",
        message: `Partition ${partition.partition} 没有可用 leader。`,
        partition: partition.partition,
      });
    }
    if (partition.isr.length < partition.replicas.length) {
      issues.push({
        code: "isr_incomplete",
        message: `Partition ${partition.partition} 的 ISR 不完整（${partition.isr.length}/${partition.replicas.length}）。`,
        partition: partition.partition,
      });
    }
    if (kind === "input") {
      if (group?.state === "Stable" && (partition.members?.length ?? 0) === 0) {
        issues.push({
          code: "owner_missing",
          message: `Stable group 未分配 Partition ${partition.partition}。`,
          partition: partition.partition,
        });
      }
      if (!partition.committedOffset) {
        issues.push({
          code: "committed_missing",
          message: `Partition ${partition.partition} 没有可用 committed next offset。`,
          partition: partition.partition,
        });
      }
    }
    const visiblePartition =
      kind === "input"
        ? partition
        : {
            partition: partition.partition,
            leader: partition.leader,
            replicas: partition.replicas,
            isr: partition.isr,
            lowOffset: partition.lowOffset,
            highOffset: partition.highOffset,
          };
    return {
      ...visiblePartition,
      status: issues.length > 0 ? ("partial" as const) : ("available" as const),
      issues,
    };
  });
  const issues = [
    ...groupIssues,
    ...analyzedPartitions.flatMap((partition) => partition.issues),
  ];
  return {
    status: issues.length > 0 ? "partial" : "available",
    issues,
    partitions: analyzedPartitions,
  };
}

async function kafkaClientConfig(
  connection: KafkaConnection,
  timeout: number,
): Promise<KafkaConfig> {
  const security = connection.security;
  const tls = security.tls;
  const sslEnabled =
    security.protocol === "ssl" || security.protocol === "sasl_ssl";
  const saslEnabled =
    security.protocol === "sasl_plaintext" || security.protocol === "sasl_ssl";
  const config: KafkaConfig = {
    clientId: `linkd-devtools-${connection.clientId ?? "admin"}`,
    brokers: connection.brokers,
    connectionTimeout: timeout,
    requestTimeout: timeout,
    logLevel: logLevel.NOTHING,
  };
  if (sslEnabled) {
    config.ssl = {
      rejectUnauthorized: !(tls?.insecure_skip_verify ?? false),
      servername: tls?.server_name,
      ca: await pemValues(tls?.ca_pem, tls?.ca_file),
      cert: await pemValue(tls?.client_cert_pem, tls?.client_cert_file),
      key: await pemValue(tls?.client_key_pem, tls?.client_key_file),
    };
  }
  if (saslEnabled && security.sasl) {
    const mechanism = security.sasl.mechanism;
    const credentials = {
      username: security.sasl.username,
      password: security.sasl.password,
    };
    config.sasl =
      mechanism === "scram_sha_256"
        ? { mechanism: "scram-sha-256", ...credentials }
        : mechanism === "scram_sha_512"
          ? { mechanism: "scram-sha-512", ...credentials }
          : { mechanism: "plain", ...credentials };
  }
  return config;
}

async function pemValue(
  inline?: string,
  file?: string,
): Promise<string | undefined> {
  if (inline) return inline;
  return file ? readFile(file, "utf8") : undefined;
}

async function pemValues(
  inline?: string,
  file?: string,
): Promise<string[] | undefined> {
  const value = await pemValue(inline, file);
  return value ? [value] : undefined;
}

export function offsetLag(
  high: string | undefined,
  committed?: string,
): string | undefined {
  const knownHigh = knownOffset(high);
  const knownCommitted = knownOffset(committed);
  if (!knownHigh || !knownCommitted) return undefined;
  try {
    const lag = BigInt(knownHigh) - BigInt(knownCommitted);
    return String(lag < 0n ? 0n : lag);
  } catch {
    return undefined;
  }
}

function knownOffset(value: string | undefined): string | undefined {
  return value && value !== "-1" ? value : undefined;
}

function safeMessage(error: unknown): string {
  return error instanceof Error ? error.message : "Kafka query failed";
}
