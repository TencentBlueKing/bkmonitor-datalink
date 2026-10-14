# 无数据描述正式接入验证

日期：2026-10-14（Asia/Shanghai）。代码基线：`feat/linkd-dev@cd492751`。
环境：个人配置中 `test-bkee5-vpn` 的 Kafka/MySQL 转发入口，对应 `test-bkee5`。
本文仅记录只读取样及本地重放；没有部署服务或写入线上 EventSource/ES。

## 已确认范围与源码依据

用户确认使用来源 `extra_data.no_data_periods` 生成
`当前指标(X)已经有N个周期无数据上报`，省略未知延时文字，保持 alarmd 输出不变。
当前权威输入规则见[标准事件契约](../../../reference/contracts/standard-event.md#无数据描述的检测事实)。

同仓库 `origin/master@64d509d4` 的 `pkg/alarmd/nodata/synthetic.go`：
`absentPeriods` 按检测时的 `LastSeen` 或 `FirstAbsent` 选定周期数；注释明确解释了
alarmd 的 round 时间与旧 bk-monitor 数据时间的差异，不能把它们当作相同 checkpoint。
`pkg/alarmd/linkdoutput/converter.go` 将检测周期数写入 `extra_data.no_data_periods`，
family 为 `no_data`，不输出观测值；`anomaly_begin_time` 是滚动窗口内最早异常点。

旧 bk-monitor 源码 `alarm_backends/core/control/mixins/nodata.py` 则用两个 checkpoint
分别计算 `no_data_period`、`anomaly_period`，并可能按两者之差生成延时文字。
本次不将滚动窗口异常数、配置 continuous 或窗口起点伪装成旧延时事实。

## 只读采集与修复前复现

Kafka 使用 `group_id=None`、`enable_auto_commit=False`，手动 assign/seek；
不生产消息、不提交 offset。仅在采集进程中将该分区的连接路由到经 ListOffsets 验证的
VPN leader 转发入口，没有修改系统 DNS、hosts 或来源订阅配置。

第一次快照保留范围为 `[2328176, 2558997)`，读取最近 100000 条
`alarmd_event` partition 0 消息：99187 条 `metric_algorithm`、813 条 `no_data`。
随后在快照 `[2539237, 2559237)` 的 20000 条中按策略/版本保留最近无数据样本。
完整 payload 仅在私有临时目录中处理，不保存来源 headers 或凭据到仓库。

MySQL 在同环境执行只读事务，核对 SplitRecord 发布版本和内容材料。
现存 ID 115、121 均为版本 `1791959436354060`；旧 ID 115 样本的其他版本无法匹配。
修复前通过正式 `DescriptionConfigurationClient` 和 Lifecycle 重放：
以下两条同版本事件均报 `nodata_evidence_missing`，没有 EventPlan 或新 Alert，来源 Event 保持不变。
旧版本样本走既有 `publication_missing` 来源原文降级，未算作无数据接入成功。

## 修复后原始输入重放

| Kafka offset | 策略 ID | 检测来源 | 原始周期数 | 新 Alert.content |
| --- | --- | --- | --- | --- |
| 2559132 | 115 | log | 634 | 当前指标(COUNT(关键字指标))已经有634个周期无数据上报 |
| 2559116 | 121 | metric | 69 | 当前指标(COUNT(主机ID))已经有69个周期无数据上报 |

两条均通过 `StandardCleaner/Mapper → Event → 正式 MySQL 配置 Reader → Resolver/Router →
Lifecycle → MemoryRepository Alert`，opening Event 预览结果一致，终态重投不重新生成内容，
Event.content 和来源 payload 保持不变，未补观测值或延时文字。
期望值按用户确认的来源周期模板及同版本监控项名称独立整理，**不是**旧 bk-monitor
checkpoint 全量描述的 oracle，也不是线上 bk-monitor 服务输出对照。

运行已有显式集成入口（连接配置仅由环境变量在进程内注入）：

```bash
go test -race ./internal/enrich/assembly \
  -run '^(TestContentLiveEventWithBoundConfiguration|TestContentReviewLiveSamples)$' -count=1 -v
```

需要 `LINKD_CONTENT_TEST_MYSQL_DSN`、`LINKD_CONTENT_TEST_EVENT_FILE`、
`LINKD_CONTENT_TEST_REVIEW_FILE`、`LINKD_CONTENT_TEST_TENANT`；本次两个测试通过，
Alert/Event 只写本地 MemoryRepository，不发布 EventSource、不写线上 ES、不发送 Hook。

## 回归材料与边界

[脱敏原始输入](../../../../internal/enrich/assembly/testdata/no_data_events.json)
保留真实周期数、窗口、时间、算法 family、策略 ID/版本及选中等级；替换租户和来源身份，
删除资源维度与摘要。普通装配测试使用冻结的必要配置字段，不访问线上 MySQL。

回归覆盖正常/最大周期、缺值、null、非法类型、零、负数、小数、溢出、标记/family 冲突、
无数据配置禁用或等级不符、普通事件 false 标记，以及原始事件到持久化/预览/重投的链路。

实际门禁结果：受影响 description/assembly 包的 `go test -race` 通过，
完整 `make check` 通过，静态分析为 0 issues，`git diff --check` 通过，
受影响文档的 45 个本地链接和锚点检查通过。
工具为 Go 1.26.7、golangci-lint 2.13.2；Helm 使用仓库 CI 固定的 3.17.3，
校验官方归档 SHA-256 后仅安装到任务临时目录。
本机 Node 26.7.0 的原生 Web Storage 与测试环境冲突，验证进程使用
`NODE_OPTIONS=--no-experimental-webstorage`；没有修改 Console 文件或业务实现。

尚未验证部署后的线上 ES 输出、来源模式发布或 KAC Hook；当前结果不等于服务已上线。
历史/预测/进程等其他缺事实算法继续沿既有边界处理。

## 无数据文案失败的原文兜底补充验证

用户进一步确认采用原始 Event.content 兜底无数据文案失败。创建和只读预览共用
`enrich.SourceContentFallbackReason` 白名单：既有 `publication_missing`，以及
`nodata_identity_invalid`、`nodata_configuration_invalid`、`nodata_evidence_missing`、
`nodata_evidence_invalid`。原始 content 必须非空白且通过大小与 Alert 校验；
兜底只初始化新 Alert.content，不修改事件的事实、判定或已创建内容。
创建日志记录具体失败码和 `source_fallback`，不记录原文。

本次再次通过同环境 VPN Kafka 只读抓取 partition 0 的 `[2602026, 2605026)`
共 3000 条消息，选择两个与正式 MySQL 发布材料匹配的无数据输入：

| offset | 策略 ID | 发布版本 |
| --- | --- | --- |
| 2604969 | 121 | 1791959436354060 |
| 2604748 | 115 | 1791959436354060 |

每个原始输入先验证正常文案生成，再构造四个**本地故障注入**副本：
family 与标记冲突、选中等级与无数据配置不符、删除周期数、周期数置 null。
修改只发生于集成测试输入副本，没有写入 Kafka、发布配置或数据库。
这八个副本用于验证错误兜底，不是八条天然存在的线上坏消息。

已有 `TestContentReviewLiveSamples` 显式集成入口使用正式配置 Reader，
全部 10 个案例通过 race 测试：均创建新 Alert；正常输入生成周期文案，故障副本
原样使用对应 Event.content；opening Event 预览一致，终态重投和来源 content 保留。
这些期望值按已确认的内容策略整理，不作为旧 bk-monitor 的全量 oracle。

普通回归测试另外验证四个错误码的具体 WARN 原因、原文空白/超限拒绝、Context
取消、重启后的 EventPlan 冻结，以及其他身份、发布材料、算法和依赖错误不兜底。
补充改动后的 Go 格式检查、`make test vet race lint`、包依赖边界测试和
`git diff --check` 均通过，静态分析 0 issues，46 个受影响文档链接/锚点通过。
上述验证没有部署服务；按用户要求未处理 Console。Git 交付状态以实际提交和远端分支为准。
