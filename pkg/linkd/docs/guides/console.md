# Linkd Console 运维调试工具

Console 是仓库内独立构建的运维控制台。默认本地模式仅监听回环地址；服务模式要求 Basic Auth，
部署方式见 [Helm 指南](helm.md)。基础设施与实体查询保持只读，来源配置及主动关闭告警通过正式控制面 API 执行。
它读取 Linkd YAML 与控制面的动态来源配置，使用 Node 连接层查询
Prometheus、Kafka、Redis 和权威 Repository；Linkd 进程不增加中间件诊断查询 API。

ES 支持范围和部署注意事项见 [Elasticsearch 版本兼容](elasticsearch-compatibility.md)。

完整启动参数、环境变量、接口与安全边界见 [Console README](../../console/README.md)。

深浅主题选择保存在当前浏览器的 localStorage，刷新页面或重新打开同站点后恢复上次选择；首次访问默认深色。
浏览器禁用本地存储时仍可切换主题，但选择仅在当前页面内有效。

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

## 实体查询与关联排障

Events、Alerts、AlertLogs 采用相同的查询工作区：快捷或自定义时间、租户和实体 ID、对象专用筛选、
更多关联标识、已应用条件标签、分布统计、结果列表和详情。枚举通过下拉选择，文本字段精确匹配；
点击分布项可继续筛选。输入条件后点击“执行查询”生效；分页保持同一时间范围，变更条件返回首页。
UTC / 本地时间切换同时影响展示和自定义时间输入；查询及详情可复制链接恢复。

“本页搜索”仅匹配当前加载页的标题、正文、对象、来源或 ID，不代表全库全文搜索。
当前 ES mapping 的标题和正文未建索引，本次不增加 mapping 或全表扫描兜底。
统计与列表共用全部筛选，分别读取快照；统计失败显示不可用，列表仍可独立使用。
MySQL Alert 统计现在也应用 JSON `update_at` 时间条件，但不提供时间趋势；大范围查询仍可能超时。

- Event 列表突出来源、处理状态、处理结果与关联告警数；详情区分来源逐级判定、Lifecycle 逐级裁决和观测值。
- Alert 列表同时呈现首次发生 `begin_at`、创建 `create_at` 和最近更新 `update_at`。默认按最近更新过滤和排序，**更新不代表新建**。
- Alert 详情首屏展示生命周期时间、当前状态、对象与来源，随后直接加载关联 Event 列表和 AlertLog 时间线，支持独立筛选、分页及就地展开记录。两类查询都绑定当前租户和 Alert ID；不会只凭 ID 跨租户查找。
- 关联查询默认覆盖最近更新前的最大允许窗口（默认七天），不是完整历史保证。Event 按接收时间、AlertLog 按记录时间；生命周期更长时明确提示窗口截断，可自行查询更早窗口。首次和最近事件仍可按精确 ID 独立查看。
- AlertLog 默认使用时间线，可切换表格，展示操作、操作方、父告警、原因及 Hook 摘要；支持按时间正序或倒序查看。

### 主动关闭告警

Alert 详情中的“主动关闭”要求填写原因并确认当前租户和 Alert；仅 active 告警可发起新关闭。
Console 通过管理 token 调用正式控制面关闭接口，不修改 Node 查询连接所指向的数据库。
Console 与控制面的 Repository 必须属于同一 Linkd 部署；功能需要同时部署包含关闭接口的新控制面与 Console。
控制面使用当前已发布来源配置执行 `CloseAlert`，完成 CAS、近期缓存、close 流水和 FinalHook；不会生成伪 Event。
来源被停用或删除后，仍可使用其已保留的最近发布配置关闭既有告警。

结果不确定时，原操作 ID、原因和时间保存在当前浏览器标签页的 sessionStorage；重新打开详情后可继续
“重试同一关闭操作”。关闭可能已经生效，不能把网络失败等同于未执行。成功后详情立即使用控制面返回的终态，
刷新列表和关联记录；ES 流水仍可能受 refresh 延迟影响。Hook 失败不会回滚告警，须查看 push 流水判断。
服务模式的操作方来自 Basic Auth 用户名，本地模式记为 `console-local`；不接受浏览器自报操作方。

协议字段、错误与重试边界见 [Alert 主动关闭 API](../reference/contracts/alert-close.md)。

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

「系统 → 指标目录」（`/metrics/catalog`）展示当前控制面二进制包含的指标定义，支持按功能模块、
指标类型、用途过滤，以及按中文名、OTel 名、Prometheus 名、维度和描述搜索。点击指标名可展开
完整统计口径、维度说明和可复制的查询序列。筛选条件保存在 URL 中，可收藏或分享同一查询。

目录由控制面 `GET /api/v1/metrics/catalog` 提供，经 Console 的 `GET /local-api/metrics/catalog`
代理读取。配置 `dispatch.url` 与 `dispatch.api_token` 即可使用；不要求配置 Prometheus，也不要求
开启 `telemetry.metrics`。管理 token 只留在 Console 服务端，worker token 无权读取此接口。

```bash
curl --fail --silent --show-error \
  -H "Authorization: Bearer ${LINKD_API_TOKEN}" \
  "${LINKD_CONTROL_PLANE_URL}/api/v1/metrics/catalog"
```

API 返回完整只读目录，不分页、不查询历史样本、不接受修改；Console 在浏览器中筛选并按 20 项分页。
响应字段如下：

| 字段 | 含义 |
| --- | --- |
| `schema_version` | 当前响应结构版本为 `1` |
| `modules` / `purposes` | `{id, name}` 形式的功能模块和用途分类 |
| `metrics[].name` / `prometheus_name` | 原始注册名与 Prometheus family 名 |
| `metrics[].display_name` / `description` | 中文短名称与完整统计口径；业务指标描述与 HELP 共用定义 |
| `metrics[].module` / `purpose` | 所属分类键 |
| `metrics[].type` / `prometheus_type` | 注册类型与导出类型；`up_down_counter` 导出为 `gauge` |
| `metrics[].unit` / `unit_label` | 注册单位与便于阅读的中文单位 |
| `metrics[].dimensions` | 可能出现的维度，每项包含原始名、Prometheus 标签名、中文说明 |
| `metrics[].series` | 可查询序列名；Histogram 包含 `_bucket`、`_sum`、`_count` |
| `metrics[].origin` | `linkd`、`runtime` 或 `exporter` |
| `common_dimensions` / `notes` | 公共 OTel scope 维度与使用边界说明 |

目录表示**二进制可提供的定义**，不代表功能已启用、产生了样本或已被采集。Go/process 指标从相同
collector 自动发现，会随平台和依赖版本变化。`Histogram` 的桶序列另带 `le` 标签，`Summary` 的
分位序列另带 `quantile` 标签；Prometheus 抓取时添加的 `job`、`instance` 不属于业务维度。
`service_version`、`linkd_role` 等 Resource 信息位于 `target_info`。

开发者增加指标的登记流程见 [指标目录与注册约束](../design/observability.md#指标目录与注册约束)。

「核心数据 → 策略活跃索引」自动列出 `active-alert-by-strategy` 的租户、策略组合，使用 SCAN/ZSCAN 游标逐批读取，点击行即可只读对账；「开始整体对账」后台检查当前缓存目标的全部租户和策略，展示进度、双向差异和无法确认的范围。
会合并已发布配置识别出的共享来源，并区分一致、Redis 缺失、Redis 独有和无法确认；查询失败、
扫描超限及并发变更不会被当成确定一致。页面展示控制面最近完整发现、待刷新数量，以及单策略校准时间、快照年龄和失败状态。用法、上限与一致性边界见
[策略索引查询与对账](active-alert-by-strategy.md#console-查询与对账)。

Cleaner 指标按 EventSource 聚合，received、settled 和 lane gauge 可以带 Kafka partition。Lifecycle
记录 Signal、Mailbox、lease、Event 裁决和 FinalHook。指标禁止包含 tenant、实体 ID、fingerprint、
topic、group、完整错误或 payload。

Lifecycle 页的「Hook 输出」及 `final_hook` 节点详情同时展示成功率、调用结果速率与 P95，
按 EventSource 和 Hook 名称区分实例。成功率使用所选计算窗口内
`succeeded / (succeeded + failed)`，跨 Worker 按调用量聚合；错误、超时与 panic 计为失败，
`skipped` 不计入。全部失败显示 0%；无调用、只有 skipped 或无时序时不补成 0% 或 100%。
成功只表示 Hook 调用返回成功，不保证下游业务处理完成；失败不会回滚已保存的 Alert 状态。

Prometheus exporter 使用统一的 `telemetry.metrics.prometheus.listen_address`。cleaner、lifecycle、
control-plane 和 all-in-one 不拥有各自的配置字段，但每个实际进程都会启动独立 exporter，并通过
`linkd.role` Resource 属性区分。多个角色共享宿主网络时必须使用不同配置文件分配不冲突的监听端口。
控制面任务额外使用固定 `linkd.task` 枚举；任务名、Stream、Group、索引名和错误文本不会作为动态属性。

## 降级语义

- Prometheus 不可用：当前 Kafka/Redis/存储快照仍可查看，历史图表显示不可用。
- 单个控制面数据源不可用：对应任务显示 `partial/unavailable`，其他任务状态与配置仍可查看。
- Kafka 或 Redis 不可用：对应页面和运行节点显示 `unavailable`，其他区域继续工作。
- Event 详情的 values/evaluations 是来源事实，`_processing.evaluations` 是逐级裁决；关联入口支持多个 Alert。
- severity 过滤针对 Alert 当前级别，不把事件多个级别合成单个 severity；values 只存储展示，不做数值聚合。
- 当前存储缺少可聚合字段时隐藏对应 facet，不扫描 `source_raw_data/extra_data/enrich/params` 等仅存储
  JSON 对象伪造结果。
- Elasticsearch 拓扑只检查 `index_prefix` 推导的稳定读 alias，不接受浏览器传入任意索引表达式；alias
  背后的时间桶元数据按有界批次读取。
- Alert 列表和详情会折叠归档瞬间同时存在于 Active/History 的相同副本；聚合统计无法原子去重，可能在
  该短暂窗口重复计数，并会在响应中返回 warning。
