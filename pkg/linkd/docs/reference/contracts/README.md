# 外部契约

| 契约                               | 内容                                                     |
| ---------------------------------- | -------------------------------------------------------- |
| [raw-event.md](raw-event.md) | `RawEventMessage` 信封与 `standard` payload |
| [kac-alarm-output.md](kac-alarm-output.md) | KAC alarm_collect_topic 扁平 Alarm JSON、身份和固定映射 |
| [alert-output.md](alert-output.md) | Kafka Alert V1 完整快照、cause 和确定性 message ID       |
| [alert-close.md](alert-close.md) | 管理 API 主动关闭 Alert、操作身份与部分成功重试 |
| [active-alert-strategy-change-v1.md](active-alert-strategy-change-v1.md) | Redis 策略索引成员变更通知 v1、channel 与消费者补读语义 |

输入 Cleaner、Kafka V1 输出和 KAC Alarm 输出分别维护自己的协议边界。当前实现支持 `standard`、
Kafka Alert V1、KAC Alarm 兼容输出与策略索引变更通知 v1；后续只有在出现真实外部消费者变更后才滚动对应协议版本，并同步更新
实现、测试和本文索引。

- [OneModel 调试查询 API](onemodel-query.md)：实例分页、关联查询与快照释放。
