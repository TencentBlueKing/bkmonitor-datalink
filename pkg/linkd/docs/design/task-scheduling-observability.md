# 任务调度可观测性

指标由现有 OTel MeterProvider 导出到各进程 Prometheus `/metrics`。未启用 telemetry 时使用 no-op。
公共调度协议通过 `taskdispatch/observation` 窄端口注入观察器，不依赖 exporter、网络或 telemetry 配置。
中心只在 Redis CAS 成功之后记录任务转换；重试和重复心跳不重复计同一转换。
同一次提交内超时终结旧代次并复用 slot 时，分别记录 authorization_expired 和 assigned，避免最终快照覆盖终态观测。

## 指标清单

以下为 OTel 名称，Prometheus 把 `.` 替换成 `_`；counter 追加 `_total`，秒 Gauge 追加 `_seconds`，秒直方图追加 `_seconds_bucket/sum/count`。

| 名称（前缀 `linkd.dispatch.`） | 类型 | 语义与属性 |
| --- | --- | --- |
| operations | Counter | operation、outcome；reconcile、heartbeat_server/client、kafka_probe、task_run、forced_exit、recovery_required |
| operation.duration | Histogram/s | 上述操作耗时；task_run 是执行开始到返回；没有执行耗时的事件为0 |
| transitions | Counter | side=controller/worker、role、from、phase、reason；中心提交转换与 worker 本地转换分开统计 |
| handoff.duration | Histogram/s | side、role；中心停止请求到停止确认/超时终结，worker 停止请求到实际返回；不是完整端到端迁移耗时 |
| controller.tasks | Gauge | role、phase；preparing/prepared/starting/running/stopping/stopped；stopped 为保留的 slot 历史 |
| controller.workers | Gauge | state=healthy/stale/draining/cooldown；按会话计数，不等于机器数量 |
| controller.replicas | Gauge | role、kind=matching/target/running/shortage；各来源角色集合求和；shortage 按每个集合先计算再累加 |
| kafka.sources | Gauge | state=ready/waiting/error；协调快照中缓存的来源元数据数量，含尚未清理的已删除来源缓存 |
| kafka.metadata.age | Gauge/s | 所有已有成功结果的最大年龄；尚无成功结果为-1；失败不清空成功时间 |
| worker.tasks | Gauge | role、phase；本进程实际本地任务状态，由独立 watchdog 每100ms采样 |
| worker.partitions | Gauge | 活动 Cleaner 最近报告的实际 partition 数之和，允许 rebalance 期间暂时为0 |
| worker.admission.paused | Gauge | 暂停接收新消息的活动任务数量，准备阶段也计入 |
| worker.heartbeat.failures | Gauge | 连续失败次数，成功后清零 |
| worker.heartbeat.age | Gauge/s | 距最近成功心跳的时间；首次成功前从 agent 启动计时 |
| worker.authorization.remaining | Gauge/s | 活动任务本地安全截止时间的最小剩余时间，已经扣除安全余量；无任务为0 |

所有快照都写出有限角色/阶段的0值，避免缩容后留下旧 Gauge。具体来源的 Kafka partition 上限、配置
数量、owner、slot、版本、等待原因仍通过 `/api/v1/runtime` 查询；聚合 matching/target 不能当作单来源配置。
心跳/任务身份、来源/租户 ID、topic、epoch、版本、任意错误文本均不作为指标属性。

## 使用与限制

- 连续心跳失败、heartbeat.age 增长、admission.paused>0：worker 已停止接新消息，检查控制连接。
- transitions 的 disconnected / authorization_deadline：worker 已请求自停；结合本地 tasks=stopped 和中心停止确认判断完成。
- controller transitions 的 authorization_expired：没有正常停止确认，已按完整授权窗口超时终结。
- controller.replicas shortage 持续非0：查看运行状态 API 的资源、标签、元数据和交接原因；分片正常限制不应单独报启动故障。
- forced_exit 是退出前的内存指标，进程立即退出可能来不及被 Prometheus 抓取，必须结合退出码/容器重启事件。
- 中心失败退出后 Gauges 不再刷新，应监控 scrape 的 `up`，不能继续信任旧样本。
- 指标只观测协议，不增加强 fencing 保证；边界见 [调度协议](task-scheduling-protocol.md)。

演练步骤见 [核心调度验证流程](../guides/task-scheduling-validation.md)。
