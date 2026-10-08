# KAC 策略条件与 Linkd 匹配器对照

调查日期：2026-09-30。KAC 源码：Kingeye `1874066858`；Linkd 开发基线：`49e0db23`，本地未提交实现。
对照对象是 Python 构造的 ES DSL 与实际 mapping/analyzer，不能用操作符名称直接推断语义。
实际实现见 `internal/policy/expression.go`、`condition.go`、`schedule.go`、`value.go`；总体规则见
[开发方案](../design/event-enrich-and-alarm-policies.md)。
初始实现进度是 2026-09-30 的调查记录，后续进度以开发方案第 11.1 节为准；本页末尾记录
2026-10-06 新增的固定源码对照，不把旧调查中的“待接入”当作当前实现状态。

## 条件和字段

源码证据：`src/kingeye/kac/common/policy_utils.py` 的 `construct_and_dsl`；
`src/kingeye/kac/alarm/models.py` 的分析器；
`src/kingeye/base/domains/alarm/models/alarm_event_document.py` 的字段 mapping。

| 配置 | KAC 实际查询 | Linkd 当前实现 |
| --- | --- | --- |
| text 字段 `term` | 对 `.keyword` 发 terms | 完整值区分大小写匹配，多目标 OR |
| text 字段 `terms` | 对分析后的 text 发 terms | 匹配索引 token，查询值自身不分词 |
| text 字段 `wildcard` | `match_phrase`，并非 glob | 相同分析器后的连续 token 短语 |
| text 字段 `regexp` | 对 `.keyword` 发 regexp | 完整值正则；只接受明确支持的共有语法 |
| keyword 字段 | 原字段 exact/phrase/regexp | 根据声明 mapping 求值，不猜测实际值类型 |
| `must_not_*` | 对相应正条件取反 | 缺失可匹配否定条件；读取失败不能取反成命中 |
| `bk_obj_asst_id` | 从关系两端查询目标模型/实例 | 匹配器委托只读关系端口；当前接入和验证进度见开发方案 |

例如 `name="CPU Load"`：`term="CPU Load"` 命中，`terms="CPU"` 不命中，`terms="c"` 命中。
这是原查询中真实存在的区别，本次保留。不能将两个操作符统一为字符串相等。

KAC text 分析器依次执行 `(.+?)` → `$1 ` 的 Java pattern_replace、whitespace tokenizer、lowercase。
Linkd 模拟其 Unicode 行结束符、Java 空白字符和大小写行为。真实 ES 对照包含普通空格、NBSP、NEL、
换行、中文、希腊字母、İ、emoji。keyword 子字段 `ignore_above=8191` 的 UTF-16 长度边界也已对照。

支持的 regexp 子集：普通字符、点、字符类、分组、分支、重复和转义字面量。
拒绝 Lucene 补集/交集/数值区间/任意字符串等扩展、锚点、RE2 字母转义及 `(?...)` 扩展；
不能把未知语法静默当作另一种正则解释。模式最多 256 字符/1024 字节，运行时输入最多 8 KiB；
超限按不可求值处理。共有子集以完整值匹配，点包含换行，区别于 Go 默认设置。

内置目录来自 KAC mapping。自定义字段必须显式声明 `field_mappings`，限定读取有效 Event/Alert 的
`labels/extra_data`，不能读取原始 payload 兜底。依赖 KAC 处置数据和标签目录的 `conductor/tag_info`
尚未提供可靠数据源，发布时拒绝，而不是假装支持。字段缺失与依赖失败使用不同结果。

## 时间和分组

时间源码证据：`src/kingeye/kac/common/policy_activate_time_service.py`。
KAC 将当前时间格式化到秒再比较，所以结束时间 `11:00:00` 包含该秒的所有毫秒。
Linkd 保留整秒端点包含、时段并集、空集合不生效和依赖屏蔽忽略时段的行为。
时区改为显式配置、默认 Asia/Shanghai，跨午夜需拆段，属于已确认调整；本地绝对时间如果遇到
DST 不存在或歧义则拒绝，可改传带明确偏移的 RFC3339 时间。

分组使用已确认的新有效值定义：缺失/null/空白字符串/空数组/空对象无效，0/false 有效。
数字 1、1.0 等价，-0 与 0 等价；字符串与数字/布尔分离；数组保留顺序，对象按键排序。
使用有界、类型化规范编码后哈希，不沿用 KAC 的真假值判断或直接字符串拼接。

## 动态分组定义读取中的额外边界

`base/domains/dynamic_group/strict.py` 读取 `dynamic_group_v2` 的当前定义、模型和空间，
不使用 `dynamic_group_member_v2` 快照。普通空间按业务过滤，全局属性来自当前租户的
`metadata_space.is_global`，再展开该租户实际业务集合；不使用硬编码全局 ID。

`utils/fetcher.py` 从条件根对象取 `rules`，`ConditionConverter.build_instance_property_filter`
重新建立 AND 根节点，因此旧根对象即使声明 OR，实际仍按 AND 执行；嵌套组的 OR 保留。
Linkd 当前按该行为编译并有对照单测。这是后续可讨论的旧语义问题，本次未改成最外层 OR。
动态分组的 `contains` 使用 OneModel 字面量包含规则，与告警策略的 `wildcard` 短语规则不同。
字段类型来自模型目录，不从实例值猜测；无效、未知类型和越界分组都拒绝本次完整解析。

主机拓扑与服务实例的数据来源不同：KAC 的服务实例 selector 有 CMDB 直查路径。
当前 Linkd 主机投影适配不能代替这条路径，服务实例适配仍属未完成项。

2026-10-05 补充核对同一只读提交 `1874066858`：

- `base/domains/onemodel/capabilities.py` 中普通 CMDB/legacy 模型没有主机式拓扑能力；主机为 `cw-Host`。服务实例在公开 V1 capabilities 仅显示 `instances`，执行层另有服务/集群/模块来源，不能据此把任意模型投射为主机成员。
- `resolve.py::_expand_service_source` 对服务实例要求具体业务；拓扑限当前业务、集群或模块，交给 `instances.py::list_cmdb_service_instance_entities`。静态服务实例也通过 `get_cmdb_service_instance_entities_by_ids` 实时读取，不走统一 ES alias。
- `instances.py` 要求完整分页、计数稳定和成员无重复，再批量读取服务实例及宿主机事实。真实请求来自 `base/infras/third_party_api/cmdb/{api,client}.py`，依赖 CMDB 详情/列表等接口及租户认证；Linkd 现有 MySQL/OneModel 资源配置尚不能代替该接口。
- 主机节点/成员索引以 `base/infras/instance_storage/cmdb_business_topology.py` 为依据，分别为带部署前缀的 `cmdb_biz_topo_node` 和 `cmdb_biz_topo_host_membership`；节点 locator 与 canonical 模型/实例是不同身份。

Linkd 已补齐主机成员 Scroll 的累计 32 MiB 限制；此前只限制每页 1 MiB 和总行数，祖先路径较长时会
超过调用的数据预算。回归用例先复现 8400 条合法成员被完整接受，再验证超限后不返回部分集合并释放 Scroll。
真实 ES 7.17.7 E2E 还发现 Scroll 请求不能携带 `track_total_hits=false`，服务器会返回 HTTP 400
及 `disabling [track_total_hits] is not allowed in a scroll context`。已移除该选项，新增请求回归；
完整性继续依赖逐页读取、身份检查、总量限制及最终实例复核，不能靠一个总数字段判断成功。

## 已执行验证和边界

- `go test -race ./internal/policy/...`：条件、布尔表达式、无效配置、时间和分组边界。
- 设置 `LINKD_TEST_ELASTICSEARCH_URL`：与 ES 7.17.7 使用同一 analyzer/mapping 的真实查询结果逐条比较。
- 设置 `LINKD_TEST_MYSQL_DSN`：策略发布和 ES/MySQL 存储共同契约；只创建和清理测试专属资源。
- `make check`：当前本地实现通过完整门禁。

以上是初始调查阶段证据，不是完整 KAC 应用进程对跑或三类策略运行验收。当时尚待接入有效字段视图、
OneModel 静态/拓扑/动态分组/关系解析、运行时准入和副作用；这些能力的后续实现见开发方案第 11.1 节。
其故障和超限须按开发方案跳过受影响策略并保留诊断。

## 合并父告警模板的源码补充（2026-09-30）

再次核对 Kingeye `1874066858` 的 `kac/alarm_merge/utils.py`、`serializers.py` 和
`base/config/kac.py`，这三个文件无本地改动：

- `generate_merge_alarm` 先处理 `new_alarm_config`，再用 builtin_info 覆盖时间、身份、来源、action、租户和策略标签；模板不能实际覆盖这些系统字段。
- `${alarm_num}` 来自成员数量；`${字段}` 只有出现在 aggregate_fields 时才会替换，取首条成员值。
- `${cw_merged_字段}` 将每个成员的字段转为字符串后去重，缺失字段使用 `--`，用 `###` 连接，再按字符截断该变量值；不是对整条最终模板统一截断。
- 正常系统默认 `max_merge_field_length=500`；读取配置缺项时回退为 200，运行时再保证最小 200。
- 原实现使用 Python set，集合连接顺序未固定；Linkd 已确认改为稳定排序。原实现保留未识别变量文本，非字符串模板值直接通过；Linkd 的发布字段/类型校验仍需按已确认的有效值规则明确收敛，不能称为已经支持完整旧输入形态。
- serializer 的 `check_new_alarm_config` 只检查重复 key；其他确定可校验的字段和输出约束须由 Linkd 明确校验。KAC 最后为缺失的基础字段补空字符串，不代表这些结果能满足 Linkd 标准 Event 的必填/类型限制。

此节记录旧实现事实。后续已采纳的 Linkd 模板规则见[开发方案](../design/event-enrich-and-alarm-policies.md#832-内部-event-与模板)：
数组/对象明确采用稳定 JSON；单值布尔/null 仍使用 KAC 文本，成员文字不二次展开；编译时拒绝未知变量和系统字段覆盖，
发布快照固定截断配置；模板输出需要满足标准 Event。初始阶段仅验证了模板单元与 Memory Lifecycle，
后续独立合并控制任务和真实流程的装配/验证结果见开发方案第 11.1 节。

## 固定源码规则执行对照（2026-10-06）

[离线对照入口与运行方法](../../tests/kac_behavior_comparison/README.md)从同一 Kingeye 快照的五个文件提取实际 AST，
校验完整文件 SHA-256 后，执行 `generate_merge_alarm`、`build_cache_keys` 和
`_PolicyActivateTimeService.is_active`。时间常量和变量正则也从固定源码读取，业务函数体没有另写
Python 模拟版本。时钟、动态配置、UUID 和最小基础字段目录是显式夹具，不初始化 Django 或读取 KAC
数据库、Redis、Celery；不能据此宣称完整应用状态机已经对跑。

CPython 3.14.3、`PYTHONHASHSEED=0` 下 46 个源码场景、四组分组身份关系通过 Go race 对照，
未出现未解释差异。13 个值/配置结果差异和四组身份差异来自既有确认：0/false 有效，Unicode 空白及
嵌套无效值拒绝，数字规范化、类型区分和无分隔符碰撞，合并变量稳定排序，复杂值稳定 JSON，成员文字
不二次展开，发布时拒绝未知/未声明变量及非法长度。其他结果要求逐项一致，包含日历、结束秒最后毫秒、
空时段、成员数量、缺失/null/布尔文字、普通聚合变量和 Unicode 逐变量截断。

同轮 `TestKACElasticsearchConditionBehaviorComparison` 在真实 ES 7.17.7 复验通过；它是原 mapping/analyzer/DSL
的查询真值测试，与上述离线源码规则测试分开。可选 JSON 报告保存实际解释器、哈希算法、源码摘要、
完整合成输入、旧/新结果及差异理由，源码漂移会主动失败。剩余旧状态机、服务实例实时来源和可靠出口
接入仍按开发方案继续，不因本轮对照通过缩小目标。

## 防抖、聚合和窗口序列对照（2026-10-06）

新增[状态序列对照](../../tests/kac_behavior_comparison/README.md#状态序列对照)，固定 convergence.py、merge.py、
celery_tasks.py、common/utils.py 的实际函数体、基准时间、hash seed 和 UTC。KAC 使用显式顺序
Redis/查询/任务夹具，Linkd 使用真实 Redis Lua；合并另经过实际 Lifecycle、Memory Alert 仓储和
MergeJudge。只移除 KAC 调度/锁装饰器，不改裁决函数体；不宣称覆盖旧并发锁、Redis TTL、Celery
执行或父告警最终落库。聚合关闭用例显式调用 Linkd 清理端口，自动关闭接线由独立 E2E 证明。

16 组序列与预期轨迹均通过 race 对照，四项差异明确保留：

- KAC 防抖重置分支使用 alarm_time，迟到事件可能使旧记录继续计数；Linkd 使用处理时间滑窗。
- KAC 同一 alarm_id 再次进入该计数函数仍会增加 current_count；Linkd 同 Event 重投复用首次结果。
- KAC 把同批 ID 集合加入计数列表，固定 seed 的 a/b/c 输入选择 b；Linkd 按处理顺序放行当前第 N 条 c。
- KAC 周期判定 `int(time.time()) < window - 1`，第 59 秒即可请求 60 秒周期的合并；Linkd 明确等待
  精确 deadline。对照记录的是裁决派发选择，不能把请求已产生等同于旧父告警已经创建。

固定聚合窗口的闭区间、主关闭后重建、重复成员，以及合并全部组/至少两个成员、缺组等待、到期失败
和后续窗口等共同场景结果一致。规则对照同时改用源码中的 md5_hash，增加第六个摘要保护文件；
46 个无状态规则和四组身份关系复验通过。本轮仍不构成完整 KAC 应用状态机对跑。

## 屏蔽入口、选主与目标范围对照（2026-10-06）

同一快照 `187406685819a71e9e361b569ca32483a14311f1` 的 shield.py、policy_utils.py、生效时间
服务及常量文件均无本地修改，按四个摘要冻结。新增[屏蔽源码对照](../../tests/kac_behavior_comparison/README.md#屏蔽入口与选主对照)
执行实际外层入口、时间/依赖处理器和启用判断；查询、目标解析、关系数据、快照、Redis 清理和 Celery
出口使用明确的端口夹具。Linkd 侧经过真实 Redis、Lifecycle、Memory 仓储，并检查准入与处置次数。

21 个场景通过 race 对照，包含时间边界/停用、活动主与待处理主优先级、最新主选定后的分钟窗口、
不回退旧主、自身排除、子条件和跨模型关系。18 个结果一致，三个用例的差异对应已确认的两条规则：
待处理主采用首次原子登记；目标集合仅用于主候选。

后者的源码链为 `RelyShieldHandler.handle_alarm` → `PolicyManage.alarm_policies_matching` →
`_legacy_es_policy_matching` → `policy2dsl`。旧处理器将同一 `target_entities` 传入两个主查询和
子查询，DSL 再把 canonical 模型/实例集合 AND 到子条件上。真实 ES 对照确认该过滤会排除主集合外
的子实例；固定源码用例同时复现交换机目标排除关联主机。用户已于本次讨论确认 Linkd 仅限制主，
子仍按租户/业务范围、`rely_policy`、时间范围和关系匹配，权威规则见
[开发方案第 7.2 节](../design/event-enrich-and-alarm-policies.md#72-自定义依赖屏蔽配置)。

本轮不改变 Linkd 生产匹配行为。关系夹具只证明可信主身份传入匹配端口，不代表真实 CMDB 读取；
旧任务仅被捕获，未运行 KAC 解除任务、数据库或 Celery。完整 make check 与此前 46 个规则、16 组
状态序列和真实 ES 条件对照复验通过，完整接入与验收仍以开发方案进度表为准。
