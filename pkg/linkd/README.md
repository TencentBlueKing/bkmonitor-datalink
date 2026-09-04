# Linkd

Linkd 是独立的 Go 告警接入和生命周期处理项目。当前领域事实以
[`docs/design/define.md`](docs/design/define.md) 为唯一权威：异构来源消息经过来源 Cleaner 标准化为
`domain.Event`，Event 持久化后由生命周期处理器按 fingerprint 创建或推进 `domain.Alert`，真实操作和
输出记录为不可变 `AlertLog`。

项目处于早期开发阶段，没有稳定 Go API 或历史版本兼容承诺。当前配置、领域身份和物理资源直接描述
当前实现；早期草案中的 `AlertEvent`、固定 RawEvent 映射、
`severity_priority`、`revision` 和 `terminal_event_id` 不属于当前模型。

## 当前链路

```text
MQ delivery
  → RawEventMessage
  → EventSource.cleaner.type 选择 SourceCleaner
  → 多 worker 并发生成 EventDraft
  → 通用事件工厂：租户、severity、fingerprint、稳定 event_id、时间 fallback
  → 各 lane 恢复连续顺序并独立批量持久化 Event
  → 各 lane 将仍为 unprocessed 的 Event ID 写入 Redis Mailbox
  → 确认原 MQ 消息
  → Mailbox Signal + fingerprint lease
  → Elasticsearch Recent Alert 缓存优先裁决
  → Alert 创建/更新/升级/抑制/终态
  → AlertLog + Kafka V1 Alert change
```

- Event action 仅 `triggered | resolved | closed`。
- Alert status 仅 `active | recovered | closed`，后两者不可重新打开。
- 同等级 `triggered` 只推进生命周期字段；更高等级关闭旧 Alert 并创建新 Alert；更低等级 Event 被抑制且不修改 Alert。
- accepted 与 suppressed Event 写入 `related_alert_id`；suppressed 不推进 Alert，orphaned/rejected 保持为空。
- Enricher 在 Alert 创建前同步执行一次；未配置规则视为 succeeded 空结果，错误降级为 failed。
- MySQL 和 Elasticsearch 都只承诺单对象 CAS；跨对象步骤依赖稳定身份和幂等流水恢复。Cleaner 确认
  原消息后不再扫描 Event 补发 Signal，因此 Redis Mailbox 必须依靠自身持久化和复制保证已确认数据。
- Elasticsearch Event create 使用 `refresh=false`；Cleaner 在主分片确认后入 Mailbox，Lifecycle 和重复
  Event 核对通过 realtime GET 读取，不依赖 `_search` 可见性。
- Elasticsearch Lifecycle 使用共享 Redis Recent Alert 缓存跨越可配置的 search refresh 窗口；Alert create/CAS
  使用 `refresh=false`，缓存写入失败时 Event 保留在 Mailbox 重试。MySQL 不启用该缓存。
- Elasticsearch 使用控制面进程内三个独立任务分别执行 Schema 与 Active 资源对账、时间桶维护和 Alert 归档：
  Event 按 received_at，AlertHistory 与 AlertLog 按 Alert 创建锚点分桶；终态 Alert 由有界 Worker 连续批量从
  Active 热索引幂等归档到 History，积压期间批次之间不等待固定周期。
- 控制面可监控 Redis Signal Stream 的长度、内存、Consumer Group、PEL 和 lag，并在超过软上限时有界
  裁剪所有 Group 都已确认的连续前缀；未读和 Pending Signal 不会为满足长度目标而删除。

## 目录

```text
cmd/linkd/            Linkd 服务进程入口
cmd/linkd-eventgen/   Standard Event 模拟器入口
configs/     示例配置
devtools/    本机只读运维调试工具
internal/    领域、Cleaner、生命周期、存储和运行时实现
docs/        设计、配置、协议、调研和审查记录
tests/       数据生成和 all-in-one E2E
```

文档导航见 [`docs/README.md`](docs/README.md)，配置见
[`docs/guides/configuration.md`](docs/guides/configuration.md)，存储边界见
[`docs/design/core-storage-contract.md`](docs/design/core-storage-contract.md)，生命周期见
[`docs/modules/lifecycle.md`](docs/modules/lifecycle.md)。后续部署形态统一为测试和小规模场景使用的
`all-in-one`，以及 Cleaner、Lifecycle、控制面（API / Leader / Manager）三进程模式；当前实现与目标
边界见 [`docs/design/deployment.md`](docs/design/deployment.md)。

## 本地开发

要求 Go 1.26.7、golangci-lint v2.13.2，以及已安装依赖的 Node.js/pnpm 环境。

```bash
go run ./cmd/linkd config validate --config ./configs/linkd.yaml
go run ./cmd/linkd run control-plane --config ./configs/linkd.yaml
go run ./cmd/linkd run cleaner --config ./configs/linkd.yaml
go run ./cmd/linkd run lifecycle --config ./configs/linkd.yaml
go run ./cmd/linkd run all-in-one --config ./configs/linkd.yaml
go run ./cmd/linkd-eventgen --config ./configs/linkd.yaml --event-source-id demo-source --tenant-id tenant-a
make check
```

独立模拟器的速率、生命周期、场景和重启边界见
[`docs/guides/event-generator.md`](docs/guides/event-generator.md)。

需要同时托管一个 all-in-one 和两组差异化 Standard Event 模拟器时，使用
[`ecosystem.config.cjs`](ecosystem.config.cjs)，构建、启动和运维命令见
[`docs/guides/pm2.md`](docs/guides/pm2.md)。

`cleaner`、`lifecycle` 和 `all-in-one` 都传播 Context 取消并执行有界排空。真实 MySQL、Elasticsearch、
Redis 和 Kafka 集成验证由 `tests/e2e/allinone` 提供，需显式配置对应环境变量。
