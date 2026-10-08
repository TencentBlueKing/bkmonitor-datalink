# 外部契约

| 契约                               | 内容                                                     |
| ---------------------------------- | -------------------------------------------------------- |
| [Linkd 标准事件](standard-event.md) | `RawEventMessage` 信封、`standard` payload 与 KAC V2 发布前校验 |
| [kac-alarm-output.md](kac-alarm-output.md) | KAC alarm_collect_topic 扁平 Alarm JSON、身份和固定映射 |
| [kac-alert-projection-v1.md](kac-alert-projection-v1.md) | 全局插件直接维护 alarm_event、独立版本元数据、字段所有权、持久重试和搜索可见性 |
| [kac-action-delivery-v2.md](kac-action-delivery-v2.md) | 独立动作身份、即时原子入队、投影门槛、持久重试、控制面自动发送及管理 API；全局插件自动启用；KAC 实际处置接收端另行接入 |
| [alert-output.md](alert-output.md) | Kafka Alert V1 完整快照、cause 和确定性 message ID       |
| [policy-api.md](policy-api.md) | 三类告警策略配置发布 v1、租户作用域、幂等重试与分页 |
| [policy-runtime-api.md](policy-runtime-api.md) | 三类策略运行态有界查询、清理明细、抑制对账、屏蔽复查及原版本保护的合并接续/关系检查 |
| [alert-close.md](alert-close.md) | 管理 API 主动关闭 Alert、操作身份与部分成功重试 |
| [task-resume.md](task-resume.md) | 确定性任务失败暂停、代次校验和管理 API 显式恢复 |
| [active-alert-strategy-change-v1.md](active-alert-strategy-change-v1.md) | Redis 策略索引成员变更通知 v1、channel 与消费者补读语义 |

输入 Cleaner、Kafka V1 输出和 KAC Alarm 输出分别维护自己的协议边界。当前实现支持 `standard`、
Kafka Alert V1、KAC Alarm 兼容输出与策略索引变更通知 v1；后续只有在出现真实外部消费者变更后才滚动对应协议版本，并同步更新
实现、测试和本文索引。

- [OneModel 调试查询 API](onemodel-query.md)：实例分页、关联查询与快照释放。

- [内部 HTTP JWT 认证](internal-token.md)：Kingeye 互信协议、签发示例和升级配置。
