# 活跃告警软限流验证

使用隔离来源和独立资源前缀，验证来源总量、分组配额与活动集合修复。参数见[使用指南](../guides/active-alert-by-strategy.md)。

## 配置与方法

测试使用真实 Kafka → Cleaner → Mailbox → Lifecycle → Elasticsearch/Redis 链路；没有直接创建 Event 或 Alert。
客户端在每步等待 Event 终态，再读取活动集合核对结果。后台修复与主流程共用该实例的 `storage.redis`。

```yaml
active_alert_limits:
  enabled: true
  count_cache_ttl_milliseconds: 200
  count_cache_max_groups: 128
  timeout_milliseconds: 100
  rules:
    - name: source-total
      group_by: []
      max_active: 3
    - name: strategy
      group_by: [labels.strategy_id]
      max_active: 2
```

后台修复采用正式默认分页预算 `page_size: 2000`、`max_bytes: 33554432`、`operation_timeout_seconds: 60`；
仅将隔离测试的修复间隔调为 3 秒。规则使用默认集合前缀，无通知配置。

## 结果

| 场景 | 实际结果 |
| --- | --- |
| A 分组新增 a1、a2，再新增 a3 | 前两条创建 Alert；a3 为 `suppressed / alert_limited`，规则 `strategy`，数量与阈值均为 2 |
| B 分组新增 b1，再新增 b2 | b1 创建后来源总量为 3；b2 命中 `source-total`，数量与阈值均为 3 |
| 满额时重复 a1 | Cleaner 放行；Lifecycle 保存 `suppressed / alert_suppressed / duplicate_trigger`，没有限流诊断，活跃数量不变 |
| 来源满额时关闭 a1，再触发 b2 | 关闭通过，额度释放；b2 可创建 |
| A 未满但来源已满时触发 a3 | 命中 `source-total`；之后关闭 b1，a3 可创建 |
| 恢复 a2 | `alert_recovered`，来源与 A 分组额度均释放 |
| 删除本测试的 A 分组 Set | 后台从真实 active 原始字段补回成员，其他分组保留 |
| 来源已满时将本测试总量 key 暂时改为错误类型，再触发 c1 | Cleaner 读失败放行，Alert 创建不回滚；恢复 Set 后后台补齐缺失成员，总量为 4，符合软限额允许超量的语义 |
| 检查本能力 key 类型与数量 | 共 4 个 key：来源总量及 A/B/C 三个分组，全部为 Set；成员仅为 fingerprint |
| 最后关闭剩余测试告警 | 全部终态，规则集合数量归零 |

最终运行的 22 条消息记录包含重跑前清理的 4 条关闭事件及正式场景的 18 条事件。限流诊断保存了规则、分组、数量和阈值，

收尾只读复核显示活动告警为 0，四组成员均为空；修复任务累计成功 84 轮、失败 0 轮，


## 复验中的发现

- 新 Kafka 消费组从队尾开始读取。首次准备阶段有一条消息在订阅建立前发送，没有被测试消费者接收；正式场景在消费者就绪后执行。
- 不能把所有 `suppressed` 都当作 Cleaner 限流。已有 fingerprint 的重复触发在通过 Cleaner 后仍可能被 Lifecycle 正常抑制，需查看 `outcome`、`reason_code` 与 `active_alert_limit`。
- 将共享 Redis 的扫描 COUNT 调为 2 时，修复轮次反复达到 60 秒操作期限。恢复正式默认分页 2000 后，缺失集合重建和缓存写失败补齐通过。
  SCAN MATCH 的成本取决于整个 DB 的 key 数量，不只取决于该来源的活动告警量；不承诺固定修复时限。
- 测试消息未配置正文，创建时记录 `content_empty` 的默认正文降级告警，不影响本次限流或生命周期结果。

## 验证边界与现场

本轮验证真实 Elasticsearch 和 Redis 的运行链路，MySQL 真实联调未执行；本地单测、竞态与静态检查记录见
[本地验证](2026-10-26-active-alert-soft-limit-validation.md)。
本轮按用户最新范围不修改 alarmd，不进行 KAC 注册及 alarmd 索引发现联调。
`built_in_bk` 的现有旧 Hook 与外部消费者迁移仍需在替换原业务实例前处理，不能以本次独立来源验证代替迁移验收。

正式场景结束后所有测试告警已关闭，规则集合归零。测试来源经正式导入命令停用，发布版本为 2；临时 Pod 与配置 Secret 已删除。
Kafka 服务端关闭了 Topic 删除功能，专用测试 Topic 因此保留，已无测试生产者和消费者；未调整集群删除开关。
独立 Elasticsearch 验证样本保留用于追溯。
