# KAC Alarm 输出契约

> 以下描述旧 type=kac Kafka Hook。全局 [KAC 插件](../../design/kac-compatibility-plugin.md)已直接维护
> 一对一 alarm_event 兼容文档并可靠通知处置；使用全局插件的 Alert 会跳过本 Hook。

`EventSource.hooks` 中 `type: kac` 的实例将本次获准输出的 Alert 快照转换为一条 KAC
`alarm_collect_topic` 扁平 JSON object。详细字段映射、可靠性和迁移说明见
[KAC Alarm Hook 设计](../../design/kac-alarm-hook.md)。

执行时，若当前 Alert 快照已有任一 `projection.targets[*].action_enabled=true`，本 Hook 返回
`Skipped`，不转换旧格式或发送 Kafka；通用 Hook 指标记录 skipped。该判断使用已保存的 Alert 绑定，
不根据最新来源是否仍声明目标、部署凭据是否可用或可靠投递是否失败回退旧通道。纯状态投影目标
不禁用本 Hook，普通 Kafka V1 状态输出也不受此规则影响。取消、非法 cause 或损坏的 Alert 仍返回错误。
该规则仅明确已有绑定的输出归属，不定义来源修改/移除目标时如何更新 Alert 绑定。

固定规则：

- `alarm_id = "linkd-" + UUIDv5(tenant, AlertID, UpdateAt, outcome)`，同一快照重试保持稳定；
- `event_id = "linkd-" + Alert.AlertID`，用于关联同一 Alert 的活动与终态消息；
- Kafka key 使用 `event_id`；
- `active/recovered/closed` 分别映射为 `firing/resolved/close`；
- 已获准的合并窗口释放使用 system_operation cause，只有保存的 merge_change.action_ready=true 且操作身份一致时允许发送 firing；单纯解除等待/屏蔽不作为旧 KAC 输入。
- update_current 升级仍发送 firing，保持 event_id，level 使用新级别，alarm_id 因新快照变化；close_and_create 升级依次发送旧 close 和新 firing，event_id 不同；
- `critical/warning/info` 分别映射为 `fatal/warning/remind`；
- 无偏移时间按 `Asia/Shanghai` 格式化为 `YYYY-MM-DD HH:mm:ss`；
- `source_name` 固定为 `鲸眼监控`；
- `bk_tenant_id` 显式进入消息；
- 丰富后的 `name` 和 `content` 均须非空；标准 Event 允许空内容，使用本 Hook 的普通或合并告警需满足额外约束，失败记 Hook 流水而不回滚 Alert；
- Log、APM、K8s 和云平台场景扩展字段首版按约定默认值输出。

活动消息省略 `close_time` 和 `close_reason`，终态消息携带这两个字段。KAC 当前 URL 路由仅接受字母、数字、下划线和连字符，UUID 形式的 `alarm_id` 用作临时兼容；`event_id` 保留 Linkd Alert 身份以维持生命周期关联。Hook 等待 Kafka all-ISR ACK；
Alert、Kafka 和 AlertLog 之间没有事务，重复执行使用稳定 `alarm_id` 和 `message_id` 收敛，KAC 接收方的重复处理能力
需要在目标环境验证。
