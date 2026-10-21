# 策略 147 的业务作用域与衍生指标真实验证

验证时间：2026-10-21。环境：test-bkee5-vpn，租户：system。使用本地未提交代码，通过 Kingeye Admin 只读获取仍活跃的 13 条告警及各自 trigger Event，覆盖策略 147 的执行修订 v1、v2。

## 方法与边界

使用已发布 `built_in_bk` Enrich 配置，完整运行 strategy、resource、k8s、apm、log、display、metric、source、fields 九个 Processor，并独立执行 `bkmonitor_description` 内容生成。MySQL 使用数据库只读事务；OneModel 使用生产 Elasticsearch Reader。普通测试不连接真实环境，集成测试需要显式注入环境配置和有界样本目录。没有装配可选 Redis 展示/动态分组 Reader，本轮不验证这些缓存。

不修改 EventSource、策略、Event 或 Alert，不投递 Kafka、不发送 Hook。对输入 Event 和 Alert 做深比较，确保内存重放没有改写原对象。未部署本地代码，因此不代表线上告警已改变。

## 数据与根因

- 事件来源空间为 `bk_biz_id=-255`，历史快照的 `space_uid=bkci__blueking`；这是合法非 BKCC 空间。K8s 资源所属 CMDB 业务与策略来源空间不是相同身份。
- 声明查询引用目录 219，其类型为 derived、名称为 `container_memory_resource_limits_ratio`、单位为 `percent`，没有物理表或字段。
- 历史声明缺少 `field_tag`；同一执行快照中的成员通过 `metric_snapshot.parent_metric_id` 与 `derived.metric_id` 一致指向父指标 219。只能据此确认父类型，不得用成员 145/180 替换声明引用。
- 原 Processor 公共正整数业务校验提前阻断查询；原物理位置解析把 derived 当作 native；描述生成也因缺少衍生类型而错误要求外层聚合方法。

## 结果

| 验证项 | 结果 |
| --- | --- |
| Strategy、Display、Metric、Source、Fields | 13/13 succeeded |
| APM、Log | 13/13 skipped，场景不适用 |
| 告警名称 | 13/13 为“限制内存使用率发生了告警” |
| 策略关联 | 13/13 输出模板 172、策略配置 ID 与策略名称 |
| 指标信息 | 13/13 输出衍生定义名、percent 单位、derived_metric 标记，不伪造物理表/指标唯一 ID |
| 描述生成 | 13/13 成功 |
| OneModel 容器实例存在 | 5/13，Resource/K8s succeeded，整个 Enrich succeeded |
| OneModel 容器实例未命中 | 8/13，独立调用同一生产 Reader 确认未命中；Resource/K8s partial + dependency_invalid(onemodel)，其他步骤正常 |
| 原始 Event/Alert | 13/13 深比较保持不变 |

实例未命中只说明查询时 OneModel 没有对应文档，不据此断定其被谁删除或断定采集正常。不能把这些 partial 写成全部成功，也不能用猜测的实例绑定消除诊断。

## 复现入口

`internal/enrich/assembly/strategy_scope_live_test.go` 中的 `TestStrategySpaceLiveEnrich` 使用 `LINKD_STRATEGY_SPACE_LIVE_CONFIG`（进程内连接 JSON）与 `LINKD_STRATEGY_SPACE_LIVE_FOLDER`（私有目录中的 samples.json、enrich.json）显式运行。连接凭据不写入仓库或报告。

验证命令：`go test ./internal/enrich/assembly -run '^TestStrategySpaceLiveEnrich$' -count=1 -v`。每条样本的状态、诊断和指标结果保存到私有目录 report.json，不含原始事件载荷或连接凭据。

## 本地质量门禁

- 缺陷回归在修复前稳定复现业务校验与 derived 物理位置错误，修复后通过。
- `go test ./internal/enrich/... ./internal/lifecycle/kachook -count=1` 通过。
- 最终 `make check` 返回 0：格式检查、普通测试、go vet、竞态测试、静态检查、控制台、Helm 与发布脚本检查通过。
- 控制台全量首次有一条未改动的侧栏导航用例失败；该用例单独复跑通过，随后完整门禁复跑通过（控制台 451 passed、8 skipped）。未修改控制台文件。
- `git diff --check` 与受影响文档本地链接路径检查通过。
