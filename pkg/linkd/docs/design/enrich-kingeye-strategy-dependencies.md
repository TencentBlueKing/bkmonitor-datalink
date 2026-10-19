# Enrich 对鲸眼监控策略的读取需求

本文记录当前 Linkd 策略读取边界。实现见 [`internal/enrich/datasources/strategy_revision.go`](../../internal/enrich/datasources/strategy_revision.go)。
2026-10-19 本地修复基于 `b6c21fe0`，保留该提交的文案统一降级、特殊检测场景、APM 附加维度和创建/预览流程；本轮仅本地提交，不推送或部署。

## 历史契约与版本

Kingeye `02c22521ef` 在 2026-10-17 改写历史契约，最新主仓 `a8b5c23c61` 沿用该接口。

| 项目 | 10/17 前 | 当前合同 |
| --- | --- | --- |
| 历史表 | `alarm_strategy_revision` | `alarm_strategy_execution_revision` |
| 身份 | 旧 Strategy 身份及修订 | 默认或覆盖 SplitRecord 自身 ID 及执行修订 |
| Reader | 租户、管理业务、ID、修订 | `get_strategy_revision(bk_tenant_id, strategy_id, strategy_revision)` |
| 管理业务列 | `bk_biz_id` | `strategy_bk_biz_id` |
| 快照存储 | BinaryField | UTF-8 TextField |
| 执行查询/算法 | `payload.strategy.items` | `payload.items` |
| 冻结声明 | `payload.strategy.runtime_config` | `payload.runtime_config` |

旧表与新表都可能使用 schema=1，不能据版本号自动兼容两种快照。
来源水位 `source_resource_version`、当前态修订 `record_revision`、缓存生成号和执行修订职责独立。
`labels.strategy_version` 与 `strategy_context.strategy_set_split_record_version` 表示执行修订，不能按数字大小猜来源或把它当成配置历史修订。
`alarm_strategy_split_record_revision` 是配置历史；本告警读取链路不使用它补齐缺失的执行历史。

普通文案的配置/绑定错误不再阻断整个 Event；无法生成内容时创建与只读预览优先保留非空白来源 content，
原文为空/空白则使用固定文案；显式自定义 content 仍可为空。控制面传输故障保留重试，读取器不借用其他版本或租户的数据。现行边界见[算法描述与原文保留](../reference/contracts/standard-event.md#算法描述与原文保留)。

## 当前读取方式

Event 的 `strategy_id`、`strategy_version`、`bk_biz_id` 须为正整数。
共享 Reader 按 `(bk_tenant_id, strategy_id, strategy_revision)` 精确读取新执行历史表，
单条 SELECT 最多两行、SQL 端限制快照为 4 MiB、超时为 5 秒。
先校验原始 UTF-8 字节数和 SHA-256，再判定 schema；根部租户、ID、修订、管理业务必须与查询及表列一致。
随后核对冻结资源的租户、模板、Config UID、default 标记和执行策略 ID。
缺失、损坏、歧义和不支持的 schema 均明确失败，不查询旧历史表、SplitRecord 当前态、revision head 或 Redis 兜底。

管理业务来自执行快照，事件业务仅用于独立业务关系检查；全局策略不能按事件业务筛选历史。
默认与覆盖各用自身 ID 和独立执行修订，当前态后来删除、禁用或修改不改变历史结果。
Scope 为同一 Event 复用读取结果；Strategy、Resource、Display、Metric 等 Processor 共用此依赖。
模板名称仍查同租户 `home_application_monitortemplate`，仅作展示，不改写历史定义。

## 冻结材料的消费

`runtime_config.metadata/spec` 提供名称、分类、模板、Config UID、默认/覆盖、模型及不透明实例身份。
普通 Enrich 保留声明查询的展示语义；缺目录引用时，仅以唯一别名和物理字段从同快照执行 item 补 `metric_ref_id`，
不按顺序猜测，不把衍生指标替换为展开成员。无关运行时字段不受声明查询 DTO 类型限制。
当前指标目录可用于 Linkd 展示和丰富，不改写 KAC 从历史 item 构造的查询定义。

文案 Reader 从根部 `items` 读取实际查询单位、指标身份和编译算法，声明上下文来自同快照 `runtime_config`。
多 item 无可信选择时拒绝猜测；读取过程不改写原始 Event 或既存 Alert。
普通文案错误、panic、空白和非法输出沿用 `b6c21fe0` 的来源原文/固定文案降级；取消保留原控制语义。
本轮重放保留上游显式自定义 content 优先、控制面传输故障重试、同级触发/升级的内容更新，
以及历史算法只描述冻结条件和当前值的模板；缺少条件时降级，不补造历史比较值。
具体规则见[告警内容生成](alert-content-generation.md)和[任务恢复与内容边界](../reference/contracts/task-resume.md#操作顺序与内容边界)。

## 与 KAC 的边界

- 新 `built_in_bk` 不生成 `metric_query_params`；指标名、单位、表、维度等其他丰富字段继续保留。
- KAC 独立指标、恢复指标和邮件共用历史查询入口，使用三参数 Reader 及根部 item。
- 已有非空旧 `metric_query_params/metric_group_params` 优先走来源插件，不因存在新上下文或旧长整数版本强制升级。
- `metric-facts` 默认读取 opening Event，恢复读取使用已保存并校验的准确恢复引用；不从最新 Event 或当前策略猜版本。
- `RecoveryFields` 只投影准确恢复 Event 已冻结并生效的丰富结果，不重跑丰富。
- 策略导航由 KAC 使用关联事实组装；APM/K8s/日志专属 Processor、字段和关联整体保留在 Linkd。
- 保存了参数不代表旧查询链支持对应语义；原先不支持的 PromQL/日志额外函数及混合组合不算迁移遗漏。

## 验证证据与限制

本轮先将 SQL/Scan 断言及快照 fixture 改为新合同，复现旧表查询失败；只修 SQL 后再次复现根部快照被旧 wrapper 解码拒绝。
回归覆盖原始字节完整性、根部身份、默认/覆盖、跨业务、缺失不回退、声明与执行单位独立、取消和既有文案降级/场景链路。
普通测试只替换数据库边界，不读取个人配置或真实业务数据库。

2026-10-19 的目标环境只读检查：旧表 `alarm_strategy_revision` 已不存在；执行历史表存在但无记录。
配置历史已有数据不表示执行历史可读。前轮对三个实际引用的缺失行为验证通过；本轮正向 `FOUND` 及真实曲线尚未验收。
不得通过写入伪造快照、使用当前配置或改读配置历史制造正向结果。
重放前基于 `b6c21fe0` 的本地验证：

- Linkd 全量普通测试、`go vet`、竞态检测、`golangci-lint` 均通过。
- Console 格式、ESLint、TypeScript、构建通过，448 个测试通过、8 个跳过。
- `make check` 在最后的 `helm-check` 因本机缺少 `helm` 中断，不能记为完整门禁通过。
- 独立本地 MySQL 测试库只建立新执行历史表及模板展示表；默认与覆盖策略各读取执行修订 1、2，共 4 组通过。读取使用只读事务，测试库已删除；这是构造数据的 SQL/TextField 集成验证。
- KAC 的历史查询、邮件、导航、恢复投影、展示及真实公共 Reader 回归共 177 个通过；包含损坏快照拒绝和旧参数优先分流。

本地构造数据不能替代目标环境正向验证；测试通过不表示已经部署或完成 Kafka 链路验收。

## 2026-10-19 rebase 验证与自审

本轮将四个本地提交重放到 `origin/feat/linkd-dev@cc0c7150`，不推送。
文档冲突保留当前任务加载来源配置的运行时语义，同时将策略历史解码对齐到根部 `items/runtime_config`。
上游显式自定义 content（包括空串）、控制面传输故障重试、持续触发的单次 CAS 内容更新及历史算法条件模板均保留；
普通文案失败沿用来源/固定文案降级，进程/端口与重启场景保持有界事实校验。

自审补齐一处 Reader 状态顺序：先验证原始字节完整性，再判断支持的 schema，最后检查 JSON。
未知 schema 配合当前版本无法解析的 JSON 时，返回 `strategy_revision_schema_unsupported`；
摘要损坏仍优先返回 `strategy_revision_corrupt`。新增回归先复现错误分类，再验证修复。

- `make check` 的普通测试、`go vet`、竞态、`golangci-lint`、Console ESLint/TypeScript、构建通过；Console 451 个测试通过、8 个跳过。
- 完整门禁仍在最后 `helm-check` 因本机没有 `helm` 中断，不记为完整通过。
- 上述 Reader 顺序修正后，重新运行 `make fmt-check test vet race lint FORMAT_CONSOLE=0` 通过。
- 修正后的新 Reader 再次通过本地独立 MySQL 默认/覆盖各两个执行修订，共 4 组；只建新表和模板展示表，读取使用只读事务，测试库已清理。
- 以上真实 SQL 测试使用构造数据；目标环境执行历史与真实查询、邮件、Kafka 端到端验收边界不变。

自审核对租户/执行修订定位、管理业务与事件业务分离、根部身份/摘要、缺失不回退、默认/覆盖独立引用，
文档核对修正配置指南中一处因上游章节更新而失效的 CMDB 锚点。未发现其余阻断问题；
原始主工作区的文档草案和未跟踪文件不纳入本轮提交。
