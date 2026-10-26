# 通用告警检索与按条件关闭 test 验证

日期：2026-10-26。功能提交：`b7e5beddf767f2092a5f9eb508caead1e4f73a6f`，
已推送到 `feat/linkd-dev`。接口见[DSL 检索](../reference/contracts/alert-search.md)和
[按 filter 批量关闭](../reference/contracts/alert-close.md#按-filter-批量关闭)。

## 实际发布范围

test 原控制面运行 `0.1.0-test.20261026.shield.2`，对应源码
`3853110dae0047741bff8f6621639b14b0b9a745`。该部署尚未执行近期的来源限流和身份格式升级，
本次在这份已部署源码上叠加查询、批量关闭接口及其配置，单独编译、测试并发布。

- 镜像版本：`linkd:0.1.0-test.20261026.alert-search.1`。
- amd64 manifest：`sha256:2a5b23bebed1403b30500af0357f4f1485b1c38d934058b770575fa06951cbe7`。
- 编译工具链：Go 1.26.10，Linux/amd64 静态二进制，运行时沿用原 test 镜像。
- 实际二进制的 `git_commit`：`3853110dae0047741bff8f6621639b14b0b9a745-dirty`，
  表示原部署基线叠加本次改动；组合来源由本报告的基线与功能提交共同确定。
- 部署位置：`kingeye/linkd-control-plane`。预演确认仅修改该 Deployment 的镜像，随后按 digest 发布并等待 Ready。
- Cleaner、Lifecycle 继续运行原 `shield.2`，原业务来源及 Helm values 未修改。

发布前在 test 用现有配置运行临时校验 Job，实际 `version` 与配置校验均通过，随后删除 Job。
控制面重建完成后，实际运行版本为本次镜像版本，Pod 为 `Running / Ready 1/1`、重启次数为 0。
过程与最终脱敏快照见[部署结果](samples/2026-10-26-alert-search/deployment.json)。

## 数据与方法

使用专用 Standard 来源 `api-dsl-test-20261026`，租户为 `system`，测试标签为
`labels.test_run=dsl-close-20261026-01`。复用既有空闲测试 Topic，使用独立消费组；
未创建新 Topic，也未改变 Topic 删除开关。

通过真实 Kafka → Cleaner → Mailbox → Lifecycle 创建六条告警，再发送一条恢复消息。
没有直接写 Event、Alert 或 AlertLog。六条告警中，三条活动告警属于 target 组，
两条活动告警属于 keep 组，另一条 target 告警已恢复。
所有关闭请求都同时限定本次来源与测试标签，操作身份、操作者、原因和时间保持稳定。

Admin 操作目录尚未登记本次新增的两个接口，功能验证从临时 Pod 直接调用集群内 Linkd 管理 API，
JWT 取自现有受保护部署配置，未输出凭据。运行状态与资源收尾通过 Admin CLI 及现有部署入口核对。

## 验证结果

| 场景 | 实际结果 |
| --- | --- |
| 无 JWT、使用 Worker Token 调用检索或批量关闭 | 均返回 401 |
| 不存在的 DSL、`limit=501`、数组形式的 query | 均返回 400 |
| 原生字段与标签组合过滤 | 找到本次真实消息生成的六条告警 |
| 每页两条连续查询 | 返回六条，与本批告警总量一致 |
| 查询活动与已恢复数据 | 取得五条 active 和一条 recovered |
| 其他租户查询，及在 should 条件中指定 system 租户 | 均返回空列表，DSL 不能扩大租户范围 |
| target 组 dry-run | 恰好选中三条 active，closed/failed 均为 0，活动状态不变 |
| target 组分页关闭，limit=2 | 第一页关闭两条，第二页关闭一条，failed 均为 0 |
| 检查非目标与终态 | 两条 keep 告警仍为 active，已恢复告警保持 recovered |
| 对一条已关闭告警重试原单条命令 | 返回 200，already_closed=true |
| 无匹配目标的 dry-run | 返回空 items |
| 收尾关闭剩余测试活动告警 | 关闭两条 keep 告警，failed=0，最终测试 active=0 |

逐项原始结果见[验证输出](samples/2026-10-26-alert-search/verification.jsonl)。
准备阶段的测试程序曾把标准输入 `alert_id` 写成内部字段 `source_alert_id`，Cleaner 按协议拒绝，
未生成告警；修正为标准 `alert_id/fingerprint` 后完成上述正式验证，没有因此修改接口实现。

## 本地检查与验收边界

- 工作区的完整 `make check` 通过，覆盖格式、普通测试、vet、race、golangci-lint、Helm 与发布脚本；lint 为 0 issues。
- 独立发布源码的全部 Go 测试、vet，以及 API、配置和 Elasticsearch 包的 race 测试通过。
- 本机真实 Elasticsearch 7.17 验证租户过滤、should 语义、Active/History 副本折叠和连续分页通过。
- 本轮 test 验证正常关闭、候选选择、空匹配、鉴权和参数错误；没有对真实环境注入依赖断网、锁冲突或关闭超时。
  部分失败、取消和未执行项由已有单元测试覆盖，不据此宣称完成了真实故障注入验收。
- 没有进行生产吞吐、容量、MySQL DSL 或 KAC 后续处置验收。当前字段检索能力继续受现有 mapping 约束。

测试结束后，专用来源停用，活动告警清零，临时校验 Job 和验证 Pod 删除。
测试 Event/Alert 历史样本保留用于追溯。原有业务告警没有参与本轮关闭。
