# KAC 插件调试字段预览

> 首版接口已实现，已完成本地真实 HTTP、verification ES 索引和原调试组件联调；完整主站验收待验证。
> 设计边界见[插件调试方案](../../design/kac-alarm-source-debug-preview.md)。

`POST /api/v1/ingest/preview` 使用现有 `Internal-Token: Bearer <JWT>` 管理认证。
KAC 后端同步请求并读取 JSON 响应，不使用 Kafka、回调或异步结果投递。
Linkd 只执行 standard 标准化及 KAC 调试字段转换，不执行丰富、内容生成、正式存储或处置。

## 请求

| 字段 | 约束 |
| --- | --- |
| bk_tenant_id、event_source_id、session_id | 明确的租户、来源与隔离调试会话身份 |
| source | 下列两种输入互斥，未知字段拒绝 |
| kac_source | `{source_id, source_name}`，来自 KAC 已鉴权来源；插件不能覆盖 |
| field_catalog | 当前租户已登记自定义字段名称，最多 128 项，禁止受保护字段 |
| samples | 1～10 项 `{sample_id, received_at, payload}`，payload 为 standard RawEvent |
| output | 固定 `kac_alarm` |

- `source.kind=draft`：提供 `calculation`，不得提供 version；新建且未发布来源也可调用。
  calculation 只允许 cleaner.type、fingerprint_mode/field/fields、severity_mapping 和 default_severity。
  不接受 enrich、processors、content_mode、storage、hooks 或任何连接配置。
- `source.kind=published`：只提供明确的正数 version，不能提供 calculation。
  只读取仍存在来源的指定已发布版本，要求来源归属租户匹配；来源停用不阻止调试。
  从 Release 中仅选取标准化字段，其他资源不进入运行时。

received_at 固定于 KAC 首次采样，sample_id 不重复；payload 的租户必须与请求一致，
event_id、alert_id 必须存在。标准化中的等级和时间缺省沿用 EventFactory 规则。
调试 Event 身份由隔离会话、样本、输入摘要和固定时间确定，不承诺等于正式 Kafka Event ID。

## 响应

config_origin、config_digest、severity_digest 标识本次固定计算配置；仅 published 返回
event_source_version，draft 不输出伪造的正式版本。

items 逐样本/标准等级返回 sample_id、severity、action、passed、kac_alarm 和 diagnostics。
action 为标准事件动作，kac_alarm.action 映射为 firing/resolved/close；level 使用当前等级快照的 KAC 映射。
终态及 `__ALL__` 不改写成触发；无法从单条输入确定的历史字段通过 history_not_available 诊断表达。
不返回真实处置状态、关联或结束时间。

kac_alarm 只展开来源已有的固定业务字段及 field_catalog 中存在于 extra_data 的字段；
不展开任意 source_raw_data 或原始 payload，目录字段不能从同名 labels 兜底。
使用与正式出口共用的标量文本、受保护字段和 Integer 类型规则，保留 0、false 与显式空值。
alarm_id 以 preview- 开头，只供展示关联，不能用于正式告警操作；event_id 保留来源关联编号。

KAC 核对样本、租户、来源和逐等级数量后保存原 verification 索引，再转换为旧页面响应。
HTTP 200 不表示已创建正式告警；任一样本硬错误拒绝整批，不返回可误标通过的部分结果。
字段差异和完整阶段轨迹尚未提供，当前仅返回字段及终态诊断。

## 预算与错误

请求和转换结果各最多 1 MiB，单样本最多 32 个 evaluations，单请求总结果最多 64 行。
进程内并发最多 4，总超时 10 秒，取消传播到配置读取；与正式处理池隔离。

| HTTP | 含义 |
| --- | --- |
| 400 | 输入身份、模式、数量、类型或未知字段错误 |
| 401 | 管理认证失败 |
| 404 | 已发布来源或指定版本不可读 |
| 422 | 标准化配置、payload、KAC 字段转换或输出预算错误 |
| 429 | 调试并发预算耗尽 |
| 502 / 504 | 配置读取依赖失败 / 超时或取消 |
| 503 | 当前进程未装配预览服务 |

错误不包含完整来源 payload、连接凭据或外部响应。Linkd 不持有 verification 写入、
正式 Repository、Mailbox、丰富引擎或投影/动作任务端口。
