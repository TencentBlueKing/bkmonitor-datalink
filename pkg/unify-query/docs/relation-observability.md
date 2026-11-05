# Legacy VM 关系查询观测

本页对应 `POST /api/v1/relation/multi_resource` 和
`POST /api/v1/relation/multi_resource_range`，路由标签为 `vm_legacy`。
v1beta3 的指标与共享拓扑说明见 [TimeGraph 关系查询观测](timegraph-relation-observability.md)。

请求级 span 为 `handler-api-relation-multi-resource` 或
`handler-api-relation-multi-resource-range`，单项 span 使用对应的 `-item` 名称。
通过 `query-count`、`query-index`、`failed-query-count` 和 `target-count`
定位批量项和输出；下游 VM span 记录实际表达式、路径及返回规模。

## 聚合指标

| 指标 | 用途 |
| --- | --- |
| `unify_query_cmdb_relation_route_total` | 按 `query_mode/result` 区分开始、成功、空结果、partial 和失败 |
| `unify_query_cmdb_relation_route_seconds` | 查询耗时分布 |
| `unify_query_cmdb_relation_query_list_size` | HTTP 批量子查询数量 |
| `unify_query_cmdb_relation_target_count` | 最终返回的目标数量 |

所有查询加 `route="vm_legacy"`，与 TimeGraph 兼容路由分别观察。
失败率只以已完成查询为分母：

```promql
sum by (query_mode) (
  rate(unify_query_cmdb_relation_route_total{route="vm_legacy",result="failed"}[5m])
)
/
sum by (query_mode) (
  rate(unify_query_cmdb_relation_route_total{route="vm_legacy",result=~"success|empty|partial|failed"}[5m])
)
```

耗时及最终目标量使用相同的模式维度：

```promql
histogram_quantile(0.95, sum by (le, query_mode) (
  rate(unify_query_cmdb_relation_route_seconds_bucket{route="vm_legacy"}[5m])
))

histogram_quantile(0.95, sum by (le, query_mode) (
  rate(unify_query_cmdb_relation_target_count_bucket{route="vm_legacy"}[5m])
))
```

[TimeGraph 仪表盘](timegraph-relation-dashboard.json)同时保留 `vm_legacy/timegraph`
路由的终态、耗时与最终目标量，便于检查兼容调用。查询范围和后端相同时再比较结果；
看板数据来自实际调用，不记录逐候选路径或执行模式指标。
