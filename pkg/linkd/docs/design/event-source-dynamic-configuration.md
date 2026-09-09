# EventSource 动态配置

本页描述来源管理和运行时装配。部分全局配置动态化仍是[独立方案](dynamic-configuration.md)，全局 Severity 当前继续启动时冻结。

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

默认两角色均为 all，支持数字 0；enabled=false 覆盖两个角色。标签按键值精确 AND 匹配。
worker 的 `require_explicit_selector=true` 排斥空选择器，其余 worker 接受空选择器。

Cleaner 目标数量为 min(配置数量、匹配 worker 数、Kafka topic partition 总数)，all 省略配置数量约束。
Lifecycle 不受 Kafka 分片上限限制。同一 worker 同源同角色只启动一个 Flow，all-in-one 两角色不冲突。
同角色多副本共享消费组，副本 slot 不代表业务分片。进程按 max_tasks、总并发和 inflight 字节预算准入。

Kafka 探测在控制面运行：30 秒目标周期、5 秒超时、最多 4 个并发，优先最久未探测的来源。
探测禁止自动创建 topic；不以在线 leader 或 ISR 数代替总 partition 数。
首次失败不启动 Cleaner；后续失败保留既有任务与最后已知上限，禁止扩容；分片增加后补齐任务。
topic 身份变化或分片异常减少报告错误，不当作普通缩容。大规模来源的实际探测间隔受有界并发限制，状态展示最近成功时间。

## 执行版本与重投

Event.event_source_version 由 EventFactory 注入当前任务启动时固定的 Release 版本；新数据要求正整数。
Alert 创建时继承触发 Event 的版本，普通更新不覆盖，等级升级的新 Alert 再次继承。
AlertLog 不添加独立顶层版本，Alert 输出快照自然携带版本。

不保存 offset→Release 区间。未落库消息按当前任务配置处理；已落库相同身份、相同原始来源事实的跨版本重投复用原 Event，包括其版本和处理状态。
原始事实不一致仍是身份冲突。版本不参与 Event ID/fingerprint，不重写旧 Event。
已发布来源的租户、指纹和 Kafka 订阅身份变化拒绝普通更新，需要独立迁移方案。

## 按来源运行 Lifecycle

每来源一个稳定 Signal Stream，同来源所有 Lifecycle 副本共享 Stream/Group，consumer 绑定任务代次。
Stream 不包含 Release 或 worker ID；Mailbox、lease 及背压也按来源隔离，租户仍在业务关联键内。

停用/删除先停止 Cleaner，再有界停止 Lifecycle。角色数量 0 只停止对应角色；未完成队列保留，不强制 ACK 或删除。
确认旧 consumer 退出或授权强切后，仅定向接管这些退休 consumer 的 Pending，不对整个 Group 零等待 Claim。
Stream 管理器按来源清单有界遍历，只裁剪已确认前缀。

## 接口与启动

- `GET /api/v1/event-sources?after=&limit=100`：有界来源列表。
- `GET /api/v1/event-sources/{id}`：编辑记录与发布指针，默认凭据脱敏。
- `PUT /api/v1/event-sources/{id}`：`{"expected_revision":0,"spec":{...}}`，0 创建，后续带当前 revision。
- `DELETE /api/v1/event-sources/{id}`：`{"expected_revision":...}`，发布停用 tombstone，不清理业务数据。
- `GET /api/v1/event-sources/{id}/releases/{version}`：脱敏历史配置。
- `GET /api/v1/runtime`：worker、任务、调度目标、分片探测及来源队列路由。
- `linkd event-source import --file <yaml>`：通过 API 增加/更新文件中的 event_sources，不删除遗漏项。

来源列表和单条读取支持管理端显式传入 `include_secrets=true` 获取完整配置；默认读取仍脱敏，
历史 Release 读取接口保持脱敏。用途及 Console 服务端凭据边界见
[动态来源的 Kafka 查询](../guides/console.md#动态来源的-kafka-查询)。

常驻进程不自动加载 YAML 来源。YAML 保留静态连接、认证、预算和可显式导入的来源清单；优先用 API/Console 修改来源。
API 保存成功返回 202，不代表所有 Flow 已切换；查看目标与实际任务状态判断应用结果。
配置 token 与 worker token 必须不同，worker 仅能读取分配给当前会话的 Release。管理编辑省略 security 时保留旧凭据，不提交脱敏占位值。

运行协议、失联自停和容灾边界见[中心化任务调度协议](task-scheduling-protocol.md)。
没有历史数据/旧 Stream 兼容层；部署新版本前自行选择新的数据空间或明确处理旧数据，本程序不自动清理。
