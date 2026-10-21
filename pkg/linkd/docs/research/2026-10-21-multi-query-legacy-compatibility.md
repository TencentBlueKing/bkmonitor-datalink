# 单个监控项多 query 的旧 KAC 兼容验证

日期：2026-10-21。Linkd 基线：`feat/linkd-dev@a77429657d2379bfe09f7060caade9e82b14efb6` 加本次 dataset 与多指标兼容修复。
旧 KAC 证据：`052f526e9f0c6b44a3359b4288233ac8a5ff1f89` 的 `kac/alarm_callback/cleaner/base.py`、`common/alarm_callback/basic_push_alarm_data.py`。

## 已确认差异与修复

旧 KAC 的 `clean_metric_name` 按 `len(strategy_detail["items"]) > 1` 判断多监控项；单个 item 内有两条 query 时仍返回首查询的 `metric_field`。
Linkd 的 `StrategyItemProjection` 表示单个监控项，但此前按 `len(QueryConfigs) > 1` 清空 `metric_name`，把成员查询数量与监控项数量混为一谈。
用户确认对齐这个差异后，移除该 query 数量判断，沿用首查询字段作为兼容指标名；监控项或任一成员含函数时继续留空，避免此次放开 query 数量后把第二个成员带函数的原有空值变成普通字段。日志、衍生与 PromQL 分支保持原有行为。

完整取数仍依赖全部 `metric_query_params.query_configs`、别名、函数和表达式；中文监控项名称继续使用发布的组合名称。
本次不定义表达式结果的独立单位或指标身份。首成员单位和唯一标识的旧兼容语义仍存在，不能据此认为组合结果就是首成员指标。
近期 `dev/zaki/p130-15353-metric-id` 开发分支的展示 ID 统一属于另一项变化，不作为本次首查询字段兼容规则。

## 回归与真实依赖验证

回归先运行失败，正常多 query 与倒序 query 均因 `metric_name` 为空而不符合旧规则；修复后验证首字段、监控项/首成员/后续成员函数分支、表达式、成员别名及输入不变。
同时更新原多指标回归及 `system_metric_data.json` 的完整五 Processor KAC 对照断言：`metric_name=usage`，全部查询参数保持不变。

test-bkee5 环境经 VPN 访问，租户 `system`，通过 Admin 重新读取活跃 Alert 及其最新真实 Event，在本地 Router 中读取真实 MySQL/OneModel：

| 策略 | 本轮 Event ID | metric_name | 中文监控项名称 | 表达式 | 完整 Enrich |
| --- | --- | --- | --- | --- | --- |
| 45/v2 | `20261021121116.system.built_in_bk.5e3525dd4f0dd8a3` | usage | CPU使用率+CPU空闲率 | A+B | succeeded |
| 46/v2 | `20261021121117.system.built_in_bk.b496b0f98c6d09c6` | free | 磁盘可用空间大小/磁盘总空间大小 | A/B | succeeded |

两条样本均保留两条 query，描述成功生成；Event 与原 Alert 在重放前后深比较一致。MySQL 使用只读事务并回滚，OneModel 只查询。
这属于本地代码对真实依赖的只读重放，不是线上部署或 KAC 图表接口端到端验证。原始材料与报告仅保存在权限受限的本机临时目录。

最终验证：`go test ./internal/enrich/... -count=1` 通过；补充后续成员函数边界后，Processor 测试和完整 `make check` 均通过。
完整门禁包含普通测试、`go vet`、竞态测试、静态分析（0 issues）、Console（451 passed、8 skipped）、Helm 和发布脚本；受影响文档的 43 个本地链接目标检查通过。

本次验证没有修改线上告警。
