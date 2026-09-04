# 部署模式与进程拓扑

本文定义 Linkd 后续交付的两种正式部署模式。两种模式共享同一套领域模型、配置语义、存储结构和
消息协议，区别只在运行职责如何装配和扩缩容，不为不同模式维护两套业务实现。

这里的“进程”表示一种可独立部署和扩缩容的逻辑角色，不限定只能运行一个操作系统进程或一个
Pod。Kafka、Redis、MySQL、Elasticsearch 等外部依赖不包含在 `all-in-one` 进程内，仍需由部署环境提供。

## 模式总览

| 模式 | 进程角色 | 适用场景 | 主要限制 |
| --- | --- | --- | --- |
| `all-in-one` | 单进程聚合 Cleaner、Lifecycle 和控制面职责 | 本地测试、集成验证和小规模部署 | 单一故障域，不能按职责独立扩缩容，资源竞争更明显 |
| 三进程 | Cleaner、Lifecycle、控制面（API / Leader / Manager） | 需要独立扩缩容、故障隔离和高可用的部署 | 需要明确进程间契约、Leader 选举和滚动升级约束 |

小规模部署默认使用单副本 `all-in-one`。当任一处理阶段需要独立容量、独立发布或独立故障隔离时，
应切换到三进程模式，而不是继续在单进程内增加特殊调度规则。

## Command 入口

| Command | 类型 | `linkd.role` | Telemetry |
| --- | --- | --- | --- |
| `linkd run all-in-one` | 常驻进程 | `all-in-one` | 单个 endpoint 汇总进程内全部职责 |
| `linkd run cleaner` | 常驻进程 | `cleaner` | 独立 endpoint，包含 Cleaner、MQ 和 Repository 指标 |
| `linkd run lifecycle` | 常驻进程 | `lifecycle` | 独立 endpoint，包含 Mailbox、lease、Lifecycle 和输出指标 |
| `linkd run control-plane` | 常驻进程 | `control-plane` | 独立 endpoint；包含各 management task 与 Redis/ES 工作量指标 |
| `linkd storage prepare` | 一次性管理操作 | 无 | 不启动长期监听端口 |
| `linkd config ...` / `linkd version` | 本地辅助操作 | 无 | 不启动长期监听端口 |

Metrics 不是单独的部署角色。每个常驻进程都创建自己的 telemetry runtime；配置 Prometheus exporter
后分别暴露 `/metrics`。因此拆分 Cleaner 或 Lifecycle 不会失去对应指标，也不依赖控制面代为采集。

## All-in-one 模式

```text
linkd run all-in-one
  ├─ Cleaner
  ├─ Lifecycle
  └─ Control Plane
      ├─ API                 （规划）
      ├─ Leader Election     （规划）
      └─ Manager
```

该模式在一个进程内复用正式职责实现，不提供仅供测试的简化数据路径。任一内部服务异常退出时，进程
应取消其余服务，等待有界排空和资源释放后整体退出，由外部监督器决定是否重启，避免部分职责停止后
进程仍被误判为健康。

`all-in-one` 适合：

- 本地开发、真实依赖集成测试和演示环境；
- 数据量、来源数和请求量较小，单副本即可承担的部署；
- 可以接受 Cleaner、Lifecycle、API 和 Manager 同时升级、重启的环境。

它不等于无依赖的单机安装包，也不作为大规模生产部署的默认拓扑。Cleaner 和 Lifecycle 共用进程
CPU、内存和连接预算，控制面任务也可能与数据面竞争资源；容量评估必须按整个进程的峰值叠加进行。

## 三进程模式

```text
Raw Event MQ
    │
    ▼
Cleaner ── Event / Mailbox ──► Lifecycle ──► Alert output
    │              │                │
    └──────────────┴──── shared storage / Redis ───────┐
                                                       │
Client ──► Control Plane (API / Leader / Manager) ◄────┘
```

### Cleaner 进程

Cleaner 消费各 EventSource 的原始消息，完成来源解析、字段提取、severity 和 fingerprint 计算，持久化
Event 并写入 Lifecycle Mailbox，成功后才确认原消息。它不执行告警生命周期裁决，不对外提供管理 API，
也不负责存储结构维护。

Cleaner 可按 Kafka partition 和 EventSource 流量水平扩展；扩容不能突破单 partition 的顺序边界。所有
副本必须使用一致的 EventSource、Severity 和身份生成配置，否则同一输入可能得到不同的领域结果。

### Lifecycle 进程

Lifecycle 消费 Mailbox Signal，按 `(bk_tenant_id, event_source_id, fingerprint)` 获取 lease，加载 Event，
推进 Alert 和 AlertLog，并在最终状态变更后执行输出 Hook。它不重复解析来源 payload，也不承担 API 或
存储控制面任务。

Lifecycle 可按待处理 fingerprint 数量水平扩展。同一 fingerprint 的跨副本串行依赖 Redis lease；副本
退出前必须停止领取新任务，并在有界时间内完成或释放正在处理的任务。

### 控制面进程（API / Leader / Manager）

第三个进程使用 `linkd run control-plane` 启动，聚合低吞吐但需要统一所有权的控制面职责：

- **API**：提供查询、管理和健康诊断入口；不得绕过领域用例直接修改 Event、Alert 或 AlertLog；
- **Leader Election**：在多个控制面副本中选出唯一活动 Leader；未成为 Leader 的副本仍可提供无副作用
  的 API；
- **Manager**：仅由当前 Leader 监督单例维护任务。Elasticsearch Schema 与 Active 资源对账、时间桶预创建使用
  独立周期；终态 Alert 归档使用连续批量循环，只在空闲、无进展或请求失败时等待配置间隔。

控制面内部以有名称、可取消的 management task 作为扩展单元。Elasticsearch 当前注册
`elasticsearch-schema-and-active-reconciler`、`elasticsearch-bucket-manager` 和 `elasticsearch-alert-archiver`
三个任务；它们共享连接和 Leader 所有权，但不是独立进程入口。Archiver 会隔离单项失败并在内部重试，只有任务本身
异常退出才会触发进程级 fail-fast；后续控制面任务统一加入同一装配和监督器。不得为每个任务新增顶级常驻 command。

各项管理操作必须可重试且幂等。Leader 租约还必须配合可验证的任期或 fencing 约束，不能只凭进程内
布尔状态假定旧 Leader 已经停止写入。失去 Leader 身份时应立即取消 Manager 任务，但不应因此把仍可
提供 API 的 follower 判定为不健康。

Leader Election 完成前，`linkd run control-plane` 只能部署单副本，当前由它运行 Elasticsearch 管理任务。
该限制不能靠部署多个没有选举能力的副本绕过，否则会失去单例维护任务的所有权保证。

控制面副本数、API 请求量和管理任务并发分别设定上限。API 的水平扩展不应制造多个活动任务所有者；
耗时管理任务也不应长期占用 API 请求 goroutine。

## 进程间边界

- 主处理链路通过 MQ、Repository 和 Redis Mailbox 协作，不依赖 Cleaner 到 Lifecycle 的同步 RPC；
- 三种进程使用相同的租户作用域、稳定身份和版本化外部协议，拆分部署不得改变业务结果；
- 每个进程只校验并使用自身职责所需的配置和凭据，部署时按最小权限分别授权；
- 进程的并发、批次、重试、队列和关闭等待均有硬上限，并传播 `context.Context` 取消；
- Elasticsearch Repository runtime 使用独立 HTTP 连接池，按 Cleaner、Lifecycle 或 Control Plane 的
  并发预算派生每节点硬上限；达到上限的请求等待连接并服从自身 Context，不由 Transport 自动重试写入；
- 日志和指标必须带进程角色与实例标识；控制面额外暴露 Leader 状态和各任务最近一次执行结果；
- 滚动升级期间，消息和存储契约必须保持兼容。需要破坏性变更时先停止相关角色并按明确步骤升级，
  不依赖不同模式之间的双写兜底。

启用 Elasticsearch Recent Alert 缓存和取消 Lifecycle Alert refresh 等待时，新旧 Lifecycle 不能混合
消费同一 Mailbox。升级或回滚前应暂停 Cleaner、排空 Signal lag/PEL 和 Mailbox，再同时替换 Lifecycle；
控制面须先把 Active 索引的 `refresh_interval` 对账为 YAML 中的配置值（默认 `5s`）。
Event create 改用 `refresh=false` 不改变 Redis 或存储 schema；新旧 Cleaner 可以滚动替换，差异只在
Event 搜索可见等待，Lifecycle 和幂等冲突核对均使用 realtime GET。

## 故障与扩缩容边界

| 异常角色 | 直接影响 | 恢复后行为 |
| --- | --- | --- |
| Cleaner | 原始消息积压，新的 Event 暂停产生 | 从 MQ 已确认边界继续消费 |
| Lifecycle | Mailbox 和 Signal 积压，已有 Event 暂停推进 Alert | 依靠稳定 Event ID、lease 和幂等处理继续收敛 |
| Control Plane API | 查询和管理请求不可用，数据面可继续处理 | API 恢复后读取权威存储状态 |
| Leader / Manager | 单例维护暂停；已预创建资源耗尽后数据面可能受影响 | 新 Leader 分别恢复幂等对账、时间桶维护和归档 |

三进程模式允许分别调整副本和资源：Cleaner 主要观察原始 MQ partition lag，Lifecycle 主要观察 Mailbox
积压和处理延迟，控制面主要观察 API 延迟、Leader 稳定性及各管理任务结果。不能只用进程存活代替
这些职责级就绪条件。

## 当前实现与演进边界

截至当前代码状态：

- `linkd run all-in-one` 已聚合 Cleaner、Lifecycle；配置 Elasticsearch 或 Redis Stream 管理任务时也运行控制面；
- `linkd run cleaner` 和 `linkd run lifecycle` 已提供独立进程入口；
- `linkd run control-plane` 已提供控制面进程入口和多任务监督边界，当前可装配两个周期性 Elasticsearch
  对账任务、一个连续批量归档任务和 `redis-stream-manager`；后者采集 Signal Stream、Consumer Group、
  PEL、lag 和内存状态，并只裁剪全部 Group 已确认的前缀。API 和 Leader Election 尚未实现，因此只能部署单副本；
- `linkd storage prepare` 是历史回放前预创建 Elasticsearch 时间桶的一次性管理命令，不是常驻进程。

因此，本文确认的是目标部署边界，不表示三进程模式已经具备完整生产交付能力。控制面实现、健康与
就绪探针、部署清单、容量基线和高可用验证完成后，再在使用指南中补充可执行的生产部署步骤。
