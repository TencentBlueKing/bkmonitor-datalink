# 外部契约

| 契约                               | 内容                                                     |
| ---------------------------------- | -------------------------------------------------------- |
| [raw-event.md](raw-event.md)       | `RawEventMessage` 信封与 `standard` payload              |
| [alert-output.md](alert-output.md) | Kafka Alert V1 完整快照、cause 和确定性 message ID       |

输入 Cleaner 和 Kafka 输出分别维护自己的协议边界；当前只有 `standard` 与 Kafka Alert V1，不存在需要
兼容的历史 schema。后续只有在出现真实外部消费者后才滚动对应协议版本，并同步更新实现、测试和本文索引。
