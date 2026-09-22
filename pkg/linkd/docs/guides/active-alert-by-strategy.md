# Redis 策略活跃告警缓存

`active-alert-by-strategy` 在 Alert 落库后提交策略刷新提示，控制面统一读取持久化 Active Alert，
按租户和策略生成 fingerprint 并集并发布到 Redis。数据库是事实来源，缓存可重建，允许短暂传播延迟。
本文依据 2026-09-22 实现整理；目标环境容量和端到端延迟需要独立验证。

## 1. 配置与生效

来源配置继续声明输出目标；控制面读取已发布 Release，包含停用和 tombstone 来源，不使用尚未发布的编辑版本。

```yaml
hooks:
  - name: active-by-strategy
    type: active-alert-by-strategy
    config:
      redis:
        mode: standalone
        address: 127.0.0.1:6379
        password: ${ACTIVE_ALERT_REDIS_PASSWORD}
        database: 8
      key_prefix: alarmd:open_alerts
      timeout_milliseconds: 1000
```

环境变量占位符仅适用于支持环境展开的进程 YAML 加载入口；通过管理 API 提交 JSON 时传入真实配置值，
不能假定 API 会展开 `${...}`。支持 Sentinel，其认证字段与 `storage.redis` 相同；此处连接独立配置。
Hook 缺省超时 1000ms，只限制提交提示。普通失败记录 `hook_failed` 后继续主流程，由周期校准恢复。
`hook_succeeded` 表示提示已提交，不表示缓存已经发布。

有此类 Hook 时控制面自动启动投影任务，不需要额外启用开关。可选调整：

```yaml
control_plane:
  active_index:
    poll_interval_seconds: 1
    reconcile_interval_seconds: 60
    operation_timeout_seconds: 10
    batch_size: 16
    max_rows: 100000
    max_bytes: 33554432
```

每个 Redis 目标独立运行，内部逐策略串行处理；每策略查询和发布共用操作期限。
提示合并等待约 1 秒，持续触发不会不断延后最早执行时间。正常传播延迟包含存储可见性、
合并等待、排队和查询；ES 活跃索引默认刷新周期为 5 秒，不能承诺 1 秒可见。
完整周期校准每 60 秒发起，积压和失败会延长完成时间，这不是恢复时限保证。

当前最多 32 个目标、512 个来源、每目标 64 个共享来源、10000 个待刷新策略。
单次完整读取默认最多 100000 条、32 MiB，超限不发布截断结果；可调至最多 1000000 条、64 MiB。
每目标 Redis 池限制 4 个连接。没有此类 Hook 时不打开额外告警仓储或目标 Redis。

切换此实现时应先停止仍直接执行 SADD/SREM 的旧版本 Worker，再启动新控制面和 Worker。
正式集合必须由当前 Linkd 部署独占维护；其他系统或另一部署需要互不嵌套的前缀。
同实例、同 DB 下禁止同时使用 `open` 与 `open:source-a` 这类父子前缀，否则父前缀扫描可能误认子集合。
控制面发现已发布目标存在此冲突时停止发布并记录 `overlapping_prefixes`，保留已有缓存。
`linkd:active-index:` 是内部保留命名空间，配置前缀不能与它重叠。不要将新投影任务与旧写入者混跑。
修改连接、DB 或前缀会为新目标回填当前活跃告警，旧目标不会自动迁移或清理；退出的旧目标需单独处理。

## 2. 集合与标签

```text
类型：set
key：<key_prefix>:<bk_tenant_id>:<strategy_id>
member：<fingerprint>
```

集合包含同一配置目标下所有已发布绑定来源的 Active Alert 的 fingerprint 并集。
A、B 来源共享 fingerprint 时，A 关闭且 B 仍活跃，成员继续保留。最后一个相关告警结束后，
下一次完整刷新移除成员。成员数是去重数量，不能等同于 Alert 记录数。

只读取 `labels.strategy_id`；字符串原样使用，有限数字转为非指数十进制文本。
字符串 `"123"` 与数字 `123` 共用集合；`"00123"` 独立。缺失或空字符串跳过，不回退 `bk_strategy_id`。
非法类型使 Hook 失败；非法存储结果使本次快照失败。策略字符串最多 1024 字节。
正式集合不设置 TTL；仅凭一段时间没有事件不能认定告警结束。

### 集合变更通知

通知始终启用，不接受 `notify_channel` 配置。channel 固定为 `<key_prefix>:changes`。
**由控制面在正式集合实际变化后发布**，一次完整替换产生一次通知，内容相同不通知。

```json
{"bk_tenant_id":"system","strategy_id":"123"}
```

消费者使用预先配置的 Redis 连接、DB 和前缀拼接集合 key，再读取最新成员。
Pub/Sub 不按 DB 隔离，跨环境或需要区分的 DB 使用不同前缀。

```bash
redis-cli -h 127.0.0.1 -p 6379 SUBSCRIBE 'alarmd:open_alerts:changes'
```

这是失效提示，不是心跳或可靠队列。订阅确认、重连后补读，并保留周期补读。
控制面通知失败不回滚已经发布的集合；之后相同内容不会补发通知。
完整协议见[策略索引变更通知 v1](../reference/contracts/active-alert-strategy-change-v1.md)。

## 3. 校准、错误与恢复

控制面持有策略租约后查询完整快照：MySQL 单条一致性 SELECT，ES 在一个 PIT 内有界分页。
读取成功后先构建带 TTL 的临时 Set，再用 Lua 验证租约、切换正式集合并条件通知。
空结果必须来自成功完成的查询；超时、缺索引、部分分片失败、超限都不解释为空。

| 场景 | 行为 |
| --- | --- |
| Hook 丢失、队列已满或进程在落库后崩溃 | 周期扫描数据库活跃策略及 Redis 已有集合，重新排队 |
| 共享来源中仍有 active 告警 | 并集继续保留成员，不按单条关闭 Hook 删除 |
| 查询或临时写入失败 | 保留旧集合，失败策略约 5 秒后重试，其他策略继续 |
| 刷新期间收到新提示 | 本次提交只确认领取时的 token，新提示留待下一轮 |
| 任务失租或控制面切换 | 最终提交拒绝旧持有者，继任任务重新查询 |
| 配置列表或 Release 读取失败 | 停止受管投影任务，恢复完整配置后重启；不按部分来源执行删除 |
| Redis 数据丢失 | 从数据库重新发现、回填；重建期间空集合不证明没有告警 |
| 发布成功但响应丢失 | 重新读取、计算；内容不变时不重复通知 |
| 通知失败 | 保留发布结果，记录投影失败；消费者靠周期补读恢复 |

周期发现失败约 5 秒后重试，已排队策略以及另一侧成功发现的策略仍可独立处理。Redis 原有残留集合也参与发现，
因此能删除最后一条告警关闭但提示丢失的成员。旧前缀、已移除的目标和外部写入不属于自动迁移范围。
ES PIT 只覆盖已经 refresh 的数据，短暂旧快照由后续提示或周期校准修复；不对告警主流程强制 refresh。

内部键使用 `linkd:active-index:<sha256(key_prefix)>` 命名空间，包含合并队列、短期 token、
策略租约、临时集合和发布状态；不保留每个历史 fingerprint 的版本。临时集合 120 秒过期，
租约是操作期限加 5 秒，非空集合的发布状态不过期，空集合状态保留 24 小时。正式集合不会因上游故障过期。
Redis ACL 需允许内部命名空间中的队列、Hash、租约、临时 Set 命令，以及 `EVAL`、`SCAN`、
`SSCAN`、`SCARD`、`RENAME`、`UNLINK`、`PERSIST`、过期命令和对应 channel 的 `PUBLISH`。
仅授予原来的 SADD/SREM 权限不足以运行新控制面。

## 4. 查询与接入验收

### Console 查询与对账

打开 Console「核心数据 → 策略活跃索引」（`/strategy-index`），选择 EventSource / Hook，
输入明确的租户 ID 和策略 ID，再执行「查询并对账」。Hook 名称不要求为 `active-by-strategy`，
只要类型为 `active-alert-by-strategy` 即可。页面同时展示 Redis 集合成员、相关活动告警及详情入口。

连接参数来自控制面已发布来源配置，凭据只在 Console 服务端使用；未配置控制面时使用本地来源配置。
该连接独立于 Console 的 `storage.redis`，支持 standalone 与 Sentinel。读取控制面失败不回退旧配置。
同一配置目标（实例定位、DB、前缀）的来源合并对账，包括暂时停用和 tombstone 来源。
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
已删除来源都可能造成差异。Console 同时核对扫描前后的发布时间及成员数；外部绕过控制面修改仍可能造成差异。

### Redis 命令与接入验收

下面是针对示例 standalone 地址和 database 的只读查询。使用真实环境时替换连接参数和 key；认证通过受控环境提供，不把密码写入命令历史。

```bash
# 查询某个 fingerprint 是否在集合中。
redis-cli -h 127.0.0.1 -p 6379 -n 8 SISMEMBER \
  'alarmd:open_alerts:tenant-a:123' 'fp-host-01'

# 查询该策略下去重后的 fingerprint 数量。
redis-cli -h 127.0.0.1 -p 6379 -n 8 SCARD \
  'alarmd:open_alerts:tenant-a:123'

# 分批读取成员，继续使用返回的游标，直到游标为 0。
redis-cli -h 127.0.0.1 -p 6379 -n 8 SSCAN \
  'alarmd:open_alerts:tenant-a:123' 0 COUNT 100
```

建议在测试来源上通过正常事件链路依次验证：创建 active 告警后成员存在；重复更新后不增加重复成员；恢复或关闭后成员移除。
再使用另一租户验证 key 隔离，并确认失败 Redis 实例不会阻断后面的正常 hook。
同时核对 Alert 状态和对应 push 流水，避免把手工 Redis 写入当成端到端接入验证。

## 5. 运行状态与排障

Console 同时展示策略最近成功校准时间、快照年龄、失败原因、是否待刷新，以及目标最近完整发现时间。
没有成功校准记录时显示尚未校准，不能把 Redis 空集合当作确定没有活动告警。
仅使用原始 SMEMBERS/SISMEMBER 的消费者无法识别重建状态；对空结果敏感的消费者应同时检查发布状态。

| 现象 | 检查 |
| --- | --- |
| Hook 成功但集合暂未变化 | 控制面任务、待刷新状态、查询失败、ES refresh 延迟 |
| 出现 `read_failed` | 告警存储连接、完整分页、来源范围、资源上限 |
| 出现 `invalid_snapshot` | 存储中策略标签、租户、fingerprint 是否符合约束 |
| 出现 `publish_failed` | Redis 权限、键类型、连接、租约及临时集合预算 |
| 出现 `discovery_failed` | 完整扫描是否超限、告警仓储和 Redis 是否可达 |
| 成员长时间未修复 | 最近成功时间、完整发现时间、积压、当前发布目标是否改变 |

控制面日志不透传 Redis/数据库原始错误文本；包含目标摘要，策略失败另含租户和策略。
Hook 指标仍表示提示提交成功率；缓存是否健康应结合控制面投影状态，而不是只看 Hook p95。

## 6. 实现与验证入口

- [控制面装配](../../internal/controlplane/process/active_index.go)。
- [投影任务与失败恢复](../../internal/activeindex/manager.go)。
- [Redis 租约、队列与原子发布](../../internal/activeindex/redis.go)。
- [Hook 刷新提示](../../internal/lifecycle/strategyhook/hook.go)。
- [设计与一致性边界](../design/active-alert-index.md)。

普通测试使用假来源和内存缓存。真实专项测试显式启用：

```bash
LINKD_TEST_REDIS_ADDRESS=127.0.0.1:6379 go test -race ./internal/activeindex -run Integration
LINKD_TEST_MYSQL_DSN='root@tcp(127.0.0.1:3306)/?parseTime=true' go test -race ./internal/store/mysql -run TestActiveIndexMySQLIntegration
LINKD_TEST_ELASTICSEARCH_URL=http://127.0.0.1:9200 go test -race ./internal/store/elasticsearch -run TestActiveIndexElasticsearchIntegration
```

仅使用专用测试实例，MySQL 测试需要建库权限；用例创建并清理独立前缀资源。
这些测试不等同于 Kingeye 目标环境联调、生产容量验证或完整事件链路压测。
