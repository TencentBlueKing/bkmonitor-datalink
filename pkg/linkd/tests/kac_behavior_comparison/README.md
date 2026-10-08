# KAC 行为一致性对照测试

本目录 `kac_behavior_comparison` 保存 Linkd 与固定版本 KAC 的行为对照，用于验证已确认的兼容规则和差异。

[`kac_policy_behavior_comparison.py`](kac_policy_behavior_comparison.py) 只读提取 Kingeye `1874066858` 的实际函数 AST，
先校验六个文件的 SHA-256，再执行生效时间、分组键和合并模板规则；MD5 辅助函数也使用实际源码。
没有复制这些业务函数的 Python
实现，也不导入 KAC 应用模块。Go 对照入口为
[`TestKACSourcePolicyRulesBehaviorComparison`](../../internal/policy/kac_behavior_comparison_integration_test.go)。

测试显式固定时钟、输入顺序、`PYTHONHASHSEED=0`、动态截断配置、UUID 和最小基础字段目录。
源文件漂移会失败，不自动改摘要或采用新的结果；重新核对差异后才能更新基线。报告包含实际 Python
版本、哈希算法、源码摘要、完整合成输入、KAC/Linkd 结果和预期差异。

在 Linkd 根目录执行，普通 `make check` 不依赖本机 Python 或 Kingeye checkout：

```bash
LINKD_TEST_KAC_SOURCE_ROOT=/path/to/kingeye \
LINKD_TEST_KAC_PYTHON=/path/to/python3 \
LINKD_TEST_KAC_BEHAVIOR_COMPARISON_REPORT=/tmp/linkd-kac-comparison.json \
go test -race -count=1 -v ./internal/policy -run '^TestKACSourcePolicyRulesBehaviorComparison$'
```

`LINKD_TEST_KAC_SOURCE_ROOT` 是显式启用开关；未设置则跳过。Python 必须为 CPython，只用标准库，
`LINKD_TEST_KAC_PYTHON` 省略时用 PATH 中的 `python3`。报告路径可省略；只包含合成数据。
子进程最多 30 秒，输入/输出各不超过 1 MiB，stderr 不超过 8 KiB，不改 KAC 源码。

当前覆盖 46 个规则场景和四组分组身份关系：

- 单次、每日、每周、每月、闰日、周日、通配日、时段并集、空时段和结束秒的最后一个毫秒。
- 空/缺失、0/false、Unicode 空白、嵌套无效值、数字/字符串/布尔类型区分、数值归一化和分隔符碰撞。
- 成员计数、缺失/null/布尔文字、普通聚合变量、Unicode 逐变量截断、稳定排序、数组/对象 JSON、
  成员文字不二次展开、未知变量/未声明聚合字段和非法截断长度。

其中 13 个值/配置结果差异及四组身份关系差异均来自开发方案已确认调整；其余结果要求一致。
Python set 的具体旧输出按固定 hash seed 检查并记录运行时版本；升级解释器引发结果变化时需重新审查，
不能因此修改 Linkd 的稳定排序约定。

这不是 KAC 应用、数据库、Redis、Celery 或 HTTP 接收端集成，不证明三类策略的完整旧状态机对跑。
查询语义另外使用 `TestKACElasticsearchConditionBehaviorComparison`，通过 `LINKD_TEST_ELASTICSEARCH_URL`
连接真实 ES，在独立临时索引上执行原 analyzer/mapping/DSL 对照并清理。
Linkd 实际生命周期与中间件流程见 [all-in-one 验收](../e2e/allinone/README.md)。

## 状态序列对照

[`kac_state_behavior_comparison.py`](kac_state_behavior_comparison.py) 从另外四个固定文件中提取实际防抖计数、聚合裁决、合并
入窗和周期裁决函数，使用真实 `md5_hash`、`str_date2timestamp`。源码和输入
[state-cases.json](testdata/state-cases.json)一同冻结，Python `TZ=UTC`、hash seed 和基准时间显式固定。
KAC 侧 Redis 命令返回 bytes，候选查询、策略快照和 Celery 收集器是顺序端口夹具；去掉的是锁/任务
装饰器，不改业务函数体，不声称验证旧锁协议、并发、Redis 物理 TTL 或接收端执行。

Go 入口为 [`TestKACStateSequenceBehaviorComparison`](../../internal/policy/runtime/kac_state_behavior_comparison_integration_test.go)：
防抖/聚合使用实际 Linkd Redis Lua；合并同时经过真实 Lifecycle 入窗、Memory Alert 仓储与 MergeJudge
重读/冻结。来源及业务范围使用明确夹具。聚合主关闭在端口上显式调用释放，不能把这组对照当作关闭
自动接线验证；该自动路径由 all-in-one 套件另外验证。合并比较的是裁决候选/释放选择，不是父告警
创建或 Celery 任务执行结果，且断言仅窗口冻结不会提前输出处置。

```bash
LINKD_TEST_KAC_SOURCE_ROOT=/path/to/kingeye \
LINKD_TEST_KAC_PYTHON=/path/to/python3 \
LINKD_TEST_REDIS_ADDRESS=127.0.0.1:6379 \
LINKD_TEST_KAC_STATE_REPORT=/tmp/linkd-kac-state.json \
go test -race -count=1 -v ./internal/policy/runtime -run '^TestKACStateSequenceBehaviorComparison$'
```

同时需要源码路径和 Redis 地址才启用；密码用 `LINKD_TEST_REDIS_PASSWORD`，不打印密码。
每个场景使用独立 Linkd 部署哈希前缀，结束只清理自己的键，不清空共享数据库。
16 组序列覆盖防抖 N 次、滑窗、闭区间、乱序事件时间、重投与批次，聚合固定窗口、重复成员和主终态，
合并提前成功/缺组等待/到期失败、单成员命中多组、周期精确门槛及后续窗口。

四项确认差异在输入文件中显式声明：按处理时间计数、同 Event 重投只计一次、放行当前第 N 条 Event，
以及周期等待精确 deadline。旧 KAC 的 `alarm_merge_calc` 用 `window - 1` 作为秒级判断端点，在
窗口第 59 秒即可请求 60 秒周期的合并；Linkd 第 59 秒仍等待、第 60 秒才裁决。它属于既定精确窗口
规则，不是本次修改生产逻辑。其余序列要求双方输出逐项一致。

## 屏蔽入口与选主对照

[`kac_shield_behavior_comparison.py`](kac_shield_behavior_comparison.py) 固定四个 KAC 源文件，执行实际 `shield_matching`、
时间/依赖处理器、策略启用判断与时段函数。输入和双方预期保存在
[shield-cases.json](testdata/shield-cases.json)，Go 入口为
[`TestKACShieldSelectionBehaviorComparison`](../../internal/policy/runtime/kac_shield_behavior_comparison_integration_test.go)。
Linkd 使用真实 Redis 和 Lifecycle、Memory Alert 仓储，检查屏蔽绑定、准入及 action Hook 次数。

KAC 查询、目标解析、关系结果、快照和任务出口是显式端口夹具：查询只实现这些用例需要的名称相等、
目标集合 AND、状态、时间闭区间及排序，不模拟通用 ES 条件引擎。CMDB 用例检查实际处理器将选中
交换机身份注入关系条件；关系数据是固定的交换机到主机关系，不代表真实 CMDB 接入。
旧查询的 canonical 目标 AND 另由 `TestKACElasticsearchConditionBehaviorComparison/dependency_child_target_scope`
在真实 ES 上验证。此对照不覆盖 KAC 解除任务、数据库、Celery 执行或完整应用状态机。

```bash
LINKD_TEST_KAC_SOURCE_ROOT=/path/to/kingeye \
LINKD_TEST_KAC_PYTHON=/path/to/python3 \
LINKD_TEST_REDIS_ADDRESS=127.0.0.1:6379 \
LINKD_TEST_KAC_SHIELD_REPORT=/tmp/linkd-kac-shield.json \
go test -race -count=1 -v ./internal/policy/runtime -run '^TestKACShieldSelectionBehaviorComparison$'
```

21 个场景覆盖时间屏蔽前后/端点/停用、无主、最新活动主、待处理主、活动优先、先选主再检查时间、
前后分钟端点、自身排除、子条件不符及目标集合外的子。18 个结果一致；三个差异用例对应两项已确认
规则：待处理主采用首次原子登记；目标集合仅限制主，允许同模型其他实例或跨模型关联子参与屏蔽。
不会将旧 KAC 的主目标集合对子查询的过滤照搬到 Linkd。报告保留摘要、双方结果、查询入参及旧自动
抑制清理 ID，仅含合成数据。启用方式、时间/输出上限、独立 Redis 前缀及清理方式与上节相同。

## CMDB 实时事实投影对照

[`kac_cmdb_behavior_comparison.py`](kac_cmdb_behavior_comparison.py) 固定 Kingeye `1874066858` 的三个文件摘要，执行实际
`get_cmdb_service_instance_entities_by_ids`、`_direct_host_to_entity` 与 `_topology_unique_id`。
合成服务详情/主机事实经过原函数投影；Entity 构造端口保留实际传参，未导入 Django 或访问外部 API。
Go 侧使用真实 CMDB HTTP 适配器连接协议服务器，比较全部身份、业务、属性、来源及 locator。
完整字段和 KAC 缺省值两组通过；不将这项有限投影对照称为完整 KAC 应用/CMDB 集成。

```bash
LINKD_TEST_KAC_SOURCE_ROOT=/path/to/kingeye \
LINKD_TEST_KAC_PYTHON=/path/to/python3 \
go test -race -count=1 -v ./internal/cmdb -run '^TestKACCMDBSourceBehaviorComparison$'
```
