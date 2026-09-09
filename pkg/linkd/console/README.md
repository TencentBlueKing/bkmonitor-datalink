# Linkd Console

Linkd Console 是运行感知与来源配置工具，默认本地模式只监听 loopback。React 页面只访问同源 `/local-api/*`；Node
连接层读取静态连接配置并代理正式 EventSource API，其他基础设施和实体查询保持只读。
它不参与消息消费或确认，不直接写入业务存储。

## 启动

要求 Node.js 24 和 pnpm 11：

```bash
cd console
pnpm install
cp ../configs/linkd.yaml ../configs/linkd.local.yaml
# 仅在被 Git 忽略的 local 文件中填写本机凭据。
export LINKD_CONFIG=../configs/linkd.local.yaml
export LINKD_CONSOLE_PROMETHEUS_URL=http://127.0.0.1:9090
pnpm dev
```

也可以使用 `pnpm dev -- --config /path/to/linkd.yaml`。开发页面为 `127.0.0.1:5173`，Node 连接层
默认为 `127.0.0.1:4399`。

Console 不再维护第二份基础设施 YAML。以下环境变量只覆盖 Console 自身行为或只读凭据：

- `LINKD_CONSOLE_HOST`、`LINKD_CONSOLE_PORT`
- `LINKD_CONSOLE_PROMETHEUS_URL`
- `LINKD_CONSOLE_PROMETHEUS_API_KEY`
- `LINKD_CONSOLE_PROMETHEUS_USERNAME`、`LINKD_CONSOLE_PROMETHEUS_PASSWORD`
- `LINKD_CONSOLE_MYSQL_PASSWORD`
- `LINKD_CONSOLE_ELASTICSEARCH_API_KEY`、`LINKD_CONSOLE_ELASTICSEARCH_PASSWORD`
- `LINKD_CONSOLE_REDIS_PASSWORD`
- `LINKD_CONSOLE_REDIS_SENTINEL_PASSWORD`
- `LINKD_CONSOLE_TIMEOUT_MILLISECONDS`、`LINKD_CONSOLE_MAX_RANGE_SECONDS`

Kafka TLS 相对文件路径按 Linkd 配置文件所在目录解析。浏览器只能看到脱敏后的连接摘要。

## 容器镜像

GitHub Actions 的手动打包入口、GHCR 地址和版本约定见 [手动构建与发布镜像](../docs/guides/image-release.md)。

在 Linkd 模块根目录构建：

```bash
make console-image IMAGE_TAG=dev
# 或直接使用 Docker；构建上下文必须是 console。
docker build -t linkd-console:dev console
```

`CONSOLE_IMAGE` 指定镜像仓库，`IMAGE_TAG` 默认使用 Git 短哈希；`NODE_IMAGE` 指定 Node 基础镜像。
`VERSION` 默认等于 IMAGE_TAG，`GIT_COMMIT` 默认取完整 Git SHA，两者写入镜像构建信息和 OCI 标签。

```bash
docker run --rm linkd-console:<tag> version
# 或 --version；无需配置连接或 Basic Auth 即可查询版本。
```

输出 `version` 和 `git_commit`。运行中的 Console 也可通过受认证保护的 `/local-api/version` 查询。
直接从源码启动且没有构建信息文件时显示 dev / unknown。
构建器默认内存参数为 `CONSOLE_BUILD_FLAGS="--memory 4G"`，可按本地构建器调整；
`NODE_BUILD_OPTIONS` 默认限制构建阶段 V8 heap 为 3 GiB，不影响运行镜像。
Dockerfile 分阶段编译网页与 Node 服务，运行镜像仅保留生产依赖和构建产物，以 `node` 用户启动。

容器默认启用 server 模式和 Basic Auth，监听 `0.0.0.0:4399`；缺少认证凭据时启动失败。
在本地私有文件 `/secure/linkd-console.env` 配置以下变量，再通过 `--env-file` 注入：

```dotenv
LINKD_CONSOLE_BASIC_AUTH_USERNAME=<username>
LINKD_CONSOLE_BASIC_AUTH_PASSWORD=<password>
```

```bash
docker run --rm --name linkd-console \
  -p 127.0.0.1:4399:4399 \
  --env-file /secure/linkd-console.env \
  -v /path/to/linkd.yaml:/app/configs/linkd.yaml:ro \
  linkd-console:dev
```

配置文件必须可被容器内 UID 1000 读取；基础设施地址需从容器内可达，不能沿用宿主机 loopback 地址。
如配置引用 TLS 文件，需一并只读挂载并保持相对配置路径有效。
可通过 `LINKD_CONFIG` 改用其他配置路径。远程访问需在入口配置 HTTPS；Kubernetes 部署见
[Helm 指南](../docs/guides/helm.md)。

## 页面与数据来源

- 系统总览：按完成速率与积压、等待位置、失败恢复分组诊断；各阶段延迟单独展示，不合计 P99。
- Cleaner：EventSource、Kafka partition、transform、Event store、Mailbox 和 Kafka confirm。
- Lifecycle：Event 处理与 Signal 调度分离；独立 Enrich 区域展示同步丰富总体状态、在途调用、平均/P95/P99、Processor 状态与 P99、诊断、DataSource 调用与 P99、payload 大小；独立 ES 合批区展示范围执行次数、提交/成功/失败操作数、每批大小、字节数、排队与执行耗时，并显示配置推导值。
- Control Plane：四个固定管理任务的 owner、依赖、周期、最近结果、耗时和收敛工作量。
- Events、Alerts、AlertLogs：只读列表、详情、关联跳转和当前 schema 能力内的统计。
- Kafka、Redis、Elasticsearch：实时只读基础设施状态；ES 节点快照单独显示 CPU、heap、write active/queue、累计 rejected、当前 merge 和未提交 translog，不把累计量解释为待处理队列。
- Configuration：Linkd YAML 的脱敏有效摘要。

处理状态、Cleaner、Lifecycle、Control Plane 与 Redis 默认每 15 秒刷新，Kafka 与 Elasticsearch 默认每 30 秒刷新。
这些页面在页头统一显示状态、最后成功时间、手动刷新和自动刷新开关；暂停后仍可手动刷新。

历史处理趋势来自 Prometheus。Kafka assignment/offset/lag、Redis PEL、目标 Signal Group 的
`lag + pending` 和 Mailbox List 扫描是 Console 请求时读取的当前快照，不会被伪装成历史时序。
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

- 本地模式只允许 loopback host；显式 server 模式允许非回环监听，并强制 Basic Auth。
- 所有基础设施客户端只执行读操作。
- 凭据、私钥、完整 payload 和未脱敏错误不返回浏览器。
- Kafka/Redis/ES 某一数据源失败只让对应区域显示 `partial/unavailable`。
- 指标不包含 tenant、Event/Alert ID、fingerprint、topic、group 或 payload。

## 验证

容器构建和 Kubernetes 安装见 [Helm 指南](../docs/guides/helm.md)。服务部署新增环境变量：

- `LINKD_CONSOLE_BASE_PATH`：默认 `/`；例如 `/kingeye-web-saas--kingeye-web--saas/linkd`。路径段支持字母、数字、下划线、连字符，最长 256 字符，子路径不以 `/` 结尾。生产镜像运行时生效，Ingress 保留完整路径转发，页面和 API 共用该前缀。Vite 本地开发仍使用根路径。
- `LINKD_CONSOLE_MODE`：`local`（默认）或 `server`；server 模式要求 Basic Auth。
- `LINKD_CONSOLE_BASIC_AUTH_ENABLED`：`true` / `false`，默认 false。
- `LINKD_CONSOLE_BASIC_AUTH_USERNAME`、`LINKD_CONSOLE_BASIC_AUTH_PASSWORD`：启用时均必填，用户名不能包含冒号。

认证覆盖页面、静态资源和所有 API；服务端不返回认证凭据。远程入口应使用 HTTPS。
默认仍支持原有本地启动方式。生产静态页面的认证浏览器测试：

```bash
pnpm build
pnpm exec playwright test --config playwright.auth.config.ts
LINKD_CONSOLE_AUTH_TEST_BASE_PATH=/kingeye-web-saas--kingeye-web--saas/linkd pnpm exec playwright test --config playwright.auth.config.ts
```

```bash
pnpm check
pnpm test:e2e
```

## EventSource 管理与来源队列

Event Sources 页面通过正式控制面 API 增删改来源、enabled、标签和两种角色的 replicas，并展示 Kafka 上限、有效目标、worker 标签和实际分区。
需要设置 Linkd YAML 的 dispatch.url（或 LINKD_CONTROL_PLANE_URL）和服务端 LINKD_API_TOKEN；token 不下发浏览器。
来源配置不再以启动 YAML 为运行权威，编辑时省略 security 保留旧凭据，禁止把脱敏占位内容提交为凭据。
Redis 页面可按来源选择派生 Stream、Mailbox 和 lease。Kafka 输入诊断采用中心元数据和 worker ownership 报告，未重复采集的 offset/ISR 显示未知。
本次新增的配置写入仅代理控制面，实体存储和其他运维查询仍只读。
