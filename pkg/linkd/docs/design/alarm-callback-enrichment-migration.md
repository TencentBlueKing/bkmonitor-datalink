# alarm_callback 清洗与丰富迁移执行计划

状态：执行计划 v1，已获用户整体认可，作为后续实施基线。
迁移边界和阶段安排已确认；完整字段与依赖矩阵在前两个阶段收敛。
2026-09-02 后续细化：用户要求先盘点旧回调读取字段，再逐项决定 Linkd 取值位置；
SourceRawData 定位为人工追溯资料，丰富尽量不读取，不作为默认输入或缺字段兜底。
旧输入读取点见[回调输入字段盘点](../research/alarm-callback-input-field-inventory.md)。用户已提出
Event 字段映射方案，见第 5.19 节；最新策略读取规则见第 5.23 节：完整 bk_strategy_id/bk_strategy_history_id
历史引用触发监控平台策略历史内容与鲸眼 StrategyConfig 两份读取，任一缺失按 partial 处理。
该规则替代第 5.20 节原先“鲸眼足够则不查平台”的方向。内容文案算法的等级映射见第 5.22 节；
智能算法专用图表增强已排除。
具体字段、平台读取契约和映射缺失处理尚未全部收敛，未冻结完整取值契约。
用户进一步明确可以决定上游数据的字段落点；后续优先按 Linkd 字段职责设计，不以旧回调格式为约束。
第 5 节保留决策依据，计划确认不表示迁移功能已经实现或通过运行验证。

日期：2026-09-02。

## 1. 目标与证据基线

把 Kingeye `src/kingeye/kac/alarm_callback` 中仍有业务价值的清洗、派生与数据补充能力迁入
Linkd，以 `AlertEnricher` 为告警丰富入口，按 Linkd 当前边界重新划分职责。

已确认交付范围：以已生成的有效 Event 为起点，完成丰富、Lifecycle 装配、存储和输出验证；
不包含旧监控回调的 SourceCleaner，不把原始回调直接接入作为本次验收要求。

执行主线：来源启用判断 → 读取丰富输入并校验策略引用成对约束 → 完整平台策略引用触发两份策略读取 →
记录依赖完整性，依据可用的分类元数据选择主分类 → 按字段来源执行实例定位及依赖查询 →
生成分组丰富结果与诊断 → Lifecycle 保存并输出 Alert。
仅在新 Alert 创建时执行；恢复、关闭沿用已有生命周期，不迁移旧终态补查。

当前仅完成计划与后续旧输入字段盘点，没有迁移代码、调用业务服务、运行业务测试或创建 Git 提交。

- 来源仓库：`kingeye@5597beae82d42b7c732bf4cc10f7cffeaaf44672`，调研时工作区干净。
- 目标仓库：`kingeye-linkd@9b698332c0f994563d6800940dc6be6763933feb`；保留用户已有的未跟踪脚本和工具。
- 已核对目标接口、生命周期调用点、领域模型、进程装配、来源 Cleaner 和相关测试源码。
- 已核对旧入口分类、处理器/转换器/清洗器的方法清单、代表性实现及现有回归测试；逐字段完整盘点
  和每个分类分支的可达性验证仍是后续第一阶段工作，不能宣称已完成全量行为对齐。

仓库已有的[回调依赖调研](../research/kingeye-alarm-callback-data-dependencies.md)针对
`kmc/home_application/utils/callback_utils/basic_push_alarm_data.py`，不是本任务指定目录。
两者的 K8s 查询等实现存在差别，不能把那份调研直接作为本次依赖清单。

## 2. 当前实现对迁移的约束

1. [enrich.go](../../internal/lifecycle/enrich.go) 的输入是 `domain.Event` 和待创建的 `domain.Alert`
   副本，结果仅包含状态和 JSON 数据，最终只能写 `Alert.enrich_status` 与 `Alert.enrich`。
2. [processor.go](../../internal/lifecycle/processor.go) 在创建 Alert 前执行丰富；首次触发和等级升级
   创建新 Alert 都会经过该入口。同等级更新、恢复和关闭不会重新丰富。
3. 丰富位于持久化前，CAS 冲突或失败恢复可能再次执行查询，不能把“创建阶段调用”理解成外部调用
   恰好一次。已成功保存的 Alert 则没有自动重丰富流程。
4. error、panic、非法状态或非法 JSON 被降级为 failed 和空对象；父 Context 取消会中止处理。
   部分依赖失败时若要保留已经得到的数据，需要返回合法的 partial 结果，而不是返回 error。
5. [领域校验](../../internal/domain/alert.go)锁定 Alert 的继承字段，当前 CAS 替换也不允许更新 enrich。
   来源 Event 除 `related_alert_id` 外不可变。后续补丰富需要另行设计更新与输出契约。
6. [进程装配](../../internal/lifecycle/process/process.go)目前固定使用 `NoopEnricher`。
   仅新增一个实现并不会使运行链路生效，必须完成配置、依赖创建、注入和资源释放。
7. [SourceCleaner](../../internal/cleaner/raw_event.go)当前只有 `standard` 实现。旧回调原始消息不能
   默认视为可直接进入 Linkd。本次已确认从有效 Event 开始，不新增旧监控回调 SourceCleaner。

另有一处资料冲突：根 `AGENTS.md` 提及 `internal/domain.AlertEvent`，但当前代码、测试和术语使用
`domain.Event`，仓库不存在该目标类型。本计划按实际 `Event` 分析，保留该差异待确认后修正文档，
不为名称差异新增兼容类型。

## 3. 功能归属与迁移范围

下表按已确认边界整理。具体字段映射与类别内部规则仍需按源码和样本盘点，不能据此宣称已经
完成全部行为设计。旧代码的“清洗”同时包含来源解析、展示加工、外部查询和生命周期信息组装，
不能按 Python 目录名称直接决定 Go 包归属。

| 旧能力 | 建议的 Linkd 归属 | 需要约束的行为 |
| --- | --- | --- |
| 来源 action、severity、时间、标识、原始维度解析 | SourceCleaner / EventFactory 的既有边界 | 本次以有效 Event 为前提，不新增旧监控回调的来源适配，不在 Enricher 重建这些事实 |
| 告警名称和内容加工 | 已确认迁入 enrich.display | 保留旧文案加工功能，不覆盖 Event/Alert 的 title、content，不修改下游展示逻辑 |
| 指标名、单位、策略别名、聚合信息、查询参数、维度展示 | AlertEnricher 的派生与元数据丰富 | 明确来源事实与展示结果的区别，不覆盖 Event 或 Alert 的继承字段 |
| 业务/集群/模块、对象模型、实例名称与拓扑、云区域 | AlertEnricher 的资源丰富 | 外部数据作为有来源的查询快照，所有读取按租户隔离，本次不新增缓存 |
| 日志主题、检索语句、关联信息、拨测节点/任务、K8s/APM/云信息 | 按类型选择的丰富步骤 | 明确类型判定、字段优先级和未命中结果，避免整段 JSON 相互覆盖 |
| 策略跳转 URL、`cw_labels`、动态分组 | 已确认保留在 strategy / resource 丰富分组 | 链接归入 strategy，动态分组及 cw_labels 归入 resource；不覆盖核心 labels 或改变下游权限逻辑 |
| 无数据过滤、实例拆分 | Event 创建前的接入语义 | 本次不新增来源过滤或事件拆分；无数据告警本身适用的丰富规则仍在迁移盘点范围 |
| 找不到策略 | 完整 bk_strategy_id/bk_strategy_history_id 引用下任一策略缺失按 partial，见第 5.23 节；其他分类失败按既定 failed | 保留已得到的有效信息与诊断，不阻断 Alert 创建，不猜测分类 |
| 旧终态时间格式化、关闭原因补查及文案转换 | 已确认不纳入本次迁移 | 不新增恢复/关闭丰富入口；终态处理与输出沿用 Linkd 现有生命周期 |
| bk_service_id、Namespace 空占位输出 | 已确认不纳入本次迁移，见第 5.31 节 | 两项旧方法均固定返回空字符串；实际 K8s namespace 仍按已确认规则输出 |
| 旧告警 ID 生成、Kafka 推送、线程池调度 | 使用 Linkd 已有能力 | 不引入时间/随机业务身份、第二套推送或无界并发 |

旧逻辑会直接重写名称、内容、模型和权限标签。本次已确认不得覆盖 Event 或 Alert 核心字段，
因此需要逐项确定这些加工结果在 enrich 中的归属。告警名称和内容已确认保留为 display 分组的
展示结果，不能据此自动改变下游对 Alert.title/content 的读取规则。

## 4. 执行阶段与交付物

### 阶段一：完成行为和依赖矩阵

逐一追踪 `entry → processors → converter → cleaner`，以输入输出与调用证据为准，盘点以下范围：

| 分类或分支 | 重点核对 |
| --- | --- |
| 基础监控 `BASE_COLLECT` | 系统指标、采集任务、监控来源、无数据、对象实例展示和拓扑 |
| 基于数据 `DATA` | 维度与实例关系、指标库、派生指标单位、模板与策略信息 |
| 日志指标 `LOG_METRIC` | 日志主题、指标别名/检索语句、内容裁剪、业务投影 |
| 日志关键字 `LOG_KEYWORD` | 检索语句、命中内容、关联信息及空字段语义 |
| 拨测 `UPTIME_CHECK` | 有/无 task_id、节点/任务名称和对象展示 |
| 云平台 `VMWARE` | 云策略、云对象、平台标识、多指标表达式和展示字段 |
| 接入对象 `ACCESS_OBJECT` | 本次排除；处理类存在，当前 `entry.py` 分类器与已核对样本均没有可达入口；行为矩阵保留源码证据与排除理由 |
| 横跨分类的 K8s/APM 分支 | cluster/namespace、应用/服务/实例身份、业务覆盖和标签优先级 |

每条规则记录：来源文件/符号、触发条件、输入字段、输出字段、默认值、查询依赖、失败行为、已有测试、
Linkd 归属、迁移/调整/排除的理由。七类名称来自旧入口的注册表，不等于七条路径都已验证可达。

外部依赖分别核对策略/模板/采集配置、指标库、Meta、CMDB 适配器、KLC、APM、Redis 动态分组和
K8s ES 查询。关闭原因补查记录为已排除的终态能力，不新增对应 client。逐项记录实际协议、认证、
租户传播、分页、数据所有者和可用的批量能力，不能因为 Linkd 已连接 Redis/MySQL 就假定可以
复用旧服务的数据空间。

交付：带源码快照的盘点记录、脱敏输入及依赖响应样本、每类迁移验收清单。

### 阶段二：收敛输入、字段与失败契约

在已确认边界内，将源码证据整理成可实现和可验收的契约。常规类型、命名和封装按仓库规范落实；
涉及缺少明确规则的业务选择或与已确认边界冲突时，再提出具体方案讨论：

1. 已确认输入是有效 Event，不新增旧监控回调 SourceCleaner。先按旧代码列出策略、模型/实例、
   维度、文案和查询参数实际需要的信息，区分原始输入、外查结果与处理中间字段；由用户逐项决定
   Linkd 取值位置。`SourceRawData` 只作为人工追溯资料，丰富尽量不读取；不得默认解析旧快照，
   也不得在普通字段缺失时自动回退该字段。现有输入盘点见[字段清单](../research/alarm-callback-input-field-inventory.md)。
   固定对 EventSourceID `built_in_bk` 应用本次规则，其他来源保持空丰富；新字段路径、优先级与局部缺失
   处理在用户明确取值契约后落实，不先实现解析器再要求输入适配。两份策略按第 5.23 节读取，
   与逐字段来源分别定义；不要求 Event 携带完整策略快照。
2. 在不覆盖 Event/Alert 核心字段的已确认边界内，明确各项旧字段加工结果的迁移位置。
   已确认名称和内容加工迁入 display；其他字段继续按业务分组盘点，丰富不得反向改变 fingerprint
   或主体身份。
3. 定义 enrich 的字段结构、类型、空值、多值、优先级、最大尺寸、步骤状态与必要的来源标识。
   已确认按业务含义分组，并以明确的 Go struct 定义结果；边界处转换为 `domain.JSONObject`，
   不复制旧大字典和 JSON 字符串嵌套。各分组的实际字段仍须逐项盘点。
4. 已确认不修改 Kingeye：旧接口由丰富内部 client 封装，旧数据库、Redis 和 ES 直接只读访问。
   逐项核对实际表/索引/key、协议、认证、租户和读取语义；不把缺少 HTTP 接口作为新增服务接口
   的理由，也不以 mock 替代真实接入完成。外部依赖封装仅供丰富使用。
5. 定义未命中、未配置、不适用、字段不足、超时、权限失败、响应非法分别如何映射结果。
   已确认丰富失败不阻断创建、允许丰富信息永久缺失，本次不增加丰富重试或补丰富机制。
   已确认局部失败保留有效结果并返回 partial；仍须区分丰富自身超时与父处理 Context 取消，
   按具体字段依赖确定失败后应跳过的派生字段。

交付：字段映射与失败矩阵。已确认术语写入现有 `docs/reference/glossary.md`，不另建重复词汇表；
跨模块取舍按仓库规则放入 `docs/design/`。只有形成难以逆转且需要解释的真实取舍才记录 ADR。

### 阶段三：实现一个贯通的基础监控切片

- 丰富入口只对 `built_in_bk` 来源生效；其他来源直接返回 succeeded 与空 enrich，不解析旧监控字段或查询旧依赖。

建议先选择有代表性样本的基础监控类型，贯通真实依赖、丰富结果、存储和最终输出：

- 保留 `internal/lifecycle/enrich.go` 作为消费方接口与生命周期保护边界。
- 实现拟放在 `internal/enrichment/`，包含分类处理、结果 struct、旧接口 client 和数据库/Redis/ES
  读取封装；不预建七个空包，不复制继承树，不新增供 Linkd 全局使用的旧系统 client 包。
- 在丰富内部实际需要查询的位置定义小接口，通过构造函数注入只读数据提供者；SQL 行、ES 文档
  和旧接口响应在这里转换，不向 Lifecycle 暴露旧 ORM 或存储类型。
- `internal/lifecycle/process` 只完成丰富组件的装配、注入和关闭，Lifecycle 核心继续依赖
  AlertEnricher。丰富的连接配置、凭据和 client 实例按组件持有，不注册全局连接或服务定位器，
  不把旧系统查询塞进 Linkd 自身的核心 Repository。
- 使用丰富包内常量 `built_in_bk` 匹配适用来源；其他来源直接返回 succeeded 与空 enrich，
  不解析旧监控字段或查询旧依赖。匹配来源再执行元数据查询及主分类选择，不由来源常量固定主分类。
- 按功能需要先读取分类元数据，再执行相应的实例查找和字段加工；单次调用中可以直接使用已经
  得到的查询结果。本阶段不为跨告警复用新增缓存、预热或刷新流程。
- 总超时、单次请求超时、并发、响应体、分页/结果数量和输出大小均有硬上限；本次不增加丰富
  重试，数据提供者也不因查询失败自动重试。
  预算须与 lifecycle 的处理超时和租约取消配合；取消后不得留下后台查询。
- 不引入进程内 TTL 缓存、Redis 二级缓存或缓存刷新机制。旧 Redis 中承担功能所需数据来源的
  投影仍按现有结构读取，不修改其生产或刷新逻辑。
- 接入配置校验、client 生命周期和脱敏可观测性；步骤失败保留已有有效结果，按确认规则返回 partial。

交付：对 `built_in_bk` 来源生效的真实 Enricher，一类告警可以从有效 Event 到 Alert 输出走通。

### 阶段四：按依赖复用扩展其余规则

建议顺序：基础监控及采集/无数据分支 → 基于数据 → 日志指标/关键字 → 拨测 → 云平台 →
K8s/APM 专项。`ACCESS_OBJECT` 已因当前入口不可达从本次实现范围排除，并保留源码证据与排除理由。
该顺序是实施安排，接口可用性确认后可调整。

每扩展一类，都完成字段映射、依赖接入、正常/边界/失败测试和输出样本。共用规则按真实复用提取，
类型差异用明确函数或步骤组合表达。对重叠的 K8s/APM/云规则测试执行顺序和覆盖优先级。

旧测试中的监控项别名优先、派生指标单位回退、APM 缺少应用 ID 时的行为可作为候选规则证据；
经确认才成为 Linkd 验收标准。旧 `--` 占位、1980 年哨兵时间和吞异常等实现不默认继承。

交付：矩阵中每个条目都有已验证实现或经用户确认的排除理由，不能靠 Noop 或空结果冒充完成。

### 阶段五：集成验证与文档交付

- 单元测试：各类型正常、缺字段、类型错误、无匹配、多匹配、依赖失败与部分成功；输入不可变；
  同一输入与同一依赖快照下结果稳定；不同租户相同资源 ID 不串数据。
- 来源适用测试：其他来源不访问旧依赖并返回成功空结果；`built_in_bk` 缺少分类必需字段时返回
  failed 与诊断；同一来源可根据告警内容进入不同主分类，来源匹配不替代查询的租户条件。
- 生命周期测试：首次创建、等级升级、同级更新、恢复/关闭、父 Context 取消、CAS 冲突和恢复；
  验证重复执行无业务副作用，已保存 Alert 的 enrich 不被重投覆盖，字段不能越权修改。
- 客户端测试：认证和租户传播、超时/取消、响应大小上限、非法响应、分页上限；有并发时
  增加退出、竞争和容量测试，并通过 race detector。
- 集成验证：实际选定的数据提供者、Repository 和 Kafka FinalHook；检查 `alert.enrich` 随快照输出，
  V1 envelope 与稳定身份不被无意改变。外部服务测试显式标记，不作为普通单元测试的隐式依赖。
- 按需执行 `make fmt`，最后执行只读 `make check`；文档链接与锚点单独验证。未执行的外部验证列出
  具体缺失环境或依赖，不用本地单测代替运行结果。
- 同步术语、生命周期/清洗器模块文档、配置说明、README；涉及已版本化外部协议时先确认兼容范围。

最终交付包含：功能矩阵、实现与测试、配置样例、依赖说明、真实验证结果和已批准的剩余限制。
没有用户明确要求时不创建 commit、不推送、不切换旧系统流量。

### 阶段检查点与仍需取证的事项

| 阶段 | 完成标志 |
| --- | --- |
| 一：行为和依赖盘点 | 各主分类及条件分支有来源符号、输入/输出、依赖和迁移结论；明确实际可达性及排除理由 |
| 二：字段与读取契约 | 明确输入路径、分组 struct、ID/空值/多值语义、字段优先级和失败矩阵；读取依据落实到真实协议或存储结构 |
| 三：贯通基础监控 | 一类有效 Event 经配置启用、真实读取适配器、Enricher 和 Lifecycle 装配产生可存储输出的 Alert；单测与真实依赖验证分开记录 |
| 四：扩展其余分类 | 每类有对应输出样本及正常、边界、失败验证；不能用空结果代替未实现的分类 |
| 五：验证与交付 | 代码门禁、文档检查有实际结果；外部联调记录环境、依赖与结果，未完成项明确披露 |

前两个阶段仍需核实的事实包括：所需输入在现有 Event 中的承载情况、旧 store
到数据库表的映射、Redis/ES 的租户作用域、各服务的认证与分页约定。`ACCESS_OBJECT` 已确认排除，
其源码存在但当前入口不可达，不再作为实施前置取证项。这些事实应从源码、样本和可用环境取证。

组内标识的类型、无匹配/多匹配语义和条件分支的覆盖顺序也尚未全部冻结；以字段矩阵记录。
若发现需要修改 Event 核心事实、增加来源适配或引入已排除能力，先报告差异，不静默扩大范围。

## 5. 已确认决策与设计依据

### 5.1 已确认原则（2026-09-02）

根据本轮用户澄清，确认以下迁移方向：

1. 不同告警分类依赖不同的外部数据，其中部分数据可由多个分类共用。
2. 每次 Enrich 根据告警特征判断适用分类，使用对应依赖，生成字段丰富结果；旧链路中的实例查找
   和后续字段清洗都应纳入迁移盘点，不能仅迁移 `cleaner/` 目录。
3. 丰富遵循追加原则，不修改告警核心字段。按当前接口落地为 `EnrichResult.Data → Alert.enrich`，
   同时报告 `enrich_status`；不借丰富覆盖 Event 来源事实或 Alert 的继承字段。
4. 按主分类组织丰富实现，各分类内部处理自己的特殊分支，共用的数据查询和字段转换按实际需要
   复用；BasicData 的特殊性保留在该分类内部。用户已明确认可这一组织方式。
5. SourceRawData 保存原始来源 payload，用于人工追溯；本次丰富尽量不读取，不作为默认输入或
   缺字段兜底。ExtraData 承载来源扩展字段，不为本次 Enricher 增设专用中间结构；各项必要信息
   在 Linkd 中的承载位置由用户基于输入盘点决定。Event 创建后，连同这两个字段在内的来源事实都不可修改，唯一允许补写的是
   `related_alert_id`，且关联后不能改绑。
6. 本次增加丰富超时控制，不增加丰富重试。丰富失败不阻断 Alert 创建，接受 Alert 缺少丰富信息
   的结果；不另行设计创建后的补丰富机制。
7. 局部失败时保留已获得的有效字段，跳过依赖失败数据的派生字段，返回 partial；无法完成分类
   等整体失败返回 failed；第 5.23 节规定的策略缺失或查询故障，明确按 partial 处理。
8. 丰富结果按业务含义分组，并使用明确的 Go struct 定义；多个分类产生的同类信息使用相同的
   分组结构。部分分组及字段归属已确认，完整组内 schema 和诊断信息字段尚未冻结。
9. 丰富结果中保存类型明确的诊断列表，记录受影响分组和简短失败原因码，用于解释 partial 或
   failed，不保存完整请求、响应、凭据或原始异常文本，不引入重试或补丰富行为。
10. 不修改 Kingeye。沿用已有接口时在丰富内部封装 client；原来读取数据库、Redis、ES 的依赖
    直接读取并封装。相关外部依赖暂时仅供丰富使用，不推广到 Linkd 全局。
11. 当前只考虑功能迁移，不新增缓存，不设计缓存预热、TTL、失效或刷新流程；读取旧 Redis
    业务投影仍属于已确认的依赖接入范围。
12. 保留旧告警名称和内容的加工功能，迁入独立 display 分组，以明确 struct 表达；不覆盖
    Event/Alert 的 title、content，也不修改 Kingeye 或下游的读取逻辑。
13. 维度展示同时保留结构化条目与旧规则生成的摘要文本，分别写入 display.dimensions 和
    display.dimension_text；条目使用明确 struct，不改写 Event.dimensions，不为这两种输出新增查询。
14. 以已生成的有效 Event 为迁移和验收起点，不包含旧监控回调的 SourceCleaner；覆盖 Event
    之后的丰富、Lifecycle 装配、存储与输出，不以旧回调直接接入作为本次交付要求。
15. 固定对 EventSourceID `built_in_bk` 启用本次迁移规则，使用丰富包内常量且不增加配置项。其他来源沿用 succeeded 与空 enrich，不查询旧依赖；
    启用来源执行分类及丰富，缺少分类必需信息通常返回 failed 与诊断；第 5.23 节的策略缺失或查询故障按
    partial 处理。不阻断创建，绑定不固定主分类。
16. 保留策略跳转链接、动态分组 ID 和 cw_labels：链接归入 enrich.strategy，动态分组与派生范围
    标签归入 enrich.resource。使用明确 struct 字段，cw_labels 保留旧字符串列表的含义，
    不覆盖 Event/Alert.labels，不增加下游展示、过滤或权限系统改造。
17. 本次不迁移旧终态时间格式化、关闭原因补查及文案转换，不新增恢复/关闭阶段的丰富入口。
    恢复、关闭沿用现有生命周期，已有丰富结果随 Alert 输出；不能宣称旧终态补查已经迁移。
18. 后续实施先列出旧回调实际读取的字段、用途与缺失影响，再由用户决定每项从哪里取值。
    不把旧 JSON 路径直接定为 Linkd 输入契约，不将查询后写入旧 alarm_info 的中间结果误认为
    上游必需字段；具体编号与证据见[回调输入字段盘点](../research/alarm-callback-input-field-inventory.md)。

具体输入字段、外部服务接口、类别集合、组内字段及逐字段依赖按前两个阶段落实，尚未全量定稿。
Event 创建前的来源过滤、拆分和结束阶段的终态补查均不进入本次 Enricher。
术语定义见现有[术语表](../reference/glossary.md)。

### 5.2 源码核对带来的细化

- 分类本身需要数据：旧 `entry.py` 先用 `alarm_info.strategy.id` 查询策略，再结合
  `config_type`、`monitor_item_type`、`metric_source`、对象模型等选择处理器。因此依赖读取应包含
  “分类前必要的元数据”和“分类后按需的数据”，不能假定所有外部查询都发生在分类之后。
- 实例查找是按类型执行的能力。旧代码有按模型/实例 ID、采集任务、IP/云区域、拨测任务等不同
  查找方式；不能把“必须找到一个 CMDB 实例”提升为所有告警丰富的前置条件。
- 主分类与类别内部的处理步骤要分开理解：旧入口按匹配优先级选择主处理器，基础监控的第二层
  匹配也只选择首个命中分支。`BasicDataClear` 内部追加 APM、云平台和 K8s 的特殊性，不能据此
  推导所有主分类都需要开放组合。注册表中的 `ACCESS_OBJECT` 也不能代替所有 APM 分支。
- 公共依赖表示多个规则共用，不表示每次 Enrich 都必须加载全部公共数据。

### 5.3 各主分类的实际差异

以下是对当前旧源码的静态核对，不代表所有路径已经运行验证：

| 主分类 | 实例或关联对象的获得方式 | Converter / Cleaner | 特殊处理所在位置 |
| --- | --- | --- | --- |
| 基础监控 | 模型与实例 ID、采集任务、无数据维度、IP/云区域或拨测任务 | BaseConverter / BaseClear | BasicEventProcessor 按优先级选一个预处理分支，通用 Cleaner 生成策略、指标、维度和拓扑等字段 |
| 基于数据 | 复用基础预处理，并在转换阶段解释数据维度及对象关系 | BasicDataConverter / BasicDataClear | 数据维度转换以及 APM、云平台、K8s 的条件追加 |
| 日志指标 | 从策略 source_config 取得日志主题 ID，以主题映射和配置回退取得名称 | BaseConverter / LogMetricCLear | 覆盖预处理，不经过基础监控的二次分发；专用 Cleaner 补主题、日志权限标签、名称并裁剪内容 |
| 日志关键字 | 同样围绕日志主题构造 instance_detail_info | LogCollectConverter / LogKeywordCLear | 专用维度转换；重写清洗入口，补检索语句、命中内容和关联信息 |
| 拨测 | 任务 ID 查 UptimeCheckTask，节点标识查 UptimeCheckNode | UptimeCheckConverter / BaseClear | 复用基础预处理，专用 Converter 展示任务、节点、目标地址和业务；没有独立 Cleaner |
| 云平台 | Converter 按 cloud_id + instanceid 查询 Cloud 与 CloudResource | VmwareConverter / PrivateCloudCLear | 云资源定位和维度展示在转换阶段；Cleaner 补平台 ID、云标签和单/多指标名称 |
| 接入对象 | 用模型的 source、ar_dimensionality 和告警原始维度，向所属服务查询展示名称 | AccessObjectConverter / BaseClear | 覆盖预处理；当前服务映射只列 kapm_saas，Converter 移除用于对象展示的唯一维度；没有独立 Cleaner |

基础监控预处理匹配顺序是：监控来源 → 采集任务 → 无数据 → 系统指标 → 拨测，首个命中即返回，
无匹配使用默认路径。类继承关系不能替代实际方法调用关系：日志两类和接入对象重写了预处理方法。

公共 Cleaner 仍包含条件能力：`BaseClear.clean_alarm_data()` 会调用 `k8s_field_add()`，
`LogMetricCLear` 和 `PrivateCloudCLear` 经 super 进入此入口，而 `LogKeywordCLear` 重写入口。
这说明 K8s 代码不只存在于 BasicDataClear，但不等于每类告警都会命中 K8s 条件；也不能把继承
带来的全部查询视为各类别必需的业务依赖。

上述证据分别来自指定源目录的 `processors/`、`converter/` 和 `cleaner/` 对应文件；
`ACCESS_OBJECT` 处理类存在，但当前 `entry.py` 分类匹配未见产生该值的分支，已核对样本也未覆盖；
本次确认将其排除，只在行为矩阵保留源码证据与入口不可达的排除理由。

### 5.4 已确认：以主分类组织丰富实现（2026-09-02）

分类器选择一个主处理路径，各分类负责自己的依赖查询与字段加工；多个分类确实
需要的查询和纯转换能力按需复用。BasicData 的条件分支先放在该分类内部，基础监控保留必要的
实例定位分支，不提前建设通用的多分类组合框架。此组织方式已获用户确认，尚未实现。

概念流程为：读取分类所需元数据 → 选择主分类 → 执行该分类的实例定位与依赖查询 → 加工丰富字段
→ 返回 EnrichResult。启用来源无法分类通常返回 failed；若因第 5.23 节规定的策略缺失或查询故障导致，
则按该节返回 partial 并跳过依赖分类的步骤。实际类别集合和字段来源在行为矩阵中落实。

Linkd 输入已有 Event；计划不以重建旧 KAC AlarmEvent 作为必要中间步骤，推送仍使用现有
FinalHook。具体类型和函数在输入契约确认后再确定。

### 5.5 已澄清：来源快照、扩展字段与 Event 不可变边界

用户指出 ExtraData 不是为丰富准备数据的字段。核对 `docs/design/define.md`、EventFactory、
StandardCleaner 和 `domain.ValidateEventReplacement` 后，明确以下语义：

| 字段 | 当前语义与构造行为 | 对本次迁移的约束 |
| --- | --- | --- |
| Event.SourceRawData | EventFactory 从接入 payload 构造的完整 JSON 对象快照，包含未映射为标准字段的数据；不等同于 MQ 信封或原始字节存档 | 人工追溯资料；本次丰富尽量不读取，不作为默认输入或缺字段兜底，不能写入分类结果、查询响应或丰富结果 |
| Event.ExtraData | 不进入核心字段的来源扩展数据；StandardCleaner 已将 payload.extra_data 复制到 EventDraft，再由工厂保存 | 不只是尚未使用的占位字段；不为 Enricher 构造专用参数袋，不接收 Event 创建后的追加数据 |
| Alert.Enrich | 新 Alert 创建前，由 Enricher 返回的补充信息 | 承载本次分类、实例定位与字段加工后需要保存的丰富结果，具体输出字段待定 |

Event 创建后，`ExtraData`、`SourceRawData`、维度、主体等全部来源字段保持不可变。
`ValidateEventReplacement` 仅排除 `related_alert_id` 后比较整个 Event；已存在的关联也不能改变。
Alert 从 opening Event 继承的 `ExtraData` 同样不可由丰富覆盖。`EnrichInput` 的深拷贝用于隔离
调用方，不能被解读为允许把输入副本当作丰富工作区。

此前把“Cleaner 提取一份丰富专用结构到 ExtraData”列为候选方案不符合本次字段定位，予以撤回。
虽然在 Event 创建前写入 EventDraft 不违反创建后不可变约束，但不能因此改变 ExtraData 的职责。
也不把 ExtraData 定义为禁止任何下游读取：它如果已有合法的来源扩展事实，可以按该事实的已确认
契约只读使用；这不同于专为 Enricher 建立一套中间字段。

后续用户进一步限定 SourceRawData 用于人工追溯，丰富尽量不读取；此前把“只读解析来源快照”
作为默认实现路径的推导已撤回。先盘点必要信息，再由用户决定读取位置，见第 5.18 节。
需要的解析值、分类上下文和外部查询响应仍保存在本次调用的独立内存结构中，最终仅返回
EnrichResult。既不回写 Event，也不将临时上下文持久化进 ExtraData。`condition_key` 表达稳定
观测条件，不能擅自等同于监控策略 ID。

当前仍只有 standard Cleaner，不能假定接入 payload 已经是旧监控回调；本次不新增旧监控回调
的接入解析，验收从有效 Event 开始，见第 5.14 节。若必要字段尚无已确认的承载位置，应记录
具体缺口并由用户决定，不能靠 SourceRawData 的可访问性跳过输入设计。

### 5.6 已确认：超时降级、不阻断创建、不重试或补丰富

用户确认本次只增加超时控制，暂不考虑丰富重试，接受丰富失败后 Alert 缺少丰富信息，也不新增
补丰富机制。分类必需的策略数据查询失败时，不能为了继续丰富而猜测分类；一般整体失败返回
failed，第 5.23 节规定的策略记录缺失或查询故障改为 partial。生命周期可继续创建 Alert 并进入原有
FinalHook 输出流程。允许创建不等于保证下游投递成功。

实现上应给丰富自身设置有界时间预算，查询遵守该预算并传播取消。丰富自身预算耗尽且父 Context
仍有效时，可以返回降级结果继续创建；父 Context 已取消或生命周期处理已超时时，仍遵循现有
取消语义中止本次处理，不能把它伪装成正常丰富失败继续写入。

诊断原因码按第 5.9.1 节的最新决定收敛，不增加 enrichment_timeout 原因码，也不在当前讨论中
展开总预算参数或性能优化。依赖返回失败及父级取消的既定处理语义继续适用。

本次不增加查询重试、丰富重试队列、后台扫描或重丰富更新接口。已有存储冲突处理、消息重投和
进程恢复机制保留，可能使尚未成功持久化的创建流程再次调用 Enricher；这不等于承诺丰富恰好执行
一次，也不是针对已创建 Alert 的补丰富。实现仍须可重复调用且不产生外部业务写入。

旧代码需要区别记录：`BaseProcessor.try_default_dict()` 确实把部分依赖异常降级为空字典；但
`entry.py` 未查到策略的告警不会加入已分类结果，`BaseProcessor.process_alarms()` 和
`clean_alarms()` 的逐条异常也可能使该条告警被跳过。因此不能把旧 alarm_callback 概括为所有
丰富或清洗失败都仍会输出该条告警。本节的不阻断创建是本次明确确认的 Linkd 行为。

### 5.7 已确认：局部失败保留有效结果

场景：已经完成分类，获得策略、指标和实例信息，但业务名称或动态分组查询超时。
保留已经获得的有效字段，跳过依赖失败数据的派生字段，以 `partial` 返回；失败原因使用
可定位且脱敏的原因码，按第 5.9 节保存为 enrich 内的结构化诊断。不能用空字符串或零值伪造查询成功。

现有调用边界支持这种结果，但实现需返回 `EnrichResult{Status: partial, Data: ...}` 与 nil error；
若同时返回 error，生命周期会把结果降级为 failed 和空对象。该局部失败策略已由用户确认。

### 5.7.1 已确认：旧查询无匹配沿用空结果语义

资源、Meta、CMDB、指标库等旧查询明确将 0 条结果处理为空对象、空字符串、省略字段或空列表时，
迁移继续使用对应空结果，不新增 partial 或 dependency_invalid。动态分组等已有合法空列表输出的字段
继续保留空列表。查询超时、连接失败、权限拒绝和响应非法仍属于依赖故障，按既定局部失败规则处理。
策略两份读取的 0 条结果继续遵循第 5.23 节已确认的 partial 特例。

实现验收按旧调用点区分“查询成功且 0 条”与“查询失败”，前者保持原状态，后者记录实际受影响分组。

### 5.8 已确认：按业务含义分组，以明确的 struct 定义结果

当前 EnrichResult.Data 和 Alert.Enrich 使用 JSONObject，没有预设内部业务字段结构。
现有 Kafka FinalHook 将完整 Alert 作为快照发送，不在该层转换 enrich 内部字段。
用户已确认结果按业务含义分组，并要求有明确的 Go struct 定义。结果结构与主分类的处理组织
分别表达数据语义和处理路径，不能根据处理器名称自动分组。

分组包含已确认的 strategy、resource、display、metric、log、apm、k8s 和 source。
多个分类产生的同类信息使用相同字段位置；每类只填充适用且有效的数据。
已确认字段归属见第 5.12、5.13、5.16、5.21.7 至 5.21.11、5.25.1、5.28、5.29、5.32 至 5.34 节；完整字段类型
及缺失语义仍随字段矩阵收敛，不能把设计 schema 当作已实现的外部契约。

实现方向：定义明确的结果总结构及各业务分组结构，分类实现以这些类型构造数据，不依靠
`map[string]any` 承担业务字段定义。与现有接口衔接时统一转换为 `EnrichResult.Data` 所需的
`domain.JSONObject`，序列化错误显式处理；此转换不修改 Event 或 Alert 的继承字段。

空组、缺失字段与合法零值的 JSON 输出规则见第 5.8.2 节，具体可选字段表达方式在组内 schema 中落实。
本轮仅确认分组与类型化方向，不把示例字段名称当作已冻结契约，也不直接复制旧完整告警对象。

#### 5.8.1 已确认：进入分组后仍保留旧 key 的资源前缀

2026-09-03，用户明确所有分类的输出 key 保留原有资源前缀。分组名称与字段前缀即使重复，
也不据此缩短字段名；本规则替代此前去掉前缀的命名方案，后续各字段表已同步修正。

日志保留 log_，APM 保留 apm_，指标保留 metric_，策略保留 strategy_，来源保留 source_。日志关联信息的
输出 key 恢复为旧 log_relate_info；输入仍为 Event.ExtraData.log_related_info。
K8s 的 bcs_cluster_id、cluster_name、workload_kind、workload_name、pod_name、container_name
等已保留原 key，不再改名，也不为旧字段统一添加原本没有的 k8s_ 前缀。

这项调整只修正此前被删除的前缀；字段分组、取值规则和此前单独确认的其他名称保持原约定。

#### 5.8.2 已确认：JSON 输出区分缺省与合法空值

2026-09-03，用户确认以下统一输出规则：

- 分组没有任何可输出字段时，省略整个分组。
- 字段未取得且没有已确认的默认值时，省略该字段；属于失败场景时，仍按既定规则保存诊断。
- 按旧规则正常得到的空字符串、数字 0、布尔值 false 和空列表均保留。例如动态分组查询
  正常但无对应记录时，输出 dynamic_group_id: []。

字段是否输出取决于是否取得结果及已确认的分类规则，不能仅按值的真伪或是否为零判断。
仍需遵循已确认的字段专用省略条件，例如日志关联信息为空时省略、必要维度缺失时省略整份
指标查询参数，以及首次异常点时间缺失时省略；本节不改变这些规则。

实现时用可选值或显式存在状态区分缺省与合法空值，避免 omitempty 误删已取得的空字符串、
0、false 或 []。没有业务分组的失败结果仍保留既定诊断；未启用来源的 Noop 继续返回空 enrich。
当前仅确认序列化规则，尚未添加业务代码或执行运行验证。

### 5.9 已确认：持久化结构化的分组失败原因

无法分类已按整体失败处理，无需再次选择默认分类。诊断信息用于解释结果：
若策略、指标分组有效而资源查询超时，仅有整体 partial 和部分业务字段，读取 Alert 时不能
直接区分某组不适用、来源信息不足还是外部查询失败。

用户已确认在 enrich 中另设类型明确的诊断列表，记录失败影响的分组和稳定原因码，例如 resource 与
dependency_invalid；不保存完整请求、响应、凭据或原始异常文本。业务分组仍保存自己的有效
字段，诊断信息不承担重试调度或补丰富职责。JSON 字段及当前原因码集合见第 5.9.1 节。

可预期的查询失败应转换为带诊断的 EnrichResult，通过 nil error 返回，由 Status 表达 partial 或
failed；否则当前生命周期兜底会将 Data 清空。诊断列表同样需要数量和大小上限，不能把任意外部
异常内容直接带入输出。未适用的分组不能仅因未执行就被记为失败。

#### 5.9.1 已确认：诊断原因码与受影响分组

2026-09-03，用户接受通用原因码与受影响输出分组独立表达的方案，并要求依赖类原因暂时
统一为 dependency_invalid，不细分依赖故障，不增加 enrichment_timeout 原因码，不展开性能设计。

| code | 含义与适用边界 |
| --- | --- |
| missing_field | 按已确认场景需要的字段缺失；例如缺策略引用标签或三个主机 ID 均缺失 |
| invalid_field | 字段违反已确认的校验规则；例如策略 ID 或策略历史记录 ID 不是正整数 |
| dependency_invalid | 未取得可用依赖数据，统一覆盖无匹配记录、连接或访问失败、请求超时、响应无法解析等情况 |
| classification_failed | 已取得分类所需信息，但仍无法确定丰富分类；因依赖失败无法分类时保留依赖根因，不重复新增此码 |

当前 code 仅保留上表四项，不再按具体依赖服务、协议或故障细节扩展原因码，也不另加故障
子类型字段继续保存已暂缓的分类。dependency_invalid 不表示一定“没有记录”，也不表示
一定是依赖返回内容损坏；它统一表达未取得可用依赖结果。

诊断列表保存于 enrich.diagnostics；条目结构如下，后续 Go 实现使用明确类型定义：

| JSON 字段 | 类型 | 规则 |
| --- | --- | --- |
| code | 字符串枚举 | 必填，使用上表原因码 |
| groups | 字符串枚举列表 | 必填，列出实际受影响的输出分组；入口失败、尚未进入具体分组时用空列表表示整体受影响 |
| dependency | 字符串 | 可选，标识发生问题的依赖；不包含连接串、凭据或原始错误文本 |
| fields | 字符串列表 | 可选，列出缺失或无效的字段路径，不保存字段值 |

groups 当前使用 strategy、resource、display、metric、log、apm、k8s、source，分别对应策略信息、资源信息、展示加工结果、
指标补充信息、日志专用信息、APM 专用信息、K8s 专用信息和来源补充信息。基础监控、拨测、云平台等处理分类
不能作为此处的分组名。一个根因实际影响多个分组时，在一条诊断中列出这些分组；不为由该
根因造成的后续跳过重复生成错误，也不把不适用或未受影响的分组列入诊断。

例如，CMDB 查询失败确实同时影响资源和对象名称展示时，记录一条 dependency_invalid，
groups 为 [resource, display]，dependency 标识 CMDB；平台历史策略和鲸眼配置各自失败
则按各自依赖记录，不因原因码相同而混为同一次失败。

code 不直接决定整体状态。缺少策略引用标签产生 missing_field，整体 failed；主机三项 ID
均缺失同样产生 missing_field，整体 partial。其他状态仍按已确认的具体场景判定。有效业务
结果与诊断一并通过合法 EnrichResult 和 nil error 返回，保留生命周期的既有异常兜底边界。

实现验收覆盖四项原因码、同一原因码对应不同整体状态、多分组根因只记录一次、两份策略
依赖分别定位、入口失败 groups 为空列表，以及不因策略标签非字符串而生成 invalid_field。
当前仅更新设计和术语，未修改业务代码或执行这些测试。

### 5.10 已确认：不修改 Kingeye，按现有数据访问方式在丰富内部封装

旧实现的数据访问方式混合了 Kingeye 进程内模型/存储调用、服务查询、Redis 投影和 ES 查询。
例如策略通过 StrategyConfig/CloudStrategyConfig.store.list 查询，指标库、拨测任务和云资源
直接使用模型查询；迁移到独立 Go 进程后，不能原样调用这些 Python 模型。

用户明确要求本次不修改 Kingeye，确认以下接入方式：

- 原来调用旧服务接口的依赖，在 Linkd 的丰富实现内部封装对应 client，继续使用已有接口。
- 原来从数据库读取的数据，直接按现有物理数据结构读取；需要追踪旧 store/模型到真实表和
  查询规则，不能只凭 Python 模型名猜测 SQL 或字段语义。
- 原来从 Redis 或 ES 获取的数据，直接读取对应数据空间，由丰富内部封装 key、索引和查询细节。
- 这些 client、读取适配器、数据结构转换及相关依赖暂时仅用于丰富，不新增全局旧系统集成层。

K8s 的读取依据已由第 5.30 节进一步统一为当前 OneModel 实例存储，不继续照搬 KAC 的
集群/Namespace 查询实现；实际 ES 索引由 OneModel 路由规则决定，见第 5.30.1 节。
该已确认调整不扩大其他依赖的接入范围。

由丰富组件持有自己的 client/连接池，通过构造函数组合所需依赖；进程装配层只管理整体配置、
注入与资源释放。配置入口仍可接入 Linkd 现有配置加载流程，但连接和查询能力不进入全局状态，
旧系统读取不并入 Linkd 用于 Event/Alert 的核心存储契约。

封装范围包括租户条件、认证、超时、结果上限、错误分类以及旧响应到内部 struct 的转换。
明确记录所依赖的 schema 和来源快照，承认直接读取与旧存储的耦合，不以增加接口包装宣称已经
消除这种耦合。本任务对 Kingeye 仅做源码与结构取证，不新增接口、修改表或变更其缓存生产逻辑。

### 5.11 已确认：本阶段只做功能迁移，不新增缓存

用户明确暂不考虑缓存。移除新增进程内 TTL 缓存和跨 Enrich 调用共享结果的设计，不展开预热、
失效、刷新或相关性能优化。一次调用中保存其已查询结果属于正常的数据传递，不形成跨调用缓存。

旧 Redis 中的动态分组等投影仍可作为本次功能所需的读取依赖；不因取消新增缓存而删去这些
功能，也不要求改造 Kingeye 侧已有缓存。Linkd 不增加这些数据的生产、刷新或回写流程。

后续工作聚焦三个具体产物：各分类的输入与字段迁移矩阵、丰富结果的分组 struct 定义、对应旧
接口/数据库/Redis/ES 的读取封装清单。仅保留功能正确性所需的租户隔离、超时取消、资源上限和
失败语义，不把性能优化项目增加到本次迁移范围。

### 5.12 已确认：旧告警名称和内容迁入 display 分组

当前旧代码明确包含这些可观察行为：

- `BaseClear.clean_name()` 优先取策略的 alarm_alias，否则组合对象名称和监控项名称生成告警名称。
- `BasicDataClear.clean_name()`、`LogMetricCLear.clean_name()`、`LogKeywordCLear.clean_name()`
  有各自的名称生成规则。
- `LogMetricCLear.clean_content()` 裁掉关联信息部分，`LogKeywordCLear.clean_content()` 另行
  加工关键字命中或无数据文案。

这些属于原迁移目标里的字段加工，但不能覆盖 Linkd 的 Event.title/content 或 Alert.title/content。
用户已接受保留功能，将加工后的展示名称和内容放入类型明确的 display 分组，对应
`enrich.display.title`、`enrich.display.content`。各分类的名称生成及内容加工规则仍需逐项迁移验证。

来源标题与内容继续作为核心来源事实保存，展示分组只表达经过旧业务规则加工的结果。
本任务不修改 Kingeye 或既有下游展示逻辑，因此不能仅凭新字段存在就宣称下游已改用该文案。
保留这些文案加工功能为独立展示结果已获确认。

### 5.13 字段盘点与已确认的维度展示输出

旧源码中的数据与展示不是完全相同的概念：

- `clean_bk_obj_id()` 获取 CMDB 模型标识，`clean_model_id()` 返回 Meta 模型 code，二者不能因为
  都叫模型 ID 就合并。`clean_bk_inst_id()`、`clean_model_inst_id()` 的适用条件和来源也不同。
- 业务、集群、模块和云区域分别具有标识与名称，应保留它们的配对关系。
- `clean_object()` 为主机或 K8s 节点使用实例展示名、IP、node 等回退值；云平台 Converter 还会
  拼接资源名称与云平台名称。因此展示对象文本不必等同于单一实例名称或主体 ID。
- Converter 已构造维度展示条目，含 name/value，云平台还保留 key，部分条目另有 real_key/real_value；
  `BaseClear.clean_dimension_info()` 再按展示名排序、执行特定过滤，拼接为 ` | ` 分隔的摘要文本。

以下字段归属均已确认；模型与实例、业务与拓扑的具体字段见第 5.13.2、5.13.3 节，
其余未列出的字段及完整 struct 定义继续随字段矩阵收敛：

| 旧信息 | 归属与确认状态 | 约束 |
| --- | --- | --- |
| Meta/CMDB 模型与实例标识、模型名称 | resource 的五个具名字段（已确认，见第 5.13.2 节） | 区分命名空间，不覆盖 Event/Alert.subject |
| 业务、集群、模块、云区域的 ID/名称 | resource 的八个具名字段（已确认，见第 5.13.3 节） | 保持 ID/名称关联，失败时保留能确定的部分 |
| clean_object 生成的对象展示文本 | display.object（已确认） | 表示经过来源规则加工的文本，不能反向作为实例身份 |
| Converter 的维度展示条目 | display.dimensions（已确认） | 使用明确的条目 struct，不替换 Event.dimensions |
| clean_dimension_info 的摘要文本 | display.dimension_text（已确认） | 由同一份展示条目按确认的排序/过滤/格式规则生成 |

用户已确认维度展示同时保留结构化条目与摘要文本。`display.dimensions` 保留 Converter、策略和依赖
共同形成的旧生成顺序；`display.dimension_text` 从条目副本按 name 升序排序，再执行旧过滤与格式化。
摘要排序不修改结构化列表顺序。依赖返回顺序与旧覆盖顺序保持原语义，不新增 key 字典序排序。
结构化条目来自原 Converter 已有的信息，
不为此新增外部查询；摘要文本承接旧清洗的可观察功能。源字段缺少 real_key/real_value 的情况
需显式表达，不能凭展示名称伪造原始身份。条目的具体字段类型、空值及显示规则继续按旧实现盘点。

补充源码事实：VmwareConverter.get_dimensions_display（44）为云维度输出 key，仅对 cloud_id、
instanceid 补充 real_key/real_value；UptimeCheckConverter 的部分目标地址条目仅有 name/value。
BaseConverter.get_obj_model_dim_display（442）在只有实例标识而缺少模型标识的分支中，构造的
条目有 real_value 而没有 real_key。字段集合与缺省规则见第 5.13.5 节，Go 类型见第 5.13.6 节。

#### 5.13.1 已确认：对象展示文本保存到 enrich.display.object

2026-09-03，用户确认旧 clean_object() 生成的对象展示文本保存到 enrich.display.object，
文本生成和回退规则按原分类迁移。此前已确认的输入位置、实例定位和局部失败规则继续适用。

[BaseClear.clean_object](../../../../../kingeye/src/kingeye/kac/alarm_callback/cleaner/base.py)（473）
按主机、K8s 节点或其他对象返回展示文本；日志指标与日志关键字的对应方法返回空字符串，
继续保留这些分类的旧行为，不仅因空字符串就新增失败诊断。

查询得到的模型、实例标识仍归 resource；对象展示文本不替代这些标识，也不用于改写
Event/Alert 的主体。展示阶段的文本回退不改变第 5.26、5.27 节已确认的对象定位输入规则。

实现验收覆盖结构化条目的生成顺序、摘要按 name 排序、摘要处理不改变结构化列表，以及不同分类的
文本生成与回退、日志分类的空文本行为、展示结果保存位置和来源事实不变。

#### 5.13.2 已确认：资源模型与实例信息保留旧字段名

2026-09-03，用户确认资源模型与实例信息统一放入 enrich.resource，并保留旧字段名称。
CMDB 与 Meta 的标识分别表达，不因部分分类取值相同而合并。

| 旧输出方法 | 新输出位置 | 含义 |
| --- | --- | --- |
| clean_bk_obj_id() | enrich.resource.bk_obj_id | CMDB 对象模型标识 |
| clean_bk_inst_id() | enrich.resource.bk_inst_id | 对应 CMDB 模型下的实例标识 |
| clean_model_id() | enrich.resource.model_id | Meta 对象模型代码 |
| clean_model_inst_id() | enrich.resource.model_inst_id | 对应 Meta 模型下的实例标识 |
| clean_model_name() | enrich.resource.model_name | Meta 对象模型名称 |

字段取值遵循旧逻辑及此前已确认的调整。旧
[BaseClear](../../../../../kingeye/src/kingeye/kac/alarm_callback/cleaner/base.py) 的 clean_bk_obj_id（721）
从模型信息取得 bk_cmdb_obj_id，clean_bk_inst_id（725）在没有对应 CMDB 模型时返回空字符串；
clean_model_id（812）返回 object_model_code，clean_model_name（816）读取模型名称。
这些适用条件继续保留，不能为填满五个字段而将一套标识复制到另一套。

主机来源 ID 按第 5.27 节的维度回退和模型限制处理；K8s 的 model_inst_id 对齐第 5.30 节
确认的 OneModel 实例定位结果，APM 的模型与实例派生遵循第 5.28 节。各分类原有的空值
与展示规则仍按对应旧实现迁移，查询或加工失败按既定局部失败规则保留有效结果与诊断。

本次只确认输出归属与字段名称，不新增身份生成规则，不把 display.object 当作实例 ID，
也不回写 Event.Dimensions 或修改 Event/Alert 的主体。Go 结构的具体字段类型与可选值表达
仍需结合既有输出和统一结果模型落实。

实现验收覆盖 CMDB 与 Meta 标识不同、无对应 CMDB 模型、主机回退、K8s/APM 专用派生，
以及五个输出字段的位置和来源事实不变。当前仅更新设计与术语，未修改业务代码或执行这些业务测试。

#### 5.13.3 已确认：业务与拓扑信息保留旧字段名

2026-09-03，用户接受业务与拓扑信息保留旧字段名并统一保存到 enrich.resource。

| 信息 | 新输出位置 |
| --- | --- |
| 业务 ID | enrich.resource.bk_biz_id |
| 业务名称 | enrich.resource.bk_biz_name |
| CMDB 业务集群 ID | enrich.resource.bk_set_id |
| CMDB 业务集群名称 | enrich.resource.bk_set_name |
| 模块 ID | enrich.resource.bk_module_id |
| 模块名称 | enrich.resource.bk_module_name |
| 云区域 ID | enrich.resource.bk_cloud_id |
| 云区域名称 | enrich.resource.bk_cloud_name |

取值优先级、名称查询与输出格式沿用旧分类逻辑及已确认的输入规则。旧
[BaseClear](../../../../../kingeye/src/kingeye/kac/alarm_callback/cleaner/base.py) 的 clean_bk_biz_id、
clean_bk_biz_name、clean_bk_set_id、clean_bk_set_name、clean_bk_module_id、
clean_bk_module_name（689–711）读取既有业务拓扑结果；云区域 ID（713）读取工作维度的
bk_target_cloud_id，云区域名称（717）从对应映射取得。各分类的覆盖规则继续分别迁移，
不将基础方法的输出格式强加给其他已有专用分支。

这里保存丰富得到的业务与拓扑信息，不覆盖 Event.Labels.bk_biz_id；业务输入仍遵循
第 5.24 节，K8s 业务归属沿用第 5.30.2 节的 bk_biz_id 读取及回退规则。
bk_set_id/bk_set_name 表示 CMDB 业务集群，K8s 集群信息继续放在 enrich.k8s；
bk_cloud_id/bk_cloud_name 表示云区域，不据字段名称将其与云平台标识合并。

标识与名称保持旧关联和输出含义；部分名称查询失败时保留已经确定的标识与其他有效结果，
按既定局部失败规则记录实际受影响分组。不通过改成列表、重新分组或覆盖来源业务标签
改变已确认的旧业务行为。

实现验收覆盖八项字段位置、各分类取值与覆盖规则、名称查询失败保留有效结果、CMDB 与
K8s 集群字段的分别归属，以及来源业务标签不变。当前仅更新设计与术语，未修改业务代码或执行这些业务测试。

#### 5.13.4 已确认：云平台标识保存到 enrich.resource.cloud_plat_id

2026-09-03，用户确认将旧 cloud_plat_id 保存到 enrich.resource.cloud_plat_id，取值和
适用条件沿用旧分类逻辑，并遵循已确认的输入位置。

旧 [PrivateCloudCLear.clean_alarm_data](../../../../../kingeye/src/kingeye/kac/alarm_callback/cleaner/private_cloud.py)
（6）直接输出 event_data.cloud_id；旧
[BasicDataClear.cloud_field_add](../../../../../kingeye/src/kingeye/kac/alarm_callback/cleaner/basic_data.py)
（116）仅在 data_source 以“云平台监控-”开头、且维度 cloud_id 为真值时补充该字段。
迁移时基础数据分支从 Event.Dimensions.cloud_id 读取，保留原有条件和空值处理；条件中
使用的 data_source 统一采用第 5.16.4 节确认的 StrategyConfig.spec.data_source。

#### 5.13.5 已确认：维度展示条目保留旧字段与独立缺省语义

2026-09-03，用户确认 enrich.display.dimensions 的条目保留以下五个字段，取值和转换
沿用旧分类逻辑，不为缺失字段补造内容。

| 条目字段 | 含义与缺省规则 |
| --- | --- |
| name | 展示名称 |
| value | 展示值 |
| key | 部分云平台条目提供，其他条目可缺省 |
| real_key | 旧分支提供的维度字段名，可独立缺省 |
| real_value | 旧分支提供的维度值，可独立缺省 |

key、real_key、real_value 按旧分支分别保留，不要求同时出现，不因其中一项缺失而填充
其他字段。例如，部分拨测目标条目仅有 name/value，只有实例标识的对象条目可有 real_value
而没有 real_key。缺省规则不改变此前已确认的维度来源、条目生成及摘要加工逻辑。

条目使用明确的 Go struct 表达，字段的具体值类型见第 5.13.6 节。
当前仅更新设计，尚未添加业务代码或运行验证。

#### 5.13.6 已确认：维度展示条目的 Go 类型

2026-09-03，用户接受以下类型定义，复用现有
[domain.Scalar](../../internal/domain/value.go) 表达字符串、有限数字或布尔值。

```go
type dimensionDisplay struct {
	Name      string         `json:"name"`
	Value     domain.Scalar  `json:"value"`
	Key       *string        `json:"key,omitempty"`
	RealKey   *string        `json:"real_key,omitempty"`
	RealValue *domain.Scalar `json:"real_value,omitempty"`
}
```

name、value 随条目输出；key、real_key、real_value 使用独立指针表达是否提供，nil 时省略。
指针非 nil 时保留其值，包括空字符串、数字 0 和布尔值 false。Scalar 需通过构造函数或
JSON 解码创建，不能用其无效零值代替数字 0 或缺省状态。

旧分类已有的值转换仍保留，先按旧规则得到展示值及维度值，再以对应 Scalar 类型承载；
不因统一结构而将所有 value 或 real_value 强制转为字符串。
以上为已确认的结构设计，尚未添加业务代码或执行序列化测试。

### 5.14 已确认：从有效 Event 开始，不包含旧监控回调 SourceCleaner

当前 `AlertEnricher` 接收已构造的 Event 和待创建 Alert；SourceCleaner 注册和配置校验目前
只支持 standard。旧监控回调的来源解析与创建有效 Event，是进入 Enricher 之前的另一项能力。

用户已明确本次以已生成的有效 Event 为验收入口：测试 Event 在创建时保留脱敏的原始 payload 快照，
验证分类、实例查找、字段加工、依赖读取和结果输出；同时完成 Enricher 在 Lifecycle 的配置装配，
验证存储与 FinalHook 快照。单元测试使用固定依赖响应，真实依赖的集成验证仍单列并报告结果。

本次不包括新增旧监控回调 SourceCleaner，也不修改事件身份、fingerprint 和接入确认流程。
其交付证明是 Event 之后的丰富能力可用，不能写成旧回调已可直接通过 MQ 端到端接入 Linkd。

这里的“有效 Event”指来源事实已经规范化，具备生命周期需要的租户、来源、稳定身份、关联键、
动作、等级和时间等信息，满足领域校验及已确认的来源映射语义；不要求已经完成策略分类、实例
查找、名称或拓扑补充。当前领域校验允许 subject 等字段为空，不能把“有效”解释为“丰富完整”。

Enricher 以 Event 为输入是当前接口与不可变约束；本次不实现旧监控回调 SourceCleaner
是用户明确选择的交付范围。接入能力的缺口单列，不以丰富组件完成代替该能力已实现。

仍须用代表性 Event 与脱敏来源样本核对丰富所需字段确实存在于用户确认的取值位置；原始快照
存在某字段不代表允许丰富读取它。区分真实来源样本与人工测试用例，不伪造输入可达性。
核心事实遵循已有 Event 契约，
不为通过丰富测试而改写 identity、fingerprint 或 subject。若旧逻辑涉及核心身份生成或事件拆分，
记录为 Event 创建前的职责和输入前提，不通过修改 Event 或新增 SourceCleaner 扩大本次范围。

### 5.15 已确认：仅对 EventSourceID `built_in_bk` 启用迁移规则

本次迁移固定以 `Event.EventSourceID == "built_in_bk"` 作为适用边界，`built_in_bk` 使用丰富包内常量表达，不增加丰富配置项。
该来源执行输入校验、
策略读取、分类和丰富；其他 EventSourceID 返回 succeeded 与空 enrich，并且不访问本次旧依赖。
来源匹配由内置常量直接完成，同一来源仍可产生基础监控、DATA、日志、拨测和云平台等分类。

`enrich.source.source_id` 同样沿用旧值 `built_in_bk`。两处当前取值一致：EventSourceID 是 Linkd
来源选择键，source.source_id 是迁移后的补充输出字段，两者保持各自职责。

已启用来源缺少分类必需信息通常返回 failed 与诊断；第 5.23 节的策略记录缺失或查询故障按 partial
处理。所有外部查询继续以 Event.BKTenantID 为租户依据，来源匹配仍需配合各依赖的租户隔离。

### 5.16 已确认：保留策略链接、派生标签和动态分组

指定旧源码中仍存在以下输出，它们不只是中间查询数据：

- `BaseClear.clean_alarm_data()` 将 `format_strategy_url()` 的结果写入
  field_extra_info.strategy_name.url；链接取决于策略配置、默认策略标记、对象信息和 Web 基础地址，
  日志关键字类有自己的 URL 构造方法。
- `make_cw_labels()` 把业务或资源范围生成字符串列表，例如 biz 与 biz|2；日志、云平台、APM、
  K8s 的相关分支会生成或替换 cw_labels。此处仅记录输出，不据此推断下游已经完成权限校验。
- `clean_dynamic_group_id()` 按模型和实例读取旧 Redis 投影，从其中的 group_ids 返回动态分组列表。
  旧 Redis 缺少记录时返回空列表，查询或解析失败需要适配已确认的局部失败语义。

用户已接受保留这些旧功能的计算结果，作为 enrich 中具有明确类型的补充数据：策略链接归入 strategy，
动态分组和派生资源标签归入 resource；字段名及列表形式见第 5.16.1 节，动态分组 ID 的
Go 类型已在第 5.16.6 节确定为 []int64。cw_labels 保留旧字符串列表的可观察含义，不直接覆盖 Event/Alert.labels，
也不因其名称自行增加授权逻辑。
链接的基础地址由丰富配置提供，不将 Kingeye 的进程配置依赖带入 Linkd。

本次只迁移输出能力及其原有依赖，不增加 Kingeye 页面、下游过滤或权限系统改造；
不能把这些值随 Alert 输出等同于旧消费方已能直接使用 Linkd 的 enrich 结构。保留范围和分组
归属已确认，动态分组与标签、策略 ID、名称、链接及数据源的输出字段已收敛，见第 5.16.1 至 5.16.4 节；
动态分组 ID 元素类型见第 5.16.6 节，URL 配置字段继续在后续 schema 中落实。

#### 5.16.1 已确认：动态分组与派生标签保留旧字段名

2026-09-03，用户确认动态分组和派生标签的输出位置及处理规则：

| 旧输出 | 新输出位置 | 内容 |
| --- | --- | --- |
| dynamic_group_id | enrich.resource.dynamic_group_id | 动态分组 ID 列表，Go 类型为 []int64；名称为单数，值仍为列表 |
| cw_labels | enrich.resource.cw_labels | 派生标签字符串列表 |

动态分组继续按旧模型与实例关系读取 Redis 投影的 group_ids。旧
[BaseClear.clean_dynamic_group_id](../../../../../kingeye/src/kingeye/kac/alarm_callback/cleaner/base.py)
（370）无对应记录时返回空列表；迁移保留此正常空结果，不把无分组视为依赖失败。
Redis 读取或解析失败按既定局部失败契约返回 partial 与 dependency_invalid 诊断，保留
其他有效结果，不以正常空列表掩盖依赖故障。租户隔离仍遵循已有读取适配要求。
具体读取键已在第 5.16.5 节确认对齐当前动态分组模块的写入规则。

cw_labels 按各分类原有的生成和覆盖顺序处理。基础规则、日志、云平台、APM、K8s 各自的
标签内容和适用条件不因结果分组而改变，也不把旧覆盖行为改成统一拼接或集合去重。
结果只保存到 enrich.resource.cw_labels，不覆盖 Event/Alert 的标签，不增加授权判定。

实现验收覆盖两个字段的位置与列表形式、无 Redis 投影时的空列表、依赖故障保留诊断和
其他有效结果，以及各分类标签内容与覆盖顺序。当前仅更新设计与术语，未修改业务代码或执行这些业务测试。

#### 5.16.2 已确认：策略名称与跳转链接归入 enrich.strategy

2026-09-03，用户接受以下策略展示输出位置：

| 旧输出 | 新输出位置 |
| --- | --- |
| strategy_name | enrich.strategy.strategy_name |
| field_extra_info.strategy_name.url | enrich.strategy.url |

名称取值沿用旧逻辑。旧
[BaseClear.clean_strategy_name](../../../../../kingeye/src/kingeye/kac/alarm_callback/cleaner/base.py)（752）
返回 Converter 已生成的 event_data.strategy_name；迁移时保留各分类的名称生成规则及
此前确定的字段来源，不将两个策略对象的同名字段合并后统一选择。

链接按 `BaseClear.format_strategy_url` 使用两类身份：`strategy_config_id` 来自
StrategyConfig/CloudStrategyConfig 的 `config_id` 并移除连字符；`monitor_strategy_id` 对应旧
`fetch_monitor_strategy_id()`，当前 Linkd 使用鲸眼配置的 `monitor_template_id`。平台输入中的
`bk_strategy_id` 与 `bk_strategy_history_id` 只服务平台历史读取和两侧关联。`monitor_strategy_id`
仅作为 URL 参数使用，enrich Value 中的字段名保持 `strategy_config_id`。

默认策略、云策略使用 `/#/kmc/manage/monitorStrategy/monitorStrategyDetails`，查询参数为
`id=<monitor_strategy_id>&strategy_config_id=<strategy_config_id>`。实例策略使用
`/#/kmc/scene/monitorViewDetails`，查询参数为 `bk_obj_id`、`bk_inst_id`、`strategy_id`、
`strategy_item_id`、`classId`、`object_model_code`。日志关键字后续按其覆盖方法移除 `classId` 与
`object_model_code`。Web 基础地址由丰富配置提供；任一必需查询身份缺失时省略 enrich.strategy.url，记录 dependency_invalid、groups
`[strategy]`，整体 partial，同时保留已取得的 strategy_name 等字段。

策略展示名称与跳转链接不承担平台历史校验；三个策略身份字段见第 5.16.3 节。

实现验收覆盖名称来源与分类规则、两个鲸眼 ID 的 URL 参数、平台 ID 不进入 URL、链接分支路径、
配置基础地址、缺任一鲸眼 ID 时省略 URL 并按 strategy 分组 partial，以及两项输出位置。
当前仅更新设计与术语，未修改业务代码或执行这些业务测试，也未验证链接目标页面的实际可用性。

#### 5.16.3 已确认：三个策略身份使用独立 key

2026-09-03，用户确认将三个不同语义的策略身份分别保存到 enrich.strategy：

| 输出位置 | 语义与来源 |
| --- | --- |
| enrich.strategy.bk_strategy_id | 监控平台策略 ID，来自 Event.Labels.bk_strategy_id |
| enrich.strategy.monitor_template_id | 沿用旧 clean_strategy_id() 的完整返回规则：监控模板命中时返回模板 id 或 name，未命中时回退 clean_strategy_name() |
| enrich.strategy.strategy_config_id | 鲸眼 StrategyConfig 的 config_id，也是当前策略 URL 使用的配置身份 |

enrich.strategy.strategy_id 和 monitor_strategy_id 均不再输出。`monitor_template_id` 保留旧可观察行为，字段值可能是
模板 ID、模板名称或策略展示名称；本次功能迁移不重新定义该字段。`bk_strategy_history_id` 只用于读取
平台历史记录，不进入 enrich.strategy 身份输出。

实现验收覆盖三个 key 的独立来源、monitor_template_id 的旧命中与回退路径，以及 URL 参数使用
strategy_config_id。

#### 5.16.4 已确认：所有分类的数据源统一读取鲸眼策略配置

2026-09-03，用户明确所有分类均从已取得的 StrategyConfig.spec.data_source 读取
data_source，直接保存到 enrich.strategy.data_source。

这项决策替代旧 clean_data_source() 的分类取值规则：不再通过监控模板与云类型名称拼接
“云平台监控-{云类型名称}”，也不再因监控模板未命中而将已有配置值置空。日志指标与日志
关键字同样使用本次依赖查询取得的 StrategyConfig，不为该字段单独重复查找策略配置。
StrategyConfig 获取失败时沿用已确认的 partial 与 dependency_invalid 规则，保留其他有效结果。

后续 APM 应用名称派生及基础数据云平台字段追加，继续按原有分支条件使用这一统一取值；
不另行生成一份旧 data_source 供内部处理。当前仅更新设计，未修改业务代码或执行运行验证。

#### 5.16.5 已确认：动态分组读取键对齐当前写入规则

2026-09-03，为确定 dynamic_group_id 的元素类型，进一步核对读取和写入代码：

- 旧回调的 clean_dynamic_group_id 使用
  [MetaRedisKey](../../../../../kingeye/src/kingeye/common/enum/cw_enum.py)（72）中的
  `meta_saas_cache_dynamic_inst_group|{cw_object_model_code}`，按实例 ID 读取 Hash 值。
- 当前动态分组模块的
  [get_dynamic_inst_group_cache_key](../../../../../kingeye/src/kingeye/base/domains/dynamic_group/constants.py)
  （116）构造 `<redis_key_prefix>dynamic_inst_group:{cw_object_model_code}`。
  [cache_dynamic_group_member](../../../../../kingeye/src/kingeye/base/domains/dynamic_group/operations/cache.py)
  （91）将整数 dynamic_group_id 加入实例的 group_ids 集合，再以 JSON 列表写入该 Hash。

两处源码的键规则不一致。用户已确认 Linkd 对齐当前动态分组模块的写入键规则：使用与
写入端一致的 redis_key_prefix，构造 `<redis_key_prefix>dynamic_inst_group:{cw_object_model_code}`，
继续按实例 ID 读取 Hash 值中的 group_ids，不再照搬旧回调的固定键模板。

动态分组读取按 Event.BKTenantID 从 Linkd 现有配置选择对应 Redis 连接，key 保持写入端格式
`<redis_key_prefix>dynamic_inst_group:{cw_object_model_code}`，Hash field 继续使用实例 ID。
租户连接缺失或连接选择失败时省略 dynamic_group_id，记录 dependency_invalid、groups `[resource]`，
整体 partial。租户隔离依赖连接/DB/前缀配置，不在 key 中另加租户片段。

此前确认的实例定位规则、无记录返回空列表、读取或解析失败返回 partial 与
dependency_invalid 仍然适用。本次只核对源码并确认设计，未连接 Redis 验证实际数据，也未修改业务代码。

#### 5.16.6 已确认：动态分组 ID 列表使用 []int64

2026-09-03，用户确认 enrich.resource.dynamic_group_id 使用 []int64，输出 JSON 数字列表，
例如 [12, 35]。当前写入代码的整数 ID 来源见第 5.16.5 节。

正常读取但无对应分组时，保留 []；该字段未取得且属于依赖失败时，按已确认的局部失败与
缺省规则省略字段并保留诊断，不能将故障结果输出为正常空列表。序列化时使用显式存在状态
区分缺省和已取得的空列表，遵循第 5.8.2 节。
当前仅确认类型及输出规则，尚未添加业务代码或执行运行验证。

### 5.17 已确认：终态字段加工与关闭原因补查不纳入本次迁移

旧 `BaseClear.clean_close_time()` 格式化 event_data.end_time；`clean_close_reason()` 在非异常态
优先使用 event_message.event_description，缺失时调用 get_close_reason，再转换关闭原因文案。
这些逻辑属于结束事件的处理，不是在异常首次触发时即可确定的资源或展示上下文。

当前 Linkd 的 AlertEnricher 仅在新 Alert 创建时调用，包括等级升级所创建的新 Alert；
来源恢复/关闭路径不会再次调用它。`sourceTerminalAlert()` 已使用 Event.OccurredAt 设置 EndAt、
Event.ActionReason 设置 EndReason。把旧关闭原因补查放到创建阶段无法覆盖实际调用时机，
也不能通过回写 Event 或更新已保存 Alert.enrich 达成该行为。

用户已接受本次不迁移旧终态时间格式化、关闭原因补查及对应文案转换，不新增结束阶段的丰富入口。
恢复、关闭及其输出继续遵循 Linkd 现有生命周期行为，已有 enrich 随 Alert 快照保留。
这项范围排除不影响恢复/关闭回归测试，也不排除异常创建时可获得的策略、指标、实例和展示信息。
上述范围已确认；不把旧终态补查记为已迁移或由现有生命周期等价实现。

### 5.18 已确认：先盘点输入字段，由用户决定取值位置

用户否定了“在已启用来源的 SourceRawData 根对象固定读取旧回调”的建议，明确要求先梳理
旧回调中被读取的字段，再由用户决定每项在 Linkd 中从哪里读取。SourceRawData 定位为人工
追溯资料，丰富尽量不读取；当前没有批准任何具体读取例外，也没有默认回退规则。

本轮已形成[回调输入字段盘点](../research/alarm-callback-input-field-inventory.md)：按公共输入、
策略快照、各分类对象定位、APM/K8s 维度列出旧路径、用途和源码证据；另列查询结果、中间字段、
已排除能力与尚未证明可达的分支。检查了旧仓库 14 个 origin_alarms 测试输入，不将其描述为
新的生产样本或完整运行验证。

该轮盘点时具体输入位置尚未指定；后续用户提出的映射方案及评估见第 5.19 节。
不能预先把 strategy.id 映射为 condition_key、把全部策略快照塞进 ExtraData，或在缺失时改读
SourceRawData。记录旧代码字段不是批准新 schema；查得的元数据、派生结果和调用上下文也不
自动成为 Event 新字段。

当前新增取证还发现 KAC K8s 调用点与公共 build_k8s_inst_id 的签名/租户参数不一致，详见盘点
第 6 节。此前关于 K8s 依赖的概述不足以证明这条路径能运行；后续依赖矩阵须区分 KAC 显式
ES 查询与当前公共函数的 cmdb_query 查询，不能将它们默认为同一协议。本轮仅记录差异，
不修改 Kingeye，不更改已确认的迁移范围。

### 5.19 用户输入映射方案与落地评估（待收敛）

用户基于字段盘点提出以下来源安排，并要求评估。本节记录已表达的选择、必要的工程适配和
尚待确认的解释；不将整套方案写成已经通过验证的输入契约。旧路径与消费证据继续只在
[输入盘点](../research/alarm-callback-input-field-inventory.md)维护，本节是 Linkd 映射的讨论位置。

**最新前提已确认**：用户拥有上游数据字段分配的决策权，可以直接确定推送数据落在哪个字段。
旧字段清单只用于识别需要的信息和业务功能，不要求兼容旧回调外壳、嵌套路径、别名或数据类型。
下表记录用户初案及后续已确认的更新；结合该前提后的推荐分配见第 5.19.5 节。推荐与初案
不一致的地方明确列出，并逐项标注确认状态，不将尚未确认的建议默认为契约。

#### 5.19.1 公共字段

| 盘点编号 | 用户指定来源 | 落地解释与状态 |
| --- | --- | --- |
| F01 | `Event.Labels["bk_strategy_id"]` | 监控平台策略 ID；与 Labels.bk_strategy_history_id 必须同时存在，不额外限定数据类型，见第 5.23.4 节。此前 strategy_id 键建议已替代，不读取 condition_key |
| F02 | `Event.Content` | 作为文案加工输入，只写 enrich.display.content，不覆盖 Event.Content 或 Event.Content |
| F03、F04 | `Event.Severity` | 合并为同一标准等级来源；旧数字等级与算法配置的比较必须先适配，不能直接字符串转数字或拿排序 priority 代替旧等级 |
| F05、F06、F07 | `Event.Labels["bk_biz_id"]` | 三处旧回调业务输入合并为来源业务；本套丰富要求该标签为数字 Scalar 正整数，见第 5.24.1 节；维度用于指标观测，外查业务归属进入资源丰富；三者不自动覆盖或互相兜底 |
| F08 | `Event.Dimensions` | 已是扁平 map，直接作为维度输入，不再遍历旧 dimensions[] 构造 map；输出展示不能反向删除或修改原维度 |
| F09 | `Event.Dimensions["bk_target_ip"]`、`Event.Dimensions["bk_target_cloud_id"]` | 已确认直接提供 IP 和云区域，替代主机组合 target；主机定位统一读取这两个键，拨测 target 保持原有语义，见第 5.26 节 |
| F10 | `Event.Dimensions["bk_host_id"]` | 已确认保留专用语义；仅在主机模型下与维度 bk_inst_id、bk_target_host_id 按顺序回退，不合并键名，三项均缺失按 partial，见第 5.27 节 |
| F11 | `Event.ExtraData["log_related_info"]` | 已确认替代 Dimensions 初案；作为独立日志关联内容输入，沿用旧加工和输出逻辑，不参与对象定位或维度展示，见第 5.25 节 |

工程上可以通过一个只读输入解析步骤形成独立调用上下文，统一处理 ID 类型、字段存在性和
别名，不需要重建 Python AlarmEvent。不得在 Enricher 内给 Event.Labels/Dimensions 补字段或
改写它们以满足旧函数的就地修改习惯。

#### 5.19.2 分类输入与“按旧逻辑”的解释

| 范围 | 用户方向 | 评估与待收敛边界 |
| --- | --- | --- |
| 基础监控、基于数据 | 从 `Event.Dimensions` 读取，保持原字段名 | 模型、实例、采集任务等仍按盘点中的具名键读取；F09 的主机 IP/云区域键已确认，见第 5.26 节。其他维度的 tags. 处理及别名仍须按用途收敛，不自动套用旧字符串替换规则 |
| 日志指标/关键字 | 按旧逻辑 | 在 F02/F05–F07/F11 的新来源约束下保留主题查询、名称加工、内容裁剪和关联信息输出；不再回到旧回调取这些公共字段 |
| 拨测 | 按旧逻辑 | task_id、node_id、url、target_host/port、target/type 等从维度取，查询任务和节点；拨测 target 保持原有地址语义，不按旧主机组合字段拆分 |
| 云平台 | 按旧逻辑 | 从维度取 cloud_id/instanceid，DATA 分支另用 type；其余对象/策略元数据沿用已确认依赖查询范围 |
| 接入对象 | 本次排除 | 处理类读取模型唯一维度的源码仅作历史证据；当前分类入口不可达，本次不设计新 Event 输入，也不实现该处理路径 |
| APM/K8s | 按旧逻辑 | 原专用维度键、模型/资源查询和字段派生是迁移依据；不据此继承 K8s 已发现的签名不匹配、旧全局租户或原地改写输入 |

这里将“按旧逻辑”理解为在用户已指定的新输入边界下保留原业务能力，不解释为撤销 F01–F11
映射、重新使用 SourceRawData、迁移终态补查或复制旧运行时缺陷。细分字段冲突仍需单独收敛。

#### 5.19.3 策略信息先明确时间语义，再决定字段结构

本节保留此前方案的讨论依据；两份策略的身份区别见第 5.20 节，最新读取规则见第 5.23 节：
完整平台 ID/版本引用触发两份读取，任一缺失按 partial 处理。“在 ExtraData.strategy 中内联完整快照”
不再是实施要求，“所有策略字段必须精确读取监控平台历史快照”也不是已确认约束。

用户初案要求策略快照“依然按旧读取路径”，后续明确上游可控，不必受旧回调格式约束。
当前 Event 没有 Strategy 字段，但这不是落地阻断：可以设计来源扩展的承载结构；
Labels/Dimensions 使用 DimensionMap，不适合存嵌套策略对象。

**此前候选，已由第 5.20 节的引用查询方向替代**：保留告警发生时的查询/算法配置快照，由上游提供到
`Event.ExtraData["strategy"]`。其内容是创建 Event 时已有的来源事实，后续只读；
不是 Enricher 外查后回填的缓存或分类中间结构。设计时遵循：

- 只保留丰富需要的监控项、查询、单位、表达式、函数和算法信息。items/query_configs 可作为
  候选组织方式，但不必复制旧完整对象或继续假定所有处理都只读第一个监控项。
- F01 策略查询标识的最新契约为 Labels.bk_strategy_id，版本为 Labels.bk_strategy_history_id，
  见第 5.23.4 节；F05–F07 仍读取 Labels.bk_biz_id。
- 不再重复存放与 Labels 同义的 id/bk_biz_id，减少多处来源和冲突。若确需不同语义的策略业务
  作用域，应单独具名设计，不能作为同名字段的隐式回退。
- 若采用此建议，有效 Event 的构造方必须在创建时提供该扩展；StandardCleaner 已支持
  payload.extra_data，不需要为了试验该形态新增旧回调 SourceCleaner。此能力不证明实际
  来源已经提供字段，也不意味着本轮要迁移来源适配。

该建议不更改既有 ExtraData 的领域定位：允许承载来源扩展事实，禁止承载 Enricher 的查询结果
或工作区。当前尚未批准该存放位置，也未批准 SourceRawData.strategy 读取例外。
改查当前策略配置是另一种设计，但它改变快照的时间语义，不能假称与保留旧快照等价。
例如触发时阈值为 90，消费前改为 95，旧事件的查询/算法信息采用哪一版会影响解释。
先确认是否保留发生时配置，再冻结扩展字段和依赖职责；不能仅为延续旧路径决定保存整份快照。

#### 5.19.4 实现前必须处理的类型和用途问题

1. **等级表示**：Event.Severity 是可配置名称，默认 critical/warning/info；旧内容算法使用
   `"1"/"2"/"3"` 到 deadly/warning/remind 的映射，旧图表算法直接比较数字 level。
   Event 输入使用 Linkd 等级名称；鲸眼或监控平台配置中的算法等级由读取适配器显式转换后比较，
   不要求来源消息重复携带另一份算法。用户已确认默认映射 1→critical、2→warning、3→info，
   自定义等级通过丰富侧显式映射并与上游保持一致，见第 5.22 节。不能按 priority 猜测，
   缺少映射的处理仍待收敛。
2. **标量和缺失**：Labels/Dimensions 当前只接收字符串、有限数字、布尔值，不接受 null、对象或
   数组。F11 已确认改放 ExtraData，见第 5.25 节，不受 Dimensions 标量容器约束；嵌套策略也
   不能放入 Labels/Dimensions。旧样本里的 null 不能原样进入 Event.Dimensions，缺字段、
   空字符串、合法 0 的语义需要分别定义，不能照抄 Python 真值判断。
   此前统一 ID 为字符串的建议不构成通用要求；策略 ID/版本已明确不限制数据类型，见第 5.23.4 节。
   其他字段分别遵循各自已确认的输入约定，不将某个字段的字符串约束推广到全部 ID。
3. **业务 ID 的不同用途**：第 5.24 节已确认 F05–F07 统一读取 Labels.bk_biz_id，指标所需
   业务观测维度读取 Dimensions.bk_biz_id，外查资源业务保存到 enrich.resource 并服务资源拓扑
   逻辑。三者按用途读取，不自动覆盖或用维度/资源归属补齐来源标签。来源标签必须为数字 Scalar 正整数，
   缺失、字符串、布尔值、非整数、0 或负数在丰富入口返回 failed，见第 5.24.1 节；租户始终来自
   Event.BKTenantID，不能由业务 ID 或快照选择。
4. **定位维度与补充文本**：第 5.25 节已确认 log_related_info 放入 ExtraData。旧日志关键字
   展示遍历全部工作维度，放入 Dimensions 会使关联文本进入维度展示；查询是否使用某个维度
   则仍取决于对应查询的选维规则，不能笼统断言全部维度都会进入每条 where。关联内容作为独立
   输入处理，不加入对象定位、查询条件或维度摘要。

#### 5.19.5 基于字段职责的推荐分配（待确认）

| 信息 | 推荐位置 | 相对初案的判断 |
| --- | --- | --- |
| 来源标题、内容、标准等级、动作和时间 | Event 已有核心字段 | F02/F03/F04 合理；来源事实只有一份，丰富生成独立展示结果 |
| 后端策略 ID、策略历史记录 ID、来源业务 ID | Labels.bk_strategy_id、Labels.bk_strategy_history_id、Labels.bk_biz_id | 三个值都使用数字 Scalar 正整数；两个策略引用必须同时存在，见第 5.23.4 节；来源业务见第 5.24.1 节；业务不替代租户 |
| 对象定位键、指标聚合和观测维度 | Dimensions 的具名标量 | 基础监控/DATA/拨测/云/APM/K8s/接入对象的方向可保留；字段名按领域含义明确，而非复制所有旧别名 |
| F09 的主机目标 | Dimensions.bk_target_ip、Dimensions.bk_target_cloud_id | 已确认，见第 5.26 节；上游直接提供两个字段，主机定位不再提供或解析组合 target；拨测 target 保持原有地址语义 |
| CMDB 实例与主机 ID（B01/F10/B06） | Dimensions.bk_inst_id、Dimensions.bk_host_id、Dimensions.bk_target_host_id | 已确认保留三个键；主机模型按所列顺序回退，三项均缺失按 partial，非主机沿用原定位逻辑；撤回统一成 bk_host_id 的建议，见第 5.27 节 |
| F11 日志关联信息 | ExtraData.log_related_info | 已确认，见第 5.25 节；沿用旧关联内容加工和输出，不参与资源定位、指标 where 条件或维度摘要。扩展内容类型、大小上限与类型错误处置尚未确认 |
| 策略查询/算法配置 | 完整 bk_strategy_id/bk_strategy_history_id 引用触发两份策略读取，在调用上下文分别持有 | 最新方向见第 5.23 节；逐字段确定消费哪份数据，不要求 Event.ExtraData.strategy 存放完整快照 |
| 上游已经能够明确声明的主体 | Event.SubjectSystem/Type/ID/Name | 使用现有主体字段；本次不要求上游完成所有实例查找，也不凭名称/临时定位值伪造主体身份 |
| 模型/实例显示名、拓扑、主题名、动态分组、APM 应用详情等外查结果 | 本次调用上下文，成功结果写 enrich 对应分组 | 保留既定查询和丰富边界，不要求上游补齐所有资源信息，不回写 ExtraData |
| 原始推送内容 | SourceRawData | 人工追溯，不作为本方案默认业务输入；不因新字段缺失而回退读取 |

F09 主机地址定位已统一为第 5.26 节的两个具名维度，不再由 `tags.ip`、`ip` 等别名覆盖定位输入；
这不等于重命名所有指标的原始查询维度。保留 Meta 模型 ID 与模型 code 的区别，它们是不同
语义，不作为普通别名合并。
通用旧 where 拼接也应改为按真实查询所需维度构造，不能因为某个字段处于 Dimensions 就自动
把它加入每一个指标查询；具体规则随策略输入结构收敛。

这些是输入设计建议，不改变原任务以有效 Event 为起点的交付范围。上游可配合提供样本与字段，
不等于本轮自动实施上游改造。后续每项决策仍由用户确认，未确认的建议不进入实现验收标准。

本节仅完成映射评估和文档记录，未修改 Event 类型、配置、Cleaner、Enricher 或测试行为。

### 5.20 已澄清：区分两份策略，优先使用鲸眼配置

> 本节保留此前决策及取证依据。2026-09-03，第 5.23 节已替代其中“鲸眼足够则不查平台”、
> “仅在字段缺口时读取平台”的调用规则和完整引用下策略缺失的 failed 规则。
> 两份策略的身份区别及源码事实仍有效；本节旧流程不再作为最新实现或验收要求。

用户明确：这里是两份策略。推送来的 bk_strategy_id 和 version 指向**监控平台策略**；
鲸眼的监控策略是 **StrategyConfig**，通过 bk_strategy_id 与监控平台策略关联。
Linkd 可能新增按 bk_strategy_id/version 读取监控平台策略表的步骤，旧 alarm_callback
没有做这件事。策略字段若能从鲸眼 StrategyConfig 满足，就不查询监控平台策略。

此前“先读版本快照再分类”的统一流程和“在鲸眼声明式存储找历史版本”的推导不成立，予以修正。
监控平台版本不是 StrategyConfig 的版本；StrategyConfig 当前记录覆盖更新的事实，不能用于
判断监控平台是否保留版本，也不要求为本次迁移给鲸眼配置增加历史库。

#### 5.20.1 两份数据各自负责什么

| 对象 | 身份与来源 | 本次使用原则 |
| --- | --- | --- |
| 监控平台策略 | bk_strategy_id + version；如实际读取还需要租户、监控平台或业务作用域，由适配器明确 | 仅作为鲸眼侧无法满足的策略信息的候选读取来源；具体表、索引、版本查询协议尚未验证 |
| 鲸眼 StrategyConfig | 鲸眼配置 UID，以及与 bk_strategy_id 的关联 | 优先提供分类、展示、查询和算法所需数据；不把监控平台 version 套到它的查询条件上 |
| 鲸眼 CloudStrategyConfig | 原云分支中的鲸眼侧配置类型 | 仍属于鲸眼配置侧，不能因名称含 Cloud 就误认为监控平台策略快照；字段映射按实际当前类型核对 |

旧链路在回调中已经拿到监控平台的 strategy 对象，再按其 id 查询鲸眼配置，混合消费两者。
本次若只收到引用，原回调中的 S01–S18 不再天然存在：先逐字段判断鲸眼侧能否直接提供或可靠
推导，只有剩余确实需要的字段才产生监控平台读取需求。不能按旧 JSON 路径整体搬迁一个
“缺 strategy 就查询整份策略”的前置步骤。

#### 5.20.2 输入引用与调用流程

此前 F01 建议 Event.Labels.strategy_id；后续第 5.23.4 节已确认最新输入契约为
Event.Labels.bk_strategy_id 与 Event.Labels.bk_strategy_history_id，必须同时存在，不额外限定数据类型。
后者现已明确对应 alarm_strategy_history.id，见第 5.23.7 节；不增加旧键或 Dimensions
读取回退。内部命名区分平台与鲸眼，不与策略内容中的固定 version="v2" 混同。

推荐流程：

```text
读取 Event 的监控平台策略 ID（及可能使用的 version）
  → 按租户和 bk_strategy_id 查关联的鲸眼 StrategyConfig
  → 根据鲸眼配置分类，明确本分类需要哪些策略信息
  → 直接读取鲸眼配置，或按已核对的转换规则推导
  → 本分类仍有必须依赖监控平台的信息？
      否：不查监控平台策略
      是：按租户、监控平台作用域、bk_strategy_id、version 读取
  → 合成有明确类型和来源的调用上下文，继续丰富
```

本次不要求 Event.ExtraData.strategy 携带完整策略，也不回退 SourceRawData.strategy。
查询结果只保留在调用上下文和最终需要的 enrich 字段中，不回写 Event，不新增缓存或重试。
缺少 version 不应让只需 StrategyConfig 的告警自动失败；只有实际进入需要指定版本读取的
步骤时，才判断版本是否齐全、是否可读。该具体失败映射仍须随字段依赖确认。

“鲸眼可提供”指能满足该字段的业务用途，不只是存在同名键。原始配置、下发时展开的配置、
平台运行时生成的字段应区分。先从鲸眼取值是用户确认的来源优先级，不代表可以把查询超时、
权限失败、非法响应或错误关联静默包装成“字段没有”并转查另一个系统。

#### 5.20.3 S01–S18 初步来源判定

基于当前 StrategyConfig 定义、策略下发构造器和指标服务完成静态核对，详见
[字段盘点第 11 节](../research/alarm-callback-input-field-inventory.md#11-鲸眼配置对策略输入的覆盖情况)。

| 类型 | 已找到的鲸眼侧数据/规则 | 当前结论 |
| --- | --- | --- |
| 分类、名称、模板/模型关联 | spec.config_type/monitor_item_type/metric_source/name/alias_name/alarm_alias，metadata.labels 的模板与模型信息 | 优先直接取鲸眼配置，分类不以前置读取监控平台快照为条件 |
| 表达式、函数、聚合参数、查询配置、算法配置 | spec.strategy_item、spec.strategy_detect_algorithms；普通查询可由下发构造规则解释 | 有鲸眼来源；需要类型、周期单位、单/多指标及查询级覆盖规则转换，不按旧路径机械对应 |
| 日志查询语句、索引集、时间字段 | spec.source_config，日志下发构造器 | 有明确鲸眼来源，不为保留这些字段自动读监控平台策略 |
| 指标字段、结果表、metric_id、单位 | 配置中的 field_name/table_id 或逐查询字段；metric_id 可按相应类别规则生成；单位部分来自鲸眼指标库/APM 等已有依赖 | 区分直接字段、派生字段与其他既有依赖，不能全部声称是 StrategyConfig 直接字段；仍不默认增加监控平台策略查询 |
| 下发目标过滤/维度、数据集解析结果 | 下发器会在配置基础上追加或展开 | 已确认查询目标是本次告警对应的观测数据，见第 5.21 节；配置+Event 维度可满足用途时不查平台，只登记仍缺少的必要平台生成信息，不以复刻完整下发配置为读取理由 |
| intelligent_detect.result_table_id、智能算法 visual_type 与离群 cluster | 鲸眼与平台存在相关字段 | 已确认排除全部智能算法专用图表增强，不迁移 is_anomaly、上下界、分数、预测指标或离群 cluster 条件注入 |
| 非智能查询的 time_field、extend_fields | 监控平台历史 content 的 query_config | 按旧默认值透传：time_field 缺失保留 null，存在时保持原 JSON 类型和值；extend_fields 缺失保留空对象，存在时必须为 JSON 对象并原样保留其内部字段 |


未核实字段不能用空结构伪装已完成；也不因为某个条件分支可能需要运行字段，就让全部分类查询
监控平台策略。只迁移丰富真正需要的只读转换，不运行旧策略下发器或复制其部署/写入副作用。

#### 5.20.4 监控平台读取的条件约束

仅当逐字段盘点确认需要时，才细化对应读取适配器：

- 查询 key 使用监控平台 bk_strategy_id/version，并显式携带租户和实际所需的作用域；
  不能混用鲸眼 config_id、metadata.uid、monitor_template_id 或 spec_hash。
- 要求指定版本时，不能把最新版伪称指定版；版本不存在、不可读、响应非法的分组失败行为
  随实际依赖确认。无法取得分类必需的鲸眼配置仍遵循既定 failed；独立查询信息失败保留已完成
  字段并按既定 partial 边界处理，不反向要求所有丰富必须依赖平台快照。
- 监控平台版本覆盖什么、历史保留多久、事件发布时是否已经可查，是该候选读取的后续协议
  取证事项。当前不要求改造监控平台或鲸眼策略生产链路，不新建策略历史存储。
- 若部分字段来自当前鲸眼配置、部分来自指定版本的监控平台策略，记录各字段实际来源，
  不把整体丰富结果称为“某版本的完整策略快照”。是否需要保留发生时值按具体字段用途判断。

#### 5.20.5 当前事实与剩余取证

2026-09-02，Kingeye 源码基线仍为 5597beae82d42b7c732bf4cc10f7cffeaaf44672：

- [KAC entry](../../../../../kingeye/src/kingeye/kac/alarm_callback/entry.py) 从回调 strategy.id
  关联鲸眼配置；其 _search_configs_by_bk_strategy_ids 不读取监控平台历史策略表。
- [StrategyConfig 定义](../../../../../kingeye/src/kingeye/base/candidacy/models/declaratives/v1alpha1/strategy.py)
  明确包含 status.bk_strategy_id、spec.strategy_item 和 spec.strategy_detect_algorithms。
  KAC entry 还尝试 backend_strategy_id 标签并回退 status 字段，实际物理查询以模型与表取证为准，
  不把旧调用中的一个过滤键自动当成实际表列。
- 鲸眼 [ResourceManager](../../../../../kingeye/src/kingeye/base/infras/declaratives/base/resource.py)
  更新当前 spec、声明式事件通过资源 UID 查回当前资源等事实，仅描述鲸眼存储，不说明监控平台
  version 的保留能力。此前据此追问是否需要新建策略快照服务偏离了两份策略的边界。
- 当前任务只确认**可能新增读取监控平台策略表**，没有验证该表的物理结构、租户字段、version
  的类型/索引或完整版本获取方式；后续只在确认必要字段后取证，不凭名称猜测 SQL。

本轮未新增客户端、数据库连接、实现或业务测试；来源覆盖矩阵是静态设计输入，不是运行验收结论。


### 5.21 已确认：指标查询参数定位本次告警的观测数据

2026-09-03，用户确认：丰富结果中指标查询参数的目标为**查询本次告警对应的观测数据**。
该目标用于判断第 5.20 节所说的“鲸眼配置能否满足用途”，不要求完整复刻当时下发的策略参数。

#### 5.21.1 来源与构造原则

1. 两份策略按第 5.23 节读取，指标、表达式、函数和聚合方式按确定的字段来源及旧转换规则
   构造查询；前述指标元数据等既有只读依赖仍按具体字段使用。
2. 以 Event.Dimensions 中与该查询有关的观测维度限定本次告警的查询范围，保留必要的过滤
   条件和聚合语义。Dimensions 还承载对象定位线索，不因字段存在就将其加入全部查询。
3. 完整 bk_strategy_id/bk_strategy_history_id 引用触发两份策略读取，租户与平台作用域显式传入。
   字段是否可从鲸眼取得不再决定是否读取平台；本次查询用途仍不要求复刻完整下发目标列表。
4. 查询配置、观测维度和外查结果在调用上下文中组合，最终参数写入丰富结果；继续遵循 Event
   不可变、SourceRawData 仅用于人工追溯、无新增缓存或重试的已定边界。

#### 5.21.2 已确认的范围示例

| 场景 | 查询范围与验收意图 |
| --- | --- |
| 策略覆盖 100 台主机，按主机维度聚合，当前告警属于 A 主机 | 查询限定到 A 主机对应的观测数据；完整平台引用触发两份读取，不以鲸眼字段充足为由跳过平台 |
| 告警由跨主机聚合产生 | 保留该聚合范围及必要的目标过滤；不因能关联某一台主机就将聚合缩减到该主机 |
| 配置与维度足够定位，但某类查询仍缺少必要的平台运行字段 | 从已读取的监控平台指定版本策略取得所需字段；任一策略记录缺失按第 5.23 节处理 |

以上为实现阶段的验收场景，尚未编写或执行对应业务测试。

#### 5.21.3 已确认：无法确定查询范围时局部降级

2026-09-03，用户确认：某查询必需的定位维度缺失，且其他约定来源也无法补齐，导致无法确定
本次告警的查询范围时，**省略受影响的查询参数，保留其他已完成的丰富信息，整体标记 partial，
并记录缺少的定位字段**。不扩大查询范围，不猜测告警所属对象。
省略范围按第 5.21.5 节落实为包含受影响子查询的整份查询参数。

该规则以“本查询无法确定观测范围”为条件，不将所有 Dimensions 字段都视为必填，也不要求
跨主机聚合告警具备单台主机身份。具备合法聚合范围的跨主机查询继续按第 5.21.2 节构造。
不把空查询结构或取消定位过滤后的查询标记为成功；缺少 Event 观测事实也不成为回退读取
SourceRawData 或重新展开完整策略目标列表的理由。

失败诊断随丰富结果指出受影响的查询信息及缺失字段；具体输出结构和原因码名称仍在结果模型
细化时确定。返回行为遵循既有 partial 契约：通过丰富结果表达局部失败，返回 nil error，保留
已完成数据，不阻断 Alert 创建；父级 context 取消仍遵循既定中止规则。

| 场景 | 实现阶段的验收预期 |
| --- | --- |
| 按主机聚合的告警缺少必要定位维度，其他约定来源也无法补齐 | 省略受影响查询参数，保留已完成的展示、策略、资源等信息；整体 partial，诊断列明缺失定位字段 |
| 必要定位信息可从已约定来源补齐并可靠确定范围 | 按已确定范围继续构造查询，不因输入中最初缺字段就直接降级 |
| 跨主机聚合告警缺少单台主机 ID，但配置与观测维度足够确定聚合范围 | 不套用单主机必填条件，保留聚合语义构造查询 |

以上只记录已确认行为及其验收意图，尚未编写或执行对应业务测试。

#### 5.21.4 已确认：沿用旧条件合并，将 OR 范围扩大标记为已知迁移风险

2026-09-03，用户明确：OR 问题保留，并作为已知迁移风险写入实现验收与交付说明；迁移后的对应代码增加注释标记。其他条件合并行为
继续按旧逻辑迁移。具体旧分组流程与输出样例以
[字段盘点第 11.3 节](../research/alarm-callback-input-field-inventory.md#113-旧指标查询条件的实际合并行为)
为证据，不再引入此前建议的“条件取交集、冲突即 partial”规则。

实现要求：

1. 保留旧合并方法按 AND/OR 分组、匹配时移除同键旧条件并追加告警维度、主循环不匹配时
   保留原条件再追加维度、尾部残留组的处理及空结果回退。不能仅凭“按旧逻辑”将其简化为
   通用取交集或无条件覆盖同键的另一种算法。
2. 保留已复现的 OR 输出行为，在对应 Go 实现旁说明旧实现中条件字典共享及连接符改写的
   原因、会扩大查询范围的例子，以及本次按用户决策保留的边界。Go 实现须以输出对照验收，
   不能因为改用值类型复制而无意修正该行为，也无需为了模拟 Python 而引入跨事件共享状态。
3. 单组条件与告警维度冲突时仍按旧方法返回合并参数，不因该冲突单独省略参数、标记 partial
   或额外重查监控平台策略；两份策略此前按第 5.23 节读取。第 5.21.3 节的维度缺失规则独立保留。
4. 第 5.21.1 节的查询用途保留；已知 OR 范围扩大是本轮明确接受的兼容例外，并作为迁移风险写入
   测试名称、验收记录和最终交付限制。文档与实现只承诺复现该条件输出，查询结果可能包含当前
   告警维度之外的数据。后续修正需要单独确认。

对应代码注释可采用以下内容；实际标识符按实现调整，不使用缺少完成条件的 TODO：

```go
// 迁移约束：本轮保留旧 create_where_with_dimensions 的 OR 输出行为。
// 旧实现跨条件组复用维度条件字典，后续修改连接符会影响前一组。
// 例如策略 disk=A OR disk=B、事件 disk=B，旧输出为 A OR B OR B，
// 查询范围仍包含 A。按已确认的迁移决策保留；后续修正须单独确认。
```

实现阶段用盘点第 11.3 节的五个纯函数样例进行输出对照，并同时覆盖第 5.21.3 节已经确认的
维度缺失降级；本轮只更新设计，尚未新增 Go 实现、代码注释或对应测试，未修改 Kingeye 源码。

#### 5.21.5 已确认：维度缺失时按整份查询参数降级

2026-09-03，用户确认第 5.21.3 节的省略单位为**整份查询参数**。旧
[get_graph_panel](../../../../../kingeye/src/kingeye/kac/alarm_callback/handle_alert_info.py)（653）将
expression、functions 和 query_configs 组织在同一个 unify_query_params 中；迁移时保留这份
参数的完整性，不只删除其中无法构造的子查询。

当其中任意一条查询因必要定位维度缺失，且无法从约定来源补齐而不能构造时：

- 省略该整份查询参数，包括其中已成功构造的子查询；不输出仍引用缺失查询的表达式。
- 保留已完成的名称、内容、策略和资源等其他丰富信息，整体标记 partial，并记录导致失败的
  子查询及缺少的定位字段；继续遵循 partial 结果返回 nil error、不阻断 Alert 创建的规则。
- 不通过改写表达式、为缺失子查询补零或去掉必要过滤来拼出一份替代参数。

| 场景 | 实现阶段的验收预期 |
| --- | --- |
| 表达式 A / B * 100，A 与 B 均可构造 | 沿用旧逻辑输出完整表达式及 A、B 查询配置 |
| 同一表达式中 A 可构造，B 因必要定位维度缺失且无法补齐而不可构造 | 整份查询参数省略；已完成的其他丰富结果保留，整体 partial，诊断定位到 B 及缺失字段 |
| 单查询因同一原因不可构造 | 省略整份查询参数，按同一 partial 规则处理 |

本次只细化已确认的维度缺失降级范围，不将其扩展为第 5.21.4 节不采用的“条件冲突即 partial”。
对应测试尚未编写或执行，也未修改查询输出结构或 Go 实现。

#### 5.21.6 后续落地细节

多查询/表达式与 PromQL 等专用路径以旧转换为基线，结合第 5.21.5 节执行已确认的维度缺失降级。
策略的读取已按第 5.23 节改为完整引用下两份均读；监控平台使用指定版本，鲸眼使用关联配置。
同一字段两份均有时沿用旧消费来源，见第 5.23.3 节，不能将整体结果称为同一时点快照。

本节记录已确认的用途与来源原则，未修改 Go 实现、事件输入协议或存储结构。

#### 5.21.7 已确认：指标查询参数保存到 enrich.metric.metric_query_params

2026-09-03，用户确认旧 metric_query_params 的输出位置统一为 enrich.metric.metric_query_params，
metric 因此纳入已确认的业务分组及第 5.9.1 节的诊断 groups。各丰富分类产生同类查询参数时，
均使用这一位置，不按基础监控、日志指标或云平台另设查询参数输出分组。

参数内部结构与构造逻辑沿用旧实现，按本节此前已确认的输入位置、策略字段来源、条件合并
和降级规则迁移。旧 BaseConverter.get_metric_query_params 返回图表转换结果的首个 target.data；
本次确认的是该查询参数对象的保存位置，不将完整图表响应或两份策略原文一并存入 metric_query_params。

必要定位维度缺失且无法从约定来源补齐时，按第 5.21.5 节省略整份 metric_query_params 字段，
包括已成功构造的子查询；保留其他有效丰富结果，整体 partial，诊断 groups 包含 metric。
不以空对象替代被省略的参数，也不修改 Event.Dimensions 或通过 SourceRawData 补齐。

实现验收覆盖完整参数保存到约定路径、多查询中一项因必要维度缺失而导致整份参数省略、
其他结果保留及对应诊断定位；不以内部参数结构不变推断下游已使用新的输出位置。
当前仅更新设计与术语，未修改 Go 实现或执行上述业务测试。

#### 5.21.8 已确认：指标展示名称、名称与单位归入 metric

2026-09-03，用户确认以下输出位置，取值、别名优先级和单位回退均按旧分类逻辑迁移，
输入位置与两份策略的字段来源仍遵循此前已确认的规则。

| 旧输出方法 | 新输出位置 | 含义 |
| --- | --- | --- |
| clean_item() | enrich.metric.display_name | 监控项展示名称 |
| clean_metric_name() | enrich.metric.metric_name | 保留旧指标名称字段的分类语义，不定义为统一指标 ID |
| clean_unit() | enrich.metric.unit | 指标单位，保留旧分类的取值与回退 |

旧 [BaseClear](../../../../../kingeye/src/kingeye/kac/alarm_callback/cleaner/base.py) 的 clean_item（531）、
clean_metric_name（598）和 clean_unit（913）分别生成上述信息。
[日志指标](../../../../../kingeye/src/kingeye/kac/alarm_callback/cleaner/log_metric.py) 的 clean_metric_name（11）
及 [日志关键字](../../../../../kingeye/src/kingeye/kac/alarm_callback/cleaner/log_keyword.py) 的
clean_item（110）、clean_metric_name（114）按别名、检索语句、占位文本的旧优先级返回展示值。
因此不得将 metric.metric_name 当作跨分类的稳定指标标识，也不据此改写查询参数中的真实指标字段。

第 5.21.5、5.21.7 节要求省略 metric_query_params 时，这三项中已经成功取得的内容仍保留，
不随整份查询参数一并清空。旧规则正常返回的空字符串或占位文本仍按原语义处理；
依赖读取失败及必要字段缺失继续按已确认的诊断和局部失败规则表达，不用占位值掩盖失败。

旧衍生指标 clean_metric_name 还会为工作区中的 metric_query_params 添加 field_tag。
迁移时保留该参数加工，但仅作用于本次可输出的查询参数；不能因名称加工重新创建已经按
缺维度规则省略的 metric_query_params，也不把此工作区副作用写回 Event。

实现验收覆盖各分类的展示名与名称差异、别名优先级、旧单位回退，以及查询参数省略后
已取得的三项内容仍保留。当前仅更新设计与术语，未修改业务代码或执行这些业务测试。


#### 5.21.9 已确认：结果表与指标唯一标识归入 metric

2026-09-03，用户确认以下输出位置，并要求 metric_unique_id 保留完整字段名。

| 旧输出 | 新输出位置 |
| --- | --- |
| result_table_id | enrich.metric.result_table_id |
| metric_unique_id | enrich.metric.metric_unique_id |

取值与回退沿用旧分类逻辑及已确认的策略字段来源。旧
[BaseClear](../../../../../kingeye/src/kingeye/kac/alarm_callback/cleaner/base.py) 的
clean_result_table_id（868）按分类读取结果表或解析云平台 metric_id，clean_metric_unique_id
（855）拼接结果表与指标字段；保留原有 PromQL 提取路径及空值处理。
[LogKeywordCLear](../../../../../kingeye/src/kingeye/kac/alarm_callback/cleaner/log_keyword.py) 的
clean_metric_unique_id（159）、clean_result_table_id（163）返回空字符串，迁移保留该行为。

当前仅确认输出字段及沿用规则，未修改业务代码或执行运行验证。

#### 5.21.10 已确认：聚合方法与周期原名归入 metric

2026-09-03，用户确认以下输出位置，沿用旧取值和默认值规则。

| 旧输出 | 新输出位置 |
| --- | --- |
| aggregate_func | enrich.metric.aggregate_func |
| time_interval | enrich.metric.time_interval |

旧 [BaseClear](../../../../../kingeye/src/kingeye/kac/alarm_callback/cleaner/base.py) 的
clean_aggregate_func（890）读取 strategy_item.agg_method，无 strategy_item 时返回 "avg"；
clean_time_interval（895）读取 strategy_item.agg_interval，无 strategy_item 时返回 "60"。
strategy_item 的取得保留原分类规则：普通分支使用鲸眼配置的 spec.strategy_item，云平台
分支使用配置的 spec.item[0]。依赖读取失败仍按已确认的局部失败规则处理。

当前仅确认输出字段及沿用规则，未修改业务代码或执行运行验证。

#### 5.21.11 已确认：维度条件文本保存到 enrich.metric.where_condition

2026-09-03，用户确认将旧 where_condition 原名保存到 enrich.metric.where_condition，
保留旧过滤和拼接规则，在独立工作副本上处理。

旧 [BaseClear.clean_where_condition](../../../../../kingeye/src/kingeye/kac/alarm_callback/cleaner/base.py)
（845）从工作维度中排除 bk_topo_node、bk_host_id，将剩余字段生成 key='value' 形式的
条目，并以 " and " 连接；没有剩余字段时返回空字符串。迁移保留这些处理规则，维度输入
遵循已确认的 Event.Dimensions 映射，过滤只作用于该输出的工作副本，不修改来源维度。

当前仅确认输出字段及沿用规则，未修改业务代码或执行运行验证。

### 5.22 已确认：内容文案算法等级显式映射到 Event.Severity

2026-09-03，用户确认在丰富侧配置显式算法等级映射。默认对应关系为：

| 策略算法等级 | Linkd Severity 名称 |
| --- | --- |
| 1 | critical |
| 2 | warning |
| 3 | info |

#### 5.22.1 读取与匹配规则

1. 按第 5.23 节读取两份策略，内容文案算法按确定的旧字段来源消费；算法等级转换为 Linkd Severity 名称，
   再与 Event.Severity 比较并选择文案加工算法。智能算法专用图表增强已从本次范围排除；普通 query_config 的 time_field 与 extend_fields 按平台历史
content 透传。time_field 缺失输出 null，存在时保留原 JSON 类型和值；extend_fields 缺失输出空对象，
存在时要求 JSON 对象并原样保留内部字段。extend_fields 类型错误时省略整份 metric_query_params，
记录 invalid_field、groups `[metric]`，整体 partial。
2. 显式映射配置存在时按配置转换；配置缺失时使用默认映射 1→critical、2→warning、3→info。
   Event.Severity 为 critical、warning 或 info 时可直接参与默认匹配。其他 Severity 名称在缺少显式
   映射时跳过依赖算法的文案加工，记录 invalid_field、groups `[display]`，整体 partial。
3. Event.Severity 仍是本次告警等级的唯一业务输入。不重新读取旧回调的内外两份 severity，
   不要求上游重复携带算法或额外数字等级，不反向修改 Event/Alert 的 Severity。
4. 显式映射配置引用 Linkd 等级表中不存在的名称，或同一算法等级出现冲突映射时，视为 Enricher
   配置无效：整体返回 failed 与 invalid_field 诊断，groups 为空列表，不执行策略或其他外部依赖读取。
   该规则与事件使用自定义 Severity 但缺少显式映射的 partial 场景分开处理。

#### 5.22.2 证据与实现验收

- [旧图表算法选择](../../../../../kingeye/src/kingeye/kac/alarm_callback/handle_alert_info.py) 的
  get_graph_panel（744）直接比较 algorithm.level 与 alert.severity。
- [鲸眼策略下发转换](../../../../../kingeye/src/kingeye/kmc/controller/executor/strategy_task/base_executor.py)
  的 init_kwargs_algorithms（373）从 strategy_detect_algorithms 读取 level.value。
- [当前 Linkd 等级配置](../../internal/config/severity.go) 支持自定义等级名称和独立的 priority；
  [Event](../../internal/domain/event.go) 的 Severity 保存字符串名称。

实现阶段至少验证：默认三个等级分别匹配对应算法；自定义名称按显式映射匹配；映射不因
priority 排序值变化而改变；鲸眼和监控平台两种数据来源使用一致的等级转换语义。
这些是待实施的验收要求，本轮没有新增配置字段、Go 实现或业务测试。

映射的具体配置键和应用作用域随配置 schema 落实。缺少显式配置时使用默认表；默认表无法识别的
Severity 按本节 partial 规则处理。显式映射配置无效时整体 failed，并在依赖读取前结束。
本次不增加 priority 推导或名称排序回退。


### 5.23 已确认：完整平台策略引用触发两份策略读取

2026-09-03，用户调整方案：查询依赖时遇到平台策略与历史记录引用，读取监控平台策略历史内容和关联的
鲸眼 StrategyConfig，要求两份同时存在；任一缺失时后续丰富按 partial 处理。该决定替代
第 5.20 节原“鲸眼字段足够则不查询平台”的调用条件。最终输入键名与位置已在第 5.23.4 节确认。

#### 5.23.1 读取条件与身份

- 对已启用本次丰富规则的来源，bk_strategy_id 与 bk_strategy_history_id 都是必填标签；缺任一项
  在丰富入口返回 failed，两个标签均存在则按实际依赖协议适配参数并读取两份策略。
  监控平台策略按租户、实际平台作用域、bk_strategy_id 和 bk_strategy_history_id 读取历史记录；
  鲸眼 StrategyConfig 按租户和 bk_strategy_id 查关联配置，监控平台历史记录 ID 不参与其查询。
- 两份策略按固定顺序读取：先读取监控平台策略历史记录，再读取关联的鲸眼 StrategyConfig。
  前一项缺失或查询故障仍继续读取后一项，以满足分别保留可用结果和诊断的 partial 契约。
  当前迁移按顺序实现，两份策略读取不引入并行调度。
- 鲸眼字段充足时仍检查监控平台策略历史记录；平台数据充足时仍检查关联的鲸眼配置。
  两份结果在本次调用上下文中独立持有，字段消费不改变读取步骤。
- 引用从 Event.Labels.bk_strategy_id 与 Event.Labels.bk_strategy_history_id 读取，两个值均须为正整数，
  见第 5.23.4 节；丰富入口先完成成对校验和值域校验，再执行策略读取。
- 平台侧按历史主键与 strategy_id 联合定位并读取 content，暂不考虑历史操作状态，见第 5.23.7 节。
  本次不新增策略历史服务、缓存或重试，SourceRawData 继续只用于人工追溯。

#### 5.23.2 依赖完整性与 partial

| 完整引用下的读取结果 | 处理要求 |
| --- | --- |
| 两份策略均存在 | 依赖完整性满足，按各字段明确的来源继续丰富；最终状态仍取决于其他步骤 |
| 监控平台策略历史记录缺失 | 标记 partial，记录缺失来源和历史引用，保留已得到的有效丰富信息 |
| 鲸眼 StrategyConfig 缺失 | 标记 partial，记录缺失关联配置；分类所需信息不足时跳过相关步骤，不猜测分类 |
| 两份均缺失 | 仍按本次策略缺失规则返回 partial 与诊断，不因没有可生成的业务分组而伪装 succeeded |

任一策略缺失都使本次结果不满足完整成功条件，不能因为最终消费字段可由剩余一份覆盖就标记
succeeded。上述策略缺失的 partial 规则优先于早先“无法分类即 failed”的一般规则；保留已得
有效数据并返回 nil error，不阻断 Alert 创建。父 Context 取消仍中止处理。

这里的“缺失”指未查到对应记录或指定版本；策略读取超时、连接失败、权限拒绝、响应无法解析
也已确认按 partial 处理，见第 5.23.6 节。按第 5.9.1 节，以上依赖问题的诊断码统一为
dependency_invalid，并标识对应依赖；不再要求持久化细分故障类型。

#### 5.23.3 已确认：重叠字段沿用旧消费来源

2026-09-03，用户确认：同一字段在两份策略中都有值时，沿用旧消费来源，不统一优先取鲸眼。

- 旧逻辑从回调中监控平台 strategy 读取的字段，改从本次查到的监控平台指定版本策略读取。
- 旧逻辑从鲸眼 StrategyConfig 读取的字段，继续从关联的鲸眼配置读取；原指标库、模板等其他
  既有依赖不因两份策略都已取得而改变来源。
- 两份策略分别持有，不按同名字段自动覆盖或合并。同一名称服务不同消费位置时，按各自旧
  读取点取值，不能创建一份合并大字典供全部步骤共享。
- F05–F07 已明确合并为 Labels.bk_biz_id 是已确认的输入变化，继续按第 5.24 节处理；不能以
  “沿用旧策略来源”为由将这些输入改回平台 strategy.bk_biz_id。
- S01–S18 的鲸眼覆盖矩阵仅作取证参考，不用于将旧平台读取替换为鲸眼直接字段或推导值。
  旧字段本身已有明确回退链时按该链路迁移，不新增统一的跨策略回退。

例如，旧查询周期来自监控平台 agg_interval，指定版本值为 1 分钟，而当前鲸眼配置为 5 分钟，
则该查询周期采用 1 分钟。鲸眼侧名称、模板关联等原消费位置仍读取鲸眼数据；两份数据的时点
不相同，不把整体丰富结果称为某一版本的完整快照。

| 场景 | 实现阶段的验收预期 |
| --- | --- |
| 两份均有查询周期，平台指定版本为 1 分钟，鲸眼当前为 5 分钟 | 旧平台查询周期读取点使用 1 分钟 |
| 旧鲸眼读取点所需数据在平台也有同名字段 | 继续使用鲸眼值，不按同名键覆盖 |
| 两份数据具有可映射的算法等级 | 先按旧消费来源取得算法，再按第 5.22 节显式映射等级；映射不改变算法来源 |

本轮只确认来源及冲突时的取值规则，未执行上述业务测试。来源字段本身缺失时，按旧回退证据
及具体失败矩阵继续细化，不能以另一份策略存在同名字段就自动认定可替代。

实现阶段验收两份均存在、分别缺失、均缺失，以及鲸眼字段足够仍读取平台的场景。本轮只更新
设计与来源说明，未新增客户端、数据库查询、Go 实现或业务测试。


#### 5.23.3.1 已确认：鲸眼配置类型回退与多匹配选择

鲸眼配置按旧入口顺序读取：先按 `bk_strategy_id` 查询 StrategyConfig；明确返回 0 条时，再查询
CloudStrategyConfig。StrategyConfig 有结果时直接使用第一条并停止类型回退；StrategyConfig 查询超时、
权限拒绝、连接失败或响应解析失败时，将鲸眼配置记为 dependency_invalid + partial，并跳过
CloudStrategyConfig。CloudStrategyConfig 的查询故障同样按 dependency_invalid + partial 处理。
两类都明确返回 0 条时，按鲸眼配置缺失返回 dependency_invalid + partial。

本次不新增唯一性校验或冲突降级。读取适配器应保留底层查询的既有排序语义；若底层协议没有稳定排序，
第一条选择可能随存储返回顺序变化，作为旧行为迁移限制记录在最终交付说明中。

实现验收覆盖：StrategyConfig 命中时不查询云配置；StrategyConfig 明确未命中且 CloudStrategyConfig
命中时使用云配置；两类都未命中时按 partial；StrategyConfig 查询故障时跳过云配置并按 partial；
任一类型返回多条时使用底层结果第一条。

#### 5.23.4 已确认：平台策略引用的输入字段

2026-09-03，用户明确两个字段均放入 Event.Labels，必须同时存在。后续领域收敛将两者统一限定为正整数：
`bk_strategy_id` 标识监控平台策略，`bk_strategy_history_id` 标识该策略的历史记录。

| 含义 | Event 读取位置 | 类型 | 示例 |
| --- | --- | --- | --- |
| 监控平台策略 ID | Event.Labels["bk_strategy_id"] | 正整数 | 123 |
| 监控平台策略历史记录 ID | Event.Labels["bk_strategy_history_id"] | 正整数 | 70001 |

丰富入口先检查两个键是否成对存在，再校验值类型和值域。缺失字段返回 failed 与 missing_field；
字符串、布尔值、非整数数值、0 或负数返回 failed 与 invalid_field。两类输入错误都在依赖查询前结束，
依赖调用次数为零。本次以有效 Event 为输入：[Event.Labels](../../internal/domain/event.go) 的
DimensionMap 仍可承载其他标量类型；本节只收窄这两个标签的领域契约。

这两项是策略关联标签，分别保留平台策略 ID 和平台策略历史记录 ID 的含义。监控平台查询时直接使用
已校验的正整数；bk_strategy_history_id 对应 alarm_strategy_history.id，历史记录的 strategy_id 对应
bk_strategy_id；鲸眼 StrategyConfig 只通过 bk_strategy_id 关联，历史记录 ID 与鲸眼配置版本语义相互独立。

用户已认定 alarm_strategy_v2 没有 history_id；bk_strategy_history_id 对应历史表主键，
平台侧直接读取匹配历史记录的 content，见第 5.23.7 节。本节不再保留主表 history_id 的
待核实事项。旧样本和代码中的固定 strategy.version="v2" 不用于定位本次快照，也不能
使用当前主表配置替代指定历史记录。既有取证见[输入盘点](../research/alarm-callback-input-field-inventory.md#12-监控平台策略版本读取的取证与缺口)。

前述 F01 的 Labels.strategy_id 建议已被替代，不同时接受 strategy_id、version 或
strategy_version 作为备用键，也不从 Dimensions 或 SourceRawData 查找替代值。

实现验收覆盖：两个正整数键同时存在时查询平台历史记录和鲸眼配置；字符串、布尔值、非整数、0 和负数
均返回 failed 与 invalid_field，依赖调用次数为零；平台查询使用策略 ID 与历史记录 ID，鲸眼查询使用策略 ID。
旧键或维度同名值不能代替标签。缺任一标签（包括两者都缺少）返回 failed 的规则见第 5.23.5 节。
本轮只更新设计，未修改 Event 类型、来源清洗、查询适配器或测试。


#### 5.23.5 已确认：策略 ID 与历史记录 ID 必须成对出现

2026-09-03，用户强制要求 bk_strategy_id 与 bk_strategy_history_id 成对出现，并进一步确认
两者都没有也视为丰富失败。因此，对已启用本套丰富规则的来源，两个标签都必须提供。
该约束针对第 5.23.4 节已确认的 Event.Labels 输入，不改变字段位置。

| bk_strategy_id | bk_strategy_history_id | 成对约束判定 |
| --- | --- | --- |
| 存在 | 存在 | 满足配对约束，不额外检查数据类型；按实际依赖协议进行参数适配与读取 |
| 存在 | 不存在 | 丰富入口判定输入无效，返回 failed 与缺失 bk_strategy_history_id 的诊断，不执行依赖查询 |
| 不存在 | 存在 | 丰富入口判定输入无效，返回 failed 与缺失 bk_strategy_id 的诊断，不执行依赖查询 |
| 不存在 | 不存在 | 丰富入口返回 failed，诊断同时列出两个缺失标签，不执行依赖查询 |

此前提出的“有 ID、缺版本时先查鲸眼并标记 partial”方案不采用。不得以补默认版本、查询最新版、
读取旧键或 SourceRawData 来补成一对。配对检查先于策略 I/O，不能先查一份再判断是否缺另一项。

2026-09-03，用户进一步确认处置规则：在丰富入口、依赖查询之前执行成对校验。对已启用本次
丰富规则的来源，缺少任一标签（包括两个都没有）时结束本次丰富，返回 failed 与缺失字段诊断；Event/Alert
仍按既定生命周期处理，不因该丰富输入错误阻断告警创建。未启用来源仍遵循既定 Noop 行为。

这里应返回合法的 EnrichResult（状态 failed、数据包含诊断）与 nil error，不通过抛出 error
表达可预期的输入错误，避免生命周期的异常兜底清空诊断。父 Context 取消仍遵循既定中止规则。

完整且有效的引用查询不到策略记录时，继续按第 5.23.2 节返回 partial；不把“输入不完整”和
“依赖记录未查到”合并为一种原因。

“两者都没有就跳过本套丰富并返回 succeeded + 空 enrich”的建议不采用。Noop 仍仅按此前
未启用来源的适用边界执行，不能把启用来源的引用缺失解释为无需丰富。

实现阶段验证：两种单字段输入与两个标签都缺少的输入均返回 failed，诊断列出实际缺失键，
依赖读取次数为零；Lifecycle 保留诊断并继续创建 Alert；完整引用仍查询两份策略，记录缺失
返回 partial。当前只更新设计，未修改 Go 实现、接入校验或执行业务测试。


#### 5.23.6 已确认：策略查询故障按 partial 处理

2026-09-03，用户确认：策略引用合法时，任一份策略读取发生超时、连接失败、权限拒绝或响应
无法解析，整体丰富结果标记 partial，保留其他已取得的有效丰富结果，跳过依赖失败数据的步骤。
两份策略都查询失败时也按此规则返回 partial 与诊断；不能因为没有可生成的业务分组改成 succeeded。

| 查询结果 | 诊断 code | 处理 |
| --- | --- | --- |
| 单次请求超时 | dependency_invalid | partial，跳过依赖该结果的步骤 |
| 连接失败 | dependency_invalid | partial，保留其他有效结果 |
| 权限拒绝 | dependency_invalid | partial，保留其他有效结果 |
| 响应无法解析 | dependency_invalid | partial，不消费无法解析的策略数据 |

诊断使用第 5.9.1 节的 code、groups 及可选定位字段，依赖问题暂不细分原因码；不保存完整
敏感响应或凭据。任一读取失败不授权用另一份策略覆盖旧字段来源，也不增加重试或创建后补丰富。
分类依赖的鲸眼配置不可用时，跳过必须依赖该分类的步骤，不猜测分类。

返回合法的 partial 结果及 nil error，以保留有效数据和诊断。父级 Context 取消或生命周期
处理已经超时，仍按既定规则中止处理；不将父级取消包装成可继续写入的局部查询失败。

本节是已确认的一般分类失败规则的特例：引用缺失等输入错误按第 5.23.5 节返回 failed；
引用合法后的上述策略依赖故障按本节返回 partial。其他程序异常的现有生命周期兜底不在本次修改。
实现验收覆盖两种策略来源的上述故障、另一份成功时有效结果保留、两份都失败及父级取消。
当前仅更新设计，未修改查询实现或执行对应业务测试。


#### 5.23.7 已确认：监控平台策略读取目标表

2026-09-03，用户确认此前所指的监控平台策略表就是 alarm_strategy_v2 及历史表
alarm_strategy_history；不再将表名作为待用户提供的集成信息。

| 表 | 本地模型与已知职责 |
| --- | --- |
| alarm_strategy_v2 | 保存当前策略配置；用户认定没有 history_id 字段 |
| alarm_strategy_history | 具有主键 id 和关联策略字段 strategy_id；本次直接读取匹配记录的 content 作为平台策略结果 |

两张表属于本次监控平台策略读取侧，不与鲸眼 StrategyConfig 混同。鲸眼配置仍另行按
bk_strategy_id 查询，两侧数据完整性及消费来源继续遵循第 5.23.1–5.23.3 节。
两侧存储连接由 Linkd 现有配置按 Event.BKTenantID 选择并注入：平台历史读取使用对应平台库连接，
鲸眼配置读取使用对应鲸眼存储连接。平台历史 SQL 只使用 `history.id + history.strategy_id` 联合条件，
随后校验 content.bk_biz_id；不从 bk_biz_id 推导租户，也不向平台历史表增加不存在的 bk_tenant_id 条件。

用户进一步明确 bk_strategy_history_id 就是 alarm_strategy_history 的主键 id；
bk_strategy_id 对应历史记录的 strategy_id，也标识 alarm_strategy_v2 中的策略。
该历史引用不读取 content.version，不使用固定 "v2" 或操作时间代替历史主键。

用户随后明确认定主表没有 history_id，不需要继续查表验证；此前“主表应该有 history_id”
的推测撤销。本节将此作为已确认事实，不增加该列，不为迁移修改 Kingeye 的表结构。

已确认的读取规则：先以 history.id = bk_strategy_history_id、history.strategy_id = bk_strategy_id
联合定位历史记录，再解析 content 并校验 content.bk_biz_id 与 Event.Labels.bk_biz_id 一致。
三项全部匹配后，以 content 作为平台侧策略输入。历史表自身没有 bk_tenant_id；平台库连接由
Event.BKTenantID 对应的 Linkd 配置选择，content 业务校验提供附加作用域保护。

| 读取结果 | 已确认的处理 |
| --- | --- |
| id、strategy_id 与 content.bk_biz_id 均匹配 | 以 content 作为平台策略输入，按既有字段消费来源继续丰富 |
| 无匹配记录，包括历史 id 属于其他 strategy_id | 视为平台策略历史记录未取得，按 partial 保留其他结果和诊断 |
| content.bk_biz_id 缺失、类型无法按已确认业务契约解释或与来源业务不一致 | 视为平台策略历史记录未取得，按 partial 处理；不消费该 content |
| content 无法解析 | 按策略响应无法解析的既定规则返回 partial，保留其他可用数据 |

content 的具体字段缺口继续按旧消费逻辑和已确认的局部失败边界处理，不因此退回当前主表。
2026-09-03，用户确认暂不考虑历史操作状态：不增加 status=true 筛选，不因 status=false
拒绝匹配记录或单独产生降级诊断；id 与 strategy_id 匹配后直接读取 content。
不以其他操作状态判断替代被排除的 status 门槛。记录缺失、content 无法解析及实际消费字段
缺口继续按既定规则处理；“暂不考虑操作状态”不等于任意内容都能完成丰富。
实现验收覆盖联合定位正确记录、content.bk_biz_id 一致、业务缺失或错配、历史 ID 与策略 ID 错配、
记录缺失、内容解析失败，以及读取历史内容后仍独立查询鲸眼配置；相同匹配条件和 content 下，
历史操作状态不同不改变读取与丰富行为。数据库连接的租户选择应另行验收。
既有取证统一见[输入盘点第 12 节](../research/alarm-callback-input-field-inventory.md#12-监控平台策略版本读取的取证与缺口)。
本轮仅按用户认定事实更新设计，未继续查表、查询业务数据库或实现读取适配器。

### 5.24 已确认：业务 ID 按来源业务、观测维度与资源归属分别使用

2026-09-03，用户确认三种业务 ID 按用途分别读取和使用：

| 信息 | 读取位置 | 使用规则 |
| --- | --- | --- |
| 来源业务 | Event.Labels.bk_biz_id | 替代 F05–F07，供来源业务、图表查询等原消费位置使用 |
| 业务观测维度 | Event.Dimensions.bk_biz_id | 仅当指标确实需要该维度时参与查询过滤或分组 |
| 资源所属业务 | 拨测任务、CMDB 等已约定依赖的查询结果 | 保存到 enrich.resource，供资源归属展示及原资源拓扑逻辑使用 |

维度值和资源业务 ID 不反向覆盖标签，也不自动作为标签缺失时的替代值。三种信息不是同一字段
的优先级列表；各消费点使用对应信息，不创建统一的业务 ID 覆盖链。原指标所需维度仍按查询
规则选择，不能将全部业务信息注入每条查询。外部读取继续使用 Event.BKTenantID 隔离租户。

旧行为证据：

- [图表查询](../../../../../kingeye/src/kingeye/kac/alarm_callback/handle_alert_info.py) 的
  get_graph_panel（658）将回调 event.bk_biz_id 放入统一查询参数；迁移后的该输入来自标签。
- [DATA 资源处理](../../../../../kingeye/src/kingeye/kac/alarm_callback/converter/basic_data.py) 的
  clean_biz_and_alarm_obj（416）从拨测任务或主机业务关系等取得资源所属业务。
- 字段盘点的 B07、D05 保留旧维度回填和资源业务回退的取证事实；本节已确认的新来源职责优先，
  不能用旧回填流程修改 Event，或把资源归属替代 F05–F07 的来源业务。

实现验收：三种业务 ID 使用不同示意值，分别验证来源查询业务、指标过滤/分组及资源展示/拓扑
读取自己的值；来源标签缺失时，维度或资源结果不会补齐该标签。标签类型、必填性与输入错误
处理见第 5.24.1 节；资源分组字段结构仍须收敛。本轮未修改 Go 实现或执行业务测试。


#### 5.24.1 已确认：来源业务标签使用数字 Scalar 正整数

2026-09-03，用户确认：对 `built_in_bk` 来源，Event.Labels["bk_biz_id"] 必须存在，并使用
数字 Scalar 表示正整数。该检查与策略引用检查同属丰富入口校验，先于任何依赖查询执行。

| 标签输入 | 入口处理 |
| --- | --- |
| 数字 Scalar，值为正整数 | 满足本节契约；其他入口检查也通过后才查询依赖 |
| 缺失 | 返回 failed 与 missing_field，不执行依赖查询 |
| 字符串或布尔值 | 返回 failed 与 invalid_field，不执行依赖查询 |
| 数字为小数、0 或负数 | 返回 failed 与 invalid_field，不执行依赖查询 |

不从 Dimensions、资源归属、策略快照或 SourceRawData 补齐来源业务标签。输入错误返回合法的
failed EnrichResult 与 nil error，生命周期继续创建 Alert；父 Context 取消仍按既定规则中止。
其他 EventSourceID 继续使用既定 Noop 行为，不执行本套入口校验。

实现验收覆盖有效正整数、缺失、字符串、布尔值、小数、0 和负数；无效输入的依赖读取次数均为零，
即使维度或其他可用信息带有业务 ID，也不能补齐标签后继续丰富；生命周期保留诊断并创建 Alert。
当前只更新设计，未修改 Go 实现或执行这些业务测试。


### 5.25 已确认：日志关联信息从 ExtraData 读取

2026-09-03，用户确认 F11 改从 Event.ExtraData["log_related_info"] 读取，替代此前放入
Dimensions 的初案；关联内容的加工和输出继续沿用旧逻辑。

旧 [LogCollectConverter](../../../../../kingeye/src/kingeye/kac/alarm_callback/converter/log_event.py)
在第 10 行独立读取 log_related_info 并覆盖内部 related_info，缺失时使用空字符串；
第 17–35 行的日志维度展示则遍历传入的全部维度。旧
[LogKeywordCLear](../../../../../kingeye/src/kingeye/kac/alarm_callback/cleaner/log_keyword.py)
在 clean_content 中使用关联信息加工文案，在 clean_log_relate_info 中输出该信息。
因此关联内容在新输入中单独承载，不作为对象定位、指标过滤或维度展示的数据。

读取时仅使用已确认的 ExtraData 键，不从 Dimensions、通用 related_info 或 SourceRawData
回退取值；缺失时沿用旧空字符串处理，不新增日志关联信息必填要求。丰富只生成独立结果，
不回写 Event.ExtraData，也不把依赖查询结果存入该字段。

读取契约仅接受 JSON 字符串。字段缺失时沿用旧空字符串语义，不产生诊断；字段存在且为字符串时，
包含空字符串在内均按旧规则处理。字段存在但类型为数字、布尔值、对象、数组或 null 时，省略
`enrich.log.log_relate_info`，记录 invalid_field、groups `[log]`，整体 partial。
`enrich.display.content` 继续只基于 Event.Content 执行可完成的日志文案加工，不消费错误类型的关联值。


实现验收：同一份日志关联文本改由 ExtraData 输入后，关联信息输出与内容加工保持旧语义；
维度展示和查询参数不因该扩展内容新增维度或条件；缺失按旧空值处理；字符串空值保留既定语义；
错误 JSON 类型产生 log 分组的 invalid_field + partial，display.content 仍基于 Event.Content 加工，Event 保持不变。
当前仅更新设计、术语和盘点引用，未修改业务代码或执行上述业务测试。

#### 5.25.1 已确认：日志专用输出归入 enrich.log

2026-09-03，用户确认日志专用输出统一保存到 enrich.log，并将 log 纳入第 5.9.1 节的
诊断 groups。日志指标与日志关键字共用适用字段，不按处理分类复制输出结构。

| 旧输出 | 新输出位置 | 适用范围与处理 |
| --- | --- | --- |
| log_theme_id | enrich.log.log_theme_id | 日志指标与日志关键字共用，取值沿用旧主题定位逻辑 |
| log_theme_name | enrich.log.log_theme_name | 日志指标与日志关键字共用，名称取得及回退沿用旧逻辑 |
| log_query_string | enrich.log.log_query_string | 按旧日志关键字分类输出，保留检索语句及其占位规则 |
| log_relate_info | enrich.log.log_relate_info | 按旧日志关键字分类输出，关联信息为空时沿用旧省略规则 |

旧 [LogMetricCLear.clean_alarm_data](../../../../../kingeye/src/kingeye/kac/alarm_callback/cleaner/log_metric.py)
（15）补充主题 ID 与名称；
[LogKeywordCLear.clean_alarm_data](../../../../../kingeye/src/kingeye/kac/alarm_callback/cleaner/log_keyword.py)
（38）还输出 log_query_string，并仅在关联信息为真值时输出 log_relate_info。
新字段位置不扩大上述分类的适用范围，不因为日志指标缺少关键字专用输出而生成失败诊断。

关联信息仍从 Event.ExtraData.log_related_info 读取，沿用第 5.25 节的输入与加工规则；
旧输入名 log_related_info 与旧输出名 log_relate_info 的拼写区别不构成额外输入别名。
加工后的告警文案仍保存到 enrich.display.content，指标查询参数仍保存到
enrich.metric.metric_query_params，派生 cw_labels 仍归 resource。

实现验收覆盖两类日志共享主题字段、关键字专用字段与空值规则、关联信息的既定输入来源，
以及日志查询或加工失败时只将实际受影响分组写入诊断。当前仅更新设计与术语，未修改
业务代码或执行这些业务测试。


### 5.26 已确认：主机 IP 与云区域使用独立维度

2026-09-03，用户确认 F09 由上游直接提供以下字段，供主机地址定位使用：

| 信息 | 输入位置 |
| --- | --- |
| 主机 IP | Event.Dimensions["bk_target_ip"] |
| 云区域 ID | Event.Dimensions["bk_target_cloud_id"] |

旧 [SystemMetricMixin.preprocess_alarms](../../../../../kingeye/src/kingeye/kac/alarm_callback/processors/basic_event.py)
第 235–242 行优先拆分 event.target，恰有两段时使用拆出的 IP 和云区域，否则回退读取工作维度。
本次统一从上表两键取得信息，主机输入不再提供或解析组合 target，也不从旧 ip、bk_cloud_id、
tags. 别名或 SourceRawData 补齐、覆盖它们。查询请求和结果关联使用同一组已解析定位值，
不保留旧流程分别从 target 与工作维度取值的两套来源。

该决定只调整 F09 的主机地址表达和读取位置，主机查询业务按旧逻辑迁移；不把两个维度
设为全部告警的公共必填字段。其他定位路径由各自决策定义；后续第 5.27 节确认了三个实例/
主机 ID 的维度来源与回退顺序；三项均缺失时仍继续 IP 查询、保持 partial 的衔接规则见第 5.27.1 节。
拨测的 Dimensions.target 继续保留原有地址语义，不套用主机的旧组合字段拆分规则。
其他指标所需的维度按各自查询定义使用，不因本节而全局重命名或修改 Event.Dimensions。

地址字段的取值、缺省、转换及查询条件已确认按旧分支处理，见第 5.27.2 节，不新增统一的
成对必填门槛。旧基础分支的云区域 -1 缺省保留在调用上下文，不回填 Event，也不新增默认
云区域 0。查询结果仍保存到已有约定的丰富分组。

实现验收：提供独立 IP/云区域时使用二者查询并关联结果；旧组合 target 或别名不能覆盖
定位值，也不能在约定字段缺失时提供回退；拨测 target 的原有处理保持不变。
当前只更新设计及盘点引用，未修改业务代码或执行上述业务测试。


### 5.27 已确认：保留三种实例标识及其维度回退顺序

2026-09-03，用户否定将主机相关输入合并成 bk_host_id 的建议，要求保留不同字段的语义，
并统一以下读取顺序。三项均来自 Event.Dimensions，不以同名外查结果冒充来源维度。

| 顺序 | 输入位置 | 语义与处理 |
| --- | --- | --- |
| 1 | Event.Dimensions["bk_inst_id"] | CMDB 模型实例 ID，结合已确定的对象模型解释 |
| 2 | Event.Dimensions["bk_host_id"] | 主机模型 HOST_OBJECT_MODEL_CODE 的专用实例 ID |
| 3 | Event.Dimensions["bk_target_host_id"] | 目标主机 ID，可能对应远程采集下发的主机；保留独立含义 |

对已确定为 HOST_OBJECT_MODEL_CODE 的对象，按 bk_inst_id → bk_host_id → bk_target_host_id 顺序选择；
三项均缺失时标记 partial，保留其他有效丰富结果及缺失诊断，不阻断 Alert 创建。
不要求三项同时提供，不把选中的值写回其他维度键，也不据此改写 Event/Alert 的主体身份。
字段选择沿用旧真值判断和已确认顺序：空字符串、数字 0 与 false 视为未提供，继续检查下一项；
非空字符串、非零数字与 true 进入对应旧转换或查询流程。选择首个真值 ID 后即停止字段回退；查询
未命中、转换失败或结果不可用时按既定 partial 处理，不再尝试后续 ID。


旧实现证据及本轮变化：

- 旧 [BaseConverter.format_event_dict](../../../../../kingeye/src/kingeye/kac/alarm_callback/converter/base.py)
  第 577–583 行先取外查 instance_detail.bk_inst_id，仅当模型为主机且缺少该 ID 时，才回退
  event.bk_host_id、工作维度 bk_target_host_id；旧首级不是直接读取 Event.Dimensions。
- 旧 [DATA get_object_model_inst_id](../../../../../kingeye/src/kingeye/kac/alarm_callback/converter/basic_data.py)
  的 system 分支先读 bk_target_host_id，再按 IP/云区域查询。旧分类之间不存在完全相同的
  三段维度读取链，不能把用户本轮统一的规则反向描述成现行 KAC 行为。
- 本轮明确的是三个输入键不合并、均从 Dimensions 读取、顺序及全部缺失时的 partial。
  其余业务继续以旧逻辑为迁移依据；查询取得的实例信息独立保留为依赖结果。

2026-09-03，用户进一步确认保留旧模型限制：仅在对象模型已确定为 HOST_OBJECT_MODEL_CODE
时启用完整三段回退。非主机模型继续使用各自原有的实例定位逻辑，不以 bk_host_id 或
bk_target_host_id 替代该模型的实例 ID；不会仅因这三个维度缺失而触发本节的 partial。
例如非主机对象仅携带采集主机 ID 时，该 ID 不能用于定位非主机实例；若其原有定位路径完整，
也不能因为没有主机 ID 而降低丰富状态。模型未知时不能仅凭出现主机 ID 推断为主机模型。

三项均缺失时与 IP/云区域查询的衔接已在第 5.27.1 节确认；有 ID 时按第一个有效字段查询，
查询未命中后停止 ID 链并按 partial 处理。主机模型限制不改变非主机分类的既有实例定位路径。

实现验收：主机模型下，三项值不同且均提供时首选 bk_inst_id；前一项缺失再取后一项；选中 ID
查询未命中时按 partial 且不尝试后续 ID；全部缺失时保留有效结果并标记 partial。非主机对象带有
采集主机 ID 时仍使用原模型的定位路径，不触发主机回退或本节的三项缺失诊断。
当前仅修改设计、术语与盘点引用，未修改业务代码或执行上述业务测试。

#### 5.27.1 已确认：三个 ID 缺失时继续 IP 查询并保持 partial

2026-09-03，用户确认：主机模型的 bk_inst_id、bk_host_id、bk_target_host_id 三个来源维度
均缺失，但 bk_target_ip、bk_target_cloud_id 可用于查询时，继续通过 IP 和云区域查询主机。
该查询承接旧主机定位能力；输入仅来自第 5.26 节确定的两个维度键。

| 查询结果 | 丰富结果 |
| --- | --- |
| 查到可用的主机信息 | 保留查得的资源信息及其可生成的丰富结果；整体仍为 partial，保留三项来源 ID 缺失诊断 |
| 未查到主机或查询失败 | 保留其他有效丰富结果及相应诊断，整体仍为 partial，不因局部定位失败阻断 Alert 创建 |

即使查询取得主机 ID，也不能将它当作已提供的 Event.Dimensions.bk_inst_id、bk_host_id 或
bk_target_host_id；不清除来源 ID 缺失诊断，不因此将状态提升为 succeeded。查询信息保存到
调用上下文和约定的丰富结果，不回填 Event 或改写 Alert 主体。partial 仍通过合法结果及
nil error 返回；父 Context 取消时继续遵循中止规则。

IP/云区域不齐全或取值无效时按旧分支处理，见第 5.27.2 节，不新增成对必填检查。
IP 与云区域查询返回多个主机候选时，沿用旧逻辑使用底层结果第一条，不新增唯一性校验或二次筛选。
读取适配器保留底层查询的既有排序语义；返回顺序缺乏稳定保证时，第一条选择可能变化，作为迁移限制记录。
选中结果只进入调用上下文和资源丰富，不改写 Event 主体或来源维度。

实现验收：三项 ID 缺失、地址维度可用且查询成功时，结果含查得的资源信息与缺失诊断，状态
为 partial；查询未命中或失败时仍保留其他有效结果；多候选时使用底层结果第一条；Event 的三个 ID
键及主体字段保持不变。
当前仅更新设计与盘点引用，未修改业务代码或执行这些业务测试。

#### 5.27.2 已确认：地址查询输入沿用旧分支处理

2026-09-03，用户要求地址查询输入按旧逻辑迁移，不采用新增的“IP 和云区域必须同时提供，
缺任一项就统一跳过查询”门槛。此前确定的字段位置、主机模型限制、三段 ID 回退及三项缺失
保持 partial 的规则继续生效；本节不恢复组合 target 或旧输入别名。

| 旧分支 | 迁移保留的取值与查询规则 |
| --- | --- |
| 基础监控 SystemMetricMixin | IP 缺失使用空字符串，云区域缺失使用 -1；构造查询时将云区域转为整数，查询条件仍为 IP 与云区域同时匹配 |
| DATA 的 system 主机分支 | 从维度读取 IP 和云区域，未启用原代码中已注释的 all 检查；构造查询时将云区域转为整数，保留 IP 与云区域两个条件 |

证据：[基础分支](../../../../../kingeye/src/kingeye/kac/alarm_callback/processors/basic_event.py)
第 241–242、267–268 行，以及 [DATA 分支](../../../../../kingeye/src/kingeye/kac/alarm_callback/converter/basic_data.py)
第 482–500 行。DATA 的“没有云区域也能检索”注释不能取代实际代码：云区域缺失导致
int(None) 失败，不能据该注释改成只按 IP 检索。数值 0 或字符串 "0" 能按旧转换生成
云区域条件，不因 Python 真值检查而跳过；本节不新增“非零才有效”的约束。

旧取值或转换无法构造请求时，在 Go 中返回可处理的转换失败，按已确认的局部丰富失败规则
保留其他结果和诊断，不使用 panic 模拟 Python 异常。对第 5.27.1 节的三个 ID 均缺失场景，
整体保持 partial；父 Context 取消仍中止。已有缺省值仅用于工作上下文，不修改来源维度。
当前 Event.Dimensions 的标量容器约束仍有效，不为复制旧输入而增加 null、对象或数组。

实现验收按旧分支分别覆盖地址齐全、字段缺失、云区域 0 及无法转换的输入，验证缺省值、
查询条件和转换失败边界；不以新增统一必填检查提前改变这些分支。当前仅更新设计与盘点引用，
未修改业务代码或执行业务测试。

### 5.28 已确认：APM 专用输出归入 enrich.apm

2026-09-03，用户确认 APM 专用输出统一保存到 enrich.apm，并将 apm 纳入第 5.9.1 节的
诊断 groups。按后续确认的统一命名规则保留旧 apm_ 前缀，见第 5.8.1 节。

| 旧输出 | 新输出位置 |
| --- | --- |
| apm_app_id | enrich.apm.apm_app_id |
| apm_app_name | enrich.apm.apm_app_name |
| apm_app_alias | enrich.apm.apm_app_alias |
| apm_service_name | enrich.apm.apm_service_name |
| apm_instance_name | enrich.apm.apm_instance_name |
| apm_interface_name | enrich.apm.apm_interface_name |
| apm_net_peer_name | enrich.apm.apm_net_peer_name |

应用查询、名称匹配和维度转换沿用旧逻辑，并遵循已确认的输入位置与租户边界。
旧 [BasicDataClear.apm_field_add](../../../../../kingeye/src/kingeye/kac/alarm_callback/cleaner/basic_data.py)
（59）根据 data_source 或结果表派生应用名称，再查询应用并按名称精确匹配；不直接采用模糊
查询返回的第一条记录。应用 ID 与别名来自匹配结果，输出应用名称来自该派生名称。
迁移所用 data_source 统一来自 StrategyConfig.spec.data_source，见第 5.16.4 节。

旧 [BaseClear.apm_dimension_key_map](../../../../../kingeye/src/kingeye/kac/alarm_callback/cleaner/base.py)
（128）将 service_name、bk_instance_id、span_name、net_peer_name 对应的维度展示条目
real_value 转为服务、实例、接口和对端名称。迁移保留该转换及适用规则，不因同名字段
出现就绕过原分类条件；原始维度仍从 Event.Dimensions 取得。

APM 派生的模型和实例标识仍归 resource，服务展示文本仍归 display.object，派生标签仍归
resource；本节不将它们复制进 apm，也不改写 Event/Alert 的主体或标签。读取失败时按已确认的
局部失败规则保留可用结果，诊断只列出实际受影响的分组。

实现验收覆盖七项字段的位置、应用精确匹配、旧维度映射，以及 APM 专用信息与资源、展示
结果的归属。当前仅更新设计与术语，未修改业务代码或执行这些业务测试。

### 5.29 已确认：K8s 专用输出归入 enrich.k8s，保留 bcs_cluster_id

2026-09-03，用户确认 K8s 输出分组与字段归属，并明确集群 ID 保留 bcs_cluster_id 命名，
不采用此前建议的 cluster_id 输出名。k8s 纳入第 5.9.1 节的诊断 groups。

| 信息 | 新输出位置 | 来源或处理 |
| --- | --- | --- |
| 集群 ID | enrich.k8s.bcs_cluster_id | 适用 K8s 分支时从 Event.Dimensions.bcs_cluster_id 取得，保留原字段名 |
| 集群名称 | enrich.k8s.cluster_name | 从查询结果取得，与 bcs_cluster_id 配对，不从同名来源维度猜测 |
| 命名空间 | enrich.k8s.namespace | 按旧分支条件填充 |
| 服务 | enrich.k8s.service | 按旧分支条件填充 |
| 工作负载类型 | enrich.k8s.workload_kind | 按旧分支条件填充 |
| 工作负载名称 | enrich.k8s.workload_name | 按旧分支条件填充 |
| Pod | enrich.k8s.pod_name | 按旧分支条件填充 |
| 容器 | enrich.k8s.container_name | 按旧分支条件填充 |
| 节点 | enrich.k8s.node | 按旧分支条件填充 |

旧 [BaseClear.k8s_field_add](../../../../../kingeye/src/kingeye/kac/alarm_callback/cleaner/base.py)（383）
按模型与维度决定是否执行，并区分集群/节点与其他模型的追加字段行为。迁移保留这些适用条件，
不因为 Event.Dimensions 中出现同名键就对所有分类填充 K8s 输出。

输出 bcs_cluster_id 与外部查询协议中的 cluster_id 分别解释；本次命名决定不授权更改外部
存储字段、查询过滤键或旧派生标签的格式，也不增加 enrich.k8s.cluster_id 别名或双写。
资源所属业务、模型与实例标识、派生标签仍归 resource，不覆盖 Event.Labels.bk_biz_id，
也不改写 Event/Alert 的主体。局部失败按已确认规则保留有效结果，诊断列出实际受影响分组。

本节确认输出结构；查询依据已在第 5.30 节统一为当前 OneModel 实例存储。
输入盘点第 6 节记录的旧 KAC 调用签名差异仍是源码事实，不能据 Linkd 的设计决定声称
旧 KAC 路径已经完成查询接口迁移。Go 侧按第 5.30.1 节直接读取 OneModel 的 ES 后端。

实现验收覆盖 bcs_cluster_id 字段名、集群 ID 与查询名称的关联、旧分支字段适用条件、
资源信息的独立归属及来源事实不变。当前仅更新设计与术语，未修改业务代码或执行这些业务测试。

### 5.30 已确认：K8s 查询统一对齐当前 OneModel 实例存储

2026-09-03，用户明确：K8s 实例定位、集群名称及 Namespace 业务归属，统一对齐当前
OneModel 实例存储。这一决定同时覆盖实例身份与展示/归属数据，不能只迁移实例 ID 查询，
却继续沿用旧 KAC 绕过 OneModel 路由、字段转换和租户约束的集群/Namespace 查询规则。

| 读取目标 | 查询与结果语义 |
| --- | --- |
| K8s 实例定位 | 按当前公共 build_k8s_inst_id 的模型与维度映射构造查询，取得 cw_object_model_inst_id；保留 Workload 的 kind=Pod 等原有分支 |
| 集群名称 | 用来源维度 bcs_cluster_id 对应存储查询字段 cluster_id，读取匹配集群的 cluster_name |
| Namespace 业务归属 | 用 cluster_id + namespace 定位 Namespace，沿用 namespace_info.get("bk_biz_id") or cluster_info.get("bk_biz_id") 的原有规则，见第 5.30.2 节 |

查询语义以当前 [公共回调实现](../../../../../kingeye/src/kingeye/common/alarm_callback/basic_push_alarm_data.py)
的 k8s_field_add（523）、clean_model_inst_id（912），以及
[公共查询函数](../../../../../kingeye/src/kingeye/common/alarm_callback/utils.py) 的 build_k8s_inst_id（534）、
search_k8s_instance_document（588）为依据。迁移不复制 KAC 未同步的 Python 调用参数，
也不依赖运行时全局租户或默认租户；所有读取显式使用 Event.BKTenantID。

输出仍遵循第 5.29 节：enrich.k8s.bcs_cluster_id 保留来源命名，查询协议内部的 cluster_id
映射由适配器处理；资源所属业务和模型实例标识归 resource，不覆盖来源标签或核心主体。
未取得可用依赖结果时按已确认的局部失败规则返回有效结果与诊断，不改走旧 KAC 查询逻辑或
SourceRawData 作为兜底，也不新增重试或创建后的补丰富流程。

本次确认数据来源与查询语义，不等于 Linkd 已能调用 Python 的 OneModel 门面。
当前源码通过可配置 Reader 和统一实例存储 SDK 执行读取；本次后端已确认使用 Elasticsearch，
Go 侧直接读取并对齐其读取规则，见第 5.30.1 节。必要连接配置仍需落实。
不新增 Kingeye 接口或修改其存储结构的既定范围继续有效。

实现验收覆盖三类读取使用统一数据来源与显式租户、模型维度映射和业务归属规则、输出
字段归属，以及查询失败不回退旧 KAC 查询规则。当前仅更新设计与术语，未修改业务代码或执行这些业务测试。

#### 5.30.1 已确认：本次接入 OneModel 的 Elasticsearch 读后端

2026-09-03，用户明确本次使用 ES。首版在 Linkd 丰富内部实现 Elasticsearch 读取适配，
按第 5.10 节的直读约定对齐 OneModel 当前数据结构和查询语义；本次不实现 Doris 适配。
不要求运行 Python OneModel 门面，也不新增 Kingeye HTTP 接口。连接由丰富配置与进程装配
提供，适配器只承担本次丰富需要的读取，不创建索引、修改映射或写入实例数据。

**索引路由取证**：不能将“统一实例存储”理解为所有 K8s 查询都固定读取 kingeye_all_instance。
当前 [SDK 装配及路由](../../../../../kingeye/src/kingeye/base/candidacy/infras/instance_storage/runtime.py)
（36、252）将模型目标解析器注入 ES 适配器；
[ES 查询适配](../../../../../kingeye/src/kingeye/base/candidacy/infras/instance_storage/elasticsearch.py)
（129、143、570）会按查询中的 cw_object_model_code 选择对应目标。当前 K8s 模型对应：

| K8s 模型 | OneModel 当前 ES 读取目标 |
| --- | --- |
| Cluster | kingeye_k8s_cluster |
| Namespace | kingeye_k8s_namespace |
| Node | kingeye_k8s_node |
| Service | kingeye_k8s_service |
| Workload | kingeye_k8s_workload |
| Pod | kingeye_k8s_pod |
| Container | kingeye_k8s_container |
| PersistentVolume | kingeye_k8s_pv |
| PersistentVolumeClaim | kingeye_k8s_pvc |

这些索引名可能与旧路径相同；是否对齐 OneModel，取决于是否使用其路由、字段、租户和
结果转换规则，而不是索引名是否变化。此前“旧独立索引”措辞在此澄清为“不回退旧 KAC
查询实现”，不额外要求迁移物理数据或建立另一套索引。

适配器按 Event.BKTenantID 构造必需的租户过滤，叠加目标模型与维度条件；返回数据也要核对
租户。ES 物理字段采用现有 SDK 映射：例如 attributes.cluster_id 对应扁平 cluster_id，
不能直接用内部 attributes. 路径查询现有 ES 文档。实例身份读取 cw_object_model_inst_id，
与第 5.29 节的 bcs_cluster_id 输出命名分别处理。

OneModel 的 bk_biz_ids 标准列表与实例属性 bk_biz_id 可以共存；后者会随实例文档保留。
本次 K8s 业务消费沿用第 5.30.2 节的旧 bk_biz_id 读取规则，不因接入 OneModel 就改为
列表消费。具体连接参数与其他未确认的字段适配继续在后续设计中落实。

实现验收覆盖按 K8s 模型选取读取目标、租户与模型过滤、字段映射及返回租户核对。
当前只核对源码和更新文档，未连接真实 ES、修改业务代码或执行上述业务测试。

#### 5.30.2 取证澄清：单实例文档保留 bk_biz_id，沿用旧消费规则

2026-09-03，用户指出 search_k8s_instance_document 返回单个实例，可直接读取 bk_biz_id。
重新核对返回与转换链路后，确认此前仅因存在标准 bk_biz_ids 列表就建议修改业务消费方式
的推导不成立，撤回该未确认建议。本节落实此前已确定的“沿用旧逻辑”，不新增多业务选择规则。

所有 K8s 模型查询沿用旧 `page_size=1` 语义，存在多个匹配文档时使用底层返回第一条，不增加
唯一性校验、显式排序或二次筛选。底层返回顺序缺乏稳定保证时，选中实例可能变化，作为迁移限制记录。


search_k8s_instance_document 返回 dict(page.items[0])，无记录时返回空字典。原文档中的 bk_biz_id
作为属性经 ES 记录转换、OneModel 实体转换及文档展开保留。资源业务沿用 namespace_info.get("bk_biz_id") or cluster_info.get("bk_biz_id")，保留旧
真值回退语义；业务名称继续按旧规则查询。单个返回实例的数量与业务字段的类型分别解释，
不能仅凭“返回一条”推导所有实例都只能属于一个业务。

本次不从 bk_biz_ids 选第一项、合并两级业务列表或增加新的列表输出；现有依赖失败与
局部降级规则继续适用，资源归属不覆盖 Event.Labels.bk_biz_id。源码证据见
[输入盘点第 6 节](../research/alarm-callback-input-field-inventory.md#6-横跨分类的-apmk8s-维度)。
实现验收覆盖 0 条、1 条和多条匹配，多条时使用底层第一条；同时验证单实例文档的 bk_biz_id
消费及 Namespace 到 Cluster 的旧真值回退。当前只完成源码核对与文档纠正，未验证真实 ES 记录或修改业务实现。

### 5.31 已确认：不迁移 bk_service_id 与 Namespace 空占位字段

2026-09-03，用户确认旧 bk_service_id 与大写 Namespace 不纳入 enrich。
旧 [BaseClear](../../../../../kingeye/src/kingeye/kac/alarm_callback/cleaner/base.py) 的
clean_bk_service_id（732）、clean_Namespace（900）均固定返回空字符串，本次涉及的
分类 Cleaner 没有重写这两个方法，因此不为它们新增输出字段或输入要求。

实际 K8s 命名空间继续保存到第 5.29 节确认的 enrich.k8s.namespace，保留原取值与适用条件。
当前仅确认范围排除，未修改业务代码或执行运行验证。

### 5.32 已确认：首次异常点时间作为可选补充信息

2026-09-03，用户确认保留 anomaly_begin_time，输入与输出位置如下：

| 含义 | 新输入位置 | 新输出位置 |
| --- | --- | --- |
| 本次告警对应的首次异常点时间 | Event.ExtraData.anomaly_begin_time | enrich.metric.anomaly_begin_time |

旧 [BaseClear.clean_anomaly_begin_time](../../../../../kingeye/src/kingeye/kac/alarm_callback/cleaner/base.py)
（820）按旧等级读取 event.anomaly 中的 anomaly_time，缺失时返回 None。迁移时由上游
将对应时间作为独立的可选来源事实提供，不要求保留完整 anomaly 对象或在丰富阶段再次按等级提取。

输入仅接受 JSON 字符串，包含空字符串；字段存在且类型正确时原样写入输出，不解析、规范化或转换
时间格式。字段缺失时省略输出且不产生诊断；字段存在但类型为数字、布尔值、对象、数组或 null 时，
省略输出，记录 invalid_field、groups `[metric]`，整体 partial。该输出不修改 Event/Alert 的核心时间字段。

实现验收覆盖普通字符串、空字符串、缺失和错误 JSON 类型，并验证输出保持来源字符串字节内容。
当前仅确认输入、输出及类型规则，未修改业务代码或执行运行验证。

### 5.33 已确认：保留旧来源标识与名称，名称配置读取暂留 TODO

2026-09-03，用户明确保留 source_id、source_name，并确认新增 enrich.source 分组保存这两个
字段，保留完整字段名；source 同时加入第 5.9.1 节的诊断 groups。

| 旧输出 | 新输出位置 | 本次取值 |
| --- | --- | --- |
| source_id | enrich.source.source_id | 沿用旧 clean_source_id()，返回 "built_in_bk" |
| source_name | enrich.source.source_name | 本次使用默认名称 "鲸眼监控"，在赋值处增加 TODO，后续改为读取配置 |

旧 [BaseClear](../../../../../kingeye/src/kingeye/kac/alarm_callback/cleaner/base.py) 的
clean_source_id（491）返回固定值；clean_source_name（495）读取 metadata_settings 中的
KMC_NAME，默认值为“鲸眼监控”。本次按用户决定暂不接入该名称配置读取。

实现时在 source_name 赋值处保留以下注释，明确当前限制与后续完成条件：

```go
// TODO: source_name 当前固定使用“鲸眼监控”；接入来源名称配置后改为读取配置，并删除此 TODO。
```

本节确认字段保留、输出位置、取值及代码注释要求。诊断仍只列出实际受影响的分组，
不因新增 source 分组而增加原因码。
当前仅更新设计，尚未添加业务代码或运行验证。

### 5.34 已确认：meta_info 从 Event.SourceEventID 取值

2026-09-03，用户明确保留 meta_info，改为直接读取 Event.SourceEventID，并确认保存到
enrich.source.meta_info，与 source_id、source_name 共用 source 分组。

旧 [BaseClear.clean_meta_info](../../../../../kingeye/src/kingeye/kac/alarm_callback/cleaner/base.py)（736）
返回 event_data.id 的字符串；该内部 ID 由旧 Processor 创建。迁移按用户决定使用
Event 已有的来源事件标识，不再生成旧内部 ID 作为此字段的取值。

本节确认字段保留、输入来源与输出位置，保留 meta_info 原字段名。
当前仅更新设计，尚未添加业务代码或运行验证。
