# KAC 全局兼容插件

> 状态：2026-10-08 Linkd 侧兼容插件已实现并完成本地验收；真实 KAC 应用接入未完成。
> 当前实现使用全局 plugins.kac，Linkd 直接维护 alarm_event；旧逐来源/逐租户出口和 HTTP 状态投影已移除。
> 本地协议模拟和真实 ES 测试不代表真实 KAC 应用接入或生产切流。

## 1. 已确认的职责

`alarm_event` 是 Linkd 为 KAC 保留的兼容存储层：一个 Linkd Alert 生命周期对应一条稳定文档。
Linkd Alert 是告警事实的权威来源，KAC 使用兼容文档完成查询和后续处置。

| 关注点 | Linkd | KAC |
| --- | --- | --- |
| 索引定义 | 保持 KAC 既有定义，维护模板、mapping、alias、ILM 和索引创建 | 使用原有查询方式 |
| 告警状态 | 创建兼容文档，更新恢复、关闭、屏蔽、合并等状态 | 不直接推进告警生命周期 |
| 处置字段 | 首次创建时初始化必要默认值，后续同步保留处置结果 | 更新分派、处理人、工单、通知执行结果等处置字段 |
| 处置通知 | 满足已有准入规则且兼容文档可搜索后，可靠通知 KAC | 投递 Celery，按处理时 ES 数据执行后续处置 |
| 插件启用 | 一个部署级开关同时启用兼容存储及处置通知 | 不要求逐来源或逐租户登记目标 |

不再通过 `projection_endpoint` 把状态同步交给 KAC。Linkd 直接写兼容 Elasticsearch 索引，
负责失败重试、版本防回退和同步进度确认。`alarm_event` 不因为 Linkd 自身使用 MySQL Repository
而改成 MySQL 表；两者可以使用不同存储连接。

插件作为内置能力提供，全局开启后覆盖各 EventSource、各租户以及内置合并来源。
“全局”指配置作用域，不改变数据隔离：文档查找、幂等身份、任务、锁和状态水位仍包含租户。
不存在逐来源启用列表、逐租户凭据表或分别开启“仅投影/仅处置”的配置。
生产环境按插件正常启用设计。2026-10-08 已确认本版不关注首次启用时的已有 Alert 补齐，
不增加活跃或历史 Alert 回填扫描，也不设计启停切换、旧目标迁移或因启用而补发处置的流程。
正常运行中的写入重试、持久任务补扫和进程重启恢复仍属于可靠性要求，不是首次启用补齐。

## 2. KAC 索引基线

取证日期：2026-10-08；Kingeye `1874066858`，以下三个源码文件在取证时无工作区修改：

- `src/kingeye/kac/alarm/models.py`：`AlarmEvent.Index` 和 `AlarmEvent.Meta`。
- `src/kingeye/base/domains/alarm/models/alarm_event_document.py`：字段定义及旧状态枚举。
- `src/kingeye/base/infras/elasticsearch/base_document_tenant.py`：`_init_index`。

| 项目 | 取证结果及实现约束 |
| --- | --- |
| alias | `{KEY_PREFIX}alarm_event`；前缀来自 KAC 部署配置，不能用 Linkd 自身的 index_prefix 替换 |
| 引导索引 | `{alias}-000001`；已有 alias 时复用既有物理索引，不新建一套替代历史数据 |
| ILM | `{KEY_PREFIX}alarm_event_policy`，hot rollover 为 `max_size=30gb`、`max_age=60d` |
| 模板 | KAC 模板名为 alias，pattern 为 `{alias}*`；接管时检查已有模板，不叠加冲突的另一套定义 |
| 字段 | 保留 Keyword、Text、Integer/Object 等现有类型及查询语义 |
| 时间 | alarm_time、close_time、storage_time 使用 `yyyy-MM-dd HH:mm:ss`；保持现有时区转换约定 |
| 分析器 | 保留 split_by_whitespace_analyzer、char_filter 及 text.keyword 子字段 |
| 动态字段 | 保留 dynamic=true 和顶层 all_as_text 动态模板 |
| settings | 与目标 KAC 部署的 shards、replicas、max_result_window、max_terms_count、字段数量上限对齐 |

上述为源码基线，不是目标环境实际配置快照。部署时需要一个公共的 KAC ES 连接及原索引 alias；
不能从租户名、EventSource 或 blueking.app_code 猜出原索引位置。
索引维护由 Linkd 控制面承担，ILM 实际轮转仍由 Elasticsearch 执行。接管不隐含新增历史数据删除、
转储、reindex 或其他不可逆清理操作。

实现前提是 KAC 停止维护同一索引及直接写告警事实；否则两个系统可能相互覆盖模板或生命周期状态。
兼容字段保持 KAC 原 mapping。版本、哈希、真实物理索引、待应用快照及暂存处置状态保存在
独立的 Linkd 同步元数据索引中，不交给 KAC 修改；详细字段与并发协议见
[兼容存储契约](../reference/contracts/kac-alert-projection-v1.md)。

## 3. 写入和处置流程

旧清洗 `extra_data.object/item/meta_info/strategy_id/dimension_info` 在有效视图中优先于默认展示值；
按字段存在性判断回退，不用真假值判断。`field_extra_info` 与内置策略链接合并，并继续保留 KAC 已写的工单/快照链接。
KAC 来源发布器将当前租户的非内置字段目录写入 fields 处理器的 `extra_data.__kac_custom_fields` 补丁，
每个 Alert 冻结目录和已求值字段；兼容文档只展开这些同名有效字段，保留 JSON 类型。
该目录不是任意 payload 展开入口：固定协议字段、租户、稳定身份、生命周期、关联及处置字段均禁止覆盖，
目录最多 128 个字段。缺失字段不输出；不新增 Event/Alert schema 或 KAC 数据库字段。
合并父事件只为模板中显式输出、且映射为 extra_data 同名键的自定义字段生成目录，不继承成员的任意扩展。


```text
Event → Enrich → Lifecycle / 策略控制任务
  → 持久化 Alert 业务变化及待完成意图
  → Linkd 兼容写入任务
      → 按租户和稳定 alarm_id 定位 alarm_event
      → 首次创建 / 更新原物理索引中的 Linkd 所有字段
      → 确认版本与搜索可见性 → 推进本地同步水位
  → 已获准动作通过可见性门槛
      → 可靠通知 KAC → KAC 投递 Celery → 分派/通知/工单等处置
```

保留现有可靠任务机制中已验证的原则：

- 持久化意图、稳定任务身份、有界重试、失败保留和人工重试。
- 状态更新与处置动作分开；屏蔽、合并等待等仍可同步状态，但不能获得处置资格。
- 同一 Alert 的 alarm_id 不随 Event、版本或触发/恢复/关闭动作改变。
- rollover 后更新原文档物理索引；不能每次对当前 write alias upsert 造成重复文档。
- 更新只覆盖 Linkd 拥有的字段；KAC 并发处置不能被整篇覆盖或重试初始化重置。
- 同版本重试幂等；低版本不能覆盖高版本；同步失败不推进水位，也不提前发送处置通知。
- 首次创建的 storage_time 和 KAC 默认字段只初始化一次。

状态写入端改为 Linkd 自有 ES 适配器。原投影 HTTP 收据和接收端实现不再是部署前提；
“写入成功且可搜索”的确认改由该适配器负责。处置通知仍保留独立 Celery 投递确认，不能把 HTTP 200
直接解释为业务通知或工单执行完成。

KAC 页面上的人工关闭等生命周期命令，应调用 Linkd 的状态变更入口，再由 Linkd 更新兼容文档。
直接在 KAC 把 status 改成 closed 而不更新 Alert，会违反本次确认的单一状态所有权。
KAC 侧的命令转发、旧状态写入退出和处置接收端适配属于后续跨仓接入项，不能用 Linkd 单仓测试替代。

## 4. 配置和现有代码调整

配置应只保留一份公共插件声明，包含全局启用开关、KAC ES 连接/索引信息和处置通知的地址及鉴权。
使用 `plugins.kac`；完整可加载片段和参数约束见[配置指南](../guides/configuration.md#kac-全局插件配置)。

2026-10-10 确认动作调用配置直接保存 KAC 的 JWT 签名密钥与调用用户名（`plugins.kac.jwt`），
不保存预生成的 JWT。HTTP Sender 复用 `internal/internaltoken`，每次请求及重试重新签发五分钟
有效的 HS256 JWT；请求头沿用 KAC 的 `Internal-Token: Bearer <JWT>`。认证凭据不参与稳定动作身份
或请求摘要，业务重试保持原动作。旧 `plugins.kac.internal_token` 明确拒绝。

| 原位置或职责 | 本轮实现 |
| --- | --- |
| EventSource.kac_targets | 移除，不再参与来源发布、Worker 读取或 Console 来源编辑 |
| resources.kac_delivery | 移除按租户的目标凭据映射，由公共插件配置承接 |
| projection_endpoint | 移除 HTTP 状态接收端依赖，替换为直接写兼容索引 |
| deliverysource | 从公共插件取得出口，不再按原来源 Release 查找路由 |
| Lifecycle 目标绑定 | 由全局插件决定，各来源一致；来源版本仅作业务溯源，移除历史来源目标查询 |
| 控制面 | 统一维护兼容索引、推进写入任务和处置任务 |
| 旧 type=kac Kafka Hook | 插件启用时不能并行触发同一告警的旧 KAC 输入链路 |
| Console | 配置脱敏、插件启用状态、索引维护状态、同步积压和处置积压；移除逐来源目标配置提示 |
| Helm | 公共插件配置统一注入控制面和 Lifecycle，防止角色开关不一致 |

凭据只存在部署配置及运行时内存，不进入 EventSource、策略发布、Alert、任务快照或日志。
不新增历史数据迁移兼容层或旧目标任务迁移流程；旧配置在移除后明确拒绝，不保留过渡配置分支。

## 5. 验证要求

1. 与固定 KAC 源码快照对照 mapping、settings、alias、模板、ILM 和新建索引；接管已有索引时不重建历史数据。
2. 无 kac_targets 的普通来源、跨租户来源及内置合并来源，全局开启后自动纳入兼容存储和处置通知。
3. ES/MySQL 两种 Linkd Repository 都能直接写同一协议的 KAC ES 兼容文档。
4. 创建、升级、恢复、关闭、屏蔽/解屏、合并/解联同步保持已确认领域语义；没有 Alert 的受抑制 Event 不额外生成 alarm_event。
5. rollover 后修改原文档；同版本重试、不确定写入重试和多实例并发不重复建文档或回退状态。
6. KAC 处置字段在并发写和 Linkd 重试下不丢失；处置状态与兼容 status 的交互按第 6 节已确认规则测试。
7. 索引或同步失败保留任务，处置通知被可见性门槛阻挡；恢复后继续推进；响应未知可能重复投递，同动作保持原身份和摘要。
8. 生产正常启用时全来源、全租户生效；进程重启后继续推进正常运行中已持久化的任务，不依赖首次启用回填。
9. Go/Console 配置严格校验与脱敏、Helm 一致性、相关 race、完整 make check 和文档链接检查。
10. 分别报告协议模拟、真实 ES 本地验证和真实 KAC 应用联调，不能混用验证结论。

## 6. 已确认的状态字段边界

2026-10-08 用户确认采用兼容规则：普通同步保留 KAC 处置状态；生命周期、屏蔽和合并变化按
兼容规则更新 status；解除关系后恢复保存的处置状态。处置态备份存入独立 Linkd 元数据索引，
不向 KAC mapping 增加备份字段。状态映射和优先级以
[字段所有权](../reference/contracts/kac-alert-projection-v1.md#索引与字段所有权)为准。

首次启用不作为切换场景处理，不补齐既有活跃或历史 Alert，不迁移旧目标或补发历史处置。
正常运行的写入重试、持久任务补扫、进程重启恢复仍然保留。

## 7. 实现位置与验收边界

- `internal/config/kac_delivery.go`：全局配置、索引参数、严格校验和脱敏。
- `internal/kaccompat`：KAC 原 mapping、索引维护、独立同步元数据及兼容文档条件更新。
- `internal/deliverysource`：全局出口解析，校验任务租户与业务溯源，不读取来源发布。
- `internal/lifecycle/process`：向全部来源注入固定插件目标及可靠动作记录器。
- `internal/controlplane/process/delivery.go`、`kac_index.go`：索引维护和投影/动作循环装配及观测。
- Console 配置读取和脱敏同步更新；来源编辑不再保留旧目标配置，控制面任务目录显示兼容索引维护状态。
- Helm 从公共 `configuration.plugins.kac` 注入，拒绝各角色或组覆盖；独立部署配置需要保持一致。

验证包含 KAC 源码 schema 对照、状态恢复/处置字段保留/轮转/延迟旧写入的回归、竞态、
ES/MySQL 两种 Repository 下的真实进程和兼容 ES 写入、处置协议模拟、Console/Helm 及完整门禁。
最终测试结果在本轮交付和[开发记录](event-enrich-and-alarm-policies.md#111-实施进度与验收记录2026-10-07)中记录。
KAC 实际接收端、生命周期命令转发和旧状态写入停用仍须在 KAC 应用中接入与联调。
策略同步及上述跨仓接入的完成条件、Console 增强和真实环境验收统一见
[当前能力与剩余差距](event-enrich-and-alarm-policies.md#112-当前能力与剩余差距2026-10-08)，
不能用历史清单勾选或本地协议模拟代替完整业务闭环验收。
