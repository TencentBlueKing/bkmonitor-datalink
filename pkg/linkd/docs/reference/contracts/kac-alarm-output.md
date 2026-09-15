# KAC Alarm 输出契约

`EventSource.hooks` 中 `type: kac` 的实例将每次 Alert 真实变化转换为一条 KAC
`alarm_collect_topic` 扁平 JSON object。详细字段映射、可靠性和迁移说明见
[KAC Alarm Hook 设计](../../design/kac-alarm-hook.md)。

固定规则：

- `alarm_id = "linkd-" + Alert.AlertID`；
- `event_id = "linkd-" + Alert.AlertID`；
- Kafka key 使用 `event_id`；
- `active/recovered/closed` 分别映射为 `firing/resolved/close`；
- `critical/warning/info` 分别映射为 `fatal/warning/remind`；
- 无偏移时间按 `Asia/Shanghai` 格式化为 `YYYY-MM-DD HH:mm:ss`；
- `source_name` 固定为 `鲸眼监控`；
- `bk_tenant_id` 显式进入消息；
- Log、APM、K8s 和云平台场景扩展字段首版按约定默认值输出。

活动消息省略 `close_time` 和 `close_reason`，终态消息携带这两个字段。Hook 等待 Kafka all-ISR ACK；
Alert、Kafka 和 AlertLog 之间没有事务，重复执行使用稳定 `message_id` 审计，KAC 接收方的重复处理能力
需要在目标环境验证。
