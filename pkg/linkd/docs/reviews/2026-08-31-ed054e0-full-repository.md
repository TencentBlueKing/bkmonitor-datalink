# Linkd 全仓库审查记录（2026-08-31 / ed054e0）

## 审查元信息

| 项目 | 内容 |
| --- | --- |
| 审查时间 | 2026-08-31 15:45 — 16:00 (UTC+8) |
| 审查基线 commit | `ed054e086dcb38c21bceac89612e91dea68afd53`（`ed054e0`） |
| 基线提交信息 | `feat: 实现 Linkd 告警处理与观测链路`（2026-08-31 15:35:01 +0800） |
| 审查范围 | 全仓库：架构、目录结构、模块实现、存储层、消费运行时、配置、可观测性、文档一致性、仓库卫生 |
| 代码规模 | 生产 Go 代码 15494 行，测试 8760 行，共 147 个 `.go` 文件；Markdown 文档 7861 行 |

审查开始时工作区存在未提交改动，不属于本次审查基线，也未在本次审查中修改：

- `docs/design/alarm-processing-flow.html` 已删除；
- `go.mod` / `go.sum` 中 `github.com/twmb/franz-go/pkg/kmsg` 由 indirect 转为直接依赖，`filippo.io/edwards25519` 由 v1.1.0 升至 v1.1.1。

## 后续处理状态（2026-08-31）

本节是对审查基线之后整改结果的补充登记。下文原始 finding 继续保留，用于说明 `ed054e0` 当时的
问题和证据；后续复核应先看本表，避免重复处理已经关闭的事项。

| Finding | 状态 | 后续处理 |
| --- | --- | --- |
| S1 | 已处理 | blocked lane 的 ready retry 不再从堆中静默丢失；Runtime 进入失败退出后主动关闭 Session，把所有未确认消息交还 Broker，而不是长期侵蚀 inflight 配额。 |
| S2 | 已处理 | `Block`、Handler panic 和重试预算耗尽统一改为“暂停可暂停 lane + Runtime fail-fast”；移除未接入控制面的 `Resume`，未引入通用 Inbox 或统一 DLQ。 |
| S3 | 已处理 | lifecycle 进程已装配 Event Replayer，按固定边界跨租户分页扫描陈旧 `unprocessed` Event，并重新投递 Redis lifecycle signal。 |
| S4 | 已处理 | Processor 会用 `latest_event_id`、`terminal_event_id` 和确定性 AlertLog ID 恢复 Alert 已创建、终态已写入及等级轮转部分成功，补齐流水后再推进 Event CAS。 |
| S5 | 暂缓 | 按本轮明确范围暂不处理 Elasticsearch 活动 Alert 唯一性，原 finding 继续有效。 |

整改定向验证：

```bash
GOCACHE=/private/tmp/linkd-recovery-go-cache go test ./internal/consume ./internal/lifecycle ./internal/config ./internal/store/memory ./internal/store/mysql ./internal/store/elasticsearch ./internal/telemetry ./internal/lifecycle/process
```

上述包测试通过；MySQL/Elasticsearch 真实服务集成用例仍由各自环境变量显式启用，不能把默认 skip
表述为本次重新完成了外部故障演练。

完整质量门禁随后使用可写缓存执行并通过：

```bash
GOCACHE=/private/tmp/linkd-recovery-go-cache \
GOLANGCI_LINT_CACHE=/private/tmp/linkd-recovery-golangci-cache \
make check
```

门禁覆盖 `fmt-check`、全部普通测试、`go vet`、race、`golangci-lint`，以及 DevTools 的
Prettier、ESLint、TypeScript、Vitest 和 Vite build。

## 审查方法与门禁结果

本次审查以阅读代码为主，并实际执行了只读门禁命令。执行结果如下，均在基线工作区状态下运行：

| 命令 | 结果 |
| --- | --- |
| `go build ./...` | 通过 |
| `go vet ./...` | 无输出 |
| `go test ./...` | 全部包通过 |
| `golangci-lint run ./...`（v2.13.2） | `0 issues` |

`make check` 未完整执行，阻断原因：其 `devtools-check` 目标依赖 `devtools/node_modules/.bin/*`，需要先执行 `pnpm --dir devtools install`，本次审查未安装该工具链。

需要强调的是 `go test ./...` 全绿并不代表外部依赖链路被验证。MySQL 契约测试需要 `LINKD_TEST_MYSQL_DSN`（`internal/store/mysql/repository_test.go:29-31`），Elasticsearch 集成测试需要对应 URL 环境变量（`internal/store/elasticsearch/integration_test.go:42`），all-in-one E2E 需要 `e2eEnabledEnv=1`（`tests/e2e/allinone/all_in_one_test.go:99`）。默认执行时这些用例全部 skip，因此本次审查中没有任何针对真实 Kafka、Redis、MySQL 或 Elasticsearch 的运行时验证。

下文所有结论均来自静态代码阅读与上述只读命令，未在真实基础设施上复现任何缺陷。标注为「推断」的条目表示尚未获得运行时证据。

## 结论摘要

项目的工程纪律明显高于同阶段平均水平：领域不变量校验、CAS 语义、确定性身份生成、配置脱敏、优雅退出与遥测仪表都实现得比较完整，静态门禁干净。目录结构本身是健康的，主要问题不在文件组织，而在两处：可靠性闭环存在缺口，以及部分抽象与真实消费者错配。

需要优先处理的是消费运行时的三条相互纠缠的缺陷（重试条目丢失、阻塞无法恢复、缺少死信出口），以及 Elasticsearch 后端缺少活动 Alert 唯一性约束——后者的影响被「默认配置即 Elasticsearch」放大。

## 严重问题

### S1. 阻塞 lane 上的重试条目被静默丢弃，并侵蚀全局 inflight 配额

`takeReady` 是 `heap.Pop` 语义，条目取出后即离开重试堆：

```go
// internal/consume/retry.go:44-49
func (q *retryQueue) takeReady(now time.Time) *retryItem {
	if len(*q) == 0 || (*q)[0].next.After(now) {
		return nil
	}
	return heap.Pop(q).(*retryItem)
}
```

`dispatchReadyRetries` 在发现 lane 已阻塞时直接 `continue`（`internal/consume/runtime.go:560-562`），条目既未放回堆也未进入 settle 流程，就此消失。

该条目仍计入 `s.inflightMessages` 且永不递减，因为它再也走不到 settle 路径。`tryReceive` 每轮以 `MaxInflightMessages - s.inflightMessages` 计算拉取容量（`internal/consume/runtime.go:352`），因此这些无主条目会持续侵蚀全局配额。

对 Kafka 的放大效应（推断，未运行时复现）：`CanPauseLane` 为 true 时本应只有单个分区被暂停，其余分区继续工作；但无主条目累积到配额耗尽后 `messageCapacity <= 0`，`tryReceive` 直接返回，**所有分区一起停止拉取**，单分区故障被放大为整个消费者停摆。

建议方向：lane 处于阻塞状态时把条目放回堆或转入 deferred 队列，并在 lane 恢复后重新 dispatch；无论采用哪种方案，都必须保证 `inflightMessages` 的增减配对。

### S2. `blockLane` 没有恢复路径，已实现的 `Resume` 从未被调用

```go
// internal/consume/runtime.go:590-607
func (s *runtimeState) blockLane(laneName string) {
	lane := s.lane(laneName)
	if lane.blocked {
		return
	}
	lane.blocked = true
	s.runtime.observer.FlowTransition(context.Background(), "pause")
	controller, ok := s.runtime.session.(FlowController)
	if !ok || !s.capabilities.CanPauseLane {
		s.globalBlocked = true
		return
	}
	// ... controller.Pause ...
}
```

全代码库中没有任何位置把 `lane.blocked` 置回 `false`，`globalBlocked` 同样只设不清。恢复能力其实已经写好：`consume.FlowController` 定义了 `Resume`（`internal/consume/types.go:116`），Kafka Session 也实现了它（`internal/consume/kafka/session.go:246-255`），但运行时从不调用。

触发条件并不罕见：Handler 返回 `Block`、重试次数或时限用尽（`internal/consume/runtime.go:514`、`:522`）、Handler panic 被转换为 Block（`internal/consume/runtime.go:213-215`），任一情况都会走到这里。

后果是该 lane 永久停止消费。由于没有死信队列，进程重启后会重放同一条毒丸消息并再次阻塞，形成无法自愈的循环。

对 Kafka 的叠加影响（推断）：Session 只在全部 receipt 确认后才允许 rebalance：

```go
// internal/consume/kafka/session.go:226-229
	if len(s.receipts) == 0 {
		close(s.batchDone)
		s.client.AllowRebalance()
	}
```

lane 阻塞后 receipt 不会被确认，`AllowRebalance` 不再被调用，consumer group 的 rebalance 被卡住，该消费者最终可能因 session timeout 被踢出组并影响组内其他成员。

建议方向：接上 `Resume` 并设定有界阻塞时长，或改为进程级 fail-fast 交由编排器重启；同时为重试耗尽的消息提供死信出口，而不是无限期阻塞。S1 与 S2 必须一并修复——只修 S2 的话，已被吞掉的条目依然回不来。

### S3. 崩溃恢复链路只存在于注释中

`ScanUnprocessedEvents` 在 `store.Repository` 中定义（`internal/store/store.go:15-20`），memory、MySQL、Elasticsearch 三个后端都已实现，契约测试也有覆盖，但**生产代码中没有任何调用方**，仅测试与 `internal/telemetry/store.go:62` 的装饰器在转发。

这一缺口与 cleaner 的两步非事务写入直接相关：先 `CreateEvent`，再向 Redis Stream `Publish` signal（`internal/cleaner/handler.go:66-83`）。Publish 失败时返回 `Retry`，重试预算耗尽后触发 S2 的 lane 阻塞，事件永久停留在 `unprocessed` 状态，而没有任何扫描器负责回收。

而 `ProcessEvent` 的注释把这个不存在的机制写成了设计前提：

```go
// internal/lifecycle/processor.go:16-18
// Event、Alert 和 AlertLog 是独立对象，本方法不承诺跨对象事务。调用方必须保证同一
// fingerprint 串行处理；进程崩溃造成的部分成功由后续恢复阶段处理。
```

根 `README.md:14` 已诚实说明「Event Replayer/跨对象崩溃恢复……尚未实现」，两处表述互相矛盾。按 AGENTS.md「不把尚未确认的设计设想写成既有事实」的要求，该注释需要修正。

建议方向：补上基于 `ScanUnprocessedEvents` 的恢复调度器，或改用 outbox 模式让事件写入与 signal 投递具备原子性；在方案落地前，先把注释改成对现状的准确描述。

### S4. lifecycle 多步写入的部分失败会静默丢日志与丢告警

由于不存在跨对象事务，`internal/lifecycle/processor.go` 中的多步序列在中途失败后，重放会进入错误分支。

丢日志路径：`createAlert` 在 `CreateAlert` 成功、`appendAlertLog` 失败后返回错误；重放时 `FindActiveAlert` 找到刚创建的 Alert，进入 `updateAlert`，而该方法开头发现 `LatestEventID == EventID` 即直接 `acceptEvent` 返回（`internal/lifecycle/processor.go:154-156`），trigger 日志永久缺失。

丢告警路径：`rotateAlert` 先 CAS 关闭旧 Alert（`internal/lifecycle/processor.go:242`），再创建新 Alert（`:252`）。中间崩溃会留下「旧 Alert 已关闭、新 Alert 不存在」的状态。重放时 `FindActiveAlert` 返回 `ErrNotFound`，若事件 action 为 `Updated`，则命中：

```go
// internal/lifecycle/processor.go:77-80
		case domain.EventActionUpdated, domain.EventActionRecovered, domain.EventActionClosed:
			return p.rejectEvent(ctx, stored, ReasonActiveAlertNotFound)
```

告警被静默拒绝，一次等级轮转即可导致整条告警链中断。

以上为静态推导，未构造崩溃场景复现。

### S5. Elasticsearch 缺少活动 Alert 唯一性约束，而默认后端即 Elasticsearch

ES 的 `CreateAlert` 是「先查后写」的非原子流程，代码注释本身已承认这一点：

```go
// internal/store/elasticsearch/alert.go:16-18
// CreateAlert 使用稳定租户复合 _id 和 create-only API 创建活动 Alert。
// 生命周期调度器必须先持有 tenant/source/fingerprint lease；Elasticsearch 本身不提供跨文档唯一约束。
```

两个并发请求使用不同 `alert_id` 但相同 source/fingerprint 时，可能都通过 `FindActiveAlert` 的 `ErrNotFound` 检查（`internal/store/elasticsearch/alert.go:32-49`），随后都 `_create` 成功，破坏「每租户每 source/fingerprint 至多一个活动 Alert」的不变量。

问题的严重性来自三个事实的组合：

1. MySQL 侧有 `uq_linkd_alert_active_identity` 唯一索引兜底（`internal/store/mysql/migrations/001_init.sql:27-31`），ES 侧没有任何兜底；
2. ES 后端把数据正确性完全外包给 Redis lease，而 Redis 锁在续租失败、网络分区或长时间 GC 停顿下会失效——这是 Redis 锁的固有属性，不是 `internal/lifecycle/scheduler/lock.go` 的实现缺陷（该实现本身是标准且正确的）；
3. 基线配置 `configs/linkd.yaml:16` 的默认值就是 `repository: elasticsearch`。

也就是说，在默认后端下 Redis 锁不是性能优化，而是唯一的正确性保障。而配置层把两个后端呈现为能力对等的二选一，没有任何地方提示这一差异。

加剧因素：ES 未接入共享契约测试。MySQL 与 memory 都调用 `storetest.RunRepositoryContract`（`internal/store/mysql/repository_test.go:51`、`internal/store/memory/repository_test.go:12`），ES 只有一套独立的 smoke 测试（`internal/store/elasticsearch/integration_test.go:39-217`），因此此类后端差异在 CI 中没有自动回归。

建议方向：为 ES 侧寻找原子写入方案（scripted update、external versioning 或单文档 active 索引），或在配置与文档中明确标注「ES 后端的活动唯一性依赖 Redis lease 可用性」；同时把 ES 接入 `RunRepositoryContract`，并补充并发 `CreateAlert` 契约用例。

## 中等问题

### M1. 凭据只能以明文写入 YAML

`applyEnvironment` 仅处理两个环境变量：

```go
// internal/config/load.go:171-178
func applyEnvironment(cfg *Config, lookupEnv func(string) (string, bool)) {
	if value, exists := lookupEnv(envLogLevel); exists {
		cfg.Logging.Level = value
	}
	if value, exists := lookupEnv(envLogFormat); exists {
		cfg.Logging.Format = value
	}
}
```

MySQL 与 Redis 密码、Elasticsearch 的 `api_key` 与 `basic_auth`、Kafka SASL 密码均没有环境变量或 secret 文件注入通道，只能写在配置文件中。基线 `configs/linkd.yaml:20,28` 已包含 `password: test123456` 形式的成品结构，该模式容易诱导使用者把生产凭据提交进版本库。

`Redacted()`（`internal/config/config.go:74-93`、`internal/config/storage.go:119-142`）的脱敏覆盖较完整，但脱敏解决的是打印问题，不解决注入问题。

### M2. 应用进程启动时无条件执行 DDL

`EnsureSchema` 的注释声明它「只适用于初始化阶段」（`internal/store/mysql/schema.go:16-17`），但 `assembly.Open` 在每次进程启动时无条件调用（`internal/store/assembly/repository.go:94`、`:133`）。多副本同时启动会并发执行 DDL，且生产环境通常不会给应用账号 DDL 权限。注释与实现存在矛盾。

### M3. `Repository` 接口远大于真实需求

`internal/store/store.go:11-65` 定义的 `Repository` 共 14 个方法，其中 `ScanUnprocessedEvents`、`GetEvents`、`GetAlerts`、`ListAlertLogs`、`QueryAlertByEvent` 均无生产消费方，仅被测试与遥测装饰器引用。

项目其实已经掌握了正确做法：`cleaner.EventWriter`（`internal/cleaner/handler.go:14-16`）与 `scheduler.EventReader`（`internal/lifecycle/scheduler/handler.go:20-22`）都是由消费方定义的单方法窄端口，完全符合 AGENTS.md。大接口的存在主要是为了让三个后端与契约测试共享基类，代价是每新增一个方法就要在 memory / MySQL / Elasticsearch / telemetry 四处实现。

建议方向：保留 `store.Repository` 作为 assembly 装配用的组合类型，业务侧按用例定义窄端口。

### M4. 热点 fingerprint 会占满全部 worker

`acquireLease` 在锁被占用时循环重试，直到 context 超时为止，等待期间持续占用一个 worker goroutine（`internal/lifecycle/scheduler/handler.go:136-153`）。注释称「等待会占用一个受 WorkerCount 限制的 goroutine，因此不会产生无界竞争者」——竞争者数量确实有界，但同一个高频抖动的 fingerprint 可以让所有 worker 同时自旋等待同一把锁，head-of-line blocking 真实存在。

### M5. Redis Stream 存在 PEL 悬空窗口

`XREADGROUP` 已把消息读入 PEL 之后才执行字节数检查，超限时直接返回错误且不登记 receipt：

```go
// internal/consume/redisstream/session.go:130-131
	if totalBytes > limits.MaxBytes {
		return nil, fmt.Errorf("redis streams receive: %w: payload bytes %d exceed %d", consume.ErrReceiveLimitExceeded, totalBytes, limits.MaxBytes)
	}
```

消息不会丢失，但要等到 `ClaimMinIdle`（基线配置 300 秒，`configs/linkd.yaml:41`）之后才被 `XAUTOCLAIM` 接管。若每次读取都超限则会形成循环。

建议方向：增加单条 payload 预检，或在超限时对已知 stream ID 做显式处理。

### M6. cleaner 在幂等命中时仍重复投递 signal

`CreateEvent` 返回 `Created: false` 时，代码仍然继续 `Publish`（`internal/cleaner/handler.go:81`）。下游按 `event_id` 幂等吸收，不会产生错误结果，但 Kafka 重放期间会给 Redis 与 lifecycle 带来成倍的无效压力。

### M7. Kafka 吞吐受「一次一批」设计限制

Session 必须等整个 poll 批次全部确认后才能拉取下一批（`internal/consume/kafka/session.go:99-106` 的 `batchDone` 机制），批内任何一条慢消息都会拖住整批。配合 `BlockRebalanceOnPoll` 这是有意的正确性取舍，但文档未量化其代价，也没有给出批次大小的调优指引。

### M8. Kafka Session 的 `laneParts` 只增不减

每条 record 都重建一个 map 并赋值（`internal/consume/kafka/session.go:144`），而 rebalance 丢失分区后对应条目从不清理。内存会缓慢增长，且 `Pause` / `Resume` 可能作用在已不属于本消费者的分区上。

### M9. MySQL Event CAS 缺少 `RowsAffected` 防御性校验

`CompareAndSetEventResult` 在 `FOR UPDATE` 之后执行带 `AND version = ?` 的 UPDATE，但未校验影响行数就直接 `Commit`（`internal/store/mysql/event.go:318-334`），而 Alert CAS 校验了 `affected != 1` 并返回 `ErrVersionConflict`（`internal/store/mysql/alert.go:294-299`）。

核对结论：当前**不构成缺陷**，因为该路径在行锁内已比对 `current.Version != expected`（`internal/store/mysql/event.go:301`），事务内是安全的。但两条 CAS 路径风格不一致且缺少防御层，隔离级别调整或引入触发器后可能出现 silent success，建议对齐。

### M10. 身份字段长度上限在三个实现间不一致

MySQL 限制 255 字节（`internal/store/mysql/repository.go:17,56-62`），Elasticsearch 限制 1024 字节（`internal/store/elasticsearch/repository.go:25,240-245`），memory 仅校验非空（`internal/store/memory/repository.go:593-600`）。使用 memory 开发、MySQL 上线时，边界长度的租户 ID 会到生产才失败，且契约测试未覆盖该边界。

### M11. `QueryAlertByEvent` 跨实现一致性语义不统一

端口注释声明「不承诺事务快照」（`internal/store/store.go:54-56`），但 memory 在单次 `RLock` 内读完 event 与 alert（`internal/store/memory/repository.go:533-548`），实际提供了近似快照；MySQL（`internal/store/mysql/query.go:17-33`）与 ES（`internal/store/elasticsearch/query.go:17-33`）为两次独立读取，可能观察到中间态。仅用 memory 测试会误以为该方法具备跨对象一致性。

### M12. Elasticsearch 错误分类与 MySQL 不一致

ES 侧 HTTP 失败返回自定义 `responseError`（`internal/store/elasticsearch/repository.go:108-119,195-219`），调用方需要 `errors.As` 解析 status，无法像 MySQL 侧那样用 `errors.Is` 统一匹配 `store.ErrNotFound` / `store.ErrVersionConflict`。上层错误处理因此需要按后端分支。

### M13. 缺少健康检查端点与链路追踪

telemetry runtime 只注册了 `/metrics`（`internal/telemetry/runtime.go:99-100`），没有 `/healthz` 或 `/readyz`，Kubernetes 部署缺少存活与就绪探针的着力点。可观测性目前只有 metrics，没有 tracing，尽管 `docs/design/observability.md` 讨论了 Trace 设计。

### M14. RabbitMQ 缺少 Nack 与死信通道

适配器仅实现逐条 `Ack(false)`（`internal/consume/rabbitmq/session.go:217-238`），没有 Nack、Reject 或 DLQ。且其 `CanPauseLane` 为 false，一旦触发 S2 的阻塞路径会直接进入 `globalBlocked`，整个队列停止消费。

### M15. `ProcessTimeout` 与 Complete 存在竞态

```go
// internal/consume/runtime.go:224-227
	if errors.Is(ctx.Err(), context.DeadlineExceeded) &&
		(outcome.Kind == OutcomeComplete || outcome.Kind == OutcomeDiscard) {
		return Retry(context.DeadlineExceeded, 0)
	}
```

Handler 已经执行完副作用（例如已完成 `CreateEvent`）仍可能被改判为 Retry，导致重复处理。当前依赖下游幂等吸收，但这一契约没有在端口文档中写明。

## 轻微问题与工程卫生

### L1. Kafka TLS 配置写死

```go
// internal/kafkaconfig/config.go:97
		options = append(options, kgo.DialTLSConfig(&tls.Config{MinVersion: tls.VersionTLS12}))
```

无法配置自定义 CA、客户端证书或 ServerName，内网自签证书场景不可用。此外该文件的导出常量（`internal/kafkaconfig/config.go:13-22`）缺少 GoDoc 注释，与 AGENTS.md 对导出常量的要求不符。

### L2. 包命名不一致

`internal/cleaner/process` 的包名是 `process`，`internal/lifecycle/process` 是 `lifecycleprocess`，`internal/store/mysql` 是 `mysqlstore`。同类位置采用了三种命名风格。

### L3. lint 配置可补充针对性检查

`.golangci.yml` 已启用 `bodyclose`、`errorlint`、`gosec`、`noctx` 等。对本项目还可考虑 `sqlclosecheck`、`rowserrcheck`（手写 SQL 与 rows 处理）与 `contextcheck`。

### L4. 已废弃的大体积生成文档

`docs/design/alarm-processing-flow.html` 为 archify 生成的单文件 HTML，13142 行，标题即为「已废弃设计快照」，且引用外部 Google Fonts CDN。该文件在审查期间已于工作区删除，此处仅作记录。

### L5. `make check` 对 Go 项目门禁引入 Node 工具链前置条件

`Makefile` 的 `check` 目标包含 `devtools-check`，后者执行 eslint、两次 tsc、vitest、vite build 与 server tsc。修改任意一行 Go 代码都需要完整前端构建，且未执行 `pnpm --dir devtools install` 时会直接失败。根 `README.md` 与 `AGENTS.md` 的质量门禁段落均未说明这一前置条件。

devtools 本身定位合理（独立只读排障工具，31 个 TS 源文件，11 个 dependencies），但它直连 MySQL/Elasticsearch 读取业务数据（依赖 `mysql2`），相当于在 Linkd 之外存在第二条数据访问路径，其认证与访问边界需要单独说明。

建议方向：拆分 `check-go` 与 `check-all` 目标，或在文档中明确 Node 24 / pnpm 11 的前置要求。

### L6. `Meta.Attempt` 与 `Redeliver` 未参与调度

三个适配器接入时 `Attempt` 均为 1（例如 `internal/consume/kafka/session.go:159`），Redis 的 `Redeliver` 来自 XAUTOCLAIM 但不递增 attempt，运维难以从指标区分首投与重投。

### L7. memory 实现的长扫描不可中断

`internal/store/memory/repository.go:172-181` 在持锁遍历过程中只在入口检查 `ctx.Err()`。仅影响测试与 mock 场景，优先级低。

### L8. 仓库卫生检查通过

`git ls-files` 中没有 `node_modules`、`dist`、`*.test` 或 `coverage.out`；`.gitignore` 对 devtools 构建产物、pnpm store 与编辑器文件的覆盖完整。未发现 tracked 空目录。

## 文档一致性问题

### D1. 配置指南把 Repository 写死为 MySQL

`docs/guides/configuration.md:223-224` 与 `docs/modules/alarm_clean.md:3-5,28-29` 写作「写入 MySQL」「启动时会连接 MySQL 并执行 schema 初始化」，但基线配置默认 `repository: elasticsearch`（`configs/linkd.yaml:16`），代码按 `storage.repository` 二选一（`internal/store/assembly/repository.go:46-53`）。该表述与根 `README.md:62-64` 及 `docs/design/architecture.md:42-43` 直接冲突。

### D2. 模块文档的实现进度与代码状态相反

`docs/modules/alert.md:27-28` 称「消息 Worker、同 fingerprint 分布式串行……尚未实现」，但 `internal/lifecycle/process/` 与 `internal/lifecycle/scheduler/` 已实现；`docs/modules/alarm_event.md:40-41` 称「清洗 producer 仍未实现」，但 `internal/cleaner/signal.go` 与 `internal/cleaner/handler.go` 已实现。

### D3. `message-consumption-runtime.md` 同文件内自相矛盾

第 9 行写「真实 Broker 集成测试尚未实现」，第 15 行写「真实 Broker 已通过集成验证」。结合本次门禁结果（集成测试默认 skip），应删除后者。

### D4. `architecture.md` V1 摘要措辞含混

`docs/design/architecture.md:56-60` 将「生产索引 Router/alias 生命周期应用服务」与已实现能力并列，又在下一句写「生产时间分桶 Router/alias 仍未实现」，与 `docs/design/alert-storage-partitioning.md:3-7` 矛盾。实际已实现的是 StaticRouter 加固定三索引。

### D5. 配置 schema 存在权威性分裂

`docs/modules/config/README.md:64-98` 的 schema 表完全缺失 `lifecycle.*` 子树；`docs/guides/configuration.md:87-113` 有 lifecycle 表，但缺 `storage.elasticsearch.api_key`、`basic_auth.*`、`lifecycle.output.kafka.client_id` 与 `security.*` 分项。

逐项核对的好消息：代码实际解析的配置键与 `configs/linkd.yaml` 的键完全一致，不存在孤儿配置项或未文档化的必需键。

### D6. 已实现但文档几乎未覆盖的部分

`internal/store/assembly`（后端装配与启动路径）、`internal/store/storetest`（共享契约测试）、`internal/testkit/rawgen`（E2E 确定性数据生成）、`tests/e2e/allinone/` 均未进入 `docs/README.md` 的阅读顺序。此外 `internal/consume/rabbitmq` 已实现但 cleaner 与 lifecycle 都未使用，文档未说明其定位。

### D7. 文档中描述但代码不存在的能力

Push HTTP / Pull 采集适配器、声明式 Filter/Transform Pipeline、维度补全与 OneModel 查询、`AlertPushTask`、HTTP API（close/query）、RocketMQ 适配器均只存在于设计文档中。按 AGENTS.md，设计草案没有对应实现不构成缺陷，但建议在 `docs/modules/README.md` 增加「仅设计、无代码包」一列，降低误读风险。

## 已排除的误报

审查过程中曾怀疑消费运行时主循环会在 `submit` 上与 worker 形成死锁：`dispatchReadyRetries` 位于 `select` 之前且可连续多次 `submit`（`internal/consume/runtime.go:300`、`:480-484`），而每轮 `select` 最多处理一个 `workerResults`。

核对后该结论**不成立**。`workerJobs` 与 `workerResults` 的缓冲均为 `MaxInflightMessages`（`internal/consume/runtime.go:97-98`），而任意时刻排队加在途的条目总数受同一个 inflight 上限约束，因此缓冲永远填不满，`submit` 不会阻塞。该尺寸是刻意设计的。

## 值得保留的设计

以下部分在审查中确认实现质量较好，后续重构时不建议改动其核心思路：

- `internal/domain` 的不变量校验体系：`Validate` / `Normalize` / `Clone` / `ValidateAlertReplacement` 覆盖了必填字段、状态机、活动态与终态互斥、CAS 替换的不可变身份字段。
- `store.VersionToken` 的不透明设计（`internal/store/types.go:19-40`），调用方无法解析或伪造版本令牌。
- 确定性身份生成：`digestStrings` 采用长度前缀哈希（`internal/lifecycle/id.go:89-101`），避免字段拼接歧义；fingerprint 路径白名单禁止不稳定字段。
- Redis lease 实现：SET NX PX 加 compare-token Lua 续租与释放、续租失败即取消工作（`internal/lifecycle/scheduler/lock.go:22-110`、`internal/lifecycle/scheduler/handler.go:155-176`），是标准且正确的写法。
- lifecycle signal 校验 `order_key` 与持久化 Event 身份，防止伪造 signal 绕过 fingerprint 串行（`internal/lifecycle/scheduler/signal.go:113-138`、`internal/lifecycle/scheduler/handler.go:80-95`）。
- Kafka 累计确认的连续前缀校验（`internal/consume/kafka/session.go:192-204`）与 receipt 不透明化，Handler 无法越权 ack。
- 遥测仪表覆盖面（`internal/telemetry/metrics.go`）：pipeline、messaging、store 三组指标，含直方图分桶视图。
- 启动前的无副作用校验与 all-in-one 的服务组退出语义（`internal/cli/all_in_one.go:42-48,90-133`）。

## 建议的修复顺序

1. **S1 + S2 + 死信出口**：三者相互纠缠，需一并处理。修复重试条目丢失、为阻塞状态提供恢复路径或改为 fail-fast、给重试耗尽的消息提供死信出口。这是当前唯一会导致生产静默停摆的组合。
2. **S5**：为 Elasticsearch 后端的活动 Alert 唯一性给出方案或明确风险标注，并把 ES 接入 `RunRepositoryContract`。默认配置即 ES，风险敞口直接暴露在默认路径上。
3. **S3 + S4**：确定崩溃恢复方案（恢复扫描器或 outbox），修正 `processor.go` 中把未实现机制写成前提的注释。
4. **M1**：为凭据增加环境变量或 secret 文件注入通道，这是容器化部署的硬门槛。
5. **M2**：将 schema 初始化从进程启动路径中剥离，改为显式的初始化命令或迁移步骤。
6. **D1 + D2 + D3**：修正会直接误导使用者的文档事实性错误。
7. 其余中等与轻微问题按迭代节奏处理。

## 未覆盖范围

以下内容不在本次审查范围内，或缺少验证条件：

- 未在真实 Kafka、Redis、MySQL、Elasticsearch 上运行任何验证，所有可靠性结论均为静态推导；
- 未执行 `make check` 的 `devtools-check` 部分，devtools 的 TypeScript 代码质量未审查；
- 未做性能压测，M4、M7 的吞吐影响没有量化数据；
- 未审查 `docs/design/v1-architecture.md` 中同步自 Kingeye 源分支的设计方案本身是否合理，只核对了它与本仓库实现的对应关系。
