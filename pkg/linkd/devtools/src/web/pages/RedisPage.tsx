import { useQuery } from "@tanstack/react-query";
import { type ReactNode, useEffect, useMemo, useState } from "react";
import { Link, useSearchParams } from "react-router-dom";

import type {
  RedisGroup,
  RedisLeaseResponse,
  RedisMailboxResponse,
  RedisSectionStatus,
} from "../../shared/contracts";
import {
  getRedisInfrastructure,
  getRedisLeases,
  getRedisMailboxes,
  getRedisPending,
} from "../api";
import { HelpLabel, HelpTableHeader } from "../components/HelpTip";
import { RefreshControls } from "../components/RefreshControls";
import { StatusBadge } from "../components/StatusBadge";
import { formatTime, useTimeMode } from "../time";

type RedisTab = "overview" | "signal" | "mailbox" | "lease";

const tabs: Array<{
  id: RedisTab;
  label: string;
  description: string;
}> = [
  { id: "overview", label: "实例总览", description: "运行与数据安全" },
  { id: "signal", label: "信号队列", description: "Stream / Group / PEL" },
  { id: "mailbox", label: "Mailbox 调度", description: "待处理 Event" },
  { id: "lease", label: "Lease / Lock", description: "关联键串行" },
];

const redisFieldHelp: Record<string, string> = {
  运行时长: "Redis 服务自本次启动以来的运行时间。",
  连接客户端: "当前连接到 Redis 实例的客户端数量。",
  阻塞客户端: "正在等待阻塞式命令返回的客户端数量。",
  内存碎片率: "RSS 与已分配内存的比值；需结合绝对内存量判断。",
  "平均 Key TTL": "当前 DB 中带过期时间 key 的平均剩余 TTL。",
  加载中: "Redis 是否正在从持久化文件加载数据。",
  AOF: "Append Only File 持久化是否启用。",
  "RDB 未保存变更": "最近一次 RDB 保存后发生的写操作数量。",
  "最近 RDB 保存": "最近一次完成 RDB 快照保存的时间。",
  "最近 RDB 后台保存": "最近一次后台 RDB 保存任务的结果。",
  "最近 AOF 写入": "最近一次 AOF 写入任务的结果。",
  角色: "该 Redis 实例当前是主节点还是副本。",
  已连接副本: "当前与主节点保持连接的副本数量。",
  上游链路: "副本与其上游主节点之间的复制链路状态。",
  "累计淘汰 Key": "因 maxmemory 策略被累计淘汰的 key 数。",
  累计拒绝连接: "因达到客户端上限等原因累计拒绝的连接数。",
  "Head Event ID": "Mailbox 队首、下一批将优先处理的 Event ID。",
  剩余TTL: "本次读取时 lease key 的剩余生存时间。",
  "剩余 TTL": "本次读取时 lease key 的剩余生存时间。",
  过期状态: "Redis PTTL 对该 lease key 的当前判定。",
  Owner: "当前 lease value 不保存可展示的 owner 身份。",
};

export function RedisPage() {
  const timeMode = useTimeMode();
  const [searchParams, setSearchParams] = useSearchParams();
  const requestedTab = searchParams.get("tab");
  const activeTab = isRedisTab(requestedTab) ? requestedTab : "overview";
  const [autoRefresh, setAutoRefresh] = useState(true);
  const [selectedGroupName, setSelectedGroupName] = useState<string>();
  const [consumerFilter, setConsumerFilter] = useState("");
  const [claimEligibleOnly, setClaimEligibleOnly] = useState(false);
  const [mailboxFilter, setMailboxFilter] = useState("");
  const [leaseFilter, setLeaseFilter] = useState("");
  const [selectedMailbox, setSelectedMailbox] =
    useState<RedisMailboxResponse["items"][number]>();
  const [selectedLease, setSelectedLease] =
    useState<RedisLeaseResponse["items"][number]>();
  const interval = autoRefresh ? 15_000 : false;
  const overview = useQuery({
    queryKey: ["redis-infrastructure"],
    queryFn: getRedisInfrastructure,
    refetchInterval: interval,
    refetchOnWindowFocus: false,
  });
  const groups = overview.data?.signalQueue.groups ?? [];
  const selectedGroup =
    groups.find((group) => group.name === selectedGroupName) ??
    groups.find((group) => group.expected) ??
    groups[0];
  const pending = useQuery({
    queryKey: ["redis-pending", selectedGroup?.name],
    queryFn: () => getRedisPending({ group: selectedGroup?.name, limit: 100 }),
    enabled: activeTab === "signal" && Boolean(selectedGroup),
    refetchInterval: interval,
    refetchOnWindowFocus: false,
  });
  const mailboxes = useQuery({
    queryKey: ["redis-mailboxes", mailboxFilter],
    queryFn: () => getRedisMailboxes({ query: mailboxFilter, limit: 100 }),
    enabled: activeTab === "mailbox",
    refetchInterval: interval,
    refetchOnWindowFocus: false,
  });
  const leases = useQuery({
    queryKey: ["redis-leases", leaseFilter],
    queryFn: () => getRedisLeases({ query: leaseFilter, limit: 100 }),
    enabled: activeTab === "lease",
    refetchInterval: interval,
    refetchOnWindowFocus: false,
  });
  const filteredConsumers = useMemo(() => {
    const query = consumerFilter.trim().toLocaleLowerCase();
    return (selectedGroup?.consumers ?? []).filter((consumer) =>
      consumer.name.toLocaleLowerCase().includes(query),
    );
  }, [consumerFilter, selectedGroup]);
  const filteredPending = (pending.data?.items ?? []).filter(
    (item) => !claimEligibleOnly || item.claimEligible,
  );

  async function refreshActiveTab() {
    const requests: Array<Promise<unknown>> = [overview.refetch()];
    if (activeTab === "signal" && selectedGroup)
      requests.push(pending.refetch());
    if (activeTab === "mailbox") requests.push(mailboxes.refetch());
    if (activeTab === "lease") requests.push(leases.refetch());
    await Promise.all(requests);
  }

  function selectTab(tab: RedisTab) {
    const next = new URLSearchParams(searchParams);
    next.set("tab", tab);
    setSearchParams(next, { replace: true });
  }

  useEffect(() => {
    if (requestedTab === activeTab) return;
    const next = new URLSearchParams(searchParams);
    next.set("tab", activeTab);
    setSearchParams(next, { replace: true });
  }, [activeTab, requestedTab, searchParams, setSearchParams]);

  const detailFetching =
    activeTab === "signal"
      ? pending.isFetching
      : activeTab === "mailbox"
        ? mailboxes.isFetching
        : activeTab === "lease"
          ? leases.isFetching
          : false;
  const refreshing = overview.isFetching || detailFetching;

  return (
    <section className="redis-page">
      <div className="page-heading redis-page-heading">
        <div>
          <p className="eyebrow">REDIS OPERATIONS</p>
          <h1>Redis</h1>
          <p>按 Linkd 实际用途检查实例、Signal、Mailbox 与分布式 lease。</p>
        </div>
        <RefreshControls
          status={overview.data?.status}
          lastSuccessfulAt={overview.dataUpdatedAt || undefined}
          isFetching={refreshing}
          autoRefresh={autoRefresh}
          intervalSeconds={15}
          onRefresh={() => void refreshActiveTab()}
          onToggleAutoRefresh={() => setAutoRefresh((value) => !value)}
        />
      </div>

      {overview.isError && (
        <div className="error-banner">查询失败：{overview.error.message}</div>
      )}

      <div
        className="redis-purpose-tabs"
        role="tablist"
        aria-label="Redis 用途"
      >
        {tabs.map((tab) => (
          <button
            key={tab.id}
            type="button"
            role="tab"
            aria-selected={activeTab === tab.id}
            onClick={() => selectTab(tab.id)}
          >
            <strong>{tab.label}</strong>
            <span>{tab.description}</span>
          </button>
        ))}
      </div>

      {!overview.data ? (
        <div className="redis-empty-state">正在读取 Redis 快照…</div>
      ) : activeTab === "overview" ? (
        <InstanceTab data={overview.data} timeMode={timeMode} />
      ) : activeTab === "signal" ? (
        <SignalsTab
          data={overview.data.signalQueue}
          selectedGroup={selectedGroup}
          filteredConsumers={filteredConsumers}
          consumerFilter={consumerFilter}
          setConsumerFilter={setConsumerFilter}
          setSelectedGroupName={setSelectedGroupName}
          pending={pending.data}
          pendingLoading={pending.isLoading}
          filteredPending={filteredPending}
          claimEligibleOnly={claimEligibleOnly}
          setClaimEligibleOnly={setClaimEligibleOnly}
        />
      ) : activeTab === "mailbox" ? (
        <MailboxesTab
          summary={overview.data.mailbox}
          result={mailboxes.data}
          loading={mailboxes.isLoading}
          filter={mailboxFilter}
          setFilter={setMailboxFilter}
          selected={selectedMailbox}
          setSelected={setSelectedMailbox}
        />
      ) : (
        <LeasesTab
          summary={overview.data.leases}
          result={leases.data}
          loading={leases.isLoading}
          filter={leaseFilter}
          setFilter={setLeaseFilter}
          selected={selectedLease}
          setSelected={setSelectedLease}
        />
      )}
    </section>
  );
}

function InstanceTab({
  data,
  timeMode,
}: {
  data: Awaited<ReturnType<typeof getRedisInfrastructure>>;
  timeMode: "local" | "utc";
}) {
  const instance = data.instance;
  const persistence = instance.aofEnabled ? "AOF 已启用" : "AOF 未启用";
  return (
    <div role="tabpanel" className="redis-tab-panel">
      <TabHeading
        eyebrow="INSTANCE"
        title="实例运行与数据安全"
        description="INFO 仅读取固定白名单字段；所有计数均为 Redis 实例级事实，不限定 Linkd key。"
        status={instance.status}
      />
      <div className="redis-fact-grid">
        <Fact
          label="连接"
          value={data.connection.ping ?? "不可用"}
          detail={`${data.connection.address ?? "—"} / DB ${data.connection.database ?? "—"}`}
          help="DevTools 对配置 Redis 地址执行 PING 的结果。"
        />
        <Fact
          label="版本 / 模式"
          value={instance.version ?? "—"}
          detail={instance.mode ?? "未知模式"}
          help="Redis 服务版本和 standalone、cluster 等运行模式。"
        />
        <Fact
          label="内存"
          value={formatBytes(instance.usedMemoryBytes)}
          detail={`RSS ${formatBytes(instance.usedMemoryRssBytes)} / 峰值 ${formatBytes(instance.peakMemoryBytes)}`}
          help="Redis 已分配内存；详情同时给出常驻内存与历史峰值。"
        />
        <Fact
          label="Maxmemory"
          value={
            instance.maxMemoryBytes
              ? formatBytes(instance.maxMemoryBytes)
              : "未设置"
          }
          detail={instance.maxMemoryPolicy ?? "策略未知"}
          help="Redis 配置的内存上限及达到上限后的淘汰策略。"
        />
        <Fact
          label="当前吞吐"
          value={formatRate(instance.operationsPerSecond, "ops/s")}
          detail={`错误回复累计 ${displayNumber(instance.totalErrorReplies)}`}
          help="Redis 实例当前每秒执行的命令数，不限定 Linkd 请求。"
        />
        <Fact
          label="Keyspace"
          value={displayNumber(instance.databaseKeys)}
          detail={`带过期 ${displayNumber(instance.expiringKeys)}`}
          help="当前选择 DB 的 key 总数及其中设置过期时间的数量。"
        />
      </div>
      <div className="redis-section-grid">
        <KeyValuePanel
          title="运行状态"
          rows={[
            ["运行时长", formatDurationSeconds(instance.uptimeSeconds)],
            ["连接客户端", displayNumber(instance.connectedClients)],
            ["阻塞客户端", displayNumber(instance.blockedClients)],
            ["内存碎片率", displayNumber(instance.fragmentationRatio)],
            [
              "平均 Key TTL",
              formatMilliseconds(instance.averageTtlMilliseconds),
            ],
          ]}
        />
        <KeyValuePanel
          title="持久化"
          rows={[
            ["加载中", yesNo(instance.loading)],
            ["AOF", persistence],
            ["RDB 未保存变更", displayNumber(instance.rdbChangesSinceLastSave)],
            [
              "最近 RDB 保存",
              formatUnixSeconds(instance.rdbLastSaveTime, timeMode),
            ],
            ["最近 RDB 后台保存", instance.rdbLastBgsaveStatus ?? "未知"],
            ["最近 AOF 写入", instance.aofLastWriteStatus ?? "未知"],
          ]}
        />
        <KeyValuePanel
          title="复制与累计异常"
          rows={[
            ["角色", instance.replicationRole ?? "未知"],
            ["已连接副本", displayNumber(instance.connectedReplicas)],
            ["上游链路", instance.masterLinkStatus ?? "不适用 / 未知"],
            ["累计淘汰 Key", displayNumber(instance.evictedKeys)],
            ["累计拒绝连接", displayNumber(instance.rejectedConnections)],
          ]}
        />
      </div>
      <div className="redis-risk-note">
        <strong>恢复边界</strong>
        <p>
          Cleaner 确认上游 Kafka 后，Mailbox 是尚未处理 Event
          的恢复来源。这里展示的 RDB、AOF
          与副本只是当前实例事实，不能证明备份可恢复，也不能替代恢复演练。
        </p>
      </div>
    </div>
  );
}

function SignalsTab({
  data,
  selectedGroup,
  filteredConsumers,
  consumerFilter,
  setConsumerFilter,
  setSelectedGroupName,
  pending,
  pendingLoading,
  filteredPending,
  claimEligibleOnly,
  setClaimEligibleOnly,
}: {
  data: Awaited<ReturnType<typeof getRedisInfrastructure>>["signalQueue"];
  selectedGroup?: RedisGroup;
  filteredConsumers: RedisGroup["consumers"];
  consumerFilter: string;
  setConsumerFilter: (value: string) => void;
  setSelectedGroupName: (value: string) => void;
  pending?: Awaited<ReturnType<typeof getRedisPending>>;
  pendingLoading: boolean;
  filteredPending: NonNullable<
    Awaited<ReturnType<typeof getRedisPending>>
  >["items"];
  claimEligibleOnly: boolean;
  setClaimEligibleOnly: (value: boolean) => void;
}) {
  const stream = data.stream;
  const priorityGroup =
    data.groups.find((group) => group.expected) ?? data.groups[0];
  const priorityGroupName = priorityGroup?.name ?? "尚无 Consumer Group";
  return (
    <div role="tabpanel" className="redis-tab-panel">
      <TabHeading
        eyebrow="SIGNAL STREAM"
        title="Lifecycle 信号队列"
        description="lag 是尚未投递的 Signal，PEL pending 是已投递但尚未 XACK；Cleaner 用目标 Group 的两者之和做近似背压。"
        status={data.status}
      />
      <div className="redis-signal-priority-grid" aria-label="Signal 核心积压">
        <Fact
          label="Group Lag"
          value={
            priorityGroup?.lag === null
              ? "未知"
              : displayNumber(priorityGroup?.lag)
          }
          detail={`${priorityGroupName} · 尚未投递`}
          help="Linkd 目标消费组尚未收到的 Stream entry 数；这是判断消费是否追上生产的主指标。"
        />
        <Fact
          label="PEL Pending"
          value={displayNumber(priorityGroup?.pending)}
          detail={`${priorityGroupName} · 已投递未 XACK`}
          help="已经投递给 Linkd consumer、但尚未 XACK 的 entry 数；需结合 idle 和接管状态判断是否卡住。"
        />
        <Fact
          label="近似未完成"
          value={
            priorityGroup?.lag === null || priorityGroup === undefined
              ? "未知"
              : displayNumber(priorityGroup.lag + priorityGroup.pending)
          }
          detail={`${priorityGroupName} · lag + pending`}
          help="Cleaner 背压使用的近似 Signal 数，不是精确 Event 数；冗余空唤醒会造成保守高估。"
        />
      </div>
      <div className="redis-fact-grid redis-signal-context-grid">
        <Fact
          label="Stream length"
          value={displayNumber(stream?.length)}
          detail="包含已确认但尚未裁剪的 entry"
          help="Signal Stream 当前保留的 entry 数，不等同于待处理量。"
        />
        <Fact
          label="Consumer Groups"
          value={displayNumber(stream?.groupsCount)}
          detail={`期望 ${data.expectedGroup ?? "—"}`}
          help="Signal Stream 上现有 consumer group 数。"
        />
        <Fact
          label="软上限"
          value={displayNumber(stream?.maxEntries)}
          detail={`超出 ${displayNumber(stream?.entriesAboveMax)}`}
          help="配置允许保留的目标 entry 数；为保护未确认数据可能暂时超出。"
        />
        <Fact
          label="最老 Entry"
          value={formatDurationSeconds(stream?.oldestEntryAgeSeconds)}
          detail={stream?.firstEntryId ?? "暂无 entry"}
          help="当前仍保留的最早 Stream entry 距今时间。"
        />
      </div>
      <div className="redis-stream-background" aria-label="Stream 背景统计">
        <span>背景统计</span>
        <div>
          <HelpLabel
            label="累计写入"
            help="Redis 记录的该 Stream 自创建以来累计写入 entry 数；它不是当前积压。"
          />
          <strong>{displayNumber(stream?.entriesAdded)}</strong>
        </div>
        <div>
          <HelpLabel
            label="Stream 内存"
            help="Signal Stream key 当前占用的近似 Redis 内存。"
          />
          <strong>{formatBytes(stream?.memoryBytes)}</strong>
          <small>{data.streamKey ?? "未配置"}</small>
        </div>
      </div>
      <DataSection
        title="Consumer Groups"
        caption="点击详情后查看 Consumer 与对应 PEL。"
        status={data.status}
        message={data.message}
      >
        <div className="redis-table-wrap">
          <table className="redis-table">
            <thead>
              <tr>
                <HelpTableHeader
                  label="Group"
                  help="消费 Signal Stream 的 Redis consumer group。"
                />
                <HelpTableHeader
                  label="Consumers"
                  help="该 group 当前登记的 consumer 数。"
                />
                <HelpTableHeader
                  label="Lag"
                  help="尚未投递给该 group 的 Stream entry 数。"
                />
                <HelpTableHeader
                  label="PEL pending"
                  help="已投递但尚未 XACK、仍在 Pending Entries List 中的数量。"
                />
                <HelpTableHeader
                  label="Last delivered"
                  help="该 group 最近投递过的 Stream ID。"
                />
                <th />
              </tr>
            </thead>
            <tbody>
              {data.groups.map((group) => (
                <tr
                  key={group.name}
                  className={
                    selectedGroup?.name === group.name ? "selected" : ""
                  }
                >
                  <td>
                    <span className="redis-id">{group.name}</span>
                    {group.expected && <small>EXPECTED</small>}
                  </td>
                  <td>{group.consumersCount}</td>
                  <td>
                    {group.lag === null ? "未知" : group.lag.toLocaleString()}
                  </td>
                  <td>{group.pending.toLocaleString()}</td>
                  <td>
                    <span className="redis-id">
                      {group.lastDeliveredId || "—"}
                    </span>
                  </td>
                  <td>
                    <button
                      type="button"
                      onClick={() => setSelectedGroupName(group.name)}
                    >
                      查看详情 →
                    </button>
                  </td>
                </tr>
              ))}
              {data.groups.length === 0 && (
                <EmptyRow
                  columns={6}
                  text={data.message ?? "当前没有 Consumer Group"}
                />
              )}
            </tbody>
          </table>
        </div>
      </DataSection>
      {selectedGroup && (
        <div className="redis-detail-panel">
          <header>
            <div>
              <p className="eyebrow">GROUP DETAIL</p>
              <h3>{selectedGroup.name}</h3>
            </div>
            <StatusBadge value={selectedGroup.consumersStatus} />
          </header>
          <div className="redis-detail-summary">
            <span>
              <HelpLabel
                label="Lag"
                help="尚未投递给该 group 的 Stream entry 数。"
              />{" "}
              <strong>
                {selectedGroup.lag === null
                  ? "未知"
                  : selectedGroup.lag.toLocaleString()}
              </strong>
            </span>
            <span>
              <HelpLabel label="PEL" help="已投递但尚未 XACK 的 entry 数。" />{" "}
              <strong>{selectedGroup.pending.toLocaleString()}</strong>
            </span>
            <span>
              <HelpLabel
                label="entries-read"
                help="该 group 累计读取的 Stream entry 数，由 Redis 维护。"
              />{" "}
              <strong>
                {selectedGroup.entriesRead === null
                  ? "未知"
                  : selectedGroup.entriesRead.toLocaleString()}
              </strong>
            </span>
            <span>
              <HelpLabel
                label="接管阈值"
                help="Pending entry 达到该 idle 时长后可被其他 consumer 接管。"
              />{" "}
              <strong>
                {data.claimMinIdleSeconds
                  ? `${data.claimMinIdleSeconds}s`
                  : "—"}
              </strong>
            </span>
          </div>
          <div className="redis-inline-filters">
            <label>
              <HelpLabel
                label="Consumer 关键字"
                help="按 consumer 名称包含关系过滤当前 group 成员。"
              />
              <input
                aria-label="Consumer 关键字"
                value={consumerFilter}
                onChange={(event) => setConsumerFilter(event.target.value)}
                placeholder="名称包含…"
              />
            </label>
            <label className="redis-check">
              <input
                type="checkbox"
                checked={claimEligibleOnly}
                onChange={(event) => setClaimEligibleOnly(event.target.checked)}
              />
              只看达到接管阈值的 PEL
            </label>
          </div>
          <h4>Consumers</h4>
          <div className="redis-table-wrap">
            <table className="redis-table compact">
              <thead>
                <tr>
                  <HelpTableHeader
                    label="Consumer"
                    help="Redis consumer group 中登记的 consumer 名称。"
                  />
                  <HelpTableHeader
                    label="Pending"
                    help="当前归属于该 consumer 且尚未 XACK 的 entry 数。"
                  />
                  <HelpTableHeader
                    label="Idle (ms)"
                    help="距 consumer 最近一次尝试交互的毫秒数。"
                  />
                  <HelpTableHeader
                    label="Inactive (ms)"
                    help="距 consumer 最近一次成功交互的毫秒数。"
                  />
                </tr>
              </thead>
              <tbody>
                {filteredConsumers.map((consumer) => (
                  <tr key={consumer.name}>
                    <td>
                      <span className="redis-id">{consumer.name}</span>
                    </td>
                    <td>{consumer.pending}</td>
                    <td>{displayNumber(consumer.idleMilliseconds)}</td>
                    <td>{displayNumber(consumer.inactiveMilliseconds)}</td>
                  </tr>
                ))}
                {filteredConsumers.length === 0 && (
                  <EmptyRow columns={4} text="没有匹配的 Consumer" />
                )}
              </tbody>
            </table>
          </div>
          <p className="redis-field-note">
            Idle 是距最近一次尝试交互，Inactive
            是距最近一次成功交互；低流量下数值较大不等于 Consumer 离线。
          </p>
          <h4>Pending Entries</h4>
          {pendingLoading ? (
            <div className="redis-empty-state">正在读取 PEL…</div>
          ) : (
            <div className="redis-table-wrap">
              <table className="redis-table compact">
                <thead>
                  <tr>
                    <HelpTableHeader
                      label="Stream ID"
                      help="Redis 为该 Signal entry 生成的唯一 Stream ID。"
                    />
                    <HelpTableHeader
                      label="Consumer"
                      help="当前拥有该 Pending entry 的 consumer。"
                    />
                    <HelpTableHeader
                      label="Idle (ms)"
                      help="该 entry 自最近一次投递后未确认的时长。"
                    />
                    <HelpTableHeader
                      label="投递次数"
                      help="该 entry 被投递或接管的累计次数。"
                    />
                    <HelpTableHeader
                      label="接管阈值"
                      help="当前 idle 是否已达到 claim_min_idle；不等于消息一定卡住。"
                    />
                  </tr>
                </thead>
                <tbody>
                  {filteredPending.map((item) => (
                    <tr key={item.id}>
                      <td>
                        <span className="redis-id">{item.id}</span>
                      </td>
                      <td>
                        <span className="redis-id">{item.consumer}</span>
                      </td>
                      <td>{item.idleMilliseconds.toLocaleString()}</td>
                      <td>{item.deliveryCount}</td>
                      <td>
                        <StatusBadge
                          value={item.claimEligible ? "达到阈值" : "阈值内"}
                        />
                      </td>
                    </tr>
                  ))}
                  {filteredPending.length === 0 && (
                    <EmptyRow
                      columns={5}
                      text={pending?.message ?? "当前没有匹配的 Pending entry"}
                    />
                  )}
                </tbody>
              </table>
            </div>
          )}
          <p className="redis-field-note">
            达到阈值只表示本次快照中的 idle 已超过 claim_min_idle；消息可能正被
            XAUTOCLAIM 并发接管，不据此判定为 stuck。
          </p>
        </div>
      )}
    </div>
  );
}

function MailboxesTab({
  summary,
  result,
  loading,
  filter,
  setFilter,
  selected,
  setSelected,
}: {
  summary: Awaited<ReturnType<typeof getRedisInfrastructure>>["mailbox"];
  result?: RedisMailboxResponse;
  loading: boolean;
  filter: string;
  setFilter: (value: string) => void;
  selected?: RedisMailboxResponse["items"][number];
  setSelected: (
    value: RedisMailboxResponse["items"][number] | undefined,
  ) => void;
}) {
  return (
    <div role="tabpanel" className="redis-tab-panel">
      <TabHeading
        eyebrow="MAILBOX"
        title="Mailbox 调度"
        description="每个 Mailbox 只用一个 List 保存待处理 Event ID；允许少量重复引用，空到非空时写入 Signal。"
        status={summary.status}
      />
      <div className="redis-fact-grid">
        <Fact
          label="Active Mailboxes"
          value={displayNumber(summary.activeMailboxes)}
          detail={summary.scanTruncated ? "有界扫描，实际更多" : "本次完整扫描"}
          help="当前包含至少一个待处理 Event 的 Mailbox 数。"
        />
        <Fact
          label="单 Mailbox 上限"
          value={displayNumber(summary.maxPendingPerMailbox)}
          detail={`单次 drain ${displayNumber(summary.maxDrainEvents)}`}
          help="一个关联键允许排队的 Event 最大数量。"
        />
      </div>
      <DataSection
        title="Mailbox 列表"
        caption="按队列深度倒序；关键字匹配 Mailbox ID 或队首 Event ID。"
        status={result?.status ?? summary.status}
        message={result?.message ?? summary.message}
      >
        <div className="redis-inline-filters">
          <label>
            <HelpLabel
              label="关键字"
              help="匹配 Mailbox ID 或队首 Event ID。"
            />
            <input
              aria-label="关键字"
              value={filter}
              onChange={(event) => setFilter(event.target.value)}
              placeholder="Mailbox ID / Event ID"
            />
          </label>
        </div>
        {loading ? (
          <div className="redis-empty-state">正在扫描配置前缀…</div>
        ) : (
          <div className="redis-table-wrap">
            <table className="redis-table">
              <thead>
                <tr>
                  <HelpTableHeader
                    label="Mailbox ID"
                    help="由租户、EventSource 与 fingerprint 派生的不可逆关联键。"
                  />
                  <HelpTableHeader
                    label="Events"
                    help="Mailbox List 中排队的 Event ID 数量。"
                  />
                  <th />
                </tr>
              </thead>
              <tbody>
                {(result?.items ?? []).map((item) => (
                  <tr
                    key={item.mailboxId}
                    className={
                      selected?.mailboxId === item.mailboxId ? "selected" : ""
                    }
                  >
                    <td>
                      <span className="redis-id">{item.mailboxId}</span>
                    </td>
                    <td>{item.eventCount}</td>
                    <td>
                      <button type="button" onClick={() => setSelected(item)}>
                        查看详情 →
                      </button>
                    </td>
                  </tr>
                ))}
                {(result?.items.length ?? 0) === 0 && (
                  <EmptyRow
                    columns={3}
                    text={result?.message ?? "当前没有待处理 Mailbox"}
                  />
                )}
              </tbody>
            </table>
          </div>
        )}
      </DataSection>
      {selected && (
        <div className="redis-detail-panel">
          <header>
            <div>
              <p className="eyebrow">MAILBOX DETAIL</p>
              <h3>{selected.mailboxId}</h3>
            </div>
            <button type="button" onClick={() => setSelected(undefined)}>
              关闭
            </button>
          </header>
          <div className="redis-detail-summary">
            <span>
              <HelpLabel
                label="List"
                help="按处理顺序保存待处理 Event ID 的队列。"
              />{" "}
              <strong>{selected.eventCount}</strong>
            </span>
          </div>
          <KeyValuePanel
            title="队首"
            rows={[
              [
                "Head Event ID",
                selected.headEventId ? (
                  <Link
                    to={`/explore/events?id=${encodeURIComponent(selected.headEventId)}`}
                  >
                    {selected.headEventId}
                  </Link>
                ) : (
                  "—"
                ),
              ],
            ]}
          />
          <p className="redis-field-note">
            Mailbox ID 是租户、EventSource 和 fingerprint
            计算出的不可逆关联键，页面不会从 key 猜测来源事实。
          </p>
        </div>
      )}
    </div>
  );
}

function LeasesTab({
  summary,
  result,
  loading,
  filter,
  setFilter,
  selected,
  setSelected,
}: {
  summary: Awaited<ReturnType<typeof getRedisInfrastructure>>["leases"];
  result?: RedisLeaseResponse;
  loading: boolean;
  filter: string;
  setFilter: (value: string) => void;
  selected?: RedisLeaseResponse["items"][number];
  setSelected: (value: RedisLeaseResponse["items"][number] | undefined) => void;
}) {
  return (
    <div role="tabpanel" className="redis-tab-panel">
      <TabHeading
        eyebrow="LEASE"
        title="Lease / Lock"
        description="SET NX PX 保证同一 Mailbox 跨进程串行；这里只读取 key 与 PTTL。"
        status={summary.status}
      />
      <div className="redis-fact-grid">
        <Fact
          label="Active Leases"
          value={displayNumber(summary.activeLeases)}
          detail={
            summary.scanTruncated ? "有界扫描，实际更多" : "短生命周期快照"
          }
          help="本次扫描发现的有效 Mailbox lease 数。"
        />
        <Fact
          label="Lease TTL"
          value={formatDurationSeconds(summary.ttlSeconds)}
          detail={`续租间隔 ${formatDurationSeconds(summary.renewIntervalSeconds)}`}
          help="新建 lease 的有效时长；处理中会按续租间隔延长。"
        />
      </div>
      <DataSection
        title="活跃 Lease"
        caption="不会读取随机 token，也不会从 key 或 consumer 名推断 owner。"
        status={result?.status ?? summary.status}
        message={result?.message ?? summary.message}
      >
        <div className="redis-inline-filters">
          <label>
            <HelpLabel
              label="Mailbox ID"
              help="按不可逆关联键包含关系过滤 lease。"
            />
            <input
              aria-label="Mailbox ID"
              value={filter}
              onChange={(event) => setFilter(event.target.value)}
              placeholder="ID 包含…"
            />
          </label>
        </div>
        {loading ? (
          <div className="redis-empty-state">正在读取 lease PTTL…</div>
        ) : (
          <div className="redis-table-wrap">
            <table className="redis-table">
              <thead>
                <tr>
                  <HelpTableHeader
                    label="Mailbox ID"
                    help="该 lease 保护的 Mailbox 关联键。"
                  />
                  <HelpTableHeader
                    label="剩余 TTL (ms)"
                    help="本次 PTTL 读取时 lease key 剩余的毫秒数。"
                  />
                  <HelpTableHeader
                    label="过期状态"
                    help="Redis PTTL 对该 lease key 的当前判定。"
                  />
                  <th />
                </tr>
              </thead>
              <tbody>
                {(result?.items ?? []).map((item) => (
                  <tr
                    key={item.mailboxId}
                    className={
                      selected?.mailboxId === item.mailboxId ? "selected" : ""
                    }
                  >
                    <td>
                      <span className="redis-id">{item.mailboxId}</span>
                    </td>
                    <td>{displayNumber(item.ttlMilliseconds)}</td>
                    <td>
                      <StatusBadge value={leaseState(item.expiryState)} />
                    </td>
                    <td>
                      <button type="button" onClick={() => setSelected(item)}>
                        查看详情 →
                      </button>
                    </td>
                  </tr>
                ))}
                {(result?.items.length ?? 0) === 0 && (
                  <EmptyRow
                    columns={4}
                    text={result?.message ?? "当前没有活跃 lease"}
                  />
                )}
              </tbody>
            </table>
          </div>
        )}
      </DataSection>
      {selected && (
        <div className="redis-detail-panel">
          <header>
            <div>
              <p className="eyebrow">LEASE DETAIL</p>
              <h3>{selected.mailboxId}</h3>
            </div>
            <button type="button" onClick={() => setSelected(undefined)}>
              关闭
            </button>
          </header>
          <KeyValuePanel
            title="当前快照"
            rows={[
              [
                "剩余 TTL",
                selected.ttlMilliseconds === null
                  ? "无可用 TTL"
                  : `${selected.ttlMilliseconds.toLocaleString()} ms`,
              ],
              ["过期状态", leaseState(selected.expiryState)],
              ["Owner", "不可用：当前 lease value 只存随机 token"],
            ]}
          />
          <p className="redis-field-note">
            PTTL 在读取过程中可能变为 -2，表示 key
            已于两次命令之间自然消失；这不是处理失败证据。
          </p>
        </div>
      )}
    </div>
  );
}

function TabHeading({
  eyebrow,
  title,
  description,
  status,
}: {
  eyebrow: string;
  title: string;
  description: string;
  status: RedisSectionStatus;
}) {
  return (
    <div className="redis-tab-heading">
      <div>
        <p className="eyebrow">{eyebrow}</p>
        <h2>{title}</h2>
        <p>{description}</p>
      </div>
      <StatusBadge value={status} />
    </div>
  );
}

function Fact({
  label,
  value,
  detail,
  help,
}: {
  label: string;
  value: ReactNode;
  detail: ReactNode;
  help: ReactNode;
}) {
  return (
    <article className="redis-fact">
      <HelpLabel label={label} help={help} />
      <strong>{value}</strong>
      <small>{detail}</small>
    </article>
  );
}

function KeyValuePanel({
  title,
  rows,
}: {
  title: string;
  rows: Array<[string, ReactNode]>;
}) {
  return (
    <article className="redis-key-value">
      <h3>{title}</h3>
      {rows.map(([label, value]) => (
        <div key={label}>
          <HelpLabel
            label={label}
            help={redisFieldHelp[label] ?? "该字段来自当前 Redis 只读快照。"}
          />
          <strong>{value}</strong>
        </div>
      ))}
    </article>
  );
}

function DataSection({
  title,
  caption,
  status,
  message,
  children,
}: {
  title: string;
  caption: string;
  status: RedisSectionStatus;
  message?: string;
  children: ReactNode;
}) {
  return (
    <article className="redis-data-section">
      <header>
        <div>
          <h3>{title}</h3>
          <p>{caption}</p>
          {message && <small>{message}</small>}
        </div>
        <StatusBadge value={status} />
      </header>
      {children}
    </article>
  );
}

function EmptyRow({ columns, text }: { columns: number; text: string }) {
  return (
    <tr>
      <td colSpan={columns}>
        <div className="redis-empty-state">{text}</div>
      </td>
    </tr>
  );
}

function displayNumber(value: number | null | undefined): string {
  return value === null || value === undefined ? "—" : value.toLocaleString();
}

function formatBytes(value: number | null | undefined): string {
  if (value === null || value === undefined) return "—";
  if (value < 1024) return `${value} B`;
  const units = ["KiB", "MiB", "GiB", "TiB"];
  let amount = value;
  let index = -1;
  do {
    amount /= 1024;
    index += 1;
  } while (amount >= 1024 && index < units.length - 1);
  return `${amount.toFixed(amount >= 10 ? 1 : 2)} ${units[index]}`;
}

function formatDurationSeconds(value: number | null | undefined): string {
  if (value === null || value === undefined) return "—";
  if (value < 60) return `${value.toFixed(value < 10 ? 1 : 0)}s`;
  if (value < 3600) return `${(value / 60).toFixed(1)}m`;
  if (value < 86400) return `${(value / 3600).toFixed(1)}h`;
  return `${(value / 86400).toFixed(1)}d`;
}

function formatMilliseconds(value: number | null | undefined): string {
  if (value === null || value === undefined) return "—";
  return value < 1000
    ? `${value.toLocaleString()} ms`
    : formatDurationSeconds(value / 1000);
}

function formatRate(value: number | undefined, unit: string): string {
  return value === undefined ? "—" : `${value.toLocaleString()} ${unit}`;
}

function formatUnixSeconds(
  value: number | undefined,
  mode: "local" | "utc",
): string {
  return value === undefined
    ? "—"
    : formatTime(new Date(value * 1000).toISOString(), mode);
}

function yesNo(value: boolean | undefined): string {
  return value === undefined ? "未知" : value ? "是" : "否";
}

function leaseState(
  value: RedisLeaseResponse["items"][number]["expiryState"],
): string {
  if (value === "no_expiry") return "无过期时间";
  if (value === "gone") return "读取时已消失";
  return "按 TTL 过期";
}

function isRedisTab(value: string | null): value is RedisTab {
  return tabs.some((tab) => tab.id === value);
}
