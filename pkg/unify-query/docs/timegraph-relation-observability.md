# v1beta3 TimeGraph 关系查询观测

v1beta3 的 instant/range 关系接口统一使用 `route="timegraph"`，包括
`/api/v1/relation/path_resources`、共享拓扑接口及其 range 接口。仪表盘 UID 为
`uq-timegraph-v1beta3`，业务为 2：

<https://bkmonitor.bkop.woa.com/?bizId=2#/grafana/d/uq-timegraph-v1beta3>

实现边界：TimeGraph 的图存储、TSDB 关系物化和路径查询位于
`cmdb/v1beta3`；版本无关的关系路径转换和排序逻辑位于 `cmdb` 公共包。
v1beta1 不再承载 TimeGraph 实现，HTTP path-resources 接口也直接使用 v1beta3 Model。

## 指标

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
