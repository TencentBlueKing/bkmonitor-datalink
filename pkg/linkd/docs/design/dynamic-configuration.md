# 动态配置同步与持久化恢复

状态：已实现，外部环境验证结果以交付记录为准。更新日期：2026-09-21。

## 边界与默认开关

`control_plane.dynamic_config.enabled` 默认 `false`。关闭时保留 YAML 行为，不构造来源连接、
不读取快照、不创建快照集合、不启动 watch 或轮询。已有来源配置可以保留且不要求补齐连接字段。
来源连接与开关是启动配置；启用后的等级表在线应用。配置来源的租户是显式配置作用域，默认 `system`，
所得等级表对该 deployment 生效；业务 Event/Alert 仍按各自 `bk_tenant_id` 隔离。

与 [EventSource 管理](event-source-dynamic-configuration.md) 独立，不修改 Record/Release，也不构造全局 ConfigRelease。

## 来源与转换

控制面执行：读取上游 → 转换/校验 → CAS 持久化 → 发布内存快照 → Worker 应用。
`Source.Read` 返回完整选定字段，`WatchSource.Watch` 仅发送需补读通知。通知合并为容量 1 的队列，读取串行执行。
配置项通过 `bindings` 显式枚举，目前只有 `severity`；未绑定来源不会建立连接。

- `kingeye_alarmlevel`：独立 MySQL 连接，默认每 30 秒按显式租户全量查询 `alarm_alarmlevel` 的 `name, priority`。
  表名必须为普通 SQL 标识符；最多读取 257 行并拒绝超过 256 个等级，不依赖 `updated_at` 识别删除和排序变更。
- `kingeye_dynamicconfig`：独立 Redis standalone/Sentinel 连接，遵循 Kingeye Redis v1。
  同一事务读取全局 revision 和绑定字段，订阅全局 events 并过滤租户。确认订阅、重连和默认每 300 秒补读。
  租户按 UTF-8 percent-encode；读取体在 Redis 服务端限制为 1 MiB + 1 字节，超限拒绝。
  revision 缺失、字段不存在、JSON null 或类型非法均为读取/配置失败，不解释为清空等级。

两种来源都将 JSON 等级数组转换为名称和 priority；未知展示字段可忽略，priority 必须是显式整数。
空表、重复名称或 priority、非法名称和超限数据不会覆盖有效配置。
MySQL 不提供默认等级：沿用 YAML 的 default_severity；若该名称被删除，改用当前 priority 最大的最轻等级。
此默认值仅服务于原始输入显式回退，已经清洗的未知标准等级不会在 Lifecycle 转为默认等级。

连接配置、凭据与 Linkd 业务存储和 Enrich 来源独立。MySQL 来源只读，不执行迁移。

## 最后有效快照

每个来源绑定仅保留一份最后有效配置，不保存历史配置链，也不按事件版本回放配置。
快照存放在 Linkd `storage.repository` 指定的 MySQL 或 Elasticsearch：

- MySQL：`linkd_dynamic_config_snapshots`。
- Elasticsearch：`<index_prefix>_dynamic_config_snapshots`。

ID 由 deployment、配置项、非敏感来源身份散列生成。来源身份包含实例、数据库、表或 Redis 前缀、租户和绑定 key，
不包含用户名/密码，凭据轮换不会废弃快照。快照保存 schema version、完整有效值、内容摘要、来源身份及持久化时间。

启动先验证并恢复快照，恢复成功即具备配置，不等待上游；后台立即重试同步。没有可信快照时有界拉取上游，
保存成功后使用；两者均不可用则采用 YAML 并标记降级。快照不设置过期失效时间。

同步和持久化失败保留内存与已有快照，不退出控制面。只有 CAS 保存成功才发布新值；保存成功但发布前崩溃可通过恢复补齐。
来源没有变化时保留同一内容摘要。存储初始化仅在启用时执行，`storage migrate` 仅初始化 Linkd 自身集合，不探测上游。

这里隔离的是上游配置服务故障；不表示 Linkd 自身业务存储整体不可用时仍能处理业务。

## 分发和生效

`GET /api/v1/dynamic-config` 使用管理 token 返回当前配置、同步状态和 Worker 反馈；
`GET /internal/settings/severity` 使用 Worker token 返回当前完整等级快照。
控制面在心跳响应头 `X-Linkd-Dynamic-Config` 提供当前摘要或 `disabled`。
Worker 仅在启用且摘要变化时获取配置，以原子指针替换；已有快照的拉取失败不会丢失有效值，首次加载未成功则暂不启动任务。
Worker 在心跳上报 `config_digest/config_error`。all-in-one 两角色共享同一状态。
显式 EventSource YAML 导入先读取控制面当前等级进行引用校验，不要求在导入文件复制 KAC 等级表。

进程之间最终一致，没有全局切换屏障。单次 Cleaner Build/Lifecycle 裁决冻结一个完整内存快照。
同一原始消息因等级映射变化重投时复用已保存的 Event，不覆盖其事实；同来源版本仍要求其他字段和动作保持一致。
新裁决使用最新配置；已经保存的 EventPlan 仅用于既有副作用的幂等收尾，不读取历史等级配置。

## 未知等级处理

- 动态模式下，未配置来源 mapping/default 的标准等级保留原名称；旧 mapping 引用的已删除等级同样落库，
  不阻止整个 Flow 装配。新建/编辑来源仍使用当前等级表进行引用校验。
- 遇到未知等级的活动 Alert，下一次同租户/来源/fingerprint 处理时系统关闭。
  关闭原因是 `unknown_severity`，记录原等级、配置摘要及操作日志，复用 CAS、缓存和 Hook。
  关闭意图与稳定时间先写入 EventPlan，失败重试补齐原操作，不在配置更新时全量扫描。
- 已清洗 Event 的任意 evaluation 等级未知时，整条 Event 进入 `rejected` 终态，reason 为 `unknown_severity`。
  不执行其中合法等级的业务裁决，记录诊断日志，持久化完成后确认 Mailbox；存储失败仍重试。
- 已处理 Event 和终态 Alert 不重写。优先级变化仅影响后续裁决。
- KAC 来源等级按原名输出；关闭未知等级不会被本地 KAC 等级转换拦截。远端 Hook 失败仍沿用既有失败日志语义。

## 验证

单元测试使用假 Source 和内存 Store；普通测试不连接上游。显式集成测试覆盖 MySQL 查询与 CAS、
Elasticsearch 实时读取/CAS、Redis 事务读取与订阅；通过对应 `LINKD_TEST_*` 环境变量启用。
配置示例及参数见 [配置指南](../guides/configuration.md#动态配置)。
