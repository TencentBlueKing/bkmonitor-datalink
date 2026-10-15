# 日志指标名称清洗与真实事件回放

日期：2026-10-15。Linkd 基线：`facb4eab`；旧 KAC 源码：`052f526e9f`。
本次按用户要求恢复日志指标名称的旧规则，行为说明见[丰富设计](../design/enrich.md#96-log_metric--log_keyword)。

## 根因和修正

旧 KAC 的 `alarm_callback/processors/log_metric.py` 将日志主题名填入实例展示名，
Converter 将其转换为 `alarm_obj`。`LogMetricCLear.clean_name` 优先使用 `alarm_alias`，
否则生成 `alarm_obj + 发生了 + clean_item() + 告警`；`clean_item()` 优先使用监控项
`alias_name`，未配置时沿用通用监控项名称清洗。

Linkd 原先直接使用可能为空的原始 `SubjectName`，并将日志指标的 `itemName` 统一替换为
`source_config.query_string`，为空时再替换为 `--`。这导致已有主题名和监控项别名也会生成
“发生了--告警”。修正后 Display 与 Log 共用租户限定主题解析和 Scope 查询缓存；名称使用
主题名及监控项别名或通用 `cleanItem` 结果。日志关键字名称、Metric 查询字段和内容生成规则未调整。

## 真实输入与结果

使用 `test-bkee5-vpn` 转发端点，Kafka 旁路消费者配置 `group_id=None`、
`enable_auto_commit=False`，只读 `alarmd_event` 分区 0 的 `[2863474, 2868474)`。
从 5,000 条原文中选择策略 121 的两种实际事件，按原始 `event_id` 查询 Linkd Event，
核对来源身份、title 和 content。环境发布材料为 `system / 121 / 1791959436354060`，
关联监控模板 139，分类为 `data / klc / metric`，不是日志关键字；策略名称中的
`LogKeyword` 字样不作为分类依据。

| 类型 | Kafka offset | 匹配的 Linkd Event | 来源版本 |
| --- | --- | --- | --- |
| `no_data` | 2863529 | `20261015040212.system.built_in_bk.5035ce6ca4ef1ce1` | 26 |
| `metric_algorithm` | 2863530 | `20261015040212.system.built_in_bk.7445acd9c134c576` | 26 |

两条原始事件的 SubjectName 均为空。分别从 Kafka 原文经 Standard Cleaner、
Strategy、Resource、Display、Metric 和 EnrichedEvent 在本地内存中回放，四个 Processor
均为 `succeeded`，输出名称均为：

```text
vmraw-doris-2181发生了【1】指标丨主机ID(vmraw-doris-2181) (bk_host_id)告警
```

期望值由指定旧 KAC 提交中的真实 `_get_strategy_config_alias`、`clean_item` 和
`clean_name` 函数 AST 执行取得，不导入旧应用或调用旧链路写入端。两条原文回放均与旧值一致。
线上 MySQL、ES 仅用于读取真实策略、指标、业务和主题；未写入线上 Event、Alert 或 KAC，
未发送动作或提交 Kafka offset。临时原文与 fixture 仅保存在私有临时目录，验证后删除。

## 验证

- 修复前，回归测试稳定得到 `发生了--告警`；修复后日志相关测试通过。
- 新增 9 个名称场景：空 subject、别名优先、监控项名称优先、空查询、完整告警别名、
  主题查询优先、跨租户拒绝、错误主题 ID 拒绝，以及创建内容模式下原始内容不变。
- `TestPublishedStrategyLiveTitle` 支持显式传入 `raw_payload`，与捕获 Event 核对后从
  Standard Cleaner 开始执行；两条真实输入分别运行 `go test -race ./internal/enrich/assembly
  -run '^TestPublishedStrategyLiveTitle$' -count=1 -v`，均通过。
- `make check` 中格式、Go 普通测试、vet、race、golangci-lint 均通过，静态分析为 0 issues；
  随后的 Console 测试有 5 条因 `localStorage` 为 undefined 失败，门禁在此结束，
  未执行后面的 Helm/release 阶段。按用户范围未修改 Console。
- 补齐原文回放入口后，再次运行 `make test vet race lint`，全部通过。

## 交付边界

本次证明修复代码对真实输入的本地只读回放结果，不代表已部署或线上新告警已验收。
既有 Alert 保存的丰富结果和 KAC 文档不会被此代码自动回写；KAC 普通同步保留首次转换的
名称等丰富数据，见[投影字段所有权](../reference/contracts/kac-alert-projection-v1.md#索引与字段所有权)。
未对历史告警执行回填。
