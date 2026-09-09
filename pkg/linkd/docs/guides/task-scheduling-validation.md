# 核心任务调度验证流程

状态：可重复执行的开发环境演练。验证实现和证据以执行记录为准；流程本身不表示场景已经通过。

## 1. 环境与证据

使用真实 Kafka、Redis、Elasticsearch，以及独立 Linkd 子进程。复用 all-in-one E2E 的连接环境变量，
每轮生成独立 deployment、来源存储索引前缀、Kafka topic/group、Stream/mailbox/lock；不读取或删除已有业务数据。
演练结束只退出本轮子进程并关闭本轮代理，不自动删除 Kafka、Redis 或 ES 资源。

拓扑：一个 all-in-one（pool=a），四个 Cleaner（c1/a、c2/b、c3/b、c4/a），一个 Lifecycle（pool=a）。
c3 配置 `require_explicit_selector=true`。每个进程有独立 Prometheus 端口；worker 控制协议经过各自的
HTTP 代理，故障开关只返回该进程的控制 RPC 503，不影响其 Redis/Kafka/指标端口。

```bash
LINKD_E2E=1 go test -count=1 -timeout=15m -run '^TestSchedulingDrillE2E$' -v ./tests/e2e/allinone
```

可设置 `LINKD_DISPATCH_DRILL_DIR` 指定证据父目录；每轮新建子目录，默认保留在系统临时目录。
连接变量见 [all-in-one E2E](../../tests/e2e/allinone/README.md)。启动前确认目标是专用测试基础设施。
不得缩短生产授权、心跳、排空和冷却常量来加速演练。

证据包括：每次 API 采样的时间、task owner/slot/epoch/version/phase、运行摘要、各进程 metrics、
步骤起止时间与断言结果、进程日志。配置文件含连接凭据，仅放权限 0600 的测试临时目录，不复制到证据目录。

## 2. 贯穿所有步骤的不变量

- 对账等待期间每 250ms 采样中心状态。同一来源/角色在一个 worker 上最多一个非 stopped assignment。
- 同一个 slot 的新 epoch 必须大于旧 epoch。正常切换依据中心已提交的 stopped 确认；异常接管不早于旧授权 Expires + 10s。
- 同一进程可以同时承担 Cleaner 和 Lifecycle。统计 preparing/starting/stopping 占位，不能只检查 running。
- 收敛检查同时要求期望 running 数和不存在中间阶段；Kafka 检查实际 partition 分配。
- Gauge 在任务停止后归零；counter 不因重复 heartbeat/reconcile 重复计算同一次状态转换。
- API 快照和 250ms 采样不能证明任意两个采样间没有重叠执行；并发互斥依赖 Redis CAS、停止握手与单元/竞态测试共同验证。
- SIGKILL 不能执行自停；“旧 worker 自停”必须用进程仍存活、控制连接中断的场景单独验证。

## 3. 顺序执行的核心场景

| 编号 | 操作 | 必须观察到的结果 |
| --- | --- | --- |
| S01 | 启动六个进程，显式 API 创建来源；空 selector、replicas=all，topic=3 partitions | 4 个默认 Cleaner 候选，3 个运行且实际持有 partition；c3 无任务；Lifecycle=2；all-in-one 两角色共存 |
| S02 | Cleaner selector 改为 pool=b，再改回空；数量切换 1、0、all；Lifecycle 单独改为 0 再恢复 | pool=b 匹配 c2/c3；数字数量正确；角色 0 不停另一角色；旧任务停止后才补新任务 |
| S03 | 修改执行配置 default_severity，等待收敛 | 活动两角色绑定新 Release，epoch 增长；保留历史 Release |
| S04 | topic partition 从 3 增至 5 | 下一轮元数据刷新后 Cleaner 从 3 补至 4，已有 assignment epoch 保持；无效扩容不算故障 |
| S05 | Kafka 安全协议暂改 ssl，测试 broker 仍为 plaintext | 探测错误指标增长；旧 Cleaner owner/epoch 保留、没有新增 Cleaner；恢复 plaintext 后重新对账，执行摘要相同的旧 Cleaner 保留其 Release/epoch |
| S06 | pool=b，正常 SIGTERM 一个正在运行的 Cleaner，随后启动同标签新进程 | 旧进程退出、停止确认；新会话执行新 epoch，可复用更低的空闲 slot；无需等到完整授权过期 |
| S07 | 固定 Cleaner replicas=2，确认中心完成对账后 SIGKILL 同一 Cleaner，立即重启该进程 | 中心保留旧占位；不得在旧授权 Expires+10s 前复用其 slot；随后恢复两个副本，中心 authorization_expired 恰好增加1 |
| S08 | 对运行中的 Cleaner 短暂返回控制 RPC 503，观察到首次失败后立即恢复（小于10s） | 暂停接收、失败指标增长；恢复后原 epoch 保持，无重复重启 |
| S09 | 再阻断同一 worker 控制连接，持续到本地 running=0，再恢复连接 | worker 自停原因 disconnected；进程仍存活；恢复后报告 stopped，再由中心发新 epoch，不能复活旧代次 |
| S10 | 发布超过 worker 预算的 Cleaner 局部 runtime | 中心报告 capacity pending，target 与 running 缺额可观测，不能突破进程预算；恢复配置后收敛 |
| S11 | 连续发布三个执行版本，不等待中间版本收敛 | 最终只运行最后版本（最终执行摘要须不同于旧版本及中间版本）；旧代次遵守停止确认，无同进程重复 Flow |
| S12 | 向经历切换后的来源发送固定业务数据集 | Event/Alert/AlertLog、输出数量与预期一致，Stream/Pending 排空；任务版本字段为正 |
| S13 | enabled=false，再恢复；最后删除来源 | 先停 Cleaner 再停 Lifecycle；恢复可运行；删除后两角色0，任务 Gauge 归零，旧 Release 可查询 |

`replicas=all` 在滚动发布时会将新旧会话都计入候选数量，可能创建独立的新 slot；这属于扩容，
不等于旧 slot 接管。S07 使用固定数量 2，排除这一混淆。

默认单步骤超时 90s，SIGKILL 接管与断线恢复不缩短 60s 授权、10s 安全余量、冷却预算。
超过阈值立即标记失败，保留证据，不把重跑成功覆盖为首次通过。

## 4. 指标检查

正式指标契约见 [调度可观测性](../design/task-scheduling-observability.md)。每次步骤结束抓取全部存活进程
`/metrics`；断线过程中额外读取 worker 的 heartbeat.failures、admission.paused、tasks 和 transitions。
验证 controller 的 transitions/handoff、worker 的 shutdown/disconnected、Kafka probe failed 都实际可抓取。

配置开关、role、phase、operation、reason 都是有限枚举。worker ID、source ID、topic、slot、epoch、
Release、错误文本和凭据不得进入调度指标标签。排查具体实例时用 Prometheus target 与运行状态 API 关联。

## 5. 补充验证与边界

执行 `make fmt`、`make check`、Console E2E，以及显式 Redis 协议集成测试。
首次元数据失败、topic 身份变化/异常减少、迟到续租、协调状态丢失、定向 Pending 接管由已有单元或
Redis 集成测试补充，不伪装成上述真实多进程演练已覆盖。冻结 VM、无限期外部 I/O、不停机自动 HA、
真实 Enrich 外部依赖、持续高吞吐压力不在本轮验收范围。

执行结果写入 `docs/reviews/`，记录代码基线、脏树状态、环境版本、每步耗时、证据路径、失败/重跑、
未执行事项。不得只写“所有测试通过”。
