# Linkd DevTools 运维调试工具

DevTools 是仓库内独立构建的本机只读控制台。它直接读取 Linkd YAML，使用 Node 连接层查询
Prometheus、Kafka、Redis 和权威 Repository；Linkd 进程不增加数据查询 API。

完整启动参数、环境变量、接口与安全边界见 [DevTools README](../../devtools/README.md)。

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
  → FinalHook Kafka
```

Kafka 和 Redis 页面展示请求时的当前快照；历史趋势只来自 Prometheus，不通过 DevTools 自建时序存储。

Control Plane 页面不展示 ES 集群、分片或索引容量，而是按当前四个 management task 展示 owner、依赖、
执行新鲜度和工作量。三个 ES 任务依次依赖 Schema/Active 资源、时间桶和 History write alias；Redis Stream
任务在逻辑上独立，但与它们共享进程监督和退出故障域。Archiver backlog 来自固定 Active alias 的只读
`_count`；页面同时展示连续归档的空闲/重试间隔、批量上限、Worker 数及最近扫描、成功和失败数量，历史执行和
归档/裁剪速率来自 Prometheus。

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
