# TimeGraph 共享构图与资源边界

`topology` / `topology_range` 从 Matrix 直接写入共享节点、关系和时间位。
同一条关系在 60 个评估点出现时，只保留一个关系身份及一个内联 uint64 状态，
不创建 60 个 `graph.Graph`，也不先建立每个节点/关系的 timestamp map。
YOLO 模式超过 64 个评估点时，时间位图自动扩展为多个 uint64，不丢弃高位时间点；
节点身份、关系身份和相同属性版本仍在整个查询内共享。
已有 path / multi_resource 接口继续使用逐时间点图。

属性不能只保存最后一次值。同一节点的相同属性版本共享一份 matcher，
不同版本用不相交的时间位表示；同一时间点的属性合并继续采用原有确定性规则。
输出快照持有自己的属性副本，清理构图状态或修改其他快照不会改写结果。
关系方向、类别、指标名及关系类型仍参与身份和过滤，H 跳之后补齐诱导边。

## 默认保护

下列配置位于 `cmdb.v1beta3`。这些是普通模式下有限资源保护的默认值，不是吞吐或延迟 SLO。
除点数上限外，非正配置回退到表中的默认值；提高限制前需要重新做容量验证。

| 配置 | 默认值 | 生效位置 |
| --- | --- | --- |
| `max_shared_topology_points` | 60 | 实际网格；可调低，不能通过配置放宽到 61–64 点，超出范围的配置回退 60 |
| `max_shared_topology_backend_bytes` | 16777216（16 MiB） | 单次后端 HTTP 响应的解压后大小；VM 调用显式传入，解码前拒绝 |
| `max_shared_topology_matrix_points` | 1000000 | 一个拓扑查询所有取数阶段的 Matrix 点数总和 |
| `max_shared_topology_output_elements` | 200000 | 实际物化的节点与边对象数：snapshots 按各帧展开，compact 按属性／关系版本 |
| `max_shared_topology_output_bytes` | 67108864（64 MiB） | 按所选表示计算的 JSON 大小保守上界，以及 HTTP 整批实际响应大小 |
| `max_shared_topology_request_bytes` | 1048576（1 MiB） | HTTP 请求体大小，直连和代理入口均生效 |
| `max_shared_topology_queries` | 16 | 单个 HTTP 批次的查询条数 |

既有 `max_graph_nodes`、`max_graph_edges`、`max_graph_node_infos` 继续约束构图。
Matrix 单次序列数也受 `max_graph_nodes` 限制。共享拓扑的边限额按完整关系身份的唯一有向边计数，属性限额按实际存储的标签版本计数；旧逐时间点路径图仍按各帧计数。相同标签跨帧复用结构限额，同端点不同关系类型、指标、类别或方向分别计数。
时间出现次数继续作为观测指标保留；Matrix 累计点数、H／K、截止时间及 Matrix／graph／output 的同时存活内存预约仍独立生效。结构计数不是堆大小保证，提高输入或资源参数仍须容量验收。

普通模式默认 HTTP 请求体最多 1 MiB，`query_list` 最多 16 项。批次串行执行，响应累计也受预算限制，
不能用更多子查询放大单请求输出上限。

## YOLO 开关

默认 `yolo_mode: false`，保留上述保护。仅在需要绕过容量限制的实例上配置：

```yaml
cmdb:
  v1beta3:
    yolo_mode: true
```

配置加载后，YOLO 模式统一忽略请求体、批量查询条数、共享拓扑时间点、后端响应字节、
Matrix 点数、图节点/边/结果/节点属性数量、物化输出元素和响应字节上限。
旧 `multi_resource` 的 range 点数和每桶目标数量限制也忽略。
不需要把各个上限逐个调大；改回 `false` 并重新加载配置即可恢复正常模式保护。

YOLO 不改变查询语义：时间戳/步长、方向和 schema 等参数仍须合法，
`max_hops` 仍决定局部拓扑深度并遵守现有最大跳数配置；取消和查询超时仍生效。
该开关只控制本服务的上述预算，不绕过 VM、网关或容器自身的限制。
它不会自动扩容，开启后大范围查询仍可能耗尽内存，应在隔离实例上使用。

`TestTopologyYoloBypassesHTTPBudgets` 覆盖直连/代理及开关恢复；
`TestYoloSharedTopologyBypassesConfiguredBudgets` 覆盖加载、构图和输出限制；
`TestDirectSharedGraphDifferential` 在 64、65、129、257 点验证关系、历史属性与 partial 状态。

## 并发请求与活动计数

普通模式使用可配置的并发、进程预留量与可见内存余量保护，立即拒绝、不排队。
YOLO 绕过这些预算；多个请求可同时取数和构图，并不是固定只能两个查询。
新键为 `max_shared_topology_concurrent_requests`；旧的
`max_shared_topology_concurrency` 不再生效。默认值和估算边界见
[输出与资源优化](timegraph-output-optimization.md)。

`AcquireSharedTopology` 负责准入、预留、活动计数和取消检查：

1. HTTP 入口检查 context；已经取消的请求直接结束，并记录 canceled/timeout。
2. 每个 HTTP 批次增加一次活动计数，模型内部通过 context 复用，不重复增加。
   直接调用模型时独立计数；已有两个请求并不意味着第三个必然拒绝，取决于配置和资源。
3. HTTP/代理响应写完后减少计数；直接模型调用在返回时减少计数。
   成功、失败、取消均清理，重复调用 release 只清理一次。

`cmdb_topology_admission_active` 观测实际活动数，`cmdb_topology_reserved_bytes`
观测估算预留量，而非 RSS。旧 `cmdb_topology_admission_limit` 和
`max_topology_concurrency` 未恢复。`TestSharedTopologyConcurrentLoadingAndRecovery` 覆盖普通/YOLO
模式下 8 个请求同时到达后端，以及成功、取消、后端失败后的计数恢复；HTTP 回归还覆盖
已有两个活动请求时 instant/range 及代理入口继续处理请求。

## 请求预算与错误

JSON 物化预算采用包括转义开销的保守估计，可能在实际编码字节数达到上限前拒绝。
这是为了在创建大快照前终止。内部共享不消除 60 份快照本身的输出量。
HTTP 批次超限整体失败；单个查询触发模型预算时，沿用批次中的失败项语义。
外层 HTTP 200 仍不表示每个子查询成功。

后端字节拒绝状态沿派生 context 共享，即使聚合后端把该路由错误转换为 partial，
拓扑入口也会明确返回容量错误。取消不伪装为空图；普通后端 partial 继续保留在快照中。

## 观测

在 `build-time-graph-from-relations` 中检查：

- `graph-storage-mode=shared`，`graph-legacy-timepoint-count=0`。
- `graph-shared-edge-count` 是唯一关系身份数。
- `graph-attribute-version-count` 是实际保存的节点属性版本数。
- `graph-timepoint-count` 表示有节点观测的时间点数，不再等于内存中的图对象个数。

`graph-edge-count` 和 `graph-node-info-count` 仍是上述逻辑预算计数。
`build` span 包含后端取数，不能把它全部解释为内存图分配时间。
响应字节、Matrix 点数及输出元素/字节拒绝均有固定 reason 指标；
见 [指标说明](timegraph-relation-observability.md)。

## 同口径测量

`TestSharedTopologyPipelineProbe` 为显式启用的本地测量入口。它从文件流提供固定
VM 响应，经过真实 HttpCurl、JSON 解码、VM Matrix 转换、构图、遍历、公共结果
转换、图清理和 JSON 编码。`legacy` 与 `shared` 只切换构图存储方式。
路由解析、真实存储以及公网 HTTP handler 不在这个隔离探针范围内。

探针为比较相同大响应，把两个模式的输出预算都临时设为 100 万元素 / 256 MiB；
这只作用于测试进程。并发档位比较核心流水线成本，不经过公开 HTTP handler，不能据此
承诺生产吞吐或峰值内存。HTTP 批次、活动计数及代理生命周期由另外的回归测试验证。

从仓库根目录执行：

```bash
cd pkg/unify-query
GOTOOLCHAIN=go1.24.4 GOMAXPROCS=4 go test -tags=jsonsonic -c -o /tmp/timegraph-pipeline.test ./cmdb/v1beta3
TG_PIPELINE_MODE=legacy TG_EDGES=1000 TG_POINTS=60 TG_CONCURRENCY=1 GOMAXPROCS=4 GOMEMLIMIT=768MiB /tmp/timegraph-pipeline.test -test.run '^TestSharedTopologyPipelineProbe$' -test.v -test.timeout=60s
TG_PIPELINE_MODE=shared TG_EDGES=1000 TG_POINTS=60 TG_CONCURRENCY=1 GOMAXPROCS=4 GOMEMLIMIT=768MiB /tmp/timegraph-pipeline.test -test.run '^TestSharedTopologyPipelineProbe$' -test.v -test.timeout=60s
```

每个模式使用新进程，外部约每 20 ms 采样 RSS；测试内部约每 5 ms 采样 HeapAlloc。
短峰值可能漏采。fixture 生成不在请求耗时内，RSS 采样则覆盖整个进程。
结果日志同时记录累计分配、GC、保留输出时的存活堆、清理后的堆和输出摘要。
累计分配量、GC 后存活堆与峰值 RSS 是不同指标，不应相互替代。

正式容量验收仍需固定真实业务画像和延迟/内存目标，并验证持续负载对普通查询的影响。
逐层过滤、候选批读与混合取数会改变进入系统的数据量，应在保持相同结果语义的前提下
独立比较；本次直接共享构图沿用已有候选取数策略。

属性选根通过 `source_expand_info` 显式请求，先查询 info 并仅按每帧命中身份筛选关系端点根。info-only 孤立节点不产生拓扑答案，邻域节点不受根属性条件限制。冲突身份拒绝，扩展主键可补全身份；未知属性字段拒绝。源 info 缺物理路由允许空结果并标记 `source_info_no_route`，后端 partial 保留 `backend_partial`。
