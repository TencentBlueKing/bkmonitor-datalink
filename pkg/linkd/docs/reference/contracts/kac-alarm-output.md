# KAC Alarm 输出契约

`EventSource.hooks` 中 `type: kac` 的实例将每次 Alert 真实变化转换为一条 KAC
`alarm_collect_topic` 扁平 JSON object。详细字段映射、可靠性和迁移说明见
[KAC Alarm Hook 设计](../../design/kac-alarm-hook.md)。

固定规则：

- `alarm_id = "linkd-" + UUIDv5(tenant, AlertID, UpdateAt, outcome)`，同一快照重试保持稳定；
- `event_id = "linkd-" + Alert.AlertID`，用于关联同一 Alert 的活动与终态消息；
- Kafka key 使用 `event_id`；
- `active/recovered/closed` 分别映射为 `firing/resolved/close`；
- update_current 升级仍发送 firing，保持 event_id，level 使用新级别，alarm_id 因新快照变化；close_and_create 升级依次发送旧 close 和新 firing，event_id 不同；
- `critical/warning/info` 分别映射为 `fatal/warning/remind`；
- 无偏移时间按 `Asia/Shanghai` 格式化为 `YYYY-MM-DD HH:mm:ss`；
- `source_name` 固定为 `鲸眼监控`；
- `bk_tenant_id` 显式进入消息；
- Log、APM、K8s 和云平台场景扩展字段首版按约定默认值输出。

活动消息省略 `close_time` 和 `close_reason`，终态消息携带这两个字段。KAC 当前 URL 路由仅接受字母、数字、下划线和连字符，UUID 形式的 `alarm_id` 用作临时兼容；`event_id` 保留 Linkd Alert 身份以维持生命周期关联。Hook 等待 Kafka all-ISR ACK；
Alert、Kafka 和 AlertLog 之间没有事务，重复执行使用稳定 `alarm_id` 和 `message_id` 收敛，KAC 接收方的重复处理能力
需要在目标环境验证。
