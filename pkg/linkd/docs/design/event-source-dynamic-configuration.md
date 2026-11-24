# EventSource 动态配置

本页描述来源管理和运行时装配。选定全局配置通过[动态配置](dynamic-configuration.md)独立同步和恢复，默认关闭时 Severity 使用启动 YAML。

## Python 采集能力的交付边界（2026-11-14 确认）

本次独立运行时和 Collector 默认不启用，保留现有 KAC 执行与配置行为。不增加逐来源执行后端切换开关，不修改现有来源的启用状态，也不在本次开发过程中部署启动新链路。后续由部署一次性切换执行权，届时统一停用 KAC 对应接收/拉取执行入口，避免双重执行。

兼容基线：旧 YAML 不要求增加 collector，旧 EventSource 不要求增加 collection；只有明确配置插件采集的来源才创建 collector 任务。运行时不会随默认角色自动启动；2.x 分支不再内置 Helm Chart，部署配置由环境侧维护。本地自动化、隔离容器和测试中间件验证不等同于启用业务来源。

2026-11-15 用户进一步确认：本次只完成独立 Python 运行时与 Linkd，KAC 管理端另做。因此 KAC 四步骤页面、在线脚本/附件发布及管理入口调用切换不作为本次完成条件。CLI 调试、Linkd 管理预览 API 和开发文档属于本次交付。存量内置插件已迁入并有本地样本验证，具体覆盖及厂商实连限制见[运行时验收](../../python/README.md#本次交付验收)。上线切换和生产部署验收是后续部署阶段，不以逐来源迁移机制代替该决策。

## 推送自动恢复的执行权（2026-11-14 确认）

用户已确认复用现有 taskdispatch 领导权，不新增分布式锁。控制面同步接收与后台恢复共用请求执行登记，同租户/来源/请求身份不得同时运行；冲突立即返回忙或跳过，不排队等待。Redis 接收、计数、输入/输出与完成提交在同一 Lua 中检查已有领导权，授权失效即拒绝写入。该保护不撤销已经发出的 Kafka 请求，重投仍沿用冻结输出与稳定消息身份。

后台按部署级持久推送索引分批扫描，使用原请求的历史发布，不依赖当前来源目录；单条恢复失败不阻断其他来源。控制面推送使用领导权，独立 pull worker 则复用既有任务授权，在同一次 Redis Lua 中核对当前任务的 worker/epoch、来源、角色、活动阶段和过期时间。两个路径均不创建新锁或授权副本；续租仍由原 taskdispatch 流程负责。默认关闭与部署一次性切换的约定保持不变。

## 配置事实与发布

ES/MySQL 只新增 `EventSourceRecord`、`EventSourceRelease` 两类来源集合，后端跟随 `storage.repository`。
Record 保存编辑版本、配置、已发布版本、待发布内容与删除标记；Release 保存单来源不可变快照、发布版本和操作信息。

写入步骤：校验 expected revision → CAS 预留 Record 版本和 pending 快照 → 幂等创建 Release → CAS 更新发布指针。
指针完成后才允许调度。中途失败由控制面对账恢复；并发编辑返回冲突，完全相同的重复操作复用结果。
不承诺跨来源、跨 Record/Release 或 Redis 的事务。ES 来源配置使用独立普通索引，按 ID 实时读取；来源管理低频写入等待搜索刷新，业务 Event 写路径不改变。

API、provider 和显式 YAML 导入共用同一服务，不区分可写空间。provider 获得只读 Reader 查询当前版本，再返回带 expected revision 的 upsert/delete。
缺失项不是删除指令。失败和重试都有超时、批量上限，控制面通过 `Dependencies.SourceProviders` 显式注入提供方。

## 任务数量与标签

```yaml
scheduling:
  cleaner:
    replicas: all
    selector:
      pool: ingestion
  lifecycle:
    replicas: 2
    selector: {}
```

Kafka 来源默认两角色均为 all，支持数字 0；enabled=false 覆盖两个角色。标签按键值精确 AND 匹配。
worker 的 `require_explicit_selector=true` 排斥空选择器，其余 worker 接受空选择器。

Cleaner 目标数量为 min(配置数量、匹配 worker 数、Kafka topic partition 总数)，all 省略配置数量约束。
Lifecycle 不受 Kafka 分片上限限制。同一 worker 同源同角色只启动一个 Flow，all-in-one 两角色不冲突。
同角色多副本共享消费组，副本 slot 不代表业务分片。进程按 max_tasks、总并发和 inflight 字节预算准入。

内置 `builtin_alarm_merge` 来源使用 `storage.type=internal_merge`：Cleaner 固定 0，只调度 Lifecycle；
不配置或探测 Kafka，不把空 Kafka 参数解释为合法订阅。它与普通来源使用相同 Record/Release 发布流程，
配置和外部字段边界见 [EventSource](../modules/event-source.md#内部合并来源)。

Kafka 探测只对外部来源在控制面运行：30 秒目标周期、5 秒超时、最多 4 个并发，优先最久未探测的来源。
探测禁止自动创建 topic；不以在线 leader 或 ISR 数代替总 partition 数。
首次失败不启动 Cleaner；后续失败保留既有任务与最后已知上限，禁止扩容；分片增加后补齐任务。
topic 身份变化或分片异常减少报告错误，不当作普通缩容。大规模来源的实际探测间隔受有界并发限制，状态展示最近成功时间。

## 执行版本与重投

外部 Event.event_source_version 由 EventFactory 注入当前任务启动时固定的 Release 版本；
内部合并 Event 由持久化合并裁决冻结其内置来源 Release，重试不切换到最新版本；新数据要求正整数。
Alert 创建时继承触发 Event 的版本；同级更新和 update_current 升级不覆盖，close_and_create 升级的新 Alert 从本次 Event 继承。
AlertLog 不添加独立顶层版本，Alert 输出快照自然携带版本。

不保存 offset→Release 区间。未落库消息按当前任务配置处理；已落库相同身份、相同原始来源事实的跨版本重投复用原 Event，包括其版本和处理状态。
原始事实不一致仍是身份冲突。版本不参与 Event ID/fingerprint，不重写旧 Event。
已发布来源默认禁止修改 Kafka brokers、topic 和 consumer_group。仅在本次管理 PUT 请求顶层显式填写 `allow_subscription_change: true` 时允许维护变更；CAS 发布新 Release，历史 Release、Event 和 Alert 不改写。该参数不写入 Record/Release，也不会授权后续更新。provider/import 和 KAC 日常发布默认不带该参数；Console 常用表单继续保持订阅只读。调度器重新探测新订阅，按现有执行版本交接流程撤销旧任务并分配新任务；同一 worker 的同来源同角色须确认旧代次停止或授权到期后才能启动新代次，多个 worker 之间仍可能滚动交接。新 topic 不可达时不会把旧分片元数据当成新 topic 的可用证明。

此更新不复制、重置或迁移 Kafka offset。仍使用同一集群/topic/group 时沿用 Kafka 已提交位点；更换 topic 或 group 时按其已有有效位点消费；没有提交位点，或首次读取时提交位点已超出 Kafka 保留范围，则从当前尾部等待新消息，不主动补读历史。该默认值适用于所有 Linkd Kafka 来源；不会跳过有效位点后的积压。来源 ID、关联租户、Cleaner 类型、fingerprint 规则和 storage.type 仍不可通过更新改变。不同 EventSource 间的历史告警关联不随订阅配置迁移；换到独立 Kafka 集群时，同名 topic/partition/offset 也不应被当作新的可靠身份空间。

## 按来源运行 Lifecycle

每来源一个稳定 Signal Stream，同来源所有 Lifecycle 副本共享 Stream/Group，consumer 绑定任务代次。
Stream 不包含 Release 或 worker ID；Mailbox、lease 及背压也按来源隔离，租户仍在业务关联键内。

停用/删除先停止 Cleaner，再有界停止 Lifecycle。角色数量 0 只停止对应角色；未完成队列保留，不强制 ACK 或删除。
确认旧 consumer 退出或授权强切后，仅定向接管这些退休 consumer 的 Pending，不对整个 Group 零等待 Claim。
Stream 管理器按来源清单有界遍历，只裁剪已确认前缀。

## 接口与启动

- `GET /api/v1/event-sources?after=&limit=100`：有界来源列表。
- `GET /api/v1/event-sources/{id}`：编辑记录与发布指针，默认凭据脱敏。
- `POST /api/v1/event-sources/{id}/plugin-preview`：管理 JWT 鉴权后按当前草稿执行一次插件预览，允许未发布或停用来源；不发布事件或提交游标，详见[插件调试说明](../guides/python-plugin-development.md#管理-api-预览)。Collector 默认关闭时不可用。
- `PUT /api/v1/event-sources/{id}`：`{"expected_revision":0,"spec":{...}}`，0 创建，后续带当前 revision。例外维护可在顶层添加 `"allow_subscription_change":true`，仅允许本次 Kafka 订阅变更，不放开租户、Cleaner 或 fingerprint 等业务身份。
- `DELETE /api/v1/event-sources/{id}`：`{"expected_revision":...}`，发布停用 tombstone，不清理业务数据。
- `GET /api/v1/event-sources/{id}/releases/{version}`：脱敏历史配置。
- `GET /api/v1/runtime`：worker、任务、调度目标、分片探测及来源队列路由。
- `linkd event-source import --file <yaml>`：通过 API 增加/更新文件中的 event_sources，不删除遗漏项。

来源列表和单条读取支持管理端显式传入 `include_secrets=true` 获取完整配置；默认读取仍脱敏，
历史 Release 读取接口保持脱敏。完整配置仅供管理服务端使用，凭据不返回浏览器。

常驻进程不自动加载 YAML 来源。YAML 保留静态连接、认证、预算和可显式导入的来源清单；优先用 API/Kingeye Admin 修改来源。
API 保存成功返回 202，不代表所有 Flow 已切换；查看目标与实际任务状态判断应用结果。
配置 token 与 worker token 必须不同，worker 仅能读取分配给当前会话的 Release。管理编辑省略输入 Kafka security 时保留旧凭据。
Hook 编辑按实例 `name + type` 匹配既有凭据，列表重排不影响匹配。Kafka/KAC 的 SASL 密码、
内联 TLS 私钥及 Redis/Sentinel 密码中的 `******` 会在发布前恢复；省略整个 Hook Kafka security
保留原安全配置。显式新密码用于轮换，Redis 空密码用于清除。新增、重命名或换类型的 Hook
必须提供自己的凭据，无法恢复的占位符会被拒绝，YAML/provider 也不能发布占位符。

运行协议、失联自停和容灾边界见[中心化任务调度协议](task-scheduling-protocol.md)。
没有历史数据/旧 Stream 兼容层；部署新版本前自行选择新的数据空间或明确处理旧数据，本程序不自动清理。

## Collector 拉取角色（开发中）

已有任务分配协议新增 collector 角色，仅 collection.mode=pull 或 recovery_interval_minutes>0 的来源创建目标任务。
scheduling.collector 使用现有副本/标签配置，但当前单来源目标最多为 1；显式副本 0 停止拉取。
运行预算按单个同步请求的请求/响应字节预算预留，不能用 Kafka 分区数扩大同一游标的执行者数量。
移除 collection 或改为没有恢复周期的 push 会撤销已有 collector；停用整个来源时先停止 collector，再停止 Cleaner/Lifecycle。
当前已完成调度规划和预算协议；定时执行 Runner、worker 命令和真实租约接管验收尚未完成。
