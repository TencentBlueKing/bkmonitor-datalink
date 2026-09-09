# Linkd Console 运维调试工具

Console 是仓库内独立构建的运维控制台。默认本地模式仅监听回环地址；服务模式要求 Basic Auth，
部署方式见 [Helm 指南](helm.md)。基础设施与实体查询保持只读，来源配置通过正式控制面 API 修改。
它读取 Linkd YAML 与控制面的动态来源配置，使用 Node 连接层查询
Prometheus、Kafka、Redis 和权威 Repository；Linkd 进程不增加中间件诊断查询 API。

ES 支持范围和部署注意事项见 [Elasticsearch 版本兼容](elasticsearch-compatibility.md)。

完整启动参数、环境变量、接口与安全边界见 [Console README](../../console/README.md)。

## 感知模型

```text
Linkd YAML ───────────────→ 配置期望
Prometheus ───────────────→ 进程与历史处理趋势
Kafka Admin / Redis XINFO → 当前 partition、lag、PEL、Mailbox
MySQL / Elasticsearch ───→ Event、Alert、AlertLog 明细与统计
```

总览只展示当前已经实现的处理链：

```text
EventSource / Kafka partition
  → Cleaner transform
  → Event store
  → Redis Mailbox + Signal
  → Lifecycle
  → Alert / AlertLog
  → EventSource.hooks
```

Kafka 和 Redis 页面展示请求时的当前快照；历史趋势只来自 Prometheus，不通过 Console 自建时序存储。
Prometheus 图表页面统一提供 15 分钟到 7 天的查询时间范围，默认 1 小时；采样步长随所选范围调整。
页面另行提供独立的“计算窗口”，默认 1 分钟，并直接用于速率、增量与直方图分位计算。

Control Plane 页面不展示 ES 集群、分片或索引容量，而是按当前四个 management task 展示 owner、依赖、
执行新鲜度和工作量。三个 ES 任务依次依赖 Schema/Active 资源、时间桶和 History write alias；Redis Stream
任务在逻辑上独立，但与它们共享进程监督和退出故障域。Archiver backlog 来自固定 Active alias 的只读
`_count`；页面同时展示连续归档的空闲/重试间隔、批量上限、Worker 数及最近扫描、成功和失败数量，历史执行和
归档/裁剪速率来自 Prometheus。

## 动态来源的 Kafka 查询

配置 `dispatch.url` 和 `dispatch.api_token` 后，Console 服务端通过控制面来源列表接口读取完整配置，
再用 Kafka Admin 查询输入 topic 和各个 Kafka hook 的输出 topic。Leader、replicas/ISR 来自 topic metadata，
High/Low 来自 topic offset 查询，Committed 来自 consumer group 的已提交位点；Lag 使用整数精度计算
`max(High - Committed, 0)`。Owner 来自 Kafka consumer group 的实际成员分配，不再用调度器分区数拼接健康快照。
没有已提交位点时保持“未知”，不会补成 0；查询失败显示不可用，不回退为 `AVAILABLE`。

控制面的 `GET /api/v1/event-sources` 和 `GET /api/v1/event-sources/{id}` 默认脱敏；显式添加
`include_secrets=true` 可返回含认证材料的完整记录，响应设置 `Cache-Control: no-store`。
两种读取均需要管理 `api_token`，worker token 不能访问。此参数只由 Console 服务端使用，
浏览器侧来源管理代理不转发该参数，运行状态响应也只包含查询结果和脱敏配置摘要。

Console 必须能够访问 Kafka bootstrap 地址及 broker 的 advertised 地址，并拥有 topic/group 的查询权限。
使用 SASL/TLS 时沿用完整来源配置；如果 TLS 材料使用文件路径，需把对应文件挂载到 Console 可读取的同一路径，
或使用内联 PEM。仅更新 Console 而未更新支持完整配置读取的控制面，无法获取受保护 Kafka 的真实凭据。
同一 Console 的并发刷新共享本轮查询，每轮最多四个 Admin 连接；请求使用配置的查询超时并关闭自动重试。
本次查询只使用 Admin 读取接口，不消费消息或提交 offset。

## 指标边界

Cleaner 指标按 EventSource 聚合，received、settled 和 lane gauge 可以带 Kafka partition。Lifecycle
记录 Signal、Mailbox、lease、Event 裁决和 FinalHook。指标禁止包含 tenant、实体 ID、fingerprint、
topic、group、完整错误或 payload。

Prometheus exporter 使用统一的 `telemetry.metrics.prometheus.listen_address`。cleaner、lifecycle、
control-plane 和 all-in-one 不拥有各自的配置字段，但每个实际进程都会启动独立 exporter，并通过
`linkd.role` Resource 属性区分。多个角色共享宿主网络时必须使用不同配置文件分配不冲突的监听端口。
控制面任务额外使用固定 `linkd.task` 枚举；任务名、Stream、Group、索引名和错误文本不会作为动态属性。

## 降级语义

- Prometheus 不可用：当前 Kafka/Redis/存储快照仍可查看，历史图表显示不可用。
- 单个控制面数据源不可用：对应任务显示 `partial/unavailable`，其他任务状态与配置仍可查看。
- Kafka 或 Redis 不可用：对应页面和运行节点显示 `unavailable`，其他区域继续工作。
- 当前存储缺少可聚合字段时隐藏对应 facet，不扫描 `source_raw_data/extra_data/enrich/params` 等仅存储
  JSON 对象伪造结果。
- Elasticsearch 拓扑只检查 `index_prefix` 推导的稳定读 alias，不接受浏览器传入任意索引表达式；alias
  背后的时间桶元数据按有界批次读取。
- Alert 列表和详情会折叠归档瞬间同时存在于 Active/History 的相同副本；聚合统计无法原子去重，可能在
  该短暂窗口重复计数，并会在响应中返回 warning。
