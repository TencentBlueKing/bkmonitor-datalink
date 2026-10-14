# 外部契约

| 契约                               | 内容                                                     |
| ---------------------------------- | -------------------------------------------------------- |
| [Linkd 标准事件](standard-event.md) | `RawEventMessage` 信封、`standard` payload 与 KAC V2 发布前校验 |
| [KAC 插件调试字段预览](kac-ingest-preview.md) | 同步 HTTP 标准化及字段返回；不执行丰富或业务写入 |
| [kac-alarm-output.md](kac-alarm-output.md) | KAC alarm_collect_topic 扁平 Alarm JSON、身份和固定映射 |
| [kac-alert-projection-v1.md](kac-alert-projection-v1.md) | 稳定身份、控制面批量投影、字段所有权与同步关闭 |
| [kac-action-delivery-v3.md](kac-action-delivery-v3.md) | 轻量动作参数、独立并发、失败观测及优雅重启 |
| [alert-output.md](alert-output.md) | Kafka Alert V1 完整快照、cause 和确定性 message ID       |
| [policy-api.md](policy-api.md) | 三类告警策略配置发布 v1、租户作用域、幂等重试与分页 |
| [policy-runtime-api.md](policy-runtime-api.md) | 三类策略运行态有界查询、清理明细、抑制对账、屏蔽复查及原版本保护的合并接续/关系检查 |
| [alert-close.md](alert-close.md) | 管理 API 主动关闭 Alert、操作身份与部分成功重试 |
| [task-resume.md](task-resume.md) | 确定性任务失败暂停、代次校验和管理 API 显式恢复 |
| [tenant-directory.md](tenant-directory.md) | 通过用户管理实时查询全局租户目录，仅管理 JWT 可访问 |
| [module-info.md](module-info.md) | 模块部署、真实存储查询目标、能力与管理端显式连接发现 |
| [active-alert-strategy-change-v1.md](active-alert-strategy-change-v1.md) | Redis 策略索引成员变更通知 v1、channel 与消费者补读语义 |

输入 Cleaner、Kafka V1 输出和 KAC Alarm 输出分别维护自己的协议边界。当前实现支持 `standard`、
Kafka Alert V1、KAC Alarm 兼容输出与策略索引变更通知 v1；后续只有在出现真实外部消费者变更后才滚动对应协议版本，并同步更新
实现、测试和本文索引。

- [OneModel 调试查询 API](onemodel-query.md)：实例分页、关联查询与快照释放。

- [内部 HTTP JWT 认证](internal-token.md)：Kingeye 互信协议、签发示例和升级配置。

- [策略流转业务事实](policy-flow-records.md)：新策略日志的冻结展示事实、动作角色、稳定身份与旧意图边界。

- [KAC 告警丰富事实查询](kac-enrich-records.md)：准确关联事件的冻结结果、字段前后对比与有界只读接口。

- [Event 丰富窗口查询](event-enrich-window.md)：内置与第三方来源的真实事件采样、逐等级结果、窗口分页及诊断。
