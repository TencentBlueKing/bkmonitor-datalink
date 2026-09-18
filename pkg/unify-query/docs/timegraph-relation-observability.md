# v1beta3 TimeGraph 关系查询观测

v1beta3 的 instant/range 关系接口统一使用 `route="timegraph"`。仪表盘 UID 为
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
- `cmdb-query-resource-matcher` / `...range`：v1beta3 关系适配层；
- `timegraph-query-relation`：TimeGraph 关系物化与路径遍历，记录 path、relation、原始结果数量；
- `/api/v1/relation/path_resources` 及 range 入口记录 query-list、失败子查询和每个子查询结果数，避免独立路径查询没有入口级观测。
- `build-time-graph-from-relations`：关系指标查询和临时图构建，记录 relation、时间窗口和 step。

无采样不能等价为零请求或零结果；需要结合 `route_total`、`api_request_total` 和 trace
一起判断服务是否有流量。

仪表盘配置以 [timegraph-relation-dashboard.json](timegraph-relation-dashboard.json)
形式保存在仓库中，可通过 Dashboard as Code 重新发布。
