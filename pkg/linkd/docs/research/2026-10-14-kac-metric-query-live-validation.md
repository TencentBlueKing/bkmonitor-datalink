# KAC 指标查询修复与真实环境验证

验证日期：2026-10-14。代码基线：`c818ad72228e99b56fbc34a2cf16591701d50add`。
环境：`kingeye-env.yaml` 的 `test-bkee5-vpn` 转发地址，凭据来自同环境 `test-bkee5` 组件。

## 问题与修改

KAC 告警 `linkd-a81bee0d-8830-542a-9f33-1330ae5a3496` 的普通单指标参数
带 `expression="a"`、空 `result_table_id` 和空 `query_configs[0].table`。
原 KAC 接口返回 `8101182`；原参数调用 KMC 返回 `8000500`，提示暂不支持表达式。

修复后的 Metric Processor 按租户和 `metric_ref_id` 读取目录主引用，生成物理表、字段
以及唯一标识；只有无函数、非衍生、单 query 的恒等别名表达式可以转换为普通查询。
目录缺失或主引用不完整会返回明确的 `dependency_invalid(metric_catalog)`。
实现及边界见 [策略依赖说明](../design/enrich-kingeye-strategy-dependencies.md)。

## 验证证据

- 回归测试先复现目录主引用丢失、单指标别名未简化，以及不完整目录被标记为成功的问题；
  修复前定向执行结果为 8 通过、16 失败（含父测试统计），修复后为 24 通过。
- `go test ./internal/enrich/... ./internal/lifecycle/kachook -count=1`：3002 通过。
- `make check`：格式、Go 普通测试、vet、race、golangci-lint、Console、Helm 和发布脚本通过。
  静态分析 0 issues；Console 438 通过、8 跳过；Chart 34 通过。
- 真实只读测试使用该告警关联的最新 Event
  `20261013073230.system.built_in_bk.9ebaadabf7dc5e6c` 的实际维度。
  原策略版本为 `1791857388118662`，当前 SplitRecord 119 已发布版本为
  `1791876921573378`。测试明确使用当前发布版本，仅修改内存中的 replay 输入，
  不冒充原历史配置重现，也不回写 Event。
- 当前配置引用目录指标 1216。修复后的生产代码自动生成
  `result_table_id=system.cpu_detail`、`metric_name=usage` 和空表达式。
  未在发送请求前手工补表名或清空表达式。
- 时间范围：`2026-10-13 09:11:00` 至 `2026-10-14 15:20:26`。
  真实 KMC 接口返回 HTTP 200、业务码 `20000`，183 个点，181 个非空值。
  复验期间有一次策略依赖读取返回 `dependency_invalid(kingeye_strategy)`；
  未修改代码再次执行后通过。该失败不算成功验证，也未据此认定具体的网络或数据库根因。

## 复验入口与限制

显式集成测试为 [TestMetricQueryLiveAlert](../../internal/enrich/processors/metric_query_live_test.go)。
先编译测试二进制，再执行 `LINKD_METRIC_QUERY_LIVE=1 <测试二进制> -test.run '^TestMetricQueryLiveAlert$' -test.v`。
通过 stdin 的有界 JSON 输入提供 MySQL 连接、真实 Event、KAC Alarm、当前发布版本、
指标 URL、起止时间和已刷新的 Cookie。凭据只在进程内传递，不保存输入文件或记录 Cookie。
KMC 直连请求依赖有效会话；可先通过 KAC 请求刷新会话，再将请求库生成的 Cookie 传入。
普通测试跳过该入口，不连接真实环境。

代码尚未部署，也没有发送 Kafka。上述只读重放不等于线上修复已部署。
随后经用户明确授权，对这一条告警进行了下面的定向 ES 修改与原路由验证。

## 用户授权的单条 ES 修改与 KAC 原入口验证

用户允许单独修改该告警的 ES 数据用于验证，并明确要求保留修改以便人工核验。
目标为租户 `system`、索引 `cw_kingeye-web_saas_3.0_alarm_event_v4-000001` 中
文档 `linkd-a81bee0d-8830-542a-9f33-1330ae5a3496`。
更新前再次从同环境指标目录核对指标 1216 的物理表与字段。

仅修改三个顶层字段：

| 字段 | 修改前 | 修改后 |
|---|---|---|
| `result_table_id` | 空字符串 | `system.cpu_detail` |
| `metric_unique_id` | `.usage` | `system.cpu_detail.usage` |
| `metric_query_params` | `expression="a"`、`query_configs[0].table=""` | `expression=""`、`query_configs[0].table="system.cpu_detail"` |

更新使用 `if_seq_no=2558007`、`if_primary_term=1` 防止覆盖并发变更，并等待 refresh。
ES 返回 `updated`，文档版本为 64、seq_no 为 2560781。
独立读回确认三个目标字段与更新值一致，其余所有字段与更新前一致。
告警状态、时间、生命周期、维度与关联身份均未修改；未修改 Linkd Alert 或 Event。

随后直接 GET 用户提供的原 KAC `/kac/alarm/active/<alarm_id>/metric/` 路由，
保持原时间范围。返回 HTTP 200、`result=true`、`message=success`；
CPU 单核使用率 `usage`、单位 `percent`，183 个点，181 个非空值。
修改已保留，没有回滚，可刷新告警详情页人工核验。

该结果证明这条告警的 KAC 原入口已能查询到指标数据；
尚未部署的 Linkd 代码修复仍需上线，才能保证后续告警自动生成正确参数。

## Rebase 后的交付验证

修复已 rebase 到 `feat/linkd-dev` 的 `c18a02b6845a8f41c15e2e385433f53ba811e556`。
调研索引冲突保留本记录和上游的 KAC 调试验证记录，指标代码保留上游的 `Scope.Facts()` 优化。

- `go test ./internal/enrich/... ./internal/lifecycle/kachook -count=1`：3071 通过。
- 最终 `make check` 全部通过：格式、Go 普通测试、vet、race、golangci-lint、Console、Helm、发布脚本。
  静态分析 0 issues；Console 466 通过、8 跳过；Chart 34 通过。
- 门禁暴露两个既有测试的时序假设：BlueKing 用户查询的三个同长超时竞争，
  以及指标目录 10 毫秒期限在 SQL 准入前到期。前者调整测试内的传输预算，
  后者增加确定的查询中取消覆盖，并允许合法的查询前期限到期；生产超时策略未修改。
- BlueKing 超时用例连续 5 次普通执行、3 次 race 执行通过；
  指标目录取消用例连续 10 次 race 执行通过。

完整门禁使用 Go 1.26.7、golangci-lint 2.13.2、Helm 3.17.3，以及 Python 3.14.6。
本轮只完成 rebase、验证和代码交付，没有执行部署或再次修改线上告警。
