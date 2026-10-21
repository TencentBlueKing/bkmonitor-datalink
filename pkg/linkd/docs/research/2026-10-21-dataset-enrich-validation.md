# 历史 dataset 查询的 Enrich 只读验证

日期：2026-10-21。环境：test-bkee5，经 test-bkee5-vpn 查询，租户 `system`。
代码基线：`feat/linkd-dev@a77429657d2379bfe09f7060caade9e82b14efb6` 加本次 dataset 绑定修复。

## 原因与修复边界

策略 18/v1、73/v1 的声明均为 `query_config_type=dataset`、`dataset_code=infra.system`、`metric_field=cpu_usage`，没有目录引用或物理表。
同一不可变发布快照的执行查询则已有 `dataset_semantic.metric_field=cpu_usage`、物理表 `system.cpu_summary`、字段 `usage`。
此前 Reader 未连接这两个视图，Display/Metric 使用空表和语义字段查询指标目录，得到 `dependency_invalid(metric_catalog)`。

修复只从同一租户、策略、执行修订的完整性校验快照连接声明与执行查询，按唯一别名和语义身份配对，不按数组位置猜测。
当前支持维度与条件保持不变的 `bk_monitor/time_series` 直接映射；需要维度、条件改名或额外来源过滤的快照明确失败，不静默生成不同语义的查询。
不把目录 ID、表达式、函数、过滤条件或当前数据集配置覆盖回历史声明，也不修改持久化快照。

## 当次已发布策略清单

额外只读查询当前 `alarm_strategy_set_split_record`，以租户 `system`、`state=active`、`enabled=1`、`publish_status=published` 过滤，按当前 `execution_revision` 连接不可变快照，共 71 条，均有对应快照。
以下是执行策略 ID，不是监控模板 ID；统计的是声明入口，实际执行时 catalog/dataset 也可解析到同一物理位置。

| 声明形态 | 数量 | 策略 ID |
| --- | --- | --- |
| dataset | 2 | 18、73 |
| 直接表/字段 | 35 | 1、2、3、4、5、6、7、8、9、10、11、12、13、14、16、17、22、26、27、33、35、37、39、41、45、46、50、76、78、79、83、86、99、128、131 |
| catalog | 22 | 87、91、94、97、100、103、104、120、123、124、125、126、129、132、134、135、136、139、141、142、146、147 |
| prometheus | 1 | 145 |
| 其余未标注类型且不带直接表名 | 11 | 24、40、43、44、67、75、80、82、114、115、121 |

直接表/字段中，1、2、4、5、6、8、9、10、11、12、13、16、22、26、27、33、45、50、83 使用 `system.cpu_summary.usage`；其他直接查询还包括磁盘、事件、自定义及 APM 表。
此清单不包含待发布、发布失败、停用或已删除策略，不能代表所有已保存的管理配置。

## 样本与结果

通过 Admin 重新读取活跃 Alert，再按其 `latest_event_id` 读取真实 Event：

| 策略 | Alert ID | 本轮使用的 Event ID | 保存的事件丰富状态 | 本地完整链结果 |
| --- | --- | --- | --- | --- |
| 18/v1 | `20261017124337.system.built_in_bk.0eb3ef535ebbd748` | `20261021114517.system.built_in_bk.818883827b057aa8` | partial | succeeded |
| 73/v1 | `20261017124347.system.built_in_bk.eed6c818ea8f8c53` | `20261021114458.system.built_in_bk.e93754ffc2e1f980` | partial | succeeded |

使用当前 `built_in_bk` 的九 Processor 配置，在本地 Router 中读取真实 MySQL/OneModel 并重放：

- 两条样本的 Strategy、Resource、Display、Metric、Source、Fields 全部 succeeded；K8s、APM、Log 均按类型 skipped，无失败诊断。
- 告警名称包含 CPU 使用率，策略名称、监控模板 ID 和策略配置 ID 均有值：策略 18 对应模板 21，策略 73 对应模板 13。
- `display_name=CPU使用率`，`metric_name=usage`，`result_table_id=system.cpu_summary`，`metric_unique_id=system.cpu_summary.usage`，`unit=percent`。
- 取数参数使用物理表/字段，保留 AVG、60 秒及当前事件维度；无函数的恒等别名 `a` 沿既有规则省略外层表达式。
- 按 Lifecycle 持续触发时的临时候选身份生成描述，均返回非空内容。Event 与原 Alert 在重放前后深比较一致。

MySQL 使用 `START TRANSACTION READ ONLY` 对应的只读事务，结束后回滚；OneModel 只查询。
原始样本及本地重放报告存于本机权限 0700 的临时目录，文件为 0600；不将凭据、事件正文或资源属性纳入仓库。

## 回归覆盖与限制

`strategy_dataset_binding_test.go` 覆盖多别名乱序配对、单查询默认值、dataset/catalog 混合、声明不变、缺失/重复绑定、数据集或语义字段不匹配、无效来源、维度/条件差异、大整数条件精度与目录身份冲突。
`processors/dataset_test.go` 验证物理位置、指标目录查询和查询参数使用一致身份，保留实际计算、过滤条件和分组，输入不变。

验证结果：

- 新增绑定回归先在修复前运行失败，修复后通过；`go test ./internal/enrich/... -count=1` 通过。
- 最终 `make check` 完整通过，包含格式、普通测试、`go vet`、竞态测试、静态分析（0 issues）、Console、Helm 和发布脚本；Console 测试 451 passed、8 skipped。
- 本地只读完整链重放 2/2 通过，另核对 18 个 Processor 状态、物理取数参数、策略身份及描述生成结果。
- 受影响文档的 16 个本地链接目标检查通过。

本次验证未部署或改写线上告警。成功结果是本地代码对真实依赖的只读重放，不等于修复已在线上运行，也不回填旧活动告警的冻结 Enrich。
