# PromQL 结果维度展示修复与只读验证

日期：2026-10-21。修复起始代码基线：`663fe97354600ce86da1129bb4964fa0d3a85c8a`。本文记录本地验证结果，不代表修改已经部署。

## 问题与修改

test-bkee5 的策略 `145/version 1` 使用 PromQL，查询声明的 `agg_dimension` 为空。原始 Event 有 14 个维度，但 Display 原来只遍历声明分组字段，输出空 `dimensions` 和 `dimension_text`，KAC 收到的 `dimension_info` 因而为空。

PromQL 现在按实际返回的标签和附加维度生成展示项，名称优先取指标元数据，未知或空名称保留原键；固定模型身份条目保持原顺序，其他标签按键排序、去重。无数据控制标记不进入展示。普通指标继续使用声明分组维度。

查询参数仍保留完整 PromQL，不推测或补写 `where`、`group_by`、`metrics`；原始 Event 和历史 Alert 不回填。规则位置见 [Display 说明](../design/enrich.md#63-display)。

## 回归测试

- 真实 PromQL 发布快照的整链测试新增断言：14 个原始维度必须全部进入展示，保留 `real_key`、`real_value`，并生成非空维度文本；原实现稳定返回 0 个维度，修复后通过。
- 表驱动覆盖无声明分组、目录缺失、未知或空名称、零值和 false、聚合后标签减少、规范身份与附加维度去重、无标签、无数据控制标记、普通指标分组行为。
- 目录读取的 Context 取消继续返回错误，不能当作目录未命中吞掉。
- KAC 兼容消息测试确认 Display 的维度补丁投影到 `dimension_info`。

## 策略 145 的真实事件验证

通过 Admin 只读取得以下 Alert 的触发事件和最新事件：

- Alert：`20261021123111.system.built_in_bk.8930dee0a34627bc`。
- 触发 Event：`20261021123111.system.built_in_bk.0f9aa63a679c997d`。
- 最新 Event：`20261021124211.system.built_in_bk.3edd6ce34934e723`。
- 对应 KAC 告警：`linkd-f939671c-e2f7-581d-83ce-81d574a8864e`。

本地通过 Go overlay 调用实际 Router，MySQL 使用只读事务读取同一精确策略版本，OneModel 只执行读取，并在本地生成 KAC 兼容消息，没有调用 Hook 或任何告警写接口。

两条 Event 均有 info、warning 判定，共 4 个结果：

| 核对项 | 结果 |
| --- | --- |
| Enrich | 全部 succeeded |
| Display 维度 | 每个结果均为 14 个 |
| Display 维度文本 | 每个结果均非空 |
| KAC dimension_info | 每个结果均非空 |
| metric_query_params | 与原先保存的对应等级结果完全一致 |
| 输入 Event/Alert | 调用前后完全一致 |

这是修改后代码的本地只读验证，不代表修改已经部署。

受影响包测试及完整 `make check` 均通过，包括普通 Go 测试、vet、race、golangci-lint、Console、Helm 和发布脚本检查。本机 Node 26 的原生 Web Storage 与 Console 测试环境冲突，最终检查设置 `NODE_OPTIONS=--no-experimental-webstorage`，并将已存在的 Helm 3.17.3 加入该次命令 PATH；没有修改 Console 或门禁配置。受影响文档链接及锚点已检查。

## 另一条告警的 content 追查

`linkd-893e7bdf-d561-584c-9884-8b596440f2bd` 对应 Linkd Alert `20261020115715.system.built_in_bk.67298d0c6be2a06d`，原始策略 `83/version 1`，KAC 的 `strategy_id=25` 是监控模板 ID。

只读查询该 Alert 关联的 63 条事件，未发生分页截断。最后一次触发是 `20261020130015.system.built_in_bk.10fcd3bbd3e73fbf`，接收时间为北京时间 2026-10-20 21:00:15。KAC 与 Linkd 保存的 content 都与该事件原文完全相同：

```text
value=23.434458831625 at 2026-10-20T12:59:00Z; 5 of 5 points in the window were anomalous
```

随后 `20261020130103.system.built_in_bk.4f52d5e65b719eca` 以 `__ALL__/closed`、`strategy_inactive` 关闭了该 Alert，关闭时间为北京时间 2026-10-20 21:01:02。`terminalAlert` 保留此前的 content；该已结束记录不属于今天部署后仍在持续触发的活跃告警，也不会因部署新渲染器而自动重算。

对最后触发事件按原策略版本执行当前线上 Admin 只读 opening preview，Enrich 为 succeeded、无 content_diagnostic，返回：

```text
AVG(CPU使用率) >= 10.0%, 当前值23.434459%
```

因此可以确认当前渲染器支持这条真实事件。保存的原文属于历史结果；本次未执行历史 content 修复，也未取得当时生命周期日志来推断具体底层报错。
