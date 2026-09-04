# Linkd DevTools

Linkd DevTools 是只监听 loopback 的只读运行感知工具。React 页面只访问本机 `/local-api/*`；Node
连接层直接读取 Linkd YAML，并查询 Prometheus、Kafka、Redis、MySQL 和 Elasticsearch。它不提供
Linkd 业务 API，也不参与消息消费、确认或数据写入。

## 启动

要求 Node.js 24 和 pnpm 11：

```bash
cd devtools
pnpm install
cp ../configs/linkd.yaml ../configs/linkd.local.yaml
# 仅在被 Git 忽略的 local 文件中填写本机凭据。
export LINKD_CONFIG=../configs/linkd.local.yaml
export LINKD_DEVTOOLS_PROMETHEUS_URL=http://127.0.0.1:9090
pnpm dev
```

也可以使用 `pnpm dev -- --config /path/to/linkd.yaml`。开发页面为 `127.0.0.1:5173`，Node 连接层
默认为 `127.0.0.1:4399`。

DevTools 不再维护第二份基础设施 YAML。以下环境变量只覆盖 DevTools 自身行为或只读凭据：

- `LINKD_DEVTOOLS_HOST`、`LINKD_DEVTOOLS_PORT`
- `LINKD_DEVTOOLS_PROMETHEUS_URL`
- `LINKD_DEVTOOLS_PROMETHEUS_API_KEY`
- `LINKD_DEVTOOLS_PROMETHEUS_USERNAME`、`LINKD_DEVTOOLS_PROMETHEUS_PASSWORD`
- `LINKD_DEVTOOLS_MYSQL_PASSWORD`
- `LINKD_DEVTOOLS_ELASTICSEARCH_API_KEY`、`LINKD_DEVTOOLS_ELASTICSEARCH_PASSWORD`
- `LINKD_DEVTOOLS_REDIS_PASSWORD`
- `LINKD_DEVTOOLS_TIMEOUT_MILLISECONDS`、`LINKD_DEVTOOLS_MAX_RANGE_SECONDS`

Kafka TLS 相对文件路径按 Linkd 配置文件所在目录解析。浏览器只能看到脱敏后的连接摘要。

## 页面与数据来源

- 系统总览：Prometheus 进程、实际 Cleaner/Lifecycle 链路和配置/运行差异。
- Cleaner：EventSource、Kafka partition、transform、Event store、Mailbox 和 Kafka confirm。
- Lifecycle：Redis Stream/PEL、Mailbox drain、lease、Event 裁决和 FinalHook。
- Control Plane：四个固定管理任务的 owner、依赖、周期、最近结果、耗时和收敛工作量。
- Events、Alerts、AlertLogs：只读列表、详情、关联跳转和当前 schema 能力内的统计。
- Kafka、Redis、Elasticsearch：实时只读基础设施状态。
- Configuration：Linkd YAML 的脱敏有效摘要。

处理状态、Cleaner、Lifecycle、Control Plane 与 Redis 默认每 15 秒刷新，Kafka 与 Elasticsearch 默认每 30 秒刷新。
这些页面在页头统一显示状态、最后成功时间、手动刷新和自动刷新开关；暂停后仍可手动刷新。

历史处理趋势来自 Prometheus。Kafka assignment/offset/lag、Redis PEL、目标 Signal Group 的
`lag + pending` 和 Mailbox List 扫描是 DevTools 请求时读取的当前快照，不会被伪装成历史时序。
处理状态、Cleaner、Lifecycle、Control Plane 和 Kafka 的 Prometheus 图表统一提供 `15m`、`1h`、`6h`、
`24h` 和 `7d` 查询时间范围，默认 `1h`；采样步长随所选范围调整。页面同时提供独立的“计算窗口”，
默认 1 分钟，可选择 30 秒、1 分钟、2 分钟、5 分钟或 15 分钟；该窗口直接用于 `rate()`、`increase()`
和 histogram quantile。计算窗口建议不小于 Prometheus scrape interval 的两倍，窗口越短越及时，但更容易抖动或因样本不足无数据。

Kafka 页面使用 `Input Topics` 与 `Output Topics` 一级 Tab 分开呈现消费端和生产端事实，
并把每个 EventSource consumer group 和 Lifecycle FinalHook Output 作为独立资源展示。
Input partition 的 `lowOffset`、`highOffset`、`committedOffset` 与 `lag` 均使用十进制字符串，
缺失值或 Kafka 的 `-1` 表示未知，不会补成 `0`；`issues` 提供 group、leader、ISR、owner
和 committed 的结构化异常。Output 展示配置的 producer client、topic metadata 和 offset 边界，
不提供 consumer owner、committed 或 lag，且 LEO 不代表 Linkd 的独自产量。

Redis 页面按 `实例总览`、`信号队列`、`Mailbox 调度`、`Lease / Lock` 四种 Linkd 用途分区。
Signal 页面分别呈现 Stream length、Group lag、PEL pending 与两者之和的近似积压；Mailbox 和 lease 只在配置的 key prefix
内执行有界扫描。页面不提供任意 key 浏览或 Redis 命令入口，不返回 Stream payload、key value、
lease token，也不会依据 Consumer idle 推断实例离线。部分 Redis 查询失败时，无法确认的计数返回
`null`，页面显示为未知，不会补成 `0`。

Control Plane 页面按 `elasticsearch-schema-and-active-reconciler`、`elasticsearch-bucket-manager`、
`elasticsearch-alert-archiver` 和 `redis-stream-manager` 展示真实任务。页面不复制 ES 集群健康或索引容量，
只读取固定 Active alias 的终态 Alert 数作为归档 backlog；Archiver 展示空闲/重试间隔、批量上限、Worker 数和
最近批次结果，并把 Redis 的 Pending/lag 作为安全裁剪决策依据。
Elasticsearch Repository 即使没有显式 `control_plane.elasticsearch` 也会使用默认周期启用前三个任务；
Redis Stream 任务必须显式配置。

## 本地 API

```text
GET /local-api/capabilities
GET /local-api/config
GET /local-api/runtime/processes
GET /local-api/runtime/cleaner
GET /local-api/runtime/lifecycle
GET /local-api/runtime/control-plane
GET /local-api/infrastructure/kafka
GET /local-api/infrastructure/redis
GET /local-api/infrastructure/redis/pending
GET /local-api/infrastructure/redis/mailboxes
GET /local-api/infrastructure/redis/leases
GET /local-api/elasticsearch/topology
GET /local-api/metrics
GET /local-api/{events|alerts|alert-logs}
GET /local-api/{events|alerts|alert-logs}/stats
GET /local-api/{events|alerts|alert-logs}/:id
```

接口只使用固定查询模板，不接受任意 SQL、PromQL、Redis 命令、Kafka Admin 写操作或 ES target。
实体列表默认最近一小时、50 条，最大七天、单页 200 条；精确 ID 可以省略时间范围。

## 存储统计能力

统计不会修改 MySQL schema 或 Elasticsearch mapping：

| 对象     | MySQL                               | Elasticsearch                                    |
| -------- | ----------------------------------- | ------------------------------------------------ |
| Event    | received 趋势、state、related Alert | received 趋势、EventSource、state、related Alert |
| Alert    | 当前 status、EventSource、severity  | update 趋势、status、EventSource、severity       |
| AlertLog | created 趋势、operation/operator    | created 趋势、operation/operator                 |

除低基数的 AlertLog `operation_kind/operator_kind` 外，开放 JSON 字段只用于已有的精确调试过滤，
不用于大范围聚合。所有统计继续受时间范围、查询超时和返回数量上限约束。

## 安全边界

- Node 只允许 loopback host。
- 所有基础设施客户端只执行读操作。
- 凭据、私钥、完整 payload 和未脱敏错误不返回浏览器。
- Kafka/Redis/ES 某一数据源失败只让对应区域显示 `partial/unavailable`。
- 指标不包含 tenant、Event/Alert ID、fingerprint、topic、group 或 payload。

## 验证

```bash
pnpm check
pnpm test:e2e
```
