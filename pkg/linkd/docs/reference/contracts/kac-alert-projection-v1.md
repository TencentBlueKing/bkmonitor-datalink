# KAC Alert 兼容存储 V1

一个 Linkd Alert 生命周期对应一条 KAC `alarm_event` 文档，由全局 `plugins.kac` 直接维护。
Linkd 是告警事实的权威来源，KAC 查询该文档并修改处置字段。
本契约中的 `linkd.kac-projection.v1` 是持久任务快照格式，**不是 KAC HTTP 接收协议**；
不再配置 `projection_endpoint`，不再要求 KAC 实现状态投影接收端。

配置见[全局 KAC 插件](../../guides/configuration.md#kac-全局插件配置)，
索引源码基线见[插件设计](../../design/kac-compatibility-plugin.md#2-kac-索引基线)。
真实 KAC 应用的处置通知接入和旧状态写入退出仍须跨仓联调；本地协议模拟不能替代该验证。

## 身份与内部快照

全局插件固定目标 ID 为 `kac`，对所有来源和租户自动启用兼容存储与获准动作。
业务数据、任务、锁和文档身份仍包含租户；公共凭据不意味着租户数据合并。

| 字段 | 含义 |
| --- | --- |
| schema_version | 固定 linkd.kac-projection.v1 |
| bk_tenant_id | 业务租户，来自 Alert |
| target_id | 全局内置插件为 kac |
| linkd_alert_id | Linkd AlertID |
| alarm_id | 一个生命周期始终不变的 linkd- 前缀 UUID |
| linkd_revision | 1..2^53-1 的业务版本 |
| content_hash | 规范 Alert 业务 JSON 的 SHA-256 小写摘要 |
| alert | 冻结的业务快照，不含同步水位和待执行元数据 |

alarm_id 使用 UUIDv5 的 URL namespace，name 为以下 UTF-8 紧凑 JSON 数组，实际编码无分隔空格：

```json
["linkd:kac-alert-projection:v1", "tenant-a", "AlertID"]
```

不使用 EventID、更新时间、当前动作或重试时间构造文档身份。
快照去除 `projection`、`policy_change`、`merge_change`、`action_pending` 和 `shield.next_check_at`，
其他业务事实来自同一版本，保留数字精度和稳定 JSON 表示。每个快照最多 1 MiB；任务信封最多
1 MiB + 16 KiB。`source_id/source_version` 仅用于业务溯源与 ACK 作用域，不再解析来源出口。

## 索引与字段所有权

兼容索引继续使用 KAC 原 alias、mapping、动态模板、分析器和 ILM。控制面启动时检查/创建资源，
此后每 60 秒对账；每轮最多 30 秒。默认 hot rollover 为 30gb/60d，实际轮转由 Elasticsearch 执行。
索引尚未就绪时，维护任务独立记录失败并重试，不停止告警主流程；写入任务保留待办，处置仍等待可见性。
已有物理索引不重建，不自动 reindex、转储或删除历史记录；字段类型冲突明确报错。
ES 对对象字段省略显式 `type: object` 的等价表示允许通过校验。
维护时先检查 alias 下所有物理索引的已有字段类型、分析器和动态模板；仅缺少现行字段时，
按现行 KAC mapping 显式追加缺失字段定义，再允许投影写入。该操作幂等，不回填历史文档，
不覆盖已有字段，也不修改 alias 成员；已有字段冲突或追加失败仍保持目标未就绪。
这样既兼容 KAC 历史索引代际，也避免后续更新历史告警时由动态映射猜测字段类型。

Linkd 在独立 `.linkd-kac-state-<alias的SHA-256前16字节十六进制>` 索引中保存同步元数据。
该索引按确定性 alarm_id 定位，读取时复核完整租户/Alert/目标身份；不向 KAC 原 mapping 增加
版本、哈希或处置状态备份字段。

| 兼容文档字段 | 所有权与更新规则 |
| --- | --- |
| alarm_id、event_id、bk_tenant_id | Linkd；稳定身份，更新不能改变 |
| name、content、对象、来源及丰富字段 | Linkd；来自 Alert 固定 opening Event 有效视图 |
| level、action、source_alarm_status、close_time、close_reason | Linkd；当前级别和生命周期的 KAC 兼容映射 |
| status | Linkd 生命周期/策略转换与 KAC 处置状态按下表协作 |
| associate_alarm_id、associate_count | Linkd；通过稳定合并关系转换父 alarm_id 和成员计数 |
| tag_info | 保留 KAC 既有标签，并入 Linkd policy_tags，不因同步删除处置侧标签 |
| field_extra_info | 递归合并 Linkd 展示信息，保留 KAC 增加的快照等子字段 |
| storage_time、conductor、notify_status | 首次创建时初始化；后续不重置 |
| 工单、通知及其他处置扩展字段 | KAC；Linkd 局部更新不覆盖未声明的处置字段 |

| Alert 事实 | 兼容 status |
| --- | --- |
| recovered | restored |
| closed 且 end_type=user | closed |
| closed 且 end_type=source | source_closed |
| 其他 closed | system_closed |
| active 且屏蔽中 | shielded |
| active 原始告警仍有合并关系 | merged_into |
| active 且等待合并，或聚合父关系尚未就绪 | pending_merge |
| 普通 active 更新 | 保留 KAC 已写处置状态；首次缺省 received |
| 解除屏蔽/合并且仍 active | 恢复保存的处置状态；没有历史处置状态时 received，不因此补发处置 |

进入阻塞状态前保存 abnormal、dispatched、pending_execute、executing、autoorder_executing、
autoexecute_executing 或 autoexecuting_failure。终态优先于策略状态。
KAC 人工关闭等生命周期命令须通过 Linkd 入口执行；直接修改兼容文档不能成为第二套生命周期写入路径。

## 写入、并发与重试

1. 首次同步先按租户和 alarm_id 检索原 alias；已有文档复用真实物理索引，否则读取唯一 write index。
   将位置与待应用快照持久化到独立元数据索引，再创建兼容文档。并发初始化由 create/CAS 收敛。
2. 更新始终使用固定物理索引；rollover 后不向新 write alias upsert 同一 Alert。
3. 每轮先实时读取兼容文档及 `_seq_no/_primary_term`，再读取最新同步意图。更高业务版本替换待应用
   完整快照，同版本不同哈希明确失败，低版本不覆盖高版本。
4. 使用刚读取的文档版本进行局部条件更新，保留 KAC 并发处置字段。冲突重新从文档读取开始；
   不能获取新 seq_no 后跳过最新意图检查。文档已写而元数据未确认的中间策略状态也须识别，
   新解屏/解联快照不能把它当作 KAC 处置状态保留。每次调用最多八轮竞争，之后交给持久任务重试。
5. 显式 refresh 兼容物理索引，检查分片结果，然后条件确认元数据。这样也覆盖重复 update=noop 的可见性。
   只有确认版本、身份和搜索可见性后才推进 Alert 同步水位。
6. 已持久化同步确认的任务仅补本地 ACK；不重复重置兼容文档。尚未确认的部分写入按同一身份重试。

兼容 HTTP 连接最多四路，单次发送上下文最多十秒，响应最多 8 MiB，请求最多 2 MiB。
HTTP 错误仅保留固定分类，不输出连接凭据或远端响应体。取消传播至所有 I/O；运行器退出后关闭连接。
元数据索引属于可靠同步事实，应与兼容文档一并保留；本版不设计丢失后的全量历史重建。

## 持久任务与同步确认

任务身份固定为租户/Alert/目标/请求业务版本；重试不生成新业务身份。状态为
pending → sending → delivered → succeeded；临时失败进入 retry，永久失败或预算耗尽进入 failed。
每轮自动重试最多八次、退避最多一分钟；sending 租约 30 秒。人工恢复保留原请求、总尝试次数和操作者记录。
每租户待办上限 1024，同租户任务准入和同 Alert/目标投递分别使用稳定 Redis 租约。

内部 Receipt 保存身份、应用版本、快照哈希、Linkd 生命周期、搜索可见性及真实物理文档引用。
应用版本可高于原请求，但只确认本任务请求版本；同版本的哈希和生命周期必须相同。
同步 ACK 不推进 Alert 业务 revision/update_at，不替代[动作受理确认](kac-action-delivery-v2.md)。

## 自动运行器

配置 Lifecycle 且 `plugins.kac.enabled=true` 时启动全局运行时：

- `kac-index-maintenance`：每 60 秒维护兼容索引及同步元数据索引。
- `projection-producer`：每五秒补扫已有 Alert 的待同步水位，包括终态；每页最多 16 条。
- `projection-delivery`：每秒推进未完成任务；每页最多 16 条，四路执行。
- `action-enqueue`、`action-delivery`：补齐持久动作意图并在投影门槛满足后通知 KAC。

扫描最多五秒，执行保留既有容量、取消、分页完整性及失败恢复约束。生产正常启用，无首次启用
历史回填、逐来源补绑或旧目标迁移流程；正常任务补扫和进程重启恢复保留。
Console 继续提供投影任务、冻结快照、失败重试、动作任务和运行指标，控制面任务目录新增索引维护状态。
管理 API 的租户范围、分页和重试协议保持现有实现；不会向来源发布或浏览器明文展示凭据。

## 管理查询与人工恢复

以下接口使用管理 JWT，认证头为 `Internal-Token: Bearer <JWT>`；不是 Worker Token。
查询不触发写入，人工恢复只恢复原 failed 任务，不修改业务快照或在请求内执行投递。

| 路由 | 行为 |
| --- | --- |
| GET /api/v1/projection-tasks | 按租户分页查询持久任务摘要 |
| GET /api/v1/projection-tasks/{id} | 查询同租户任务摘要及最近状态 |
| GET /api/v1/projection-tasks/{id}/snapshot | 显式读取原冻结快照，不包含连接凭据 |
| POST /api/v1/projection-tasks/{id}/retry | 根据原 CAS 和稳定操作身份恢复失败任务 |

GET 必须提供 bk_tenant_id；列表支持 alert_id、target_id、source_id、state、after、limit。
limit 为 1..4，缺省 4；游标绑定租户和筛选条件，不能跨条件复用，未知或重复参数拒绝。
返回空 items 但 next 非空时仍须继续分页。请求共享两并发管理额度，十秒超时。
恢复正文包含 bk_tenant_id、expected_version、operation_id、operator_id、reason，保留原任务与请求身份。
成功只表示恢复排队；不得解释为兼容文档已经写入或 KAC 已完成处置。

## Console 运行观测

投影任务页面可查看水位、原业务快照、最近错误和重试记录；动作页面独立显示处置通知进度。
控制面任务目录显示索引维护及投影/动作循环的启用、存活和最近执行结果。
配置页面展示脱敏后的全局插件，来源页面不再需要目标配置。
任务指标区分扫描、入队、等待可见性、发送失败及本地确认，不把扫描次数当作唯一告警数或实际处置次数。
