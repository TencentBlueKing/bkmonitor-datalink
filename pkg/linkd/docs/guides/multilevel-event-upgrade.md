# 多级别事件改造：Elasticsearch + Helm 测试环境重置升级

适用前提：允许中断，不保留旧测试事件、告警和待处理队列；使用 Elasticsearch 与 Helm。
需要保留历史或连续活动告警的环境不能直接套用此流程，当前代码没有旧事件数据转换工具。
本页是操作方案，不代表已经在目标集群执行或通过端到端验证。

## 1. 为什么需要重置

新版 Standard 输入必须携带 `evaluations`，增加 `values`，关联结果改为 `related_alert_ids`。
旧 payload 交给新版 Cleaner 会被判为无效输入，旧存量 Event 也不能直接按新模型读取。

`linkd storage migrate` 和 Helm migrate Job 只初始化资源，不转换旧 Event 或更新已有时间桶的字段
mapping。更新 ES 模板不等于更新已有索引；仅升级镜像或仅清空索引文档都不足以完成此次切换。

本方案选择新的 ES 业务索引前缀及 Redis 队列/缓存前缀，保留原 release、namespace、
`dispatch.deployment`、认证 Secret 和 EventSource Record/Release。来源集合按 deployment 派生身份，
不使用 ES 业务 index_prefix，切换业务前缀不会自动清空来源配置。

## 2. 准备交付物和配置

先构建并发布包含本次改造的 Linkd 与 Console 镜像；如使用 Eventgen，也准备其独立的新镜像。
仓库默认 tag 不表示本地变更已经发布。流程见[镜像发布](image-release.md)和[自建构建机发布](ci-release.md)。

保存本次 release 的原始 values，记录所有启用来源的 Kafka topic/consumer_group 及 Redis hook 配置。
这些文件可能包含凭据，应放在受限目录，不能提交到仓库或写入公共日志。

在原 values 上合并下列覆盖。`linkd-test-v2` 和 `linkd:test-v2:*` 是一次重置的示例名称，执行前
确认未被任何环境使用；重复重置时选择另一套未使用名称。

```yaml
image:
  tag: "<包含本次改造的新 Linkd 标签>"
  digest: ""
console:
  image:
    tag: "<包含本次改造的新 Console 标签>"
    digest: ""
eventgen:
  enabled: false
migrate:
  enabled: true
  watch: true
configuration:
  storage:
    elasticsearch:
      index_prefix: linkd-test-v2
  lifecycle:
    severity_upgrade_policy: close_and_create
    signal:
      stream: linkd:test-v2:signals
    mailbox:
      key_prefix: linkd:test-v2:mailbox
    lock:
      key_prefix: linkd:test-v2:lock
```

- 保留原有地址、认证、资源预算与正常副本数，保持 release、namespace 和 dispatch.deployment 不变。
- 所有 Cleaner/Lifecycle/Control Plane 必须使用一致的新前缀，Console 读取同一 ES 前缀；检查组或角色覆盖。
- 使用 existingSecret 时修改对应 key 内的完整 Linkd YAML，不要同时设置内联 configuration；已有 Secret
  必须在 migrate Hook 启动前更新。Secret 变化不产生配置 checksum，停机后要确保所有实例以新配置重建。
- 代码默认值见[配置指南](configuration.md#全局级别升级策略)，本例的新前缀不是代码默认值。
- 默认升级策略是 close_and_create；需要保留 Alert ID 原地升级时改为 update_current。

## 3. 停旧实例并跳过旧输入

先暂停全部上游生产者及模拟器。若有 GitOps/HPA 自动恢复副本，先暂停该 release 的自动调谐。
下面的 NS、RELEASE 使用实际环境值：

```bash
kubectl -n "$NS" scale deployment \
  -l "app.kubernetes.io/instance=$RELEASE" --replicas=0

# 若本 release 有仍在运行的有限周期 Eventgen Job，停止它们。
kubectl -n "$NS" delete job \
  -l "app.kubernetes.io/instance=$RELEASE,app.kubernetes.io/component=eventgen" \
  --ignore-not-found

kubectl -n "$NS" wait --for=delete pod \
  -l "app.kubernetes.io/instance=$RELEASE,app.kubernetes.io/component in (control-plane,cleaner,lifecycle,console,eventgen)" \
  --timeout=180s
```

确认所有旧 worker 均已退出、相关 Kafka consumer group 无活动成员，且上游保持停产。
对本环境专用的每组 consumer group + topic 先预览，再执行重置：

```bash
kafka-consumer-groups.sh --bootstrap-server "$BROKERS" \
  --group "$GROUP" --topic "$TOPIC" --reset-offsets --to-latest

kafka-consumer-groups.sh --bootstrap-server "$BROKERS" \
  --group "$GROUP" --topic "$TOPIC" --reset-offsets --to-latest --execute
```

这会跳过尚未消费的测试消息，不删除 topic；仅操作本环境使用的 group/topic，TLS/SASL 连接使用
现有管理客户端配置。Kafka 要求消费者处于非活动状态后执行重置，见[官方操作说明](https://kafka.apache.org/30/operations/basic-kafka-operations/)。

Redis 旧 Signal、Mailbox、锁和 Recent Alert 缓存由新前缀隔离，不要 FLUSHDB/FLUSHALL。
保持调度历史，不执行 scheduling init。停机不等于清空持久化协调状态，普通升级也不需要重建它。

## 4. 初始化并启动新版

在所有旧实例停止、offset 已处理、新配置已就绪之后执行：

```bash
helm upgrade "$RELEASE" ./linkd-<新 Chart 版本>.tgz \
  --namespace "$NS" \
  -f /secure/linkd-values.yaml \
  --wait --timeout 10m
```

这里的 values 是已经合并新前缀和镜像标签的完整配置，正常副本数应保持原值。同步 migrate Hook
成功初始化新业务资源后，Helm 更新常驻进程。初始化失败时先排查保留的 migrate Job，不启动旧版本
去消费新队列，也不把 watch=false 当作绕过初始化失败的办法。

恢复生产前确认：

1. Control Plane、Cleaner、Lifecycle 已注册，来源任务接管成功；Pod Ready 不能替代此检查。
2. 上游全部发送新版 evaluations；Eventgen 使用包含本次输入格式变更的镜像后再启用。
3. 如启用 active-alert-by-strategy，先通过来源 API 或 event-source import 将其 key_prefix 切换为
   本次测试专用前缀，并同步读取该索引的消费者；等待来源新 Release 生效。不能把旧策略集合当作新环境状态。
4. Console 指向新的业务索引，来源配置和认证仍可使用。

## 5. 验收和旧资源清理

使用同一租户、来源和 fingerprint，依次发送：warning 触发；critical 触发且 warning 恢复；critical 恢复。

- Event 包含正确的 values/evaluations；逐级结果与 related_alert_ids 可查询。
- 同一 fingerprint 最多一个 active Alert；close_and_create 产生旧、新两个 Alert，update_current 保持 ID。
- 高级别触发优先于旧级别恢复；最终 critical 进入 recovered。
- 没有输入丢弃、strict mapping 错误或旧 Event ID 找不到导致的队首重试。
- Kafka lag 和新 Mailbox 正常推进，Console 可关联到全部受影响 Alert。

验收后按明确资源清单清理旧业务索引和旧队列键；不要仅凭广泛通配符删除共享 ES/Redis 数据。
保留 EventSource records/releases、认证 Secret 和调度状态。旧策略 hook 集合只在确认无人再使用后清理。

此次跨协议升级不能在保持新队列、新数据和新生产者的同时直接 helm rollback 到旧镜像。
若测试失败，先停产停 worker，再以相同重置流程恢复旧版本可读的隔离资源和旧输入协议。
