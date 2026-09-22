# Redis 策略活跃告警 hook 使用指南

`active-alert-by-strategy` 将 Alert 状态投影到 Redis set，供消费者按租户和策略查询活跃 fingerprint。
它随单个 EventSource 发布，由 Lifecycle 执行。Redis 集合是尽力更新的输出索引，Alert 的权威状态仍在告警存储中。

本文依据 2026-09-22 仓库代码整理，包含 Console 查询与对账，描述当前实现，不代表目标环境已完成联调。
插件状态规则以 [Lifecycle 插件行为](../modules/lifecycle.md#23-enricher-与-finalhook) 为权威定义；本文侧重接入、查询和排障。

## 1. 配置与生效

将以下片段加入目标 EventSource。它是来源配置的一部分，不能作为独立的进程配置文件使用：

```yaml
hooks:
  - name: active-by-strategy
    type: active-alert-by-strategy
    config:
      redis:
        mode: standalone
        address: 127.0.0.1:6379
        database: 0
      key_prefix: linkd:active-alert-by-strategy
      timeout_milliseconds: 1000
```

| 配置项 | 要求及用途 |
| --- | --- |
| `name` | 来源内唯一、稳定的实例名，参与日志身份和指标；1～64 个字符，首字符为字母或数字，其余允许字母、数字、下划线和连字符 |
| `type` | 固定为 `active-alert-by-strategy` |
| `config.redis` | 必填，独立于 `storage.redis`；支持 standalone 和 Sentinel，认证字段与全局 Redis 配置相同 |
| `config.key_prefix` | 必填，1～256 字节，不能有首尾空白；不会自动追加来源 ID。通知 channel 固定为 `<key_prefix>:changes` |
| `config.timeout_milliseconds` | 正整数毫秒，缺省为 1000；不接受 0、负值、null、超出 Duration 范围的数值或旧秒级字段 |

每个来源最多配置 16 个 hook，按列表顺序执行；可以与 Kafka hook 组合，也可以配置多个 Redis 实例。
省略 `hooks` 或设置为空列表表示不执行输出。完整配置规则见 [EventSource hooks](configuration.md#eventsource-hooks)。

通过现有 YAML 导入、管理 API 或 provider 更新来源并生成 Release，等待来源任务切换后生效。
仅修改本地 YAML 不会自动更新运行中的来源；具体入口见 [动态 EventSource 与调度](configuration.md#动态-eventsource-与调度)。
配置校验和客户端装配不预连输出 Redis，因此发布成功不能证明 Redis 可达或认证有效。

## 2. 输入与集合结构

hook 读取最终 Alert 快照的 `bk_tenant_id`、`labels.strategy_id`、`fingerprint` 和 `status`。
接入时应确认清洗后的标签最终进入 Alert 的 `labels`，仅在原始 payload 中存在策略 ID 并不足够。

```text
Redis 类型：set
key：<key_prefix>:<bk_tenant_id>:<strategy_id>
member：<fingerprint>
```

例如前缀为 `linkd:active-alert-by-strategy`，租户为 `tenant-a`，策略为 `123`，fingerprint 为 `fp-host-01`：

```text
active           → SADD linkd:active-alert-by-strategy:tenant-a:123 fp-host-01
recovered/closed → SREM linkd:active-alert-by-strategy:tenant-a:123 fp-host-01
```

成员不包含 `alert_id`、来源 ID 或完整告警内容。集合大小表示去重后的 fingerprint 数量，不能直接等同于告警记录数。

| `labels.strategy_id` | 结果 |
| --- | --- |
| 字符串 `"123"` | 使用 `123` 作为 key 的策略段 |
| 有限数字 `123` | 转为稳定十进制文本 `123`，与字符串 `"123"` 共用集合 |
| 字符串 `"00123"` | 原样保留，与 `123` 属于不同集合 |
| 缺失或空字符串 | 跳过 Redis 写入，不生成该实例的 push 流水 |
| 只有 `bk_strategy_id` | 不回退读取，按缺失处理 |
| 布尔值或非法值 | hook 失败，不写 Redis |

字符串不做裁剪；调用方应统一策略 ID 的类型和格式。hook 不设置 TTL，也不执行周期性清理。

### 集合变更通知

集合变更通知始终启用，无需也不接受 `notify_channel` 配置。channel 与集合使用共同的 `key_prefix`，
固定为 `<key_prefix>:changes`，同前缀的所有租户和策略共享该 channel。
Hook 通过同一次 Lua 执行集合修改和条件发布：`SADD/SREM` 返回实际变更数
大于 0 才执行 `PUBLISH`。重复添加已有成员、删除不存在成员不通知；删除最后一个成员导致 key 消失时仍通知。
客户端需要集合的读写权限、`EVAL` 和对应 channel 的 `PUBLISH` 权限；不要求打开 Redis keyspace notifications。

消息只包含 `bk_tenant_id` 和 `strategy_id`。消费者使用已约定的 Redis 连接、DB 和 `key_prefix`，
拼接 `<key_prefix>:<bk_tenant_id>:<strategy_id>` 后重新读取集合。Pub/Sub 不按 DB 隔离，
不同环境或需要隔离的 DB 应使用不同 `key_prefix`，从而使用不同 channel。
共享同一前缀的多个 Hook 自动使用同一 channel；不同前缀的通知各自隔离。
通知只覆盖本 Hook 发起的修改，外部客户端直接改集合不会自动发布。

若 Hook 使用 DB 8、前缀 `alarmd:open_alerts`，通知 channel 自动为 `alarmd:open_alerts:changes`。
租户 `system`、策略 `123` 的集合变化时，通知为：

```json
{
  "bk_tenant_id": "system",
  "strategy_id": "123"
}
```

订阅示例（连接和认证按实际环境设置）：

```bash
redis-cli -h 127.0.0.1 -p 6379 SUBSCRIBE 'alarmd:open_alerts:changes'
```

收到消息后，用预先配置的普通 Redis 连接选择 DB 8，对 `alarmd:open_alerts:system:123` 执行 `SSCAN` 或 `SISMEMBER`。
消息不携带成员增删明细；读取时 key 可能已被删除，按空集合处理。使用 RESP2 时不要在订阅连接上执行集合查询。

这是即时失效通知，不是心跳或可靠消息队列。订阅确认后应补读，断线重连后也需要补读。
发布失败记录 Hook 失败，集合可能已经修改；随后无变化的重试不会补发通知。完整消息与故障语义见
[策略索引变更通知 v1](../reference/contracts/active-alert-strategy-change-v1.md)。

## 3. 触发与一致性边界

Lifecycle 在 Alert 真实变更后执行 hook，包括创建、更新和终态转换；它不是收到每条输入消息就执行的清洗回调。
每个实例接收独立 Alert 快照。close_and_create 升级先执行旧告警的全部关闭 hook，再执行新告警的全部创建 hook；
update_current 升级仅对原 Alert 的 active 快照执行 SADD，不经过 SREM。

单次 `SADD` 或 `SREM` 可以重复执行，但整个告警存储、Redis 更新和流水提交不属于一个原子事务：

| 场景 | 当前行为及影响 |
| --- | --- |
| Redis 错误或实例超时 | 记录失败并继续后续 hook；不因该输出失败回滚已经完成的 Alert 状态 |
| 父上下文取消 | 停止当前处理及后续 hook；已经发生的 Redis 写入不会被撤销 |
| 旧快照晚于新快照执行 | 没有版本比较或乱序保护，旧状态可能重新加入或删除成员 |
| close_and_create 等级升级 | 删除旧成员与加入新成员是独立命令，可能短暂查不到成员；任一步失败也可能造成偏差 |
| Redis 丢数据或新启用 hook | 没有全量回填或自动重建；后续更新仅覆盖实际触发 hook 的告警 |
| 切换 Redis、database 或前缀 | 使用当前任务 Release 的配置，不按 Alert 创建版本查找旧配置；不迁移或清理旧集合 |

Lifecycle 恢复已保存计划时，若源事件的 active 快照已被更晚的独立关闭命令推进为终态，会跳过旧 active hook。
这只是该恢复分支的保护，不构成 hook 自身的通用乱序保证。

hook 没有业务级失败补偿队列，失败不会自动安排后续补写。消费者需要准确状态时，应以告警存储核对，不能把集合缺失解释为确定没有活跃告警。

### 来源共享与隔离

租户作为 key 的必填段，来源是否共享由 `key_prefix` 决定。在同一个 Redis database 下，同前缀、同租户、同策略、同 fingerprint 只保留一个成员，没有引用计数。

例如来源 A、B 都加入 `fp-host-01`，随后 A 关闭，hook 会删除该成员，即使 B 仍然活跃。
需要来源隔离时，应显式使用不同前缀，例如 `linkd:active-alert-by-strategy:source-a` 和 `linkd:active-alert-by-strategy:source-b`。
修改实例 `name` 不会改变集合 key，也不能实现数据隔离。

## 4. 查询与接入验收

### Console 查询与对账

打开 Console「核心数据 → 策略活跃索引」（`/strategy-index`），选择 EventSource / Hook，
输入明确的租户 ID 和策略 ID，再执行「查询并对账」。Hook 名称不要求为 `active-by-strategy`，
只要类型为 `active-alert-by-strategy` 即可。页面同时展示 Redis 集合成员、相关活动告警及详情入口。

连接参数来自控制面当前来源配置，凭据只在 Console 服务端使用；未配置控制面时使用本地来源配置。
该连接独立于 Console 的 `storage.redis`，支持 standalone 与 Sentinel。读取控制面失败不回退旧配置。
同一配置目标（实例定位、DB、前缀）的来源合并对账，包括暂时停用但仍可能保留活动告警的来源。
不同 DNS 别名、不同 Sentinel 入口或外部写入者不能仅凭配置识别为同一个实例，需人工核对范围。

| 结果 | 含义 |
| --- | --- |
| 一致 | 同一 fingerprint 在两侧本轮读取中均存在；可关联多个来源的 Alert |
| Redis 缺失 | 已找到该策略的活动告警，但完整读取的 Redis 集合中没有成员 |
| Redis 独有 | Redis 中存在，但完整读取的当前关联来源中没有相应策略的活动告警；不能直接认定可删除 |
| 无法确认 | 对应另一侧读取失败或超限，不能据此推断缺失 |

MySQL 查询当前 `status=active` 记录，Elasticsearch 只查询 `<index_prefix>-alerts-active`，
不套用 Explorer 的默认时间窗口，不读取历史索引。策略标签按 Hook 规则比较：字符串原样，
有限数字转为非指数十进制，布尔值、缺失和空字符串不匹配。对账以 fingerprint 集合为准，
集合成员数不等于 Alert 条数。

先在存储侧按租户、共享来源、active 状态和策略标签筛选，再按 Hook 规则核验。
一次最多读取 5000 条候选 Active Alert；Redis
最多读 5000 个成员、100 轮 SSCAN、累计 2 MiB 成员内容。每次最多 64 个共享来源，配置列表
最多 512 个此类 Hook，同时最多 4 个查询；超限或错误显示不完整。页面每页显示 100 个 fingerprint。
对账不提供任意地址或 key 输入，不执行 SADD、SREM、删除、回填或修复。

这是一次只读观察，不是跨存储事务快照。ES 刷新延迟、并发告警变更、来源发布传播、历史前缀及
已删除来源都可能造成差异。即使扫描前后 SCARD 相同也不能证明期间没有成员替换；修复前应复查。

### Redis 命令与接入验收

下面是针对示例 standalone 地址和 database 的只读查询。使用真实环境时替换连接参数和 key；认证通过受控环境提供，不把密码写入命令历史。

```bash
# 查询某个 fingerprint 是否在集合中。
redis-cli -h 127.0.0.1 -p 6379 -n 0 SISMEMBER \
  'linkd:active-alert-by-strategy:tenant-a:123' 'fp-host-01'

# 查询该策略下去重后的 fingerprint 数量。
redis-cli -h 127.0.0.1 -p 6379 -n 0 SCARD \
  'linkd:active-alert-by-strategy:tenant-a:123'

# 分批读取成员，继续使用返回的游标，直到游标为 0。
redis-cli -h 127.0.0.1 -p 6379 -n 0 SSCAN \
  'linkd:active-alert-by-strategy:tenant-a:123' 0 COUNT 100
```

建议在测试来源上通过正常事件链路依次验证：创建 active 告警后成员存在；重复更新后不增加重复成员；恢复或关闭后成员移除。
再使用另一租户验证 key 隔离，并确认失败 Redis 实例不会阻断后面的正常 hook。
同时核对 Alert 状态和对应 push 流水，避免把手工 Redis 写入当成端到端接入验证。

## 5. 排障

| 现象 | 优先检查 |
| --- | --- |
| 没有成员，也没有 push 流水 | 当前任务 Release 是否包含该 hook；Alert 是否发生变更；最终 `labels.strategy_id` 是否缺失或为空 |
| 出现失败流水 | 按实例名、租户和 Alert ID 定位；检查 Redis 连接、认证、database、超时，以及策略标签类型 |
| 告警仍活跃但成员消失 | 是否有其他来源共享前缀和 fingerprint；是否发生终态写入、乱序或 Redis 数据丢失 |
| 告警已结束但成员仍存在 | 终态 hook 是否失败；结束时是否使用了新的 Redis 目标或前缀；是否有旧 active 快照晚到 |
| 新前缀没有历史成员 | 当前实现不回填历史活跃告警；需要另行制定基于权威告警存储的核对、重建方案 |

push AlertLog 的结果为 `hook_succeeded` 或 `hook_failed`，附带 `hook_name`、`transport`、`destination`、`message_id` 和 `lifecycle_outcome`。
Redis hook 的 `transport` 为 `redis`，已解析策略时 `destination` 为完整集合 key。
跳过标签缺失的告警不会产生 push 流水，因此没有流水不一定表示插件未执行。

运行日志使用 `alert final hook failed`，包含 `bk_tenant_id`、`alert_id`、`hook_name` 等定位字段。
普通 Redis 错误与超时在该调度日志中统一分类为 `hook_error`，不会透传 Redis 服务端原始错误文本；不能仅凭这个分类判断为认证故障或网络超时。
启用遥测后，可查看 `linkd.final_hook.operations` 和 `linkd.final_hook.duration`，结合 `linkd.hook.name`、`linkd.event_source_id`、`linkd.outcome` 区分实例和执行结果。

## 6. 实现与验证入口

- [配置校验与默认值](../../internal/config/hooks.go)。
- [Redis 集合操作与策略标签解析](../../internal/lifecycle/strategyhook/hook.go)。
- [实例装配与客户端关闭](../../internal/lifecycle/process/hooks.go)。
- [串行执行与失败流水](../../internal/lifecycle/hook.go)。
- [集合、标签、超时、取消及 Redis 集成测试](../../internal/lifecycle/strategyhook/hook_test.go)。

Hook 的真实 Redis 集成用例通过 `LINKD_TEST_REDIS_ADDRESS` 显式启用。
Console 对账集成测试见 `console/src/server/strategy-index.integration.test.ts`，需要同时设置
`LINKD_TEST_REDIS_ADDRESS` 与 `LINKD_TEST_ELASTICSEARCH_URL`；只创建并清理唯一前缀的测试资源。
MySQL 查询测试通过 `LINKD_TEST_MYSQL_URL` 显式启用，需要专用测试实例的建库权限，使用并清理独立测试数据库。
普通测试使用假来源和假连接；这些专项验证不等同于 Kingeye 目标环境或完整业务链路验证。
