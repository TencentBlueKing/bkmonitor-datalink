# 2026-09-07 核心任务调度验证记录

结论：最终第 5 轮 S01–S13 全部通过，测试耗时 **347.54 秒**。已经补齐中心和 worker 的调度指标，
并在真实强杀与控制 RPC 故障场景抓取到超时接管、自停指标。此前三次场景失败和一次指标漏计发现均保留记录。

本记录对应 [核心验证流程](../guides/task-scheduling-validation.md) 和 [指标契约](../design/task-scheduling-observability.md)，
不是生产高可用或无重复执行的无条件保证。

## 1. 代码、环境与拓扑

- 基线 HEAD：`11641f0e`，包含此前已暂存的 EventSource 合并改动，以及本次未提交的指标、测试和文档改动。该轮验证执行时尚未创建 commit 或推送。
- 验证时 Go 输入摘要：`754ce14b685899c35d57fa9ab4151052bf83a64e3cfb8e28d1fd6bd8d88f80e5`。算法与所有步骤原始摘要保存在 [结构化证据](evidence/2026-09-07-task-scheduling.json)。
- 最终执行起点：`2026-09-07T16:34:27.993443+08:00`；系统 macOS/arm64，Go 1.26.7，golangci-lint 2.13.2。
- 真实数据服务：Elasticsearch 7.17.7（127.0.0.1:9200）、Redis 7.2.16（127.0.0.1:16379）、Kafka（127.0.0.1:9092）。Kafka broker 版本没有独立核实，不用客户端库版本代替 broker 版本。
- 补充 Redis 协议集成测试使用本轮单独启动的 Redis 8.0.0（127.0.0.1:16389），测试结束关闭。
- 六个真实 Linkd 进程：一个 all-in-one、四个 Cleaner、一个 Lifecycle；worker 标签分 pool=a/b，其中一个拒绝空 selector。每个进程独立 metrics 端口。
- 控制连接故障使用仅影响指定 worker 的代理返回 HTTP 503；Kafka、Redis 与 worker 指标端口仍正常。

## 2. 最终逐项结果

| 场景 | 内容 | 结果 | 步骤耗时/秒 |
| --- | --- | --- | --- |
| S01 | 多进程与 Kafka 分片上限 | 通过 | 13.535 |
| S02 | 标签、数字、0 与角色隔离 | 通过 | 66.180 |
| S03 | 执行配置发布 | 通过 | 14.865 |
| S04 | Kafka 分片增加 | 通过 | 9.177 |
| S05 | Kafka 探测失败与恢复 | 通过 | 29.991 |
| S06 | SIGTERM 后重启 | 通过 | 9.400 |
| S07 | 固定副本 SIGKILL 后接管 | 通过 | 77.198 |
| S08 | 短暂控制 RPC 失败 | 通过 | 5.954 |
| S09 | 持续失联自停与重连 | 通过 | 35.946 |
| S10 | 资源预算缺额 | 通过 | 30.752 |
| S11 | 连续发布 | 通过 | 14.902 |
| S12 | 恢复后的业务链路 | 通过 | 3.393 |
| S13 | 总开关、删除与 Gauge 归零 | 通过 | 33.019 |

耗时包含配置发布、对账、握手、指标抓取等完整步骤，不是单个 RPC 延迟或纯故障恢复时长。

核心实测：

- 默认空 selector 时四个 Cleaner 候选受三个 partition 限制，只有三个运行且各自拿到 partition；显式选择器 worker 未被空调度选中。
- pool=a 选择时 all-in-one 同时运行同源 Cleaner 与 Lifecycle；pool=b 选择可调度要求显式匹配的 worker。
- partition 从 3 增为 5 后，Cleaner 从 3 补至 4，已有任务 epoch 保持。
- ssl 探测失败时旧 Cleaner 保留 owner/epoch；恢复原执行配置时允许继续使用原 Release，而不是强制等于最新编辑版本。
- 固定两个副本执行 SIGKILL 后，替代任务没有早于旧授权 Expires + 10s 获得分配；随后数量恢复。
- 短暂 RPC 故障恢复后 epoch 保持；持续故障时进程仍存活、本地 running 已归零，随后重连、确认停止并启动新 epoch。
- WorkerCount=129 超过进程预算128时 Cleaner 为0、目标2、缺额2，API 与指标一致；恢复预算后继续运行。
- 固定业务集实际落库 Event=10、Alert=5、AlertLog=19；输入12、输出10；Stream/Pending 排空。追加查询确认全部 Event 和 Alert 的 event_source_version 均为18。
- enabled=false 期间检查 Lifecycle 没有先于仍活动的 Cleaner 进入停止；删除后两角色为0，旧 Release 1仍可读取。

## 3. 指标证据

最终证据中的具体样本摘录：

```text
controller transitions: reason=authorization_expired, role=cleaner, from=stopping, phase=stopped → 1
worker transitions: reason=disconnected, role=cleaner, from=running, phase=stopping → 1
controller tasks: role=cleaner, phase=running → 0（删除来源后）
controller tasks: role=lifecycle, phase=running → 0（删除来源后）
```

完整标签与原始样本在 [结构化证据](evidence/2026-09-07-task-scheduling.json) 的 `final_metric_samples`。
原始 `states.jsonl`、`steps.jsonl`、各步骤 `.prom` 和进程日志保留在执行者本地。
公开版本不包含本机绝对路径、临时目录标识或本地日志链接；可复核的步骤结果与指标样本保存在仓库内 JSON 和本文中。

## 4. 前置失败、复核与修复

1. 第 1 轮 S05 失败：测试要求恢复原连接配置后所有任务都等于最新编辑版本。快照显示 Cleaner 正确保留相同执行摘要的旧 Release。修正为检查 epoch 保留、探测恢复与数量收敛。
2. 第 2 轮 S06 失败：测试要求重启回到原 slot。实际旧任务已停止，新会话使用更低空闲 slot 和更大 epoch。改为断言停止确认、新会话与新执行代次。
3. 第 3 轮 S07 失败：replicas=all 时新会话扩大候选集合，创建的是独立新 slot，并非提前接管旧 slot。改用固定两个副本并确认中心完成对账后测试超时接管；此规则已写回流程。
4. 第 4 轮13项场景通过，但复核 Prometheus 证据发现同一次 Redis 提交内“旧代次超时→slot 复用”只记录 assigned，漏计 authorization_expired。修复为 CAS 成功后同时记录旧代次终态与新代次创建；新增真实 Redis 回归测试保证恰好一次，并在 S07 增加实际指标断言。
5. 第 5 轮完整重跑，通过全部场景及新增超时指标断言。

这些修正没有缩短生产心跳、授权、排空或安全余量，也没有修改副本数量规则来迁就测试。
前三轮是验证断言过强/条件混淆；第4轮发现的是本次新增指标代码的真实缺陷，已修复。

## 5. 质量门禁和补充测试

| 命令或检查 | 演练结束时的结果 |
| --- | --- |
| make fmt | 通过 |
| make check 中的格式、普通 Go 测试、go vet、race | 通过 |
| make check 的 golangci-lint | 未通过：上游已有7项 Enrich lint 问题；本次新增代码没有剩余 lint 报告 |
| make devtools-check | 通过，23个测试文件/110项测试，类型检查与构建通过 |
| LINKD_DEVTOOLS_E2E_PORT=5187 make devtools-e2e | 6项通过 |
| 显式 Redis 协议集成与 race | 5项通过：中心CAS/迟到报告、Agent停止握手、Agent失联自停、同提交超时重分配指标、定向 Pending 接管 |
| 修改文档的本地链接、git diff --check | 通过 |

演练结束时的完整门禁日志保留在本地。当时阻断项为：
`onemodel_client.go:78` 的 Close 错误未检查；`bk_strategy_client.go:81`、`cw_strategy_client.go:123`、
`metric_client.go:97`、`onemodel_client.go:150` 的错误包装；`enrich/input.go:70`、`processors/result.go:20` 的未使用函数。
这些问题在演练时来自基线代码，因此当时不能将 make check 整体写为通过。推送前的修复与复查结果另见下文。

Redis 与浏览器测试原始日志保留在本地；上述通过数量是当时实际执行结果。

## 6. 保留资源与未覆盖范围

各轮外部资源均使用独立命名空间并保留；只退出本轮子进程和代理，不删除既有数据。
业务索引前缀和 Kafka topic 前缀均为 `linkd-e2e-<deployment>`；来源 Record/Release ES 索引使用
`linkd_event_source_<sha256(deployment)>_records/releases`；调度 Redis 键包含 `linkd:dispatch:{<deployment>}`。

公开证据以 `run-01` 至 `run-05` 标识轮次，不公开实际 deployment、进程号或本机路径；
这些别名只用于本记录，不是可用于访问测试服务的资源名称。

本轮没有做以下验证：

- MySQL 后端的新版多进程演练（普通 MySQL 仓储测试通过不等于真实 MySQL 外部集成通过）。
- 故障期间持续高吞吐或大量在途业务消息。故障阶段任务实际连接 Kafka/Redis，但业务集是在恢复后发送；不据此证明强杀时在途消息的完整性。
- 控制面进程崩溃、Redis 服务断开/历史丢失的真实多进程恢复演练；历史缺失与迟到续租由协议测试补充。
- 真实 TCP 丢包、长时延、进程/VM冻结、无限外部 I/O、长期滚动发布压力、真实 Enrich 外部依赖。
- Kafka 无 leader、ISR缩减、topic身份变化/异常减少的真实 broker 故障注入。缓存/规划单元测试不冒充真实 broker 演练。

250ms 对账采样未发现同 worker/来源/角色重复占位；它不能捕捉所有采样间瞬态。安全判断仍依赖 Redis
原子状态转移、停止报告与确认、完整授权窗口及有界自停假设，不能表述为无限故障模型下的强 fencing。

## 7. 推送前整理与复查

公开版本移除了本机绝对路径、本地日志链接、临时目录标识以及包含进程号的 deployment；
轮次、实际耗时、断言结果和指标样本保持不变，完整未脱敏原始记录仍保留在本地。

推送前最小化修复了第5节记录的7项 lint 问题：保留底层错误链、显式关闭 HTTP 响应，
删除两个没有消费者的函数，并按仓库提交钩子补齐新 Go 文件版权头和导入分组。之后重新执行 `make fmt` 和完整 `make check`，全部通过，
其中 golangci-lint 为0项问题，DevTools 为23个测试文件、110项测试。
上述13项多进程演练仍对应第1节的原始代码输入摘要，未将这次 lint 修复描述为重新完成整轮外部演练。
