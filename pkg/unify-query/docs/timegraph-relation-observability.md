# v1beta3 TimeGraph 关系查询观测

v1beta3 关系查询使用 TimeGraph。旧 `multi_resource` 模型指标使用
`route="timegraph"`；完整共享拓扑使用独立的 `cmdb_topology_*` 指标。
路径查询和拓扑查询共用 `cmdb_timegraph_*` 构图与取数指标。仪表盘 UID 为
`uq-timegraph-v1beta3`，业务为 2：

<https://bkmonitor.bkop.woa.com/?bizId=2#/grafana/d/uq-timegraph-v1beta3>

实现边界：TimeGraph 的图存储、TSDB 关系物化和路径查询位于
`cmdb/v1beta3`；版本无关的关系路径转换和排序逻辑位于 `cmdb` 公共包。
v1beta1 不再承载 TimeGraph 实现，HTTP path-resources 接口也直接使用 v1beta3 Model。

## 指标

共享拓扑时间网格保留请求起点，不按 step 向下对齐；源信息、关系边和目标信息使用
同一网格，VM 查询禁用可能移动采样点的缓存。当前 VM 协议使用整数秒，时间参数可用
秒或整秒对应的毫秒格式，step 必须为正整秒；非整秒精度在取数前明确拒绝。
后端样本偏离网格时返回错误，不转换成完整空图；instant 和 range 均保留 backend partial。

同类型多跳或 H 跳内可能回到起点类型时，关系取数取消种子下推，以保留后续边和
边界节点间的诱导边。路径查询的 `max_graph_results` 同时限制每层展开状态和最终累计
结果；中间层超限也会拒绝，即使后续过滤可能减少最终结果。显式路径仍受静态关系方向约束。

- `unify_query_api_request_total`：v1beta3 HTTP 请求量，按接口和状态区分；
- `unify_query_cmdb_relation_route_total`：TimeGraph 查询结果，`result` 为
  `started`、`success`、`empty` 或 `failed`；
- `unify_query_cmdb_relation_route_seconds`：TimeGraph 关系查询耗时；
- `unify_query_cmdb_relation_path_result_total`：候选路径整体结果；
- `unify_query_cmdb_relation_candidate_path_count`：候选路径数量；
- `unify_query_cmdb_relation_target_count`：最终目标数量；
- `unify_query_cmdb_relation_timegraph_result_count`：TimeGraph 返回的原始路径资源数量；
- `unify_query_cmdb_relation_timegraph_bucket_count`：range 查询产生的非空时间桶数量；
- `unify_query_tsdb_request_seconds`：底层 VictoriaMetrics 请求耗时。

共享拓扑和共用阶段的指标如下，均以 `unify_query_` 为前缀：

| 指标 | 标签与口径 |
| --- | --- |
| `cmdb_topology_operations_total` | `scope=request/query`、`query_mode=instant/range`、`result`；每个请求或子查询只记录一次终态 |
| `cmdb_topology_operation_seconds` | `scope`、`query_mode`；包含失败、拒绝、取消和超时的耗时 |
| `cmdb_topology_inflight` | `query_mode`；正在执行的模型查询数，所有返回路径均减一 |
| `cmdb_topology_size` | `query_mode`、`kind=points/nodes/edges/partial_snapshots`；返回结果的评估点数、所有快照节点/边的数量之和、不完整快照数 |
| `cmdb_topology_rejections_total` | `query_mode`、`reason`；固定的参数或容量拒绝原因 |
| `cmdb_timegraph_stage_seconds` | `stage`、`result`；构图、源信息/关系边/目标信息取数、拓扑遍历各阶段耗时，`_count` 同时表示阶段执行次数 |
| `cmdb_timegraph_size` | `stage`、`kind`；构图节点、边、节点属性、时间桶数量，以及 Matrix 序列数、样本点数 |

子查询 `result` 为 `success/empty/partial/failed/rejected/canceled/timeout`。
有任意不完整快照时优先记 `partial`，即使节点为空也不记成 `empty`。
HTTP `scope=request` 的空批次记 `empty`，所有子查询失败记 `failed`，
混合成功/失败或有不完整结果记 `partial`；非法 JSON 记 `rejected`。
外层 HTTP 200 不能代表子查询成功。HTTP 时间模式校验失败未进入模型时，也记录一次
`scope=query` 拒绝；进入模型后由模型统一记录，避免重复计数。

`reason` 只允许 `invalid_request/max_shared_topology_points/max_graph_nodes/`
`max_graph_edges/max_graph_node_infos/max_graph_results/max_targets/`
`max_response_bytes/max_topology_matrix_points/max_topology_matrix_series/`
`max_topology_output_elements/max_topology_output_bytes/max_topology_concurrency/`
`max_topology_queries/other`。
`stage` 只允许 `build/source-info/relation-edge/target-info/topology-traversal`。
错误原文、租户、资源类型、关系名称、matcher、trace ID 不作为指标标签；trace exemplar
仍按现有机制关联。构图规模在失败时也记录；响应和 Matrix 规模只记录成功返回的数据，
不会用失败时的零值污染结果规模分布。响应节点/边总数不是跨时间去重数量，也不是堆内存。

新指标不会合并到旧 `route_total` 中；旧 route、path result、target 和 bucket 指标
仍描述 `multi_resource` 适配路径，不能用它们的存在证明 topology 已被监控。

拓扑入口现已直接共享构图。时间点及边计数保留逻辑预算口径，不能把它们解释为
图对象数量或内存字节；构图模式、默认资源保护及完整流水线探针见
[共享构图与资源边界](timegraph-shared-memory.md)。

`candidate_path_count` 和路径结果使用 `execution_mode="all_paths"`，因为 TimeGraph
会在一次查询窗口内物化候选关系并统一遍历，不再按 SurrealDB path 逐条执行。

## Trace

关键 span 及属性：

- `handler-api-relation-multi-resource-v1beta3` / `...range-v1beta3`：HTTP 批量请求；
- `handler-api-relation-path-resources` / `...range`：path-resources HTTP 入口；每个子查询记录 source、target、路径、matcher、step、结果数和失败数；
- `handler-api-relation-v1beta3-topology` / `...-range` 及对应 item span：共享拓扑 HTTP 入口；记录请求点数、时间范围、跳数、过滤条件、快照节点/边数和 partial 数；
- `timegraph-query-path-resources`、`timegraph-query-relation-path-resources` 及 range 变体：模型入口的参数校验、路径解析和输出结果；
- `timegraph-normalize-topology-grid`：共享拓扑时间网格规范化，记录原始范围、step、点数和可配置上限；`timegraph-normalize-lookback` 记录回溯窗口解析；
- `timegraph-resolve-topology-relations`：按 namespace、类别、关系类型和方向筛选关系；
- `timegraph-resolve-and-query` / `timegraph-resolve-paths`：路径来源、请求路径数、解析路径数和最终结果数；
- `build-time-graph-from-relations`：临时图构建，记录关系数、时间窗口、step、图容量、VM 子查询阶段数和最终图统计；
- `timegraph-build-config`：记录 namespace 级 schema 配置快照及节点、边、结果、节点属性上限；
- `timegraph-build-resource-info-query` / `timegraph-build-relation-query`：记录 source-info、relation-edge、target-info 查询构造及失败点；
- `timegraph-query-matrix`：每个 VM 或测试查询记录 `query-stage`、查询范围、series、point、partial 和错误；生产阶段值为 `source-info`、`relation-edge`、`target-info`；
- `timegraph-prepare-vm-query`：记录 QueryTs、PromQL 表达式长度和查询参数准备错误；
- `timegraph-resolve-querier`、`timegraph-adapt-instant-result` / `...range`：记录后端解析、TimeGraph 结果适配、候选路径、桶和目标数量；
- `timegraph-query-relation`：关系指标物化与路径遍历，记录 path、relation、原始结果、时间点、目标类型和路径节点数量；
- `timegraph-find-shortest-path` / `timegraph-find-relation-paths`：记录图时间点、候选节点、已见路径和结果数；
- `timegraph-find-shared-topology`：共享拓扑遍历和输出，记录图状态、源节点匹配、可达节点、跳数、快照节点/边和 partial 数；
- `timegraph-build-shared-topology-state`：记录时间网格上的节点位图和过滤后边状态。

所有阶段 span 都通过 `span.End(&err)` 记录错误，包括参数校验、VM 查询、图构建、路径遍历和 context cancellation。热点节点、边循环不创建逐元素 span，只在阶段 span 中记录聚合计数，避免 trace 数量随图规模线性膨胀。

无采样不能等价为零请求或零结果；需要结合 `route_total`、`api_request_total` 和 trace
一起判断服务是否有流量。

仪表盘配置以 [timegraph-relation-dashboard.json](timegraph-relation-dashboard.json)
形式保存在仓库中，可通过 Dashboard as Code 重新发布。
