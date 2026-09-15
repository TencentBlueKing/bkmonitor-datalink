# 外部契约

| 契约                               | 内容                                                     |
| ---------------------------------- | -------------------------------------------------------- |
| [raw-event.md](raw-event.md) | `RawEventMessage` 信封与 `standard` payload |
| [kac-alarm-output.md](kac-alarm-output.md) | KAC alarm_collect_topic 扁平 Alarm JSON、身份和固定映射 |
| [alert-output.md](alert-output.md) | Kafka Alert V1 完整快照、cause 和确定性 message ID       |

输入 Cleaner、Kafka V1 输出和 KAC Alarm 输出分别维护自己的协议边界。当前实现支持 `standard`、
Kafka Alert V1 与 KAC Alarm 兼容输出；后续只有在出现真实外部消费者变更后才滚动对应协议版本，并同步更新
实现、测试和本文索引。
