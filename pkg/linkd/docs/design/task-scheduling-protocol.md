# 中心化任务调度协议

调度核心供 Cleaner、Lifecycle 共用。单活动中心装配在 control-plane，Redis 保存协调状态，来源配置仍在 ES/MySQL。
首版不提供调度中心自动 HA，不依赖普通 Redis 自动主从提升来证明协调历史不回退。

## 身份与互斥

任务集合由部署作用域、EventSource、角色标识；集合内以 slot 标识副本，assignment epoch 标识执行代次。
同一 slot 同时一个 owner；同一 worker 同源同角色最多占一个位置，preparing、running、stopping 都计入占位。
worker ID 是本次进程会话。all-in-one 显式聚合两种角色为一个 agent，共享标签与进程资源预算。
配置版本不等于执行代次，调度标签/数量变化不必重启仍符合要求的 Flow。

规划保留健康分配，然后按稳定顺序补足目标。Cleaner 受 Kafka 分片数限制，Lifecycle 仅受选择器、数量及资源限制。
同源同角色副本共享 MQ consumer group，由 Kafka/Redis 分配消息；Lifecycle 继续通过 fingerprint lease 限制业务并发。
来源出现重复 Kafka subscription 时拒绝重复分配，避免不同来源瓜分同一消费责任。

## 协议与状态

worker 每 3 秒发送独立心跳，带会话、递增请求序号、任务代次、阶段与实际 partition；RPC 超时 2 秒。
中心原子检查 Leader 身份与完整旧协调快照，再保存转移和新授权。迟到/重复序号不会延长租约。

```text
preparing → worker 加载固定 Release → prepared 报告
          → 中心记录 starting 授权 → worker 启动 → running 报告
配置变化 → stopping → worker 关闭领取、排空、退出并关闭任务资源
          → stopped 报告 → 中心确认并释放占位 → 下一代 assignment
```

worker 只在原会话、原代次、有效授权下启动；stopping 不可逆。控制响应丢失后重新对账，不自行复活旧任务。
普通重启在停止确认后立即继续，不等待完整故障期限。纯准备/无在途任务会快速退出；消费任务只排空已领取消息，不要求上游 backlog 清空。
Kafka ownership 回调在空 topic 时也即时上报 partition，并在 Runtime 已退出后解除回调等待，避免 Close 阻塞。

## 失联、防抖与恢复

- 单次心跳失败暂停新领取，保持已有工作的有界处理。恢复有效授权后继续，不立即重启。
- 连续失败至少 3 次且超过 10 秒容错窗口，进入自停。
- 服务端授权 TTL=60 秒。worker 依据请求发起单调时间和响应剩余 TTL，提前 10 秒截止，并预留 20 秒排空及 5 秒强制退出。
- 独立 watchdog 不受业务队列阻塞；无法停止时由进程入口结束整个 worker。
- 中心按最后已提交的授权到期时间加 10 秒余量强切，拒绝旧代次报告/续租。未确认的旧 assignment 始终占位，直到停止或合法强切。
- 异常恢复有 15 秒稳定窗口，强切节点有 60 秒冷却，单任务失败按有上限的退避重试，避免持续重建。

这是已确认的有界自停模型：覆盖正常取消、控制网络故障、崩溃和容器发布。
不承诺冻结整个 VM/OS 或无限延迟外部请求下的绝对互斥；控制 epoch 不等于资源端原子 fencing。
现有业务幂等、CAS、Kafka group generation 与 fingerprint lease 继续生效，但不将它们宣称为完整跨系统事务。

SIGTERM 触发同一 agent 的幂等关闭，两角色在 all-in-one 中均等待任务停止后释放共享资源。
容器 terminationGracePeriodSeconds 至少覆盖整个进程的有界停止、报告和资源关闭；建议从 60 秒起根据任务预算校验。
preStop 耗时也计入容器终止宽限期；控制面短暂不可用不应由 liveness 立即重启全部 worker。

协调 state 不随心跳 TTL 删除。Redis 状态缺失时停止调度，不能将历史丢失视作“没有任务”。
只有部署方确认旧中心及所有 worker 已停止，才能执行：

```bash
linkd scheduling init --confirm-stopped --config <config.yaml>
```

命令仅创建缺失状态，不覆盖已有协调记录、不删除来源或业务数据。首次没有来源的全新环境可正常初始化。

## 实现与验证边界

Redis 协调快照有 8 MiB 上限；worker 注册最多 256，单进程任务数默认 16，最多 256。
静态 worker 默认预算为 128 并发、256 MiB inflight，可显式调整；匹配但容量不足的副本保持 Pending。
配置 API 和 worker API 分别鉴权，回传状态不含 Kafka 凭据。

测试应覆盖：重复/乱序心跳、停止交接、授权到期、标签和数量、分片上限及探测失败、Pending 定向 Claim、进程停止、资源共享和 Redis 状态丢失。
真实外部 E2E 必须显式开启，不用普通单测或“Redis 只有一个 owner”替代端到端运行证据。
