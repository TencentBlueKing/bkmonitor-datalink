import { useQuery } from "@tanstack/react-query";
import { useState } from "react";
import { useSearchParams } from "react-router-dom";

import type {
  KafkaPartition,
  KafkaResource,
  MetricPanel,
} from "../../shared/contracts";
import { getKafkaInfrastructure, getMetrics } from "../api";
import { HelpLabel, HelpTableHeader, HelpTip } from "../components/HelpTip";
import { MetricPanelCard } from "../components/MetricPanelCard";
import { MetricQueryControls } from "../components/MetricQueryControls";
import { RefreshControls } from "../components/RefreshControls";
import { StatusBadge } from "../components/StatusBadge";
import {
  defaultMetricCalculationWindowSeconds,
  defaultMetricRangeSeconds,
  metricStep,
} from "../metricRange";
import { useReportPageQueryFailure } from "../navigation";
import { formatTime, useTimeMode } from "../time";
import {
  filterKafkaResources,
  type ResourceStatusFilter,
} from "./KafkaPage.logic";

const TOPICS_PER_PAGE = 20;

export function KafkaPage() {
  const [rangeSeconds, setRangeSeconds] = useState(defaultMetricRangeSeconds);
  const [calculationWindowSeconds, setCalculationWindowSeconds] = useState(
    defaultMetricCalculationWindowSeconds,
  );
  const [autoRefresh, setAutoRefresh] = useState(true);
  const [searchParams, setSearchParams] = useSearchParams();
  const activeTab: KafkaResource["kind"] =
    searchParams.get("tab") === "output" ? "output" : "input";
  const [keyword, setKeyword] = useState("");
  const [statusFilter, setStatusFilter] = useState<ResourceStatusFilter>("all");
  const [selectedKey, setSelectedKey] = useState<string>();
  const [onlyAbnormal, setOnlyAbnormal] = useState(false);
  const [topicPage, setTopicPage] = useState(0);
  const timeMode = useTimeMode();
  const infrastructure = useQuery({
    queryKey: ["infrastructure", "kafka"],
    queryFn: getKafkaInfrastructure,
    refetchInterval: autoRefresh ? 30_000 : false,
    retry: false,
  });
  useReportPageQueryFailure(infrastructure.isError && !infrastructure.data);

  const resources = infrastructure.data?.resources ?? [];
  const tabResources = resources.filter(
    (resource) => resource.kind === activeTab,
  );
  const filteredResources = filterKafkaResources(tabResources, {
    keyword,
    status: statusFilter,
  });
  const topicPageCount = Math.max(
    1,
    Math.ceil(filteredResources.length / TOPICS_PER_PAGE),
  );
  const currentTopicPage = Math.min(topicPage, topicPageCount - 1);
  const visibleResources = filteredResources.slice(
    currentTopicPage * TOPICS_PER_PAGE,
    (currentTopicPage + 1) * TOPICS_PER_PAGE,
  );
  const selected =
    visibleResources.find(
      (resource) => resourceKey(resource) === selectedKey,
    ) ??
    visibleResources.find((resource) => resource.status === "available") ??
    visibleResources[0];
  const metrics = useQuery({
    queryKey: [
      "kafka-resource-metrics",
      selected?.eventSourceId,
      rangeSeconds,
      calculationWindowSeconds,
    ],
    enabled: selected?.kind === "input" && Boolean(selected.eventSourceId),
    queryFn: () => {
      const to = new Date();
      return getMetrics({
        from: new Date(to.getTime() - rangeSeconds * 1000),
        to,
        step: metricStep(rangeSeconds),
        calculationWindowSeconds,
        eventSourceId: selected?.eventSourceId,
      });
    },
    refetchInterval: autoRefresh ? 30_000 : false,
    retry: false,
  });
  const inputResources = resources.filter(
    (resource) => resource.kind === "input",
  );
  const outputResources = resources.filter(
    (resource) => resource.kind === "output",
  );
  const inputFacts = kafkaInputFacts(inputResources);
  const outputFacts = kafkaOutputFacts(outputResources);
  const clusters = kafkaClusters(tabResources);
  const lastSuccess = infrastructure.dataUpdatedAt
    ? formatTime(new Date(infrastructure.dataUpdatedAt).toISOString(), timeMode)
    : "尚未成功";
  const refreshing =
    infrastructure.isFetching ||
    (selected?.kind === "input" && metrics.isFetching);

  function refresh(): void {
    void Promise.all([
      infrastructure.refetch(),
      selected?.kind === "input" ? metrics.refetch() : Promise.resolve(),
    ]);
  }

  function changeTab(kind: KafkaResource["kind"]): void {
    setSearchParams(
      (current) => {
        const next = new URLSearchParams(current);
        next.set("tab", kind);
        return next;
      },
      { replace: true },
    );
    setKeyword("");
    setStatusFilter("all");
    setSelectedKey(undefined);
    setOnlyAbnormal(false);
    setTopicPage(0);
  }

  function changeKeyword(value: string): void {
    setKeyword(value);
    setSelectedKey(undefined);
    setTopicPage(0);
  }

  function changeStatus(value: ResourceStatusFilter): void {
    setStatusFilter(value);
    setSelectedKey(undefined);
    setTopicPage(0);
  }

  function changeTopicPage(page: number): void {
    setTopicPage(page);
    setSelectedKey(undefined);
    setOnlyAbnormal(false);
  }

  return (
    <section className="kafka-page">
      <div className="page-heading kafka-page-heading">
        <div>
          <p className="eyebrow">KAFKA OPERATIONS</p>
          <h1>Kafka</h1>
          <p>
            围绕 Linkd Input consumer group 与 Lifecycle FinalHook Output
            查看分区归属、offset、lag 和副本健康。
          </p>
        </div>
        <div className="metric-page-control-stack">
          <MetricQueryControls
            rangeSeconds={rangeSeconds}
            calculationWindowSeconds={calculationWindowSeconds}
            onRangeChange={setRangeSeconds}
            onCalculationWindowChange={setCalculationWindowSeconds}
          />
          <RefreshControls
            status={infrastructure.data?.status}
            lastSuccessfulAt={infrastructure.dataUpdatedAt || undefined}
            isFetching={refreshing}
            autoRefresh={autoRefresh}
            intervalSeconds={30}
            onRefresh={refresh}
            onToggleAutoRefresh={() => setAutoRefresh((value) => !value)}
          />
        </div>
      </div>

      {infrastructure.isError && !infrastructure.data && (
        <div className="error-banner">
          Kafka 快照查询失败：{infrastructure.error.message}
        </div>
      )}
      {infrastructure.isRefetchError && infrastructure.data && (
        <div className="warning-banner" role="status">
          最近一次刷新失败，当前保留的是 {lastSuccess}{" "}
          的旧快照；恢复前请勿把它视为实时状态。
        </div>
      )}

      <div
        className="kafka-topic-tabs"
        role="tablist"
        aria-label="Kafka Topic 类型"
      >
        <button
          type="button"
          role="tab"
          aria-selected={activeTab === "input"}
          onClick={() => changeTab("input")}
        >
          Input Topics <span>{inputResources.length}</span>
        </button>
        <button
          type="button"
          role="tab"
          aria-selected={activeTab === "output"}
          onClick={() => changeTab("output")}
        >
          Output Topics <span>{outputResources.length}</span>
        </button>
      </div>

      <div
        role="tabpanel"
        aria-label={activeTab === "input" ? "Input Topics" : "Output Topics"}
      >
        {activeTab === "input" ? (
          <div className="kafka-fact-grid">
            <FactCard
              label="Input Topics"
              value={String(inputFacts.topicCount)}
              detail={`${inputFacts.partitionCount} partitions`}
              help="由 EventSource 配置声明、供 Cleaner 消费的 topic 数。"
            />
            <FactCard
              label="Stable Groups"
              value={`${inputFacts.stableGroups}/${inputFacts.topicCount}`}
              detail="consumer group state"
              help="状态为 Stable 的 consumer group 数 / Input topic 总数。"
              tone={
                inputFacts.stableGroups < inputFacts.topicCount
                  ? "amber"
                  : undefined
              }
            />
            <FactCard
              label="Total Lag"
              value={inputFacts.lag.value}
              detail={inputFacts.lag.detail}
              help="所有可计算 Input partition 的 LEO 减 committed next offset 之和。"
              tone={inputFacts.lag.partial ? "amber" : undefined}
            />
            <FactCard
              label="Abnormal Partitions"
              value={String(inputFacts.abnormalPartitions)}
              detail="leader、ISR、owner 或 committed 异常"
              help="存在 leader、ISR、owner、committed 或查询可用性问题的 partition 数。"
              tone={inputFacts.abnormalPartitions > 0 ? "red" : undefined}
            />
          </div>
        ) : (
          <div className="kafka-fact-grid">
            <FactCard
              label="Output Topics"
              value={String(outputFacts.topicCount)}
              detail="Lifecycle FinalHook targets"
              help="Lifecycle FinalHook 配置为生产目标的 Kafka topic 数。"
            />
            <FactCard
              label="Clusters"
              value={String(outputFacts.clusterCount)}
              detail="按 cluster id / brokers 去重"
              help="当前 Output topic 实际解析到的 Kafka 集群数。"
            />
            <FactCard
              label="Partitions"
              value={String(outputFacts.partitionCount)}
              detail={`${outputFacts.knownLeo}/${outputFacts.partitionCount} LEO known`}
              help="所有 Output topic 的 partition 总数；LEO 缺失时不会按 0 处理。"
              tone={
                outputFacts.knownLeo < outputFacts.partitionCount
                  ? "amber"
                  : undefined
              }
            />
            <FactCard
              label="Abnormal ISR / Leader"
              value={String(outputFacts.abnormalPartitions)}
              detail="只检查 producer 可见的 topic metadata"
              help="缺少 leader 或 ISR 不完整的 Output partition 数。"
              tone={outputFacts.abnormalPartitions > 0 ? "red" : undefined}
            />
          </div>
        )}
        <ClusterStrip clusters={clusters} />
        <div className="kafka-master-detail">
          <TopicNavigator
            kind={activeTab}
            resources={visibleResources}
            filteredCount={filteredResources.length}
            total={tabResources.length}
            selected={selected}
            keyword={keyword}
            status={statusFilter}
            page={currentTopicPage}
            pageCount={topicPageCount}
            onKeywordChange={changeKeyword}
            onStatusChange={changeStatus}
            onClear={() => {
              changeKeyword("");
              setStatusFilter("all");
            }}
            onPageChange={changeTopicPage}
            onSelect={setSelectedKey}
          />
          <div className="kafka-detail-workspace">
            {selected ? (
              <>
                <KafkaResourceDetail
                  resource={selected}
                  onlyAbnormal={onlyAbnormal}
                  onOnlyAbnormalChange={setOnlyAbnormal}
                />
                <KafkaMetrics
                  resource={selected}
                  panels={metrics.data?.panels ?? []}
                  loading={metrics.isLoading}
                  error={metrics.error}
                />
              </>
            ) : (
              <div className="kafka-detail-empty">
                当前筛选下没有可展示的{" "}
                {activeTab === "input" ? "Input" : "Output"} Topic 详情。
              </div>
            )}
          </div>
        </div>
      </div>
    </section>
  );
}

function TopicFilterBar({
  kind,
  keyword,
  status,
  onKeywordChange,
  onStatusChange,
  onClear,
}: {
  kind: KafkaResource["kind"];
  keyword: string;
  status: ResourceStatusFilter;
  onKeywordChange: (value: string) => void;
  onStatusChange: (value: ResourceStatusFilter) => void;
  onClear: () => void;
}) {
  return (
    <div className="kafka-filter-bar">
      <label>
        <HelpLabel
          label="KEYWORD"
          help="在当前 tab 内匹配 EventSource、client、topic 或 consumer group。"
        />
        <input
          aria-label="KEYWORD"
          value={keyword}
          placeholder={
            kind === "input"
              ? "搜索 EventSource、topic 或 group"
              : "搜索 producer client 或 topic"
          }
          onChange={(event) => onKeywordChange(event.target.value)}
        />
      </label>
      <label>
        <HelpLabel
          label="STATUS"
          help="按资源快照的 available、partial 或 unavailable 状态过滤。"
        />
        <select
          aria-label="STATUS"
          value={status}
          onChange={(event) =>
            onStatusChange(event.target.value as ResourceStatusFilter)
          }
        >
          <option value="all">全部</option>
          <option value="available">Available</option>
          <option value="partial">Partial</option>
          <option value="unavailable">Unavailable</option>
        </select>
      </label>
      <button type="button" onClick={onClear}>
        清除筛选
      </button>
    </div>
  );
}

function TopicNavigator({
  kind,
  resources,
  filteredCount,
  total,
  selected,
  keyword,
  status,
  page,
  pageCount,
  onKeywordChange,
  onStatusChange,
  onClear,
  onPageChange,
  onSelect,
}: {
  kind: KafkaResource["kind"];
  resources: KafkaResource[];
  filteredCount: number;
  total: number;
  selected?: KafkaResource;
  keyword: string;
  status: ResourceStatusFilter;
  page: number;
  pageCount: number;
  onKeywordChange: (value: string) => void;
  onStatusChange: (value: ResourceStatusFilter) => void;
  onClear: () => void;
  onPageChange: (page: number) => void;
  onSelect: (key: string) => void;
}) {
  return (
    <aside
      className="kafka-topic-navigator"
      aria-label={`${kind === "input" ? "Input" : "Output"} Topic 导航`}
    >
      <header>
        <div>
          <h2>
            {kind === "input" ? "EventSource Inputs" : "FinalHook Outputs"}
          </h2>
          <p>
            {kind === "input"
              ? "选择 consumer topic 查看 group 与 lag"
              : "选择 producer topic 查看 metadata 与 offset"}
          </p>
        </div>
        <span>
          {filteredCount} / {total}
        </span>
      </header>
      <TopicFilterBar
        kind={kind}
        keyword={keyword}
        status={status}
        onKeywordChange={onKeywordChange}
        onStatusChange={onStatusChange}
        onClear={onClear}
      />
      <div className="kafka-topic-list">
        {resources.map((resource) => {
          const isSelected = resourceKey(resource) === resourceKey(selected);
          const lag = kafkaResourceLag(resource);
          return (
            <button
              className="kafka-topic-card"
              type="button"
              aria-current={isSelected ? "true" : undefined}
              key={resourceKey(resource)}
              onClick={() => onSelect(resourceKey(resource))}
            >
              <span className="kafka-topic-card-title">
                <strong>{resource.topic}</strong>
                <StatusBadge value={resource.status} />
              </span>
              <small>
                {kind === "input"
                  ? (resource.eventSourceId ?? "EventSource 未知")
                  : `Lifecycle FinalHook · ${resource.clientId ?? "client 未配置"}`}
              </small>
              <span className="kafka-topic-card-facts">
                {kind === "input" ? (
                  <>
                    <em>GROUP {resource.group?.state ?? "未知"}</em>
                    <em>
                      LAG {lag.value}
                      {lag.partial ? " · PARTIAL" : ""}
                    </em>
                  </>
                ) : (
                  <>
                    <em>{resource.partitions.length} PARTITIONS</em>
                    <em>ISR {isrSummary(resource)}</em>
                  </>
                )}
              </span>
            </button>
          );
        })}
        {resources.length === 0 && (
          <div className="kafka-topic-list-empty">
            {total === 0
              ? `当前配置没有 ${kind === "input" ? "EventSource Input" : "Lifecycle Output"} Topic。`
              : `没有符合筛选条件的 ${kind === "input" ? "Input" : "Output"} Topic。`}
          </div>
        )}
      </div>
      <footer className="kafka-topic-pagination">
        <span>
          第 {page + 1} / {pageCount} 页
        </span>
        <div>
          <button
            type="button"
            disabled={page === 0}
            onClick={() => onPageChange(page - 1)}
          >
            上一页
          </button>
          <button
            type="button"
            disabled={page + 1 >= pageCount}
            onClick={() => onPageChange(page + 1)}
          >
            下一页
          </button>
        </div>
      </footer>
    </aside>
  );
}

function FactCard({
  label,
  value,
  detail,
  help,
  tone,
}: {
  label: string;
  value: string;
  detail: string;
  help: string;
  tone?: "amber" | "red";
}) {
  return (
    <article className={`stat-card ${tone ?? ""}`}>
      <HelpLabel label={label} help={help} />
      <strong>{value}</strong>
      <small>{detail}</small>
    </article>
  );
}

function ClusterStrip({
  clusters,
}: {
  clusters: ReturnType<typeof kafkaClusters>;
}) {
  return (
    <article className="kafka-cluster-panel">
      <header>
        <div>
          <div className="section-title-with-help">
            <h2>Cluster 摘要</h2>
            <HelpTip label="Cluster 摘要">
              按 cluster ID 去重；ID 不可用时使用 broker 列表识别同一集群。
            </HelpTip>
          </div>
          <p>同一集群只展示一次；展开查看 controller 与 broker。</p>
        </div>
        <span>{clusters.length} clusters</span>
      </header>
      <div>
        {clusters.map((cluster) => (
          <details key={cluster.key}>
            <summary>
              <strong>{cluster.id ?? "Cluster ID 未知"}</strong>
              <span>{cluster.brokers.length} brokers</span>
              <span>{cluster.resourceCount} resources</span>
              <span>controller {cluster.controller ?? "—"}</span>
            </summary>
            <ul>
              {cluster.brokers.map((broker) => (
                <li key={`${broker.nodeId}:${broker.host}:${broker.port}`}>
                  <code>{broker.nodeId >= 0 ? `#${broker.nodeId}` : "#?"}</code>
                  <span>
                    {broker.host}:{broker.port}
                  </span>
                </li>
              ))}
            </ul>
          </details>
        ))}
        {clusters.length === 0 && (
          <div className="kafka-cluster-empty">尚未识别出 Kafka 集群。</div>
        )}
      </div>
    </article>
  );
}

function KafkaResourceDetail({
  resource,
  onlyAbnormal,
  onOnlyAbnormalChange,
}: {
  resource: KafkaResource;
  onlyAbnormal: boolean;
  onOnlyAbnormalChange: (value: boolean) => void;
}) {
  const partitions = [...resource.partitions]
    .sort((left, right) => left.partition - right.partition)
    .filter((partition) => !onlyAbnormal || partition.status !== "available");
  return (
    <article className="kafka-detail-panel">
      <header>
        <div>
          <span>
            {resource.kind === "input"
              ? "EVENTSOURCE INPUT"
              : "LIFECYCLE OUTPUT"}
          </span>
          <h2>
            {resource.kind === "input"
              ? `Input Topic 详情 · ${resource.eventSourceId ?? "未知 EventSource"}`
              : `Output Topic 详情 · ${resource.topic}`}
          </h2>
          <p className="mono">{resource.topic}</p>
        </div>
        <StatusBadge value={resource.status} />
      </header>

      {resource.message && (
        <div className="error-banner">{resource.message}</div>
      )}
      <div className="kafka-detail-facts">
        {resource.kind === "input" ? (
          <>
            <div>
              <HelpLabel
                label="EventSource"
                help="声明该 Input topic 的接入源稳定标识。"
              />
              <strong>{resource.eventSourceId ?? "未知"}</strong>
            </div>
            <div>
              <HelpLabel
                label="Topic"
                help="Cleaner 实际消费的 Kafka topic。"
              />
              <strong>{resource.topic}</strong>
            </div>
            <div>
              <HelpLabel
                label="Consumer Group"
                help="Cleaner 提交 offset 和维护成员关系的消费组。"
              />
              <strong>{resource.consumerGroup ?? "未知"}</strong>
            </div>
            <div>
              <HelpLabel
                label="Group State"
                help="Kafka 协调器返回的 group 状态；Stable 表示当前未在 rebalance。"
              />
              <strong>{resource.group?.state ?? "未知"}</strong>
            </div>
          </>
        ) : (
          <>
            <div>
              <HelpLabel
                label="Producer Client"
                help="Lifecycle FinalHook Kafka producer 的 client ID。"
              />
              <strong>{resource.clientId ?? "未配置"}</strong>
            </div>
            <div>
              <HelpLabel
                label="Topic"
                help="Lifecycle FinalHook 写入的 Kafka topic。"
              />
              <strong>{resource.topic}</strong>
            </div>
            <div>
              <HelpLabel label="Cluster" help="Kafka broker 返回的集群标识。" />
              <strong>{resource.cluster?.id ?? "未知"}</strong>
            </div>
            <div>
              <HelpLabel
                label="Partitions"
                help="该 Output topic 当前可见的 partition 数。"
              />
              <strong>{resource.partitions.length}</strong>
            </div>
          </>
        )}
      </div>

      {resource.issues.length > 0 && (
        <div className="kafka-issue-list" aria-label="Kafka 状态问题">
          {resource.issues.map((issue, index) => (
            <span key={`${issue.code}:${issue.partition ?? "group"}:${index}`}>
              <code>{issue.code}</code>
              {issue.message}
            </span>
          ))}
        </div>
      )}

      {resource.kind === "input" && (
        <>
          <section className="kafka-assignment-section">
            <header>
              <h3>Group Member Assignments</h3>
              <span>{resource.group?.members.length ?? 0} members</span>
            </header>
            <div className="table-scroll kafka-assignment-table">
              <table>
                <thead>
                  <tr>
                    <HelpTableHeader
                      label="Member"
                      help="Kafka 为本次 group membership 分配的成员 ID。"
                    />
                    <HelpTableHeader
                      label="Client"
                      help="Consumer 向 Kafka 上报的 client ID。"
                    />
                    <HelpTableHeader
                      label="Host"
                      help="Kafka 协调器看到的 consumer 客户端地址。"
                    />
                    <HelpTableHeader
                      label="Partitions"
                      help="当前分配给该 member 的 partition 编号。"
                    />
                  </tr>
                </thead>
                <tbody>
                  {(resource.group?.members ?? []).map((member) => (
                    <tr key={member.memberId}>
                      <td className="mono" title={member.memberId}>
                        {member.memberId}
                      </td>
                      <td className="mono">{member.clientId}</td>
                      <td className="mono">{member.clientHost}</td>
                      <td className="mono">
                        {[...member.partitions]
                          .sort((a, b) => a - b)
                          .join(", ") || "—"}
                      </td>
                    </tr>
                  ))}
                  {(resource.group?.members.length ?? 0) === 0 && (
                    <tr>
                      <td className="empty-row" colSpan={4}>
                        当前没有 group member assignment。
                      </td>
                    </tr>
                  )}
                </tbody>
              </table>
            </div>
          </section>
          <div className="kafka-contract-note">
            <strong>Committed 的 Linkd 语义</strong>
            <p>
              Linkd 只按 partition 的连续完成前缀推进 committed next
              offset。正常消息必须完成 Event 存储与 Mailbox 入队，确定性 discard
              也可以推进；因此 committed 的增量不等于入库 Event 数。
            </p>
          </div>
        </>
      )}
      {resource.kind === "output" && (
        <div className="kafka-contract-note">
          <strong>Output 没有 consumer lag</strong>
          <p>
            该资源是 Lifecycle FinalHook 的生产目标，因此 owner、committed 和
            lag 均不适用。LEO 是 topic 的全局日志末端，不等于 Linkd 独自产量。
          </p>
        </div>
      )}

      <section className="kafka-partition-section">
        <header>
          <div>
            <h3>Partitions</h3>
            <span>按 partition 数值升序</span>
          </div>
          <label>
            <input
              type="checkbox"
              checked={onlyAbnormal}
              onChange={(event) => onOnlyAbnormalChange(event.target.checked)}
            />
            仅看异常
          </label>
        </header>
        <div className="table-scroll kafka-partition-table">
          <table>
            <thead>
              <tr>
                <HelpTableHeader
                  label="Partition"
                  help="Kafka topic 内独立有序的分片编号。"
                />
                <HelpTableHeader
                  label="Leader"
                  help="负责该 partition 读写请求的 broker 节点 ID。"
                />
                <HelpTableHeader
                  label="Replicas"
                  help="配置为保存该 partition 副本的 broker 节点。"
                />
                <HelpTableHeader
                  label="ISR"
                  help="当前与 leader 保持同步的副本集合。"
                />
                {resource.kind === "input" && (
                  <HelpTableHeader
                    label="Owner"
                    help="当前被分配处理该 partition 的 consumer member。"
                  />
                )}
                <HelpTableHeader
                  label="Log Start"
                  help="该 partition 当前保留的最早消息 offset。"
                />
                <HelpTableHeader
                  label="LEO"
                  help="Log End Offset，即下一条写入消息将使用的 offset。"
                />
                {resource.kind === "input" && (
                  <HelpTableHeader
                    label="Committed Next"
                    help="Consumer group 已提交的下一条待消费 offset。"
                  />
                )}
                {resource.kind === "input" && (
                  <HelpTableHeader
                    label="Lag"
                    help="LEO 减 Committed Next，表示尚未被 group 确认的消息量。"
                  />
                )}
                <HelpTableHeader
                  label="Status"
                  help="综合 leader、ISR、owner、offset 和查询结果得到的状态。"
                />
              </tr>
            </thead>
            <tbody>
              {partitions.map((partition) => (
                <PartitionRow
                  key={partition.partition}
                  resourceKind={resource.kind}
                  partition={partition}
                />
              ))}
              {partitions.length === 0 && (
                <tr>
                  <td
                    className="empty-row"
                    colSpan={resource.kind === "input" ? 10 : 7}
                  >
                    没有符合条件的 partition。
                  </td>
                </tr>
              )}
            </tbody>
          </table>
        </div>
      </section>
    </article>
  );
}

function PartitionRow({
  resourceKind,
  partition,
}: {
  resourceKind: KafkaResource["kind"];
  partition: KafkaPartition;
}) {
  return (
    <tr>
      <td className="mono">{partition.partition}</td>
      <td className="mono">{partition.leader ?? "—"}</td>
      <td className="mono">{partition.replicas.join(", ") || "—"}</td>
      <td className="mono">{partition.isr.join(", ") || "—"}</td>
      {resourceKind === "input" && (
        <td className="mono" title={partition.members?.join(", ")}>
          {partition.members?.join(", ") || "—"}
        </td>
      )}
      <td className="mono">{offsetValue(partition.lowOffset)}</td>
      <td className="mono">{offsetValue(partition.highOffset)}</td>
      {resourceKind === "input" && (
        <td className="mono">{offsetValue(partition.committedOffset)}</td>
      )}
      {resourceKind === "input" && (
        <td className="mono">{offsetValue(partition.lag)}</td>
      )}
      <td>
        <StatusBadge value={partition.status} />
        {partition.issues.length > 0 && (
          <small>
            {partition.issues.map((issue) => issue.code).join(", ")}
          </small>
        )}
      </td>
    </tr>
  );
}

function KafkaMetrics({
  resource,
  panels,
  loading,
  error,
}: {
  resource: KafkaResource;
  panels: MetricPanel[];
  loading: boolean;
  error: Error | null;
}) {
  const visible = panels.filter((panel) =>
    ["received-rate", "settled-rate"].includes(panel.id),
  );
  return (
    <article className="kafka-metrics-panel">
      <header>
        <div>
          <h2>关联处理指标</h2>
          <p>只展示能按所选资源可靠归因的 Linkd 指标。</p>
        </div>
        <span>最近 1 小时</span>
      </header>
      {resource.kind === "output" ? (
        <div className="kafka-metrics-unavailable">
          <strong>Unavailable</strong>
          <span>
            现有 Prometheus label 不包含 Output topic；FinalHook 指标按
            EventSource 聚合，无法可靠归因到这个 Kafka Output。
          </span>
        </div>
      ) : error ? (
        <div className="kafka-metrics-unavailable">
          <strong>Unavailable</strong>
          <span>关联指标查询失败。</span>
        </div>
      ) : loading ? (
        <div className="kafka-metrics-unavailable">
          <span>正在加载 EventSource 指标…</span>
        </div>
      ) : (
        <div className="chart-grid kafka-chart-grid">
          {visible.map((panel) => (
            <MetricPanelCard key={panel.id} panel={panel} />
          ))}
          {visible.length === 0 && (
            <div className="kafka-metrics-unavailable">
              <span>指标响应中没有可可靠关联的面板。</span>
            </div>
          )}
        </div>
      )}
    </article>
  );
}

function kafkaInputFacts(resources: KafkaResource[]) {
  return {
    topicCount: resources.length,
    stableGroups: resources.filter(
      (resource) => resource.group?.state === "Stable",
    ).length,
    partitionCount: resources.reduce(
      (total, resource) => total + resource.partitions.length,
      0,
    ),
    lag: aggregateLag(
      resources.flatMap((resource) => resource.partitions),
      resources.filter((resource) => resource.status === "unavailable").length,
    ),
    abnormalPartitions: abnormalPartitionCount(resources),
  };
}

function kafkaOutputFacts(resources: KafkaResource[]) {
  const partitions = resources.flatMap((resource) => resource.partitions);
  return {
    topicCount: resources.length,
    clusterCount: kafkaClusters(resources).length,
    partitionCount: partitions.length,
    knownLeo: partitions.filter((partition) => Boolean(partition.highOffset))
      .length,
    abnormalPartitions: abnormalPartitionCount(resources),
  };
}

function abnormalPartitionCount(resources: KafkaResource[]): number {
  return resources.reduce(
    (total, resource) =>
      total +
      resource.partitions.filter(
        (partition) => partition.status !== "available",
      ).length,
    0,
  );
}

function aggregateLag(partitions: KafkaPartition[], unavailableResources = 0) {
  const known = partitions.flatMap((partition) => {
    if (!partition.lag || !/^\d+$/.test(partition.lag)) return [];
    return [BigInt(partition.lag)];
  });
  if (partitions.length === 0) {
    return unavailableResources > 0
      ? {
          value: "未知",
          detail: `${unavailableResources} 个 Input 资源不可用`,
          partial: true,
        }
      : { value: "不适用", detail: "没有 Input partition", partial: false };
  }
  if (known.length === 0) {
    return {
      value: "未知",
      detail: `0/${partitions.length} partitions 可计算`,
      partial: true,
    };
  }
  const value = known.reduce((sum, item) => sum + item, 0n).toString();
  const partial =
    known.length !== partitions.length || unavailableResources > 0;
  return {
    value,
    detail: partial
      ? `${known.length}/${partitions.length} partitions 可计算${unavailableResources > 0 ? `，${unavailableResources} 个资源不可用` : ""}`
      : `${partitions.length} partitions 全部可计算`,
    partial,
  };
}

function kafkaResourceLag(resource: KafkaResource) {
  return aggregateLag(
    resource.partitions,
    resource.status === "unavailable" ? 1 : 0,
  );
}

function isrSummary(resource: KafkaResource): string {
  const complete = resource.partitions.filter(
    (partition) => partition.isr.length >= partition.replicas.length,
  ).length;
  return `${complete}/${resource.partitions.length}`;
}

function offsetValue(value: string | undefined): string {
  return value ?? "— 未知";
}

function resourceKey(resource: KafkaResource | undefined): string {
  if (!resource) return "";
  return [
    resource.kind,
    resource.eventSourceId,
    resource.topic,
    resource.consumerGroup,
  ].join(":");
}

function kafkaClusters(resources: KafkaResource[]) {
  const clusters = new Map<
    string,
    {
      key: string;
      id?: string;
      controller: number | null;
      brokers: Array<{ nodeId: number; host: string; port: number }>;
      resourceCount: number;
    }
  >();
  for (const resource of resources) {
    const fallbackBrokers = resource.brokers
      .map((broker) => ({ nodeId: -1, host: broker, port: 0 }))
      .map((broker) => {
        const separator = broker.host.lastIndexOf(":");
        const port = Number(broker.host.slice(separator + 1));
        return separator > 0 && Number.isInteger(port)
          ? { ...broker, host: broker.host.slice(0, separator), port }
          : broker;
      });
    const brokers = resource.cluster?.brokers ?? fallbackBrokers;
    const key =
      resource.cluster?.id ||
      brokers
        .map((broker) => `${broker.host}:${broker.port}`)
        .sort()
        .join(",");
    if (!key) continue;
    const existing = clusters.get(key);
    if (existing) existing.resourceCount += 1;
    else
      clusters.set(key, {
        key,
        id: resource.cluster?.id,
        controller: resource.cluster?.controller ?? null,
        brokers,
        resourceCount: 1,
      });
  }
  return [...clusters.values()];
}
