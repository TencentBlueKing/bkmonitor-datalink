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
`StrategyRevisionQuery` 明确携带事件租户、SplitRecord 自身 ID 和执行修订；类型名不再使用宽泛的 `StrategyQuery`。
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
普通 Enrich 保留声明查询的展示语义。声明缺少目录引用且执行 item 有真实 `metric_ref_id` 时，
仅以唯一别名和相同物理字段补齐；不按顺序猜测，不把衍生指标替换为展开成员。
没有目录引用的物理查询不强制绑定目录，`metric_source=klc` 的日志索引集查询也不参与补目录身份；
编译导致的字段或别名变化不覆盖声明值。实际目录绑定的引用缺失、负数、重复别名和字段冲突仍明确失败。
执行 item 的数量、形状和错误状态校验仍保留；无关运行时字段不受声明查询 DTO 类型限制。
当前指标目录可用于 Linkd 展示和丰富，不在 KAC 查询时改写已冻结的兼容参数。

文案 Reader 从根部 `items` 读取实际查询单位、指标身份和编译算法，声明上下文来自同快照 `runtime_config`。
多 item 无可信选择时拒绝猜测；读取过程不改写原始 Event 或既存 Alert。
普通文案错误、panic、空白和非法输出沿用 `b6c21fe0` 的来源原文/固定文案降级；取消保留原控制语义。
本轮重放保留上游显式自定义 content 优先、控制面传输故障重试、同级触发/升级的内容更新，
以及历史算法只描述冻结条件和当前值的模板；缺少条件时降级，不补造历史比较值。
具体规则见[告警内容生成](alert-content-generation.md)和[任务恢复与内容边界](../reference/contracts/task-resume.md#操作顺序与内容边界)。

## 与 KAC 的边界

- 策略 URL 由 KAC 根据冻结的 `strategy_context` 生成，Linkd 不持有域名、产品路由或页面开关。
- 2026-10-29 调整为所有告警源均不再生成 `metric_query_params`；内置来源由 KAC 按准确 Event 与执行历史组装，外部来源由所属插件承接，历史已有参数保持读取兼容。
- 不新增 `metric-facts`；KAC 重建入口按执行引用取历史，指标目录仍服务 Linkd 的指标事实丰富，不覆盖既存冻结结果。先完成新版 KAC 与真实曲线验收，再部署此 Linkd 配套改动。
- 恢复字段使用准确恢复 Event 的有效补丁，透传其已有参数和策略关联，不用 opening 或 current 替换。
- APM/K8s/日志场景、文案降级和准确执行历史读取修复保留，历史记录不回填；`bk_cmdb_level` 本轮暂不修改。

## 验证证据与限制

以下保留各阶段的历史验证记录；2026-10-19 曾撤回重建，2026-10-27 再恢复配套改动。
旧测试数字不作为本次验证结论，当前职责以上文“与 KAC 的边界”为准。

本轮先将 SQL/Scan 断言及快照 fixture 改为新合同，复现旧表查询失败；只修 SQL 后再次复现根部快照被旧 wrapper 解码拒绝。
回归覆盖原始字节完整性、根部身份、默认/覆盖、跨业务、缺失不回退、声明与执行单位独立、取消和既有文案降级/场景链路。
普通测试只替换数据库边界，不读取个人配置或真实业务数据库。

2026-10-19 较早两轮的目标环境只读检查：旧表 `alarm_strategy_revision` 已不存在；当时执行历史表存在但无记录。
配置历史已有数据不表示执行历史可读。前轮对三个实际引用的缺失行为验证通过；当时正向 `FOUND` 尚未验收。
后续真实执行历史的正向读取结果见下文；真实曲线仍需独立验收。
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

## 2026-10-19 配置清理、查询命名及内容核对

`strategy` Processor 当前没有配置项，构造时不再读取、验证或规范化 `web_saas_module_url`，
也不为旧 EventSourceRelease 保留专门分支。整个传入 `config` 不影响装配与关联事实输出；
配置指南及 KAC 投影契约同步说明这一点。回归覆盖键缺失、正常/畸形 URL、非字符串和空值，
并通过真实装配入口及普通/云策略事实检查确认不再生成前端 URL。

Go 查询类型统一为 `StrategyRevisionQuery`，接口、Scope、数据源、遥测包装和测试同步重命名，
不保留旧类型别名。字段 `TenantID/ID/Version` 及执行修订协议不变。

当前创建链路为 `planNewAlert → buildNewAlertContent → BuildAlertContent`。
对已经允许创建 Alert 的合法 triggered Event，普通生成错误、panic、空白、超限或非法编码的生成结果
均返回可冻结的兜底内容，而不是以文案失败阻止 Alert 创建：

- 本次 Event 的来源 `content` 可用且非空白时逐字沿用，模式为 `source_fallback`；不借用既存 Alert 的旧内容。
- 来源为空、空白或编码不可用时使用“告警已触发，详细内容暂不可用。”，模式为 `default_fallback`。
- 显式提交的自定义 content 优先，允许空串；已冻结 EventPlan 重投不重新生成。
- 输入身份/判定非法、原始来源内容超限、整个 Worker Context 取消或到期仍返回错误。
- 显式控制面传输不可用保留事件重试，不冻结文案兜底；存储等其他阶段的失败不属于文案降级。

上述例外是当前已有边界。本轮只核对内容行为，不修改这些生成或重试规则。
证据见 [`internal/enrich/content.go`](../../internal/enrich/content.go)、
[创建与重投回归](../../internal/lifecycle/content_test.go)、
[控制面故障回归](../../internal/lifecycle/control_plane_failure_test.go)及
[自定义文案回归](../../internal/lifecycle/custom_content_test.go)。

本轮验证：新增忽略配置用例先复现原 URL/类型校验失败，移除校验后，真实装配、关联事实、
查询 Reader、遥测及内容创建/预览的全量 Go 测试、`go vet`、竞态和静态检查通过；
Console 451 个测试通过、8 个跳过，ESLint/TypeScript 及构建通过。`make check` 最后因本机缺少 Helm 中断，
不记为完整门禁通过。文档本地链接/锚点及 `git diff --check` 通过；本轮修改未提交、未推送。

## 2026-10-19 真实执行历史消费修复

目标环境新增 67 条执行历史后，只读检查确认全部快照字节、摘要和身份有效；
修复前共享 Reader 能解码全部快照，但目录补绑定使策略 18、73、82、121 的策略视图和文案配置读取失败。
18、73 的声明字段 `cpu_usage` 编译为物理字段 `usage`，执行查询没有目录引用；
82、121 是按索引集定位的日志指标，不能因缺少目录引用而被判为目录绑定错误。
本轮按上文边界收窄补绑定，不新增目录反查或历史兼容层，也不替换声明字段、表达式或别名。

回归先复现四类非目录查询读取失败，修复后策略视图、文案配置均可读取；
仍覆盖真实目录引用的唯一绑定、引用缺失/负数、重复别名、字段冲突、执行展开歧义和衍生身份保留。
使用目标环境只读事务重读同一批 67 条执行历史，策略视图和文案配置均 67/67 成功，
管理业务、模板/配置身份、名称/数据来源及文案运行时单位、指标身份与快照一致。
这些记录全部为默认策略、执行修订 1，不能替代覆盖策略和多修订的真实环境验收。

此前 KAC 迁移查询层额外保留历史分组所需的 `bk_host_id`，该查询层现已撤回；
发布侧 `A/a` 差异和真实 Event 缺少其他分组维度的后续结论，见 Kingeye
`docs/specs/kac-linkd-integration.md` 的“发布别名一致性修复与拓扑维度缺失定位”。
本次不修改发布器、不回填历史，不以读取成功代替曲线、邮件或 Kafka 链路验收；修改仅留在本地。

本轮自审及门禁：`make check` 的格式、全量 Go 测试、`go vet`、竞态和静态分析通过；
Console 初次运行因 Node 26.7.0 实验性 WebStorage 导致 `localStorage` 不可用，
仅为测试进程设置 `NODE_OPTIONS=--no-experimental-webstorage` 后，`make console-check` 通过
（451 个测试通过、8 个跳过，ESLint/TypeScript/构建通过），未修改 Console 源码。
`make helm-check` 和 `make release-check` 中的 Chart 检查因本机缺少 Helm 未通过；
发布脚本的 7 个 Python 测试及 4 个镜像工作流测试通过，不记为完整 `make check` 通过。
KAC 174 个相关回归、Ruff、文档硬检查、受影响文档 30 个本地链接/锚点和两仓 `git diff --check` 通过。
自审核对目录补绑定仍原子生效、声明不被覆盖、真实缺失维度继续拒绝，未发现本轮修复的新增阻断问题。


## 2026-10-19 发布别名修复与拓扑维度缺失定位

后续 Kingeye 本地修复 Log/APM 编译器固定输出 `a` 以及空查询合成仍生成 `a` 的问题，
保留声明别名大小写及表达式。365 个相关回归通过，同批 11 份真实声明内存重编译均为 `A/A`；
没有调用保存或发布。已冻结的错误执行历史不改写，既有 SplitRecord 载荷需重编译并发布新执行修订，
仅刷新 Redis 不能修复。本轮 Linkd 生产代码无改动，策略名称、数据来源、场景处理和文案降级边界不变。

真实 Event 的 `source_raw_data.dimensions` 已缺少 `bk_cmdb_level`，持久化事件同样缺失，
不是 Linkd 清洗或丰富丢失。策略 97、125 的执行分组仍要求它，但目标环境元数据将
`system.cpu_summary.bk_cmdb_level` 标为 `influxdb_disabled="true"`，默认存储为 InfluxDB；
Transfer 入库前排除此维度。当前 Alarmd 对查询缺失的声明身份维度补 JSON null，
标准事件转换再省略 null；该中间过程由源码与两个已有回归证明，未抓取这两条事件当次 UQ 响应。
此前 KAC 迁移探针对缺少必要维度明确拒绝；该新查询路径现已撤回。本轮不修改该维度的声明、Transfer、Alarmd 或 Linkd 行为。
详细证据和验收边界见 Kingeye `docs/specs/kac-linkd-integration.md`
“发布别名一致性修复与拓扑维度缺失定位”，该节说明了暂不修改该维度的范围；不能因 InfluxDB 禁写就全局删除此字段。实际曲线尚未验收。
